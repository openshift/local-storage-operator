package lv

import (
	"bytes"
	"context"
	logError "errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/openshift/local-storage-operator/common"
	"github.com/openshift/local-storage-operator/internal"
	"github.com/openshift/local-storage-operator/localmetrics"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
	"k8s.io/utils/mount"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	staticProvisioner "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"

	localv1 "github.com/openshift/local-storage-operator/api/v1"
	storagev1 "k8s.io/api/storage/v1"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"

	provCache "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/cache"
	provDeleter "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/deleter"
	provUtil "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/util"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
)

// DiskMaker is a small utility that reads configmap and
// creates and symlinks disks in location from which local-storage-provisioner can access.
// It also ensures that only stable device names are used.

var (
	checkDuration = 60 * time.Second
	diskByIDPath  = "/dev/disk/by-id/*"
)

const (
	ownerNamespaceLabel = "local.storage.openshift.io/owner-namespace"
	ownerNameLabel      = "local.storage.openshift.io/owner-name"
	// ComponentName for lv symlinker
	ComponentName = "localvolume-symlink-controller"
)

var nodeName string

func init() {
	nodeName = common.GetNodeNameEnvVar()
}

type DiskLocation struct {
	// diskNamePath stores full device name path - "/dev/sda"
	diskNamePath string
	diskID       string
	blockDevice  internal.BlockDevice
}

func (r *LocalVolumeReconciler) createSymlink(
	deviceNameLocation DiskLocation,
	symLinkSource string,
	symLinkTarget string,
	idExists bool,
) bool {
	// get PV creation lock which checks for existing symlinks to this device
	pvLock, pvLocked, existingSymlinks, err := internal.GetPVCreationLock(
		symLinkSource,
		r.symlinkLocation,
	)

	unlockFunc := func() {
		err := pvLock.Unlock()
		if err != nil {
			r.Log.Error(err, " failed to unlock device")
		}
	}

	defer unlockFunc()

	if len(existingSymlinks) > 0 { // already claimed, fail silently
		for _, path := range existingSymlinks {
			if path == symLinkTarget { // symlinked in this folder, ensure the PV exists
				return true
			}
		}
		msg := fmt.Sprintf("not symlinking, already claimed: %v", existingSymlinks)
		r.eventSync.Report(r.localVolume, newDiskEvent(DeviceSymlinkExists, msg, symLinkTarget, corev1.EventTypeWarning))
		klog.Errorf(msg)
		r.Log.Error(logError.New("not symlinking, already claimed"), "Symlink already exists.", "symlink", existingSymlinks)
		return false
	} else if err != nil || !pvLocked { // locking failed for some other reasion
		msg := fmt.Sprintf("not symlinking, locking failed: %+v", err)
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorCreatingSymLink, msg, symLinkTarget, corev1.EventTypeWarning))
		r.Log.Error(err, "not symlinking, could not get lock")
		return false
	}

	symLinkDir := filepath.Dir(symLinkTarget)

	err = os.MkdirAll(symLinkDir, 0755)
	if err != nil {
		msg := "error creating symlink dir "
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, symLinkTarget, corev1.EventTypeWarning))
		r.Log.Error(err, msg, "symLinkDir", symLinkDir)
		return false
	}

	if fileExists(symLinkTarget) {
		r.Log.Info("symlink already exists", "symLink", symLinkTarget)
		return true
	}

	err = os.Symlink(symLinkSource, symLinkTarget)
	if err != nil {
		msg := "error creating symlink "
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, symLinkSource, corev1.EventTypeWarning))
		r.Log.Error(err, msg, "symLink", symLinkTarget)
		return false
	}

	if !idExists {
		msg := fmt.Sprintf("created symlink on device name %s with no disk/by-id. device name might not persist on reboot", symLinkSource)
		r.eventSync.Report(r.localVolume, newDiskEvent(SymLinkedOnDeviceName, msg, symLinkSource, corev1.EventTypeWarning))
		r.Log.Info("created symlink on device name with no disk/by-id. device name might not persist on reboot", "deviceName", symLinkSource)
	}
	successMsg := fmt.Sprintf("found matching disk %s with id %s", deviceNameLocation.diskNamePath, deviceNameLocation.diskID)
	r.eventSync.Report(r.localVolume, newDiskEvent(FoundMatchingDisk, successMsg, deviceNameLocation.diskNamePath, corev1.EventTypeNormal))
	return true

}
func diskMakerLabels(crName string) map[string]string {
	return map[string]string{
		"app": fmt.Sprintf("local-volume-diskmaker-%s", crName),
	}
}
func (r *LocalVolumeReconciler) generateConfig() *DiskConfig {
	configMapData := &DiskConfig{
		Disks:           map[string]*Disks{},
		OwnerName:       r.localVolume.Name,
		OwnerNamespace:  r.localVolume.Namespace,
		OwnerKind:       localv1.LocalVolumeKind,
		OwnerUID:        string(r.localVolume.UID),
		OwnerAPIVersion: localv1.GroupVersion.String(),
	}

	storageClassDevices := r.localVolume.Spec.StorageClassDevices
	for _, storageClassDevice := range storageClassDevices {
		disks := new(Disks)
		if len(storageClassDevice.DevicePaths) > 0 {
			disks.DevicePaths = storageClassDevice.DevicePaths
		}
		configMapData.Disks[storageClassDevice.StorageClassName] = disks
	}

	return configMapData
}
func addOwner(meta *metav1.ObjectMeta, cr *localv1.LocalVolume) {
	trueVal := true
	meta.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: localv1.GroupVersion.String(),
			Kind:       localv1.LocalVolumeKind,
			Name:       cr.Name,
			UID:        cr.UID,
			Controller: &trueVal,
		},
	}
}
func addOwnerLabels(meta *metav1.ObjectMeta, cr *localv1.LocalVolume) bool {
	changed := false
	if meta.Labels == nil {
		meta.Labels = map[string]string{}
		changed = true
	}
	if v, exists := meta.Labels[ownerNamespaceLabel]; !exists || v != cr.Namespace {
		meta.Labels[ownerNamespaceLabel] = cr.Namespace
		changed = true
	}
	if v, exists := meta.Labels[ownerNameLabel]; !exists || v != cr.Name {
		meta.Labels[ownerNameLabel] = cr.Name
		changed = true
	}

	return changed
}

