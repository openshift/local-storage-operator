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
	"k8s.io/apimachinery/pkg/util/sets"
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

type LocalVolumeReconciler struct {
	Client client.Client
	// ClientReader can be used for directly reading from apiserver
	// skipping cache
	ClientReader    client.Reader
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

	effectiveRequeueTime time.Duration
}

func (r *LocalVolumeReconciler) createSymlink(
	deviceNameLocation *internal.DiskLocation,
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

	if deviceNameLocation.ForceWipe {
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
	successMsg := fmt.Sprintf("found matching disk %s with id %s", deviceNameLocation.DiskNamePath, deviceNameLocation.DiskID)
	r.eventSync.Report(r.localVolume, newDiskEvent(FoundMatchingDisk, successMsg, deviceNameLocation.DiskNamePath, corev1.EventTypeNormal))
	return true
}

func diskMakerLabels(crName string) map[string]string {
	return map[string]string{
		"app": fmt.Sprintf("local-volume-diskmaker-%s", crName),
	}
}

func (r *LocalVolumeReconciler) checkForExistingPV(deviceNameLocation *internal.DiskLocation) bool {
	pvs := r.runtimeConfig.Cache.ListPVs()
	nodeLabels := r.runtimeConfig.Node.GetLabels()

	hostname, found := nodeLabels[corev1.LabelHostname]
	if !found {
		return false
	}

	deviceName := filepath.Base(deviceNameLocation.DiskNamePath)

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

func (r *LocalVolumeReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	r.effectiveRequeueTime = defaultRequeueTime

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
		err := r.Client.List(ctx, pvList)
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
			r.effectiveRequeueTime = fastRequeueTime
		}
		return ctrl.Result{Requeue: true, RequeueAfter: r.effectiveRequeueTime}, nil
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
		devices = append(devices, devicePaths...)
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
	ignoredDevices := make([]internal.BlockDevice, 0)

	for _, blockDevice := range blockDevices {
		if ignoreDevices(blockDevice) {
			ignoredDevices = append(ignoredDevices, blockDevice)
			continue
		}
		validBlockDevices = append(validBlockDevices, blockDevice)
	}

	if len(ignoredDevices) > 0 {
		r.processRejectedDevicesForDeviceLinks(ctx, ignoredDevices, diskConfig)
	}

	mountPointMap, err := common.GenerateMountMap(r.runtimeConfig)
	if err != nil {
		klog.ErrorS(err, "failed to generate mountPointMap")
		return ctrl.Result{}, err
	}

	if len(validBlockDevices) > 0 {
		r.processValidDevices(ctx, validBlockDevices, diskConfig, mountPointMap)
	}

	return ctrl.Result{Requeue: true, RequeueAfter: r.effectiveRequeueTime}, nil
}

func (r *LocalVolumeReconciler) processValidDevices(ctx context.Context, validDevices []internal.BlockDevice, diskConfig *DiskConfig, mountPointMap sets.String) {
	for storageClass, disks := range diskConfig.Disks {
		var totalProvisionedPVs int
		var blockDeviceList []internal.BlockDevice

		devicePaths := disks.DevicePaths
		forceWipe := disks.ForceWipeDevicesAndDestroyAllData
		symLinkDirPath := path.Join(r.symlinkLocation, storageClass)

		for _, devicePath := range devicePaths {
			deviceLocation, matched, err := r.resolveValidDeviceLocation(devicePath, forceWipe, validDevices)
			if err != nil {
				r.reportDeviceResolutionError(devicePath, err)
				continue
			}
			if !matched {
				continue
			}

			blockDeviceList = append(blockDeviceList, deviceLocation.BlockDevice)
			if r.provisionValidDevice(ctx, storageClass, symLinkDirPath, devicePath, deviceLocation, mountPointMap) {
				totalProvisionedPVs += 1
			}
		}
		localmetrics.SetLVProvisionedPVMetric(nodeName, storageClass, totalProvisionedPVs)
		orphanSymlinkDevices, err := internal.GetOrphanedSymlinks(symLinkDirPath, blockDeviceList)
		if err != nil {
			klog.ErrorS(err, "failed to get orphaned symlink devices in current reconcile")
		}

		if len(orphanSymlinkDevices) > 0 {
			klog.InfoS("found orphan symlinked devices in current reconcile",
				"scName", storageClass, "orphanedDevices", orphanSymlinkDevices)
		}

		// update metrics for orphaned symlink devices
		localmetrics.SetLVOrphanedSymlinksMetric(nodeName, storageClass, len(orphanSymlinkDevices))
	}
}

func (r *LocalVolumeReconciler) resolveValidDeviceLocation(devicePath string, forceWipe bool, validDevices []internal.BlockDevice) (*internal.DiskLocation, bool, error) {
	deviceLocation := &internal.DiskLocation{
		UserProvidedPath: devicePath,
		ForceWipe:        forceWipe,
	}

	baseDeviceName := ""
	if strings.HasPrefix(devicePath, diskByIDPrefix) {
		matchedDeviceID, matchedDiskName, err := r.findDeviceByID(devicePath)
		if err != nil {
			return nil, false, err
		}
		baseDeviceName = filepath.Base(matchedDiskName)
		deviceLocation.DiskNamePath = matchedDiskName
		deviceLocation.DiskID = matchedDeviceID
	} else {
		diskDevPath, err := r.fsInterface.evalSymlink(devicePath)
		if err != nil {
			return nil, false, err
		}
		baseDeviceName = filepath.Base(diskDevPath)
		deviceLocation.DiskNamePath = diskDevPath
	}

	blockDevice, matched := hasExactDisk(validDevices, baseDeviceName)
	if !matched {
		return nil, false, nil
	}
	deviceLocation.BlockDevice = blockDevice
	return deviceLocation, true, nil
}

func (r *LocalVolumeReconciler) provisionValidDevice(ctx context.Context, storageClass, symLinkDirPath, devicePath string, deviceLocation *internal.DiskLocation, mountPointMap sets.String) bool {
	existingSymlink, err := common.GetSymlinkedForCurrentSC(symLinkDirPath, deviceLocation.BlockDevice.KName)
	if err != nil {
		r.reportSymlinkLookupError(symLinkDirPath, devicePath, err)
		return false
	}

	if existingSymlink != "" {
		if err := r.processExistingSymlink(ctx, storageClass, existingSymlink, deviceLocation, mountPointMap); err != nil {
			r.reportProvisioningError(devicePath, err)
			return false
		}
		return true
	}

	if deviceLocation.DiskID == "" {
		matchedDeviceID, err := deviceLocation.BlockDevice.GetUncachedPathID()
		if err == nil {
			deviceLocation.DiskID = matchedDeviceID
		} else {
			klog.ErrorS(err, "unable to find disk ID for local pool", "diskName", deviceLocation.DiskNamePath)
		}
	}

	provisioned, err := r.processNewSymlink(ctx, storageClass, deviceLocation, mountPointMap)
	if err != nil {
		r.reportProvisioningError(devicePath, err)
		return false
	}
	return provisioned
}

func (r *LocalVolumeReconciler) reportDeviceResolutionError(devicePath string, err error) {
	deviceKind := "disk"
	if strings.HasPrefix(devicePath, diskByIDPrefix) {
		deviceKind = "disk-id"
	}
	msg := fmt.Sprintf("unable to add %s %s to local disk pool: %v", deviceKind, devicePath, err)
	r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, devicePath, corev1.EventTypeWarning))
	klog.Error(msg)
}

