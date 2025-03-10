package lv

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	localv1 "github.com/openshift/local-storage-operator/api/v1"
	"github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/diskmaker"
	"github.com/openshift/local-storage-operator/pkg/internal"
	"github.com/openshift/local-storage-operator/pkg/localmetrics"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	provDeleter "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/deleter"
)

// DiskMaker is a small utility that reads configmap and
// creates and symlinks disks in location from which local-storage-provisioner can access.
// It also ensures that only stable device names are used.

const (
	ownerNamespaceLabel = "local.storage.openshift.io/owner-namespace"
	ownerNameLabel      = "local.storage.openshift.io/owner-name"
	// ComponentName for lv symlinker
	ComponentName      = "localvolume-symlink-controller"
	diskByIDPath       = "/dev/disk/by-id/*"
	diskByIDPrefix     = "/dev/disk/by-id"
	defaultRequeueTime = time.Minute
	fastRequeueTime    = 5 * time.Second
)

var nodeName string
var watchNamespace string

func init() {
	nodeName = common.GetNodeNameEnvVar()
	watchNamespace, _ = common.GetWatchNamespace()
}

type DiskLocation struct {
	// diskNamePath stores full device name path - "/dev/sda"
	diskNamePath string
	// path that was supplied by the user in LocalVolume CR
	userProvidedPath string
	diskID           string
	blockDevice      internal.BlockDevice
	forceWipe        bool
}

type LocalVolumeReconciler struct {
	Client          client.Client
	Scheme          *runtime.Scheme
	symlinkLocation string
	localVolume     *localv1.LocalVolume
	eventSync       *eventReporter
	cacheSynced     bool

	// static-provisioner stuff
	cleanupTracker *provDeleter.CleanupStatusTracker
	runtimeConfig  *provCommon.RuntimeConfig
	deleter        *provDeleter.Deleter
	fsInterface    FileSystemInterface
	firstRunOver   bool
}