//+kubebuilder:rbac:groups=local.storage.openshift.io,namespace=default,resources=*,verbs=get;list;watch;create;update
//+kubebuilder:rbac:groups=security.openshift.io,resources=securitycontextconstraints,verbs=use,resourceNames=privileged
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
//+kubebuilder:rbac:groups="";storage.k8s.io,resources=configmaps;storageclasses;persistentvolumeclaims;persistentvolumes,verbs=*

func (r *LocalVolumeReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("request.namespace", request.Namespace, "Request.Name", request.Name)
	logger.Info("Reconciling LocalVolume")

	lv := &localv1.LocalVolume{}
	err := r.Client.Get(ctx, request.NamespacedName, lv)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Return and don't requeue
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	r.localVolume = lv

	// don't provision for deleted lvs
	if !lv.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// ignore LocalVolumes whose LabelSelector doesn't match this node
	// NodeSelectorTerms.MatchExpressions are ORed

	r.runtimeConfig.Node = &corev1.Node{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: os.Getenv("MY_NODE_NAME")}, r.runtimeConfig.Node)
	if err != nil {
		return ctrl.Result{}, err
	}

	matches, err := common.NodeSelectorMatchesNodeLabels(r.runtimeConfig.Node, lv.Spec.NodeSelector)
	if err != nil {
		logger.Error(err, "failed to match nodeSelector to node labels")
		return ctrl.Result{}, err
	}

	if !matches {
		return ctrl.Result{}, nil
	}

	// get associated provisioner config
	cm := &corev1.ConfigMap{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: common.ProvisionerConfigMapName, Namespace: request.Namespace}, cm)
	if err != nil {
		logger.Error(err, "could not get provisioner configmap")
		return ctrl.Result{}, err
	}

	// read provisioner config
	provisionerConfig := staticProvisioner.ProvisionerConfiguration{}
	staticProvisioner.ConfigMapDataToVolumeConfig(cm.Data, &provisionerConfig)

	r.runtimeConfig.DiscoveryMap = provisionerConfig.StorageClassConfig
	r.runtimeConfig.NodeLabelsForPV = provisionerConfig.NodeLabelsForPV
	r.runtimeConfig.Namespace = request.Namespace
	r.runtimeConfig.SetPVOwnerRef = provisionerConfig.SetPVOwnerRef
	r.runtimeConfig.Name = common.GetProvisionedByValue(*r.runtimeConfig.Node)

	// ignored by our implementation of static-provisioner,
	// but not by deleter (if applicable)
	r.runtimeConfig.UseNodeNameOnly = provisionerConfig.UseNodeNameOnly
	r.runtimeConfig.MinResyncPeriod = provisionerConfig.MinResyncPeriod
	r.runtimeConfig.UseAlphaAPI = provisionerConfig.UseAlphaAPI
	r.runtimeConfig.LabelsForPV = provisionerConfig.LabelsForPV

	// unsupported
	r.runtimeConfig.UseJobForCleaning = false

	err = os.MkdirAll(r.symlinkLocation, 0755)
	if err != nil {
		logger.Error(err, " error creating local-storage directory ", "symLinkLocation", r.symlinkLocation)
		os.Exit(-1)
	}
	diskConfig := r.generateConfig()
	// run command lsblk --all --noheadings --pairs --output "KNAME,PKNAME,TYPE,MOUNTPOINT"
	// the reason we are using KNAME instead of NAME is because for lvm disks(and may be others)
	// the NAME and device file in /dev directory do not match.
	cmd := exec.Command("lsblk", "--all", "--noheadings", "--pairs", "--output", "KNAME,PKNAME,TYPE,MOUNTPOINT")
	var out bytes.Buffer
	cmd.Stdout = &out
	err = cmd.Run()
	if err != nil {
		msg := " error running lsblk"
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorRunningBlockList, msg, "", corev1.EventTypeWarning))
		logger.Error(err, msg)
		return ctrl.Result{}, err
	}

	// list block devices
	blockDevices, badRows, err := internal.ListBlockDevices()
	if err != nil {
		msg := fmt.Sprintf("failed to list block devices: %v", err)
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorRunningBlockList, msg, "", corev1.EventTypeWarning))
		logger.Error(err, msg, "lsblk.BadRows", badRows)
		return ctrl.Result{}, err
	} else if len(badRows) > 0 {
		msg := "error parsing rows "
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorRunningBlockList, msg, "", corev1.EventTypeWarning))
		logger.Error(err, "could not parse all the lsblk rows", "lsblk.BadRows", badRows)
	}

	validBlockDevices := make([]internal.BlockDevice, 0)
	for _, blockDevice := range blockDevices {
		if ignoreDevices(blockDevice, logger) {
			continue
		}
		validBlockDevices = append(validBlockDevices, blockDevice)
	}

	if len(validBlockDevices) == 0 {
		logger.Info("unable to find any new disks")
		return ctrl.Result{}, nil
	}

	allDiskIds, err := filepath.Glob(diskByIDPath)
	if err != nil {
		msg := " error listing disks in /dev/disk/by-id"
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorListingDeviceID, msg, "", corev1.EventTypeWarning))
		logger.Error(err, msg)
		return ctrl.Result{}, nil
	}

	deviceMap, err := r.findMatchingDisks(diskConfig, validBlockDevices, allDiskIds)
	if err != nil {
		msg := " error finding matching disks"
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, "", corev1.EventTypeWarning))
		logger.Error(err, msg)
		return ctrl.Result{}, nil
	}

	if len(deviceMap) == 0 {
		msg := ""
		if len(diskConfig.Disks) == 0 {
			// Note that this scenario shouldn't be possible, as diskConfig.Disks is a required attribute
			msg = "devicePaths was left empty; this attribute must be defined in the LocalVolume spec"
		} else {
			// We go through and build up a set to display the differences between expected devices
			// and invalid formatted entries (such as /tmp)
			diff := sets.NewString()
			for _, v := range diskConfig.Disks {
				set := sets.NewString()
				for _, devicePath := range v.DevicePaths {
					set.Insert(devicePath)
				}
				diff = set.Difference(v.DeviceNames())
			}
			msg = strings.Join(diff.List(), ", ") + " was defined in devicePaths, but expected a path in /dev/"
		}
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, "", corev1.EventTypeWarning))
		logger.Info(msg)
		return ctrl.Result{}, nil
	}

	mountPointMap, err := common.GenerateMountMap(r.runtimeConfig)
	if err != nil {
		logger.Error(err, "failed to generate mountPointMap")
		return ctrl.Result{}, err
	}

	var errors []error

	for storageClassName, deviceArray := range deviceMap {
		var totalProvisionedPVs int
		var blockDeviceList []internal.BlockDevice
		symLinkDirPath := path.Join(r.symlinkLocation, storageClassName)
		for _, deviceNameLocation := range deviceArray {
			blockDeviceList = append(blockDeviceList, deviceNameLocation.blockDevice)
			devLogger := logger.WithValues("Device.Name", deviceNameLocation.diskNamePath)
			source, target, idExists, err := common.GetSymLinkSourceAndTarget(deviceNameLocation.blockDevice, symLinkDirPath)
			if err != nil {
				logger.Error(err, "failed to get symlink source and target")
				errors = append(errors, err)
				break
			}
			shouldCreatePV := r.createSymlink(deviceNameLocation, source, target, idExists)
			if shouldCreatePV {
				storageClass := &storagev1.StorageClass{}
				err := r.Client.Get(ctx, types.NamespacedName{Name: storageClassName}, storageClass)
				if err != nil {
					devLogger.Error(err, "failed to fetch storageClass")
					errors = append(errors, err)
					break
				}
				lvOwnerLabels := map[string]string{
					common.LocalVolumeOwnerNameForPV:      r.localVolume.Name,
					common.LocalVolumeOwnerNamespaceForPV: r.localVolume.Namespace,
				}

				err = common.CreateLocalPV(
					lv,
					r.runtimeConfig,
					r.cleanupTracker,
					devLogger,
					*storageClass,
					mountPointMap,
					r.Client,
					target,
					filepath.Base(deviceNameLocation.diskNamePath),
					idExists,
					lvOwnerLabels,
				)
				if err != nil {
					devLogger.Error(err, "could not create local PV")
					errors = append(errors, err)
					break
				}

				totalProvisionedPVs += 1
			}
		}

		// update metrics for total persistent volumes provisioned
		localmetrics.SetLVProvisionedPVMetric(nodeName, storageClassName, totalProvisionedPVs)

		orphanSymlinkDevices, err := internal.GetOrphanedSymlinks(symLinkDirPath, blockDeviceList)
		if err != nil {
			logger.Error(err, "failed to get orphaned symlink devices in current reconcile")
		}

		if len(orphanSymlinkDevices) > 0 {
			logger.Info("found orphan symlinked devices in current reconcile",
				"storageClass.Name", storageClassName, "orphanedDevices", orphanSymlinkDevices)
		}

		// update metrics for orphaned symlink devices
		localmetrics.SetLVOrphanedSymlinksMetric(nodeName, storageClassName, len(orphanSymlinkDevices))
	}

	return ctrl.Result{Requeue: true, RequeueAfter: checkDuration}, nil
}