func (r *LocalVolumeReconciler) reportSymlinkLookupError(symLinkDirPath, devicePath string, err error) {
	msg := fmt.Sprintf("error listing symlinks in %s: %v", symLinkDirPath, err)
	r.eventSync.Report(r.localVolume, newDiskEvent(ErrorListingDeviceID, msg, devicePath, corev1.EventTypeWarning))
	klog.Error(msg)
}

func (r *LocalVolumeReconciler) reportProvisioningError(devicePath string, err error) {
	r.effectiveRequeueTime = fastRequeueTime
	msg := fmt.Sprintf("unable to provision volumes %s: %v", devicePath, err)
	r.eventSync.Report(r.localVolume, newDiskEvent(ErrorProvisioningVolume, msg, devicePath, corev1.EventTypeWarning))
	klog.Error(msg)
}

func (r *LocalVolumeReconciler) processNewSymlink(ctx context.Context, scName string, diskLocation *internal.DiskLocation, mountPointMap sets.String) (bool, error) {
	symLinkDirPath := path.Join(r.symlinkLocation, scName)
	source, target, idExists, err := getSymlinkSourceAndTarget(diskLocation, symLinkDirPath)
	if err != nil {
		klog.ErrorS(err, "failed to get symlink source and target", "deviceNameLocation", diskLocation)
		return false, err
	}
	diskLocation.SymlinkSource = source
	diskLocation.SymlinkPath = target
	diskLocation.ByIDPathExists = idExists

	shouldCreatePV := r.createSymlink(diskLocation, source, target, idExists)
	if shouldCreatePV {
		return true, r.provisionPV(ctx, scName, diskLocation, mountPointMap)
	}
	return false, nil
}