func (r *LocalVolumeReconciler) createSymlink(
	deviceNameLocation DiskLocation,
	symLinkSource string,
	symLinkTarget string,
	idExists bool,
) bool {
	diskDevPath, err := r.fsInterface.evalSymlink(symLinkSource)
	if err != nil {
		klog.ErrorS(err, "failed to evaluated symlink", "symlinkSource", symLinkSource)
		return false
	}

	// get PV creation lock which checks for existing symlinks to this device
	pvLock, pvLocked, existingSymlinks, err := internal.GetPVCreationLock(
		diskDevPath,
		r.symlinkLocation,
	)

	unlockFunc := func() {
		err := pvLock.Unlock()
		if err != nil {
			klog.ErrorS(err, "failed to unlock device")
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
		klog.Error(msg)
		return false
	} else if err != nil || !pvLocked { // locking failed for some other reasion
		msg := fmt.Sprintf("not symlinking, locking failed: %+v", err)
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorCreatingSymLink, msg, symLinkTarget, corev1.EventTypeWarning))
		klog.Error(msg)
		return false
	}

	symLinkDir := filepath.Dir(symLinkTarget)

	err = os.MkdirAll(symLinkDir, 0755)
	if err != nil {
		msg := fmt.Sprintf("error creating symlink dir %s: %v", symLinkDir, err)
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, symLinkTarget, corev1.EventTypeWarning))
		klog.Error(msg)
		return false
	}

	if fileExists(symLinkTarget) {
		klog.V(4).Infof("symlink %s already exists", symLinkTarget)
		return true
	}

	// if device has no disk/by-id and there is an existing PV for same device
	// then we should skip trying to create symlink for this device
	if !idExists && r.checkForExistingPV(deviceNameLocation) {
		msg := fmt.Sprintf("error creating symlink, no disk/by-id found and found an existing PV for same device %s: %v", symLinkTarget, err)
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, symLinkSource, corev1.EventTypeWarning))
		klog.Error(msg)
		return false
	}

	if deviceNameLocation.forceWipe {
		cmd := exec.Command("wipefs", "-a", "-f", symLinkSource)
		err = cmd.Run()
		if err != nil {
			msg := fmt.Sprintf("error wipefs device %s: %v", symLinkSource, err)
			r.eventSync.Report(r.localVolume, newDiskEvent(ErrorCreatingSymLink, msg, symLinkSource, corev1.EventTypeWarning))
			klog.Error(msg)
			return false
		}
	}

	err = os.Symlink(symLinkSource, symLinkTarget)
	if err != nil {
		msg := fmt.Sprintf("error creating symlink %s: %v", symLinkTarget, err)
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, symLinkSource, corev1.EventTypeWarning))
		klog.Error(msg)
		return false
	}

	if !idExists {
		msg := fmt.Sprintf("created symlink on device name %s with no disk/by-id. device name might not persist on reboot", symLinkSource)
		r.eventSync.Report(r.localVolume, newDiskEvent(SymLinkedOnDeviceName, msg, symLinkSource, corev1.EventTypeWarning))
		klog.Info(msg)
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

func (r *LocalVolumeReconciler) checkForExistingPV(deviceNameLocation DiskLocation) bool {
	pvs := r.runtimeConfig.Cache.ListPVs()
	nodeLabels := r.runtimeConfig.Node.GetLabels()

	hostname, found := nodeLabels[corev1.LabelHostname]
	if !found {
		return false
	}

	deviceName := filepath.Base(deviceNameLocation.diskNamePath)

	for i := range pvs {
		pv := pvs[i]
		pvLabels := pv.GetLabels()
		pvAnnotations := pv.GetAnnotations()
		// if this PV has same hostname and same device name, we should not use the device for
		// creating a new PV
		if pvLabels[corev1.LabelHostname] == hostname && pvAnnotations[common.PVDeviceNameLabel] == deviceName {
			return true
		}
	}
	return false
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
		disks.ForceWipeDevicesAndDestroyAllData = storageClassDevice.ForceWipeDevicesAndDestroyAllData
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
	requeueTime := defaultRequeueTime

	lv := &localv1.LocalVolume{}
	err := r.Client.Get(ctx, request.NamespacedName, lv)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Return and don't requeue
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}
	r.localVolume = lv

	klog.InfoS("Reconciling LocalVolume", "namespace", request.Namespace, "name", request.Name)

	err = common.ReloadRuntimeConfig(ctx, r.Client, request, os.Getenv("MY_NODE_NAME"), r.runtimeConfig)
	if err != nil {
		return ctrl.Result{}, err
	}

	if !r.cacheSynced {
		pvList := &corev1.PersistentVolumeList{}
		err := r.Client.List(context.TODO(), pvList)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to initialize PV cache: %w", err)
		}
		for _, pv := range pvList.Items {
			// skip non-owned PVs
			if !common.PVMatchesProvisioner(&pv, r.runtimeConfig.Name) ||
				!common.IsLocalVolumePV(&pv) {
				continue
			}
			common.AddOrUpdatePV(r.runtimeConfig, &pv)
		}
		r.cacheSynced = true
	}

	// ignore LocalVolumes whose LabelSelector doesn't match this node
	// NodeSelectorTerms.MatchExpressions are ORed
	matches, err := common.NodeSelectorMatchesNodeLabels(r.runtimeConfig.Node, lv.Spec.NodeSelector)
	if err != nil {
		klog.ErrorS(err, "failed to match nodeSelector to node labels")
		return ctrl.Result{}, err
	}

	if !matches {
		return ctrl.Result{}, nil
	}

	// Delete PV's before creating new ones
	klog.InfoS("Looking for released PVs to cleanup", "namespace", request.Namespace, "name", request.Name)
	r.deleter.DeletePVs()

	// Cleanup symlinks for deleted PV's
	klog.InfoS("Looking for symlinks to cleanup", "namespace", request.Namespace, "name", request.Name)
	ownerLabels := map[string]string{
		common.PVOwnerKindLabel:      localv1.LocalVolumeKind,
		common.PVOwnerNamespaceLabel: lv.Namespace,
		common.PVOwnerNameLabel:      lv.Name,
	}
	err = common.CleanupSymlinks(r.Client, r.runtimeConfig, ownerLabels,
		func() bool {
			// Only delete the symlink if the owner LV is deleted.
			return !lv.DeletionTimestamp.IsZero()
		})
	if err != nil {
		msg := fmt.Sprintf("failed to cleanup symlinks: %v", err)
		r.eventSync.Report(r.localVolume, newDiskEvent(diskmaker.ErrorRemovingSymLink, msg, "", corev1.EventTypeWarning))
		klog.Error(msg)
		return ctrl.Result{}, err
	}

	// don't provision for deleted lvs
	if !lv.DeletionTimestamp.IsZero() {
		// If there are released PV's for this owner in the cache, use
		// the fast requeue time, as it implies a cleanup job may be in
		// progress and we should call DeletePVs() again soon to check
		// for job completion and to delete the PV.
		if common.OwnerHasReleasedPVs(r.runtimeConfig, ownerLabels) {
			requeueTime = fastRequeueTime
		}
		return ctrl.Result{Requeue: true, RequeueAfter: requeueTime}, nil
	}

	klog.InfoS("Looking for valid block devices", "namespace", request.Namespace, "name", request.Name)

	err = os.MkdirAll(r.symlinkLocation, 0755)
	if err != nil {
		klog.ErrorS(err, "error creating local-storage directory", "symLinkLocation", r.symlinkLocation)
		return ctrl.Result{}, err
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
		msg := fmt.Sprintf("error running lsblk: %v", err)
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorRunningBlockList, msg, "", corev1.EventTypeWarning))
		klog.Error(msg)
		return ctrl.Result{}, err
	}

	// compose the list of devices from diskConfig
	devices := make([]string, 0)
	for _, disks := range diskConfig.Disks {
		devicePaths := disks.DevicePaths
		for _, devicePath := range devicePaths {
			devices = append(devices, devicePath)
		}
	}

	// list block devices
	blockDevices, badRows, err := internal.ListBlockDevices(devices)
	if err != nil {
		msg := fmt.Sprintf("failed to list block devices: %v", err)
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorRunningBlockList, msg, "", corev1.EventTypeWarning))
		klog.Error(msg)
		return ctrl.Result{}, err
	} else if len(badRows) > 0 {
		msg := fmt.Sprintf("error parsing rows: %+v", badRows)
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorRunningBlockList, msg, "", corev1.EventTypeWarning))
		klog.Error(msg)
	}

	validBlockDevices := make([]internal.BlockDevice, 0)
	for _, blockDevice := range blockDevices {
		if ignoreDevices(blockDevice) {
			continue
		}
		validBlockDevices = append(validBlockDevices, blockDevice)
	}

	if len(validBlockDevices) == 0 {
		klog.V(3).Info("unable to find any new disks")
		return ctrl.Result{}, nil
	}

	deviceMap, err := r.findMatchingDisks(diskConfig, validBlockDevices)
	if err != nil {
		msg := fmt.Sprintf("error finding matching disks: %v", err)
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, "", corev1.EventTypeWarning))
		klog.Error(msg)
		return ctrl.Result{}, nil
	}

	if len(deviceMap) == 0 {
		msg := "found empty matching device list"
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, "", corev1.EventTypeWarning))
		klog.Info(msg)
		return ctrl.Result{}, nil
	}

	mountPointMap, err := common.GenerateMountMap(r.runtimeConfig)
	if err != nil {
		klog.ErrorS(err, "failed to generate mountPointMap")
		return ctrl.Result{}, err
	}

	var errors []error

	for storageClassName, deviceArray := range deviceMap {
		var totalProvisionedPVs int
		var blockDeviceList []internal.BlockDevice
		symLinkDirPath := path.Join(r.symlinkLocation, storageClassName)
		for _, deviceNameLocation := range deviceArray {
			blockDeviceList = append(blockDeviceList, deviceNameLocation.blockDevice)
			source, target, idExists, err := getSymlinkSourceAndTarget(deviceNameLocation, symLinkDirPath)
			if err != nil {
				klog.ErrorS(err, "failed to get symlink source and target",
					"deviceNameLocation", deviceNameLocation)
				errors = append(errors, err)
				break
			}
			shouldCreatePV := r.createSymlink(deviceNameLocation, source, target, idExists)
			if shouldCreatePV {
				storageClass := &storagev1.StorageClass{}
				err := r.Client.Get(ctx, types.NamespacedName{Name: storageClassName}, storageClass)
				if err != nil {
					klog.ErrorS(err, "failed to fetch storageClass",
						"deviceNameLocation", deviceNameLocation)
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
					*storageClass,
					mountPointMap,
					r.Client,
					target,
					filepath.Base(deviceNameLocation.diskNamePath),
					idExists,
					lvOwnerLabels,
				)
				if err == common.ErrTryAgain {
					requeueTime = fastRequeueTime
				} else if err != nil {
					klog.ErrorS(err, "could not create local PV",
						"deviceNameLocation", deviceNameLocation)
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
			klog.ErrorS(err, "failed to get orphaned symlink devices in current reconcile")
		}

		if len(orphanSymlinkDevices) > 0 {
			klog.InfoS("found orphan symlinked devices in current reconcile",
				"scName", storageClassName, "orphanedDevices", orphanSymlinkDevices)
		}

		// update metrics for orphaned symlink devices
		localmetrics.SetLVOrphanedSymlinksMetric(nodeName, storageClassName, len(orphanSymlinkDevices))
	}

	return ctrl.Result{Requeue: true, RequeueAfter: requeueTime}, nil
}

func getSymlinkSourceAndTarget(devLocation DiskLocation, symlinkDir string) (string, string, bool, error) {
	if devLocation.diskID != "" {
		target := path.Join(symlinkDir, filepath.Base(devLocation.diskID))
		return devLocation.diskID, target, true, nil
	} else {
		target := path.Join(symlinkDir, filepath.Base(devLocation.userProvidedPath))
		return devLocation.userProvidedPath, target, false, nil
	}
}

func ignoreDevices(dev internal.BlockDevice) bool {
	if hasBindMounts, _, err := dev.HasBindMounts(); err != nil || hasBindMounts {
		klog.InfoS("ignoring mount device", "devName", dev.Name)
		return true
	}

	if hasChildren, err := dev.HasChildren(); err != nil || hasChildren {
		klog.InfoS("ignoring root device", "devName", dev.Name)
		return true
	}

	return false
}

func (r *LocalVolumeReconciler) findMatchingDisks(diskConfig *DiskConfig, blockDevices []internal.BlockDevice) (map[string][]DiskLocation, error) {

	// blockDeviceMap is a map of storageclass and device locations
	blockDeviceMap := make(map[string][]DiskLocation)
	addDiskToMap := func(scName, stableDeviceID, diskName, userDevicePath string, blockDevice internal.BlockDevice, forceWipe bool) {
		deviceArray, ok := blockDeviceMap[scName]
		if !ok {
			deviceArray = []DiskLocation{}
		}
		deviceArray = append(deviceArray, DiskLocation{diskName, userDevicePath, stableDeviceID, blockDevice, forceWipe})
		blockDeviceMap[scName] = deviceArray
	}

	for storageClass, disks := range diskConfig.Disks {
		devicePaths := disks.DevicePaths
		forceWipe := disks.ForceWipeDevicesAndDestroyAllData
		for _, devicePath := range devicePaths {
			// handle user provided device_ids first
			if strings.HasPrefix(devicePath, diskByIDPrefix) {
				matchedDeviceID, matchedDiskName, err := r.findDeviceByID(devicePath)
				if err != nil {
					msg := fmt.Sprintf("unable to add disk-id %s to local disk pool: %v", devicePath, err)
					r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, devicePath, corev1.EventTypeWarning))
					klog.Error(msg)
					continue
				}
				baseDeviceName := filepath.Base(matchedDiskName)
				// We need to make sure that requested device is not already mounted.
				blockDevice, matched := hasExactDisk(blockDevices, baseDeviceName)
				if matched {
					addDiskToMap(storageClass, matchedDeviceID, matchedDiskName, devicePath, blockDevice, forceWipe)
				}
			} else {
				// handle anything other than device ids here - such as:
				//   /dev/sda
				//   /dev/sandbox/local
				//   /dev/disk/by-path/ww-xx

				// Evaluate symlink in case the device is a LVM device or something else
				diskDevPath, err := r.fsInterface.evalSymlink(devicePath)
				if err != nil {
					msg := fmt.Sprintf("unable to add disk %s to local disk pool: %v", devicePath, err)
					r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, devicePath, corev1.EventTypeWarning))
					klog.Error(msg)
					continue
				}
				baseDeviceName := filepath.Base(diskDevPath)
				blockDevice, matched := hasExactDisk(blockDevices, baseDeviceName)
				if matched {
					matchedDeviceID, err := blockDevice.GetPathByID("" /*existing symlinkpath */)
					// This means no /dev/disk/by-id entry was created for requested device.
					if err != nil {
						klog.ErrorS(err, "unable to find disk ID for local pool",
							"diskName", diskDevPath)
						addDiskToMap(storageClass, "", diskDevPath, devicePath, blockDevice, forceWipe)
						continue
					}
					addDiskToMap(storageClass, matchedDeviceID, diskDevPath, devicePath, blockDevice, forceWipe)
					continue
				} else {
					r.logDeviceError(diskDevPath)
				}
			}
		}
	}

	return blockDeviceMap, nil
}