func ignoreDevices(dev internal.BlockDevice, log logr.Logger) bool {
	if hasBindMounts, _, err := dev.HasBindMounts(); err != nil || hasBindMounts {
		log.Info("ignoring mount device", "deviceName", dev.Name)
		return true
	}

	if hasChildren, err := dev.HasChildren(); err != nil || hasChildren {
		log.Info("ignoring root device", "deviceName", dev.Name)
		return true
	}

	return false
}

func (r *LocalVolumeReconciler) findMatchingDisks(diskConfig *DiskConfig, blockDevices []internal.BlockDevice, allDiskIds []string) (map[string][]DiskLocation, error) {

	// blockDeviceMap is a map of storageclass and device locations
	blockDeviceMap := make(map[string][]DiskLocation)

	addDiskToMap := func(scName, stableDeviceID, diskName string, blockDevice internal.BlockDevice) {
		deviceArray, ok := blockDeviceMap[scName]
		if !ok {
			deviceArray = []DiskLocation{}
		}
		deviceArray = append(deviceArray, DiskLocation{diskName, stableDeviceID, blockDevice})
		blockDeviceMap[scName] = deviceArray
	}
	for storageClass, disks := range diskConfig.Disks {
		// handle diskNames
		deviceNames := disks.DeviceNames().List()
		for _, diskName := range deviceNames {
			baseDeviceName := filepath.Base(diskName)
			blockDevice, matched := hasExactDisk(blockDevices, baseDeviceName)
			if matched {
				matchedDeviceID, err := r.findStableDeviceID(baseDeviceName, allDiskIds)
				// This means no /dev/disk/by-id entry was created for requested device.
				if err != nil {
					r.Log.Error(err, " unable to find disk ID for local pool", "diskName", diskName)
					addDiskToMap(storageClass, "", diskName, blockDevice)
					continue
				}
				addDiskToMap(storageClass, matchedDeviceID, diskName, blockDevice)
				continue
			} else {
				if !fileExists(diskName) {
					msg := "no file exists for specified device "
					r.Log.Info(msg, "diskName", diskName)
					continue
				}
				fileMode, err := os.Stat(diskName)
				if err != nil {
					r.Log.Error(err, "error attempting to examine ", "diskName", diskName)
					continue
				}
				msg := ""
				switch mode := fileMode.Mode(); {
				case mode.IsDir():
					msg = fmt.Sprintf("unable to use directory %v for local storage. Use an existing block device.", diskName)
				case mode.IsRegular():
					msg = fmt.Sprintf("unable to use regular file %v for local storage. Use an existing block device.", diskName)
				default:
					msg = fmt.Sprintf("unable to find matching disk %v", diskName)
				}
				r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, diskName, corev1.EventTypeWarning))
				r.Log.Info(msg)
			}
		}

		deviceIds := disks.DeviceIDs().List()
		// handle DeviceIDs
		for _, deviceID := range deviceIds {
			matchedDeviceID, matchedDiskName, err := r.findDeviceByID(deviceID)
			if err != nil {
				msg := fmt.Sprintf("unable to add disk-id %s to local disk pool: %v", deviceID, err)
				r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, deviceID, corev1.EventTypeWarning))
				r.Log.Error(err, " unable to add disk-id to local disk pool", "Device-id", deviceID)
				continue
			}
			baseDeviceName := filepath.Base(matchedDiskName)
			// We need to make sure that requested device is not already mounted.
			blockDevice, matched := hasExactDisk(blockDevices, baseDeviceName)
			if matched {
				addDiskToMap(storageClass, matchedDeviceID, matchedDiskName, blockDevice)
			}
		}
	}
	return blockDeviceMap, nil
}