func (r *LocalVolumeReconciler) processExistingSymlink(
	ctx context.Context, scName string,
	existingSymlinkName string,
	diskLocation *internal.DiskLocation,
	mountPointMap sets.String) error {

	symlinkDirPath := filepath.Join(r.symlinkLocation, scName)
	symlinkPath := filepath.Join(symlinkDirPath, existingSymlinkName)

	// read the current source to which symlink in /mnt/local-storage points to
	effectiveCurrentSource, err := os.Readlink(symlinkPath)
	if err != nil {
		klog.ErrorS(err, "error evaluting symlink", "symlink", symlinkPath)
		return err
	}

	if strings.HasPrefix(effectiveCurrentSource, diskByIDPrefix) {
		diskLocation.ByIDPathExists = true
	}
	diskLocation.SymlinkSource = effectiveCurrentSource
	diskLocation.SymlinkPath = symlinkPath
	return r.provisionPV(ctx, scName, diskLocation, mountPointMap)
}

func (r *LocalVolumeReconciler) provisionPV(ctx context.Context, scName string, deviceNameLocation *internal.DiskLocation, mountPointMap sets.String) error {
	storageClass := &storagev1.StorageClass{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: scName}, storageClass)
	if err != nil {
		klog.ErrorS(err, "failed to fetch storageClass", "deviceNameLocation", deviceNameLocation)
		return fmt.Errorf("error fetching storageclass %s: %w", scName, err)
	}

	lvOwnerLabels := map[string]string{
		common.LocalVolumeOwnerNameForPV:      r.localVolume.Name,
		common.LocalVolumeOwnerNamespaceForPV: r.localVolume.Namespace,
	}
	createLocalPVArgs := common.CreateLocalPVArgs{
		LocalVolumeLikeObject: r.localVolume,
		RuntimeConfig:         r.runtimeConfig,
		StorageClass:          *storageClass,
		MountPointMap:         mountPointMap,
		Client:                r.Client,
		ClientReader:          r.ClientReader,
		SymLinkPath:           deviceNameLocation.SymlinkPath,
		IDExists:              deviceNameLocation.ByIDPathExists,
		ExtraLabelsForPV:      lvOwnerLabels,
		CurrentSymlink:        deviceNameLocation.SymlinkSource,
		BlockDevice:           deviceNameLocation.BlockDevice,
	}

	return common.CreateLocalPV(ctx, createLocalPVArgs)
}