func (r *LocalVolumeReconciler) logDeviceError(diskDevPath string) {
	if !fileExists(diskDevPath) {
		klog.InfoS("no file exists for device", "diskName", diskDevPath)
		return
	}
	fileMode, err := os.Stat(diskDevPath)
	if err != nil {
		klog.ErrorS(err, "error attempting to stat", "diskName", diskDevPath)
		return
	}
	msg := ""
	switch mode := fileMode.Mode(); {
	case mode.IsDir():
		msg = fmt.Sprintf("unable to use directory %v for local storage. Use an existing block device.", diskDevPath)
	case mode.IsRegular():
		msg = fmt.Sprintf("unable to use regular file %v for local storage. Use an existing block device.", diskDevPath)
	default:
		msg = fmt.Sprintf("unable to find matching disk %v", diskDevPath)
	}
	r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, diskDevPath, corev1.EventTypeWarning))
	klog.Info(msg)
}

// findDeviceByID finds device ID and return device name(such as sda, sdb) and complete deviceID path
func (r *LocalVolumeReconciler) findDeviceByID(deviceID string) (string, string, error) {
	diskDevPath, err := r.fsInterface.evalSymlink(deviceID)
	if err != nil {
		return "", "", fmt.Errorf("unable to find device at path %s: %v", deviceID, err)
	}
	return deviceID, diskDevPath, nil
}