// findDeviceByID finds device ID and return device name(such as sda, sdb) and complete deviceID path
func (r *LocalVolumeReconciler) findDeviceByID(deviceID string) (string, string, error) {
	diskDevPath, err := filepath.EvalSymlinks(deviceID)
	if err != nil {
		return "", "", fmt.Errorf("unable to find device at path %s: %v", deviceID, err)
	}
	return deviceID, diskDevPath, nil
}

func (r *LocalVolumeReconciler) findStableDeviceID(diskName string, allDisks []string) (string, error) {
	for _, diskIDPath := range allDisks {
		diskDevPath, err := filepath.EvalSymlinks(diskIDPath)
		if err != nil {
			continue
		}
		diskDevName := filepath.Base(diskDevPath)
		if diskDevName == diskName {
			return diskIDPath, nil
		}
	}
	return "", fmt.Errorf("unable to find ID of disk %s", diskName)
}

func hasExactDisk(blockDevices []internal.BlockDevice, device string) (internal.BlockDevice, bool) {
	for _, blockDevice := range blockDevices {
		if blockDevice.KName == device {
			return blockDevice, true
		}
	}
	return internal.BlockDevice{}, false
}

// fileExists checks if a file exists
func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return true
}

type LocalVolumeReconciler struct {
	Client          client.Client
	Scheme          *runtime.Scheme
	Log             logr.Logger
	symlinkLocation string
	localVolume     *localv1.LocalVolume
	eventSync       *eventReporter

	// static-provisioner stuff
	cleanupTracker *provDeleter.CleanupStatusTracker
	runtimeConfig  *provCommon.RuntimeConfig
	deleter        *provDeleter.Deleter
	firstRunOver   bool
}