// processRejectedDevicesForDeviceLinks reconciles devices which were rejected for PV creation
// but otherwise matched user specified path in LocalVolume object.
//
// This handles LVDL creation for clusters which were upgraded from older versions of OCP
// and also updates preferredSymlink, fileSystemUUID and validLinks for PVs which are already
// mounted and in-use by kubelet.
//
// This function is called periodically with Reconcile loop every defaultRequeueTime (1 minute)
func (r *LocalVolumeReconciler) processRejectedDevicesForDeviceLinks(ctx context.Context, rejectedDevices []internal.BlockDevice, diskConfig *DiskConfig) {
	for storageClassName, disks := range diskConfig.Disks {
		symLinkDirPath := path.Join(r.symlinkLocation, storageClassName)
		devicePaths := disks.DevicePaths
		for _, devicePath := range devicePaths {
			// if devicePath is possibly a symlink we will evaluate it
			diskDevPath, err := r.fsInterface.evalSymlink(devicePath)
			if err != nil {
				klog.ErrorS(err, "error reading existing symlinks for device",
					"blockDevice", devicePath)
				continue
			}
			baseDeviceName := filepath.Base(diskDevPath)
			blockDevice, matched := hasExactDisk(rejectedDevices, baseDeviceName)
			if !matched {
				continue
			}

			existingSymlink, err := common.GetSymlinkedForCurrentSC(symLinkDirPath, baseDeviceName)
			if err != nil {
				klog.ErrorS(err, "error reading existing symlinks for device",
					"blockDevice", devicePath)
				continue
			}

			if existingSymlink == "" {
				continue
			}

			// since symlinks can change after initial creation, we should evaluate them again
			symlinkPath := filepath.Join(symLinkDirPath, existingSymlink)

			currentLinkTarget, err := os.Readlink(symlinkPath)
			if err != nil {
				klog.ErrorS(err, "failed to read current symlink target", "devicePath", symlinkPath)
				continue
			}

			lvdlName := common.GeneratePVName(existingSymlink, r.runtimeConfig.Node.Name, storageClassName)
			deviceHandler := internal.NewDeviceLinkHandler(currentLinkTarget, r.Client, r.ClientReader)

			lvdl, err := deviceHandler.FindLVDL(ctx, lvdlName, r.runtimeConfig.Namespace)
			if err != nil && !apierrors.IsNotFound(err) {
				klog.ErrorS(err, "error finding lvdl", "lvdl", lvdlName)
			}
			var lvdlError error
			if internal.HasMismatchingSymlink(lvdl) {
				_, lvdlError = deviceHandler.RecreateSymlinkIfNeeded(ctx, lvdl, symlinkPath, blockDevice)
			} else {
				_, lvdlError = deviceHandler.ApplyStatus(ctx, lvdlName, r.runtimeConfig.Namespace, blockDevice, r.localVolume)
			}
			if lvdlError != nil {
				msg := fmt.Errorf("failed to process lvdl %w", lvdlError)
				r.eventSync.Report(r.localVolume, newDiskEvent(diskmaker.FailedLVDLProcessing, msg.Error(), blockDevice.KName, corev1.EventTypeWarning))
				klog.ErrorS(lvdlError, "error updating LocalVolumeDeviceLink", "device", blockDevice.Name)
			}
		}
	}
}

func getSymlinkSourceAndTarget(devLocation *internal.DiskLocation, symlinkDir string) (string, string, bool, error) {
	if devLocation.DiskID != "" {
		target := path.Join(symlinkDir, filepath.Base(devLocation.DiskID))
		return devLocation.DiskID, target, true, nil
	} else {
		target := path.Join(symlinkDir, filepath.Base(devLocation.UserProvidedPath))
		return devLocation.UserProvidedPath, target, false, nil
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

func NewLocalVolumeReconciler(client client.Client, clientReader client.Reader, scheme *runtime.Scheme, symlinkLocation string, cleanupTracker *provDeleter.CleanupStatusTracker, rc *provCommon.RuntimeConfig) *LocalVolumeReconciler {
	deleter := provDeleter.NewDeleter(rc, cleanupTracker)

	lvReconciler := &LocalVolumeReconciler{
		Client:          client,
		ClientReader:    clientReader,
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