func (r *LocalVolumeReconciler) findStableDeviceID(diskName string, allDisks []string) (string, error) {
	for _, diskIDPath := range allDisks {
		diskDevPath, err := r.fsInterface.evalSymlink(diskIDPath)
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

func NewLocalVolumeReconciler(client client.Client, scheme *runtime.Scheme, symlinkLocation string, cleanupTracker *provDeleter.CleanupStatusTracker, rc *provCommon.RuntimeConfig) *LocalVolumeReconciler {
	deleter := provDeleter.NewDeleter(rc, cleanupTracker)

	lvReconciler := &LocalVolumeReconciler{
		Client:          client,
		Scheme:          scheme,
		symlinkLocation: symlinkLocation,
		eventSync:       newEventReporter(rc.Recorder),
		fsInterface:     NixFileSystemInterface{},
		cleanupTracker:  cleanupTracker,
		runtimeConfig:   rc,
		deleter:         deleter,
	}

	return lvReconciler
}

func (r *LocalVolumeReconciler) WithManager(mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		// set to 1 explicitly, despite it being the default, as the reconciler is not thread-safe.
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		For(&localv1.LocalVolume{}).
		Watches(
			&corev1.ConfigMap{},
			handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), &localv1.LocalVolume{})).
		// update owned-pv cache used by provisioner/deleter libs and enequeue owning lvset
		// only the cache is touched by
		Watches(&corev1.PersistentVolume{}, &handler.Funcs{
			GenericFunc: func(ctx context.Context, e event.GenericEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				pv, ok := e.Object.(*corev1.PersistentVolume)
				if ok && common.IsLocalVolumePV(pv) {
					common.HandlePVChange(r.runtimeConfig, pv, q, watchNamespace, false)
				}
			},
			CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				pv, ok := e.Object.(*corev1.PersistentVolume)
				if ok && common.IsLocalVolumePV(pv) {
					common.HandlePVChange(r.runtimeConfig, pv, q, watchNamespace, false)
				}
			},
			UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				pv, ok := e.ObjectNew.(*corev1.PersistentVolume)
				if ok && common.IsLocalVolumePV(pv) {
					common.HandlePVChange(r.runtimeConfig, pv, q, watchNamespace, false)
				}
			},
			DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				pv, ok := e.Object.(*corev1.PersistentVolume)
				if ok && common.IsLocalVolumePV(pv) {
					common.HandlePVChange(r.runtimeConfig, pv, q, watchNamespace, true)
				}
			},
		}).
		Complete(r)

	return err
}