func (r *LocalVolumeReconciler) SetupWithManager(mgr ctrl.Manager, cleanupTracker *provDeleter.CleanupStatusTracker, pvCache *provCache.VolumeCache) error {
	clientSet := provCommon.SetupClient()
	runtimeConfig := &provCommon.RuntimeConfig{
		UserConfig: &provCommon.UserConfig{
			Node: &corev1.Node{},
		},
		Cache:    pvCache,
		VolUtil:  provUtil.NewVolumeUtil(),
		APIUtil:  provUtil.NewAPIUtil(clientSet),
		Client:   clientSet,
		Recorder: mgr.GetEventRecorderFor(ComponentName),
		Mounter:  mount.New("" /* defaults to /bin/mount */),
		// InformerFactory: , // unused

	}

	r.runtimeConfig = runtimeConfig
	r.eventSync = newEventReporter(mgr.GetEventRecorderFor(ComponentName))
	r.symlinkLocation = common.GetLocalDiskLocationPath()
	r.deleter = provDeleter.NewDeleter(runtimeConfig, cleanupTracker)
	r.cleanupTracker = cleanupTracker

	return ctrl.NewControllerManagedBy(mgr).
		// set to 1 explicitly, despite it being the default, as the reconciler is not thread-safe.
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		For(&localv1.LocalVolume{}).
		Watches(&source.Kind{Type: &corev1.ConfigMap{}}, &handler.EnqueueRequestForOwner{
			OwnerType: &localv1.LocalVolume{},
		}).
		// TODO enqueue for the PV based on labels
		// update owned-pv cache used by provisioner/deleter libs and enequeue owning lvset
		// only the cache is touched by
		Watches(&source.Kind{Type: &corev1.PersistentVolume{}}, &handler.Funcs{
			GenericFunc: func(e event.GenericEvent, q workqueue.RateLimitingInterface) {
				pv, ok := e.Object.(*corev1.PersistentVolume)
				if ok {
					handlePVChange(runtimeConfig, pv, q, false)
				}
			},
			CreateFunc: func(e event.CreateEvent, q workqueue.RateLimitingInterface) {
				pv, ok := e.Object.(*corev1.PersistentVolume)
				if ok {
					handlePVChange(runtimeConfig, pv, q, false)
				}
			},
			UpdateFunc: func(e event.UpdateEvent, q workqueue.RateLimitingInterface) {
				pv, ok := e.ObjectNew.(*corev1.PersistentVolume)
				if ok {
					handlePVChange(runtimeConfig, pv, q, false)
				}
			},
			DeleteFunc: func(e event.DeleteEvent, q workqueue.RateLimitingInterface) {
				pv, ok := e.Object.(*corev1.PersistentVolume)
				if ok {
					handlePVChange(runtimeConfig, pv, q, true)
				}
			},
		}).
		Complete(r)
}

func handlePVChange(runtimeConfig *provCommon.RuntimeConfig, pv *corev1.PersistentVolume, q workqueue.RateLimitingInterface, isDelete bool) {
	// skip non-owned PVs
	name, found := pv.Annotations[provCommon.AnnProvisionedBy]
	if !found || name != runtimeConfig.Name {
		return
	}

	// enqueue owner
	ownerName, found := pv.Labels[common.PVOwnerNameLabel]
	if !found {
		return
	}
	ownerNamespace, found := pv.Labels[common.PVOwnerNamespaceLabel]
	if !found {
		return
	}
	ownerKind, found := pv.Labels[common.PVOwnerKindLabel]
	if ownerKind != localv1.LocalVolumeKind || !found {
		return
	}

	q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: ownerName, Namespace: ownerNamespace}})

}
