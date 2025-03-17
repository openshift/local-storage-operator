package lvset

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	localv1 "github.com/openshift/local-storage-operator/api/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/diskmaker"
	"github.com/openshift/local-storage-operator/pkg/internal"
	"github.com/openshift/local-storage-operator/pkg/localmetrics"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
	provDeleter "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/deleter"
)

const (
	// ComponentName for lvset symlinker
	ComponentName      = "localvolumeset-symlink-controller"
	pvOwnerKey         = "pvOwner"
	defaultRequeueTime = time.Minute
	fastRequeueTime    = 5 * time.Second
)

var nodeName string
var watchNamespace string

func init() {
	nodeName = common.GetNodeNameEnvVar()
	watchNamespace, _ = common.GetWatchNamespace()
}

// Reconcile reads that state of the cluster for a LocalVolumeSet object and makes changes based on the state read
// and what is in the LocalVolumeSet.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *LocalVolumeSetReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	requeueTime := defaultRequeueTime

	// Fetch the LocalVolumeSet instance
	lvset := &localv1alpha1.LocalVolumeSet{}
	err := r.Client.Get(ctx, request.NamespacedName, lvset)
	if err != nil {
		if kerrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Return and don't requeue
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	klog.InfoS("Reconciling LocalVolumeSet", "namespace", request.Namespace, "name", request.Name)

	err = common.ReloadRuntimeConfig(ctx, r.Client, request, r.nodeName, r.runtimeConfig)
	if err != nil {
		return ctrl.Result{}, err
	}

	if !r.cacheSynced {
		err = r.syncCaches()
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	// ignore LocalVolmeSets whose LabelSelector doesn't match this node
	// NodeSelectorTerms.MatchExpressions are ORed
	matches, err := common.NodeSelectorMatchesNodeLabels(r.runtimeConfig.Node, lvset.Spec.NodeSelector)
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
		common.PVOwnerKindLabel:      localv1.LocalVolumeSetKind,
		common.PVOwnerNamespaceLabel: lvset.Namespace,
		common.PVOwnerNameLabel:      lvset.Name,
	}
	err = common.CleanupSymlinks(r.Client, r.runtimeConfig, ownerLabels,
		func() bool {
			// Only delete the symlink if the owner LVSet is deleted.
			return !lvset.DeletionTimestamp.IsZero()
		})
	if err != nil {
		msg := fmt.Sprintf("failed to cleanup symlinks: %v", err)
		r.eventReporter.Report(lvset, newDiskEvent(diskmaker.ErrorRemovingSymLink, msg, "", corev1.EventTypeWarning))
		klog.Error(msg)
		return ctrl.Result{}, err
	}

	// don't provision for deleted lvsets
	if !lvset.DeletionTimestamp.IsZero() {
		// update metrics for deletion timestamp
		localmetrics.SetLVSDeletionTimestampMetric(lvset.GetName(), lvset.GetDeletionTimestamp().Unix())
		// If there are released PV's for this owner in the cache, use
		// the fast requeue time, as it implies a cleanup job may be in
		// progress and we should call DeletePVs() again soon to check
		// for job completion and to delete the PV.
		if common.OwnerHasReleasedPVs(r.runtimeConfig, ownerLabels) {
			requeueTime = fastRequeueTime
		}
		return ctrl.Result{Requeue: true, RequeueAfter: requeueTime}, nil
	}
	// since deletion timestamp is notset, clear out its metrics
	localmetrics.RemoveLVSDeletionTimestampMetric(lvset.GetName())

	klog.InfoS("Looking for valid block devices", "namespace", request.Namespace, "name", request.Name)
	// get associated storageclass
	storageClassName := lvset.Spec.StorageClassName
	storageClass := &storagev1.StorageClass{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: storageClassName}, storageClass)
	if err != nil {
		klog.ErrorS(err, "could not get storageclass")
		return ctrl.Result{}, err
	}

	// get symlinkdir
	symLinkConfig, ok := r.runtimeConfig.DiscoveryMap[storageClassName]
	if !ok {
		return ctrl.Result{}, fmt.Errorf("could not find storageclass entry %q in provisioner config: %+v", storageClassName, r.runtimeConfig.DiscoveryMap)
	}
	symLinkDir := symLinkConfig.HostDir

	// list block devices
	blockDevices, badRows, err := internal.ListBlockDevices([]string{})
	if err != nil {
		msg := fmt.Sprintf("failed to list block devices: %v", err)
		r.eventReporter.Report(lvset, newDiskEvent(diskmaker.ErrorRunningBlockList, msg, "", corev1.EventTypeWarning))
		klog.Error(msg)
		return ctrl.Result{}, err
	} else if len(badRows) > 0 {
		msg := fmt.Sprintf("error parsing rows: %+v", badRows)
		r.eventReporter.Report(lvset, newDiskEvent(diskmaker.ErrorRunningBlockList, msg, "", corev1.EventTypeWarning))
		klog.Error(msg)
	}

	// find disks that match lvset filters and matchers
	validDevices, delayedDevices := r.getValidDevices(lvset, blockDevices)

	// update metrics for unmatched disks
	localmetrics.SetLVSUnmatchedDiskMetric(nodeName, storageClassName, len(blockDevices)-len(validDevices))

	// process valid devices
	var totalProvisionedPVs int
	var noMatch []string
	for _, blockDevice := range validDevices {
		existingSymlink, err := getSymlinkedForCurrentSC(symLinkDir, blockDevice)
		if err != nil {
			klog.ErrorS(err, "error reading existing symlinks for device",
				"blockDevice", blockDevice.Name)
			continue
		}

		symlinkSourcePath, symlinkPath, idExists, err := common.GetSymLinkSourceAndTarget(blockDevice, symLinkDir, existingSymlink)
		if err != nil {
			klog.ErrorS(err, "error discovering symlink source and target",
				"blockDevice", blockDevice.Name)
			continue
		}
		if !idExists {
			klog.InfoS("Using real device path, this could have problems if device name changes",
				"blockDevice", blockDevice.Name)
		}

		// validate MaxDeviceCount
		var alreadyProvisionedCount int
		var currentDeviceSymlinked bool
		alreadyProvisionedCount, currentDeviceSymlinked, noMatch, err = getAlreadySymlinked(symLinkDir, blockDevice, blockDevices)

		totalProvisionedPVs = alreadyProvisionedCount

		if err != nil && lvset.Spec.MaxDeviceCount != nil {
			r.eventReporter.Report(lvset, newDiskEvent(ErrorListingExistingSymlinks, "error determining already provisioned disks", "", corev1.EventTypeWarning))
			return ctrl.Result{}, fmt.Errorf("could not determine how many devices are already provisioned: %w", err)
		}
		withinMax := true
		if lvset.Spec.MaxDeviceCount != nil {
			withinMax = int32(alreadyProvisionedCount) < *lvset.Spec.MaxDeviceCount
		}
		// skip this device if this device is not already symlinked and provisioning it would exceed the maxDeviceCount
		if !(withinMax || currentDeviceSymlinked) {
			break
		}

		mountPointMap, err := common.GenerateMountMap(r.runtimeConfig)
		if err != nil {
			return ctrl.Result{}, err
		}

		klog.InfoS("provisioning PV", "blockDevice", blockDevice.Name)
		r.eventReporter.Report(lvset, newDiskEvent(diskmaker.FoundMatchingDisk, "provisioning matching disk", blockDevice.KName, corev1.EventTypeNormal))
		err = r.provisionPV(lvset, blockDevice, *storageClass, mountPointMap, symlinkSourcePath, symlinkPath, idExists)
		if err == common.ErrTryAgain {
			requeueTime = fastRequeueTime
		} else if err != nil {
			msg := fmt.Sprintf("provisioning failed for %s: %v", blockDevice.Name, err)
			r.eventReporter.Report(lvset, newDiskEvent(diskmaker.ErrorProvisioningDisk, msg, blockDevice.KName, corev1.EventTypeWarning))
			klog.Error(msg)
			continue
		}

		klog.InfoS("provisioning succeeded", "blockDevice", blockDevice.Name)
	}

	klog.InfoS("total devices provisioned", "storagecClass", storageClassName, "count", totalProvisionedPVs)

	// update metrics for total persistent volumes provisioned
	localmetrics.SetLVSProvisionedPVMetric(nodeName, storageClassName, totalProvisionedPVs)

	orphanSymlinkDevices, err := internal.GetOrphanedSymlinks(symLinkDir, validDevices)
	if err != nil {
		klog.ErrorS(err, "failed to get orphaned symlink devices in current reconcile")
	}

	if len(orphanSymlinkDevices) > 0 {
		klog.InfoS("found orphan symlinked devices in current reconcile",
			"orphanedDevices", orphanSymlinkDevices)
	}

	// update metrics for orphaned symlink devices
	localmetrics.SetLVSOrphanedSymlinksMetric(nodeName, storageClassName, len(orphanSymlinkDevices))

	if len(noMatch) > 0 {
		klog.InfoS("found stale symLink entries", "storageClass", storageClassName,
			"paths", noMatch, "directory", symLinkDir)
	}

	// shorten the requeueTime if there are delayed devices
	if len(delayedDevices) > 1 && requeueTime == defaultRequeueTime {
		requeueTime = deviceMinAge / 2
	}

	return ctrl.Result{Requeue: true, RequeueAfter: requeueTime}, nil
}

func (r *LocalVolumeSetReconciler) syncCaches() error {
	pvList := &corev1.PersistentVolumeList{}
	err := r.Client.List(context.TODO(), pvList)
	if err != nil {
		return fmt.Errorf("failed to initialize PV cache: %w", err)
	}
	for _, pv := range pvList.Items {
		// skip non-owned PVs
		if !common.PVMatchesProvisioner(&pv, r.runtimeConfig.Name) ||
			!common.IsLocalVolumeSetPV(&pv) {
			continue
		}
		common.AddOrUpdatePV(r.runtimeConfig, &pv)
	}
	r.cacheSynced = true
	return nil
}

// runs filters and matchers on the blockDeviceList and returns valid devices
// and devices that are not considered old enough to be valid yet
// i.e. if the device is younger than deviceMinAge
// if the waitingDevices list is nonempty, the operator should requeueue
func (r *LocalVolumeSetReconciler) getValidDevices(
	lvset *localv1alpha1.LocalVolumeSet,
	blockDevices []internal.BlockDevice,
) ([]internal.BlockDevice, []internal.BlockDevice) {
	validDevices := make([]internal.BlockDevice, 0)
	delayedDevices := make([]internal.BlockDevice, 0)
	// get valid devices
DeviceLoop:
	for _, blockDevice := range blockDevices {

		// store device in deviceAgeMap
		r.deviceAgeMap.storeDeviceAge(blockDevice.KName)

		for name, filter := range FilterMap {
			var valid bool
			var err error
			valid, err = filter(blockDevice, nil)
			if err != nil {
				klog.ErrorS(err, "filter error", "device",
					blockDevice.Name, "filter", name)
				valid = false
				continue DeviceLoop
			} else if !valid {
				klog.InfoS("filter negative", "device",
					blockDevice.Name, "filter", name)
				continue DeviceLoop
			}
		}

		// check if the device is older than deviceMinAge
		isOldEnough := r.deviceAgeMap.isOlderThan(blockDevice.KName)

		// skip devices younger than deviceMinAge
		if !isOldEnough {
			delayedDevices = append(delayedDevices, blockDevice)
			// record DiscoveredDevice event
			if lvset != nil {
				r.eventReporter.Report(
					lvset,
					newDiskEvent(
						DiscoveredNewDevice,
						fmt.Sprintf("found possible matching disk, waiting %v to claim", deviceMinAge),
						blockDevice.KName, corev1.EventTypeNormal,
					),
				)
			}
			continue DeviceLoop
		}

		for name, matcher := range matcherMap {
			valid, err := matcher(blockDevice, lvset.Spec.DeviceInclusionSpec)
			if err != nil {
				klog.ErrorS(err, "match error", "device",
					blockDevice.Name, "filter", name)
				valid = false
				continue DeviceLoop
			} else if !valid {
				klog.InfoS("match negative", "device",
					blockDevice.Name, "filter", name)
				continue DeviceLoop
			}
		}
		klog.InfoS("matched disk", "device", blockDevice.Name)
		// handle valid disk
		validDevices = append(validDevices, blockDevice)

	}
	return validDevices, delayedDevices
}

// returns:
// count of already symlinked from validDevices
// if the currentDevice is alreadysymlinks
// list of symlinks that don't match validDevices
// err
func getAlreadySymlinked(symLinkDir string, currentDevice internal.BlockDevice, validDevices []internal.BlockDevice) (int, bool, []string, error) {
	count := 0
	noMatch := make([]string, 0)
	currentDeviceSymlinked := false
	paths, err := filepath.Glob(filepath.Join(symLinkDir, "/*"))
	if err != nil {
		return 0, currentDeviceSymlinked, []string{}, err
	}

PathLoop:
	for _, path := range paths {
		for _, device := range validDevices {
			isMatch, err := internal.PathEvalsToDiskLabel(path, device.KName)
			if err != nil {
				return 0, currentDeviceSymlinked, []string{}, err
			}
			if isMatch {
				count++
				if currentDevice.KName == device.KName {
					currentDeviceSymlinked = true
				}
				continue PathLoop
			}
		}
		noMatch = append(noMatch, path)
	}
	return count, currentDeviceSymlinked, noMatch, nil
}

func getSymlinkedForCurrentSC(symlinkDir string, currentDevice internal.BlockDevice) (string, error) {
	paths, err := filepath.Glob(filepath.Join(symlinkDir, "/*"))
	if err != nil {
		return "", err
	}

	for _, path := range paths {
		isMatch, err := internal.PathEvalsToDiskLabel(path, currentDevice.KName)
		if err != nil {
			return "", err
		}
		if isMatch {
			return filepath.Base(path), nil
		}
	}
	return "", nil
}

func (r *LocalVolumeSetReconciler) provisionPV(
	obj *localv1alpha1.LocalVolumeSet,
	dev internal.BlockDevice,
	storageClass storagev1.StorageClass,
	mountPointMap sets.String,
	symlinkSourcePath string,
	symlinkPath string,
	idExists bool,
) error {

	// get /dev/KNAME path
	devLabelPath, err := dev.GetDevPath()
	if err != nil {
		return err
	}

	symLinkDir := filepath.Dir(symlinkPath)

	// ensure symLinkDirExists
	err = os.MkdirAll(symLinkDir, 0755)
	if err != nil {
		return fmt.Errorf("could not create symlinkdir: %w", err)
	}

	// get PV creation lock which checks for existing symlinks to this device
	pvLock, pvLocked, existingSymlinks, err := internal.GetPVCreationLock(
		devLabelPath,
		filepath.Dir(symLinkDir),
	)
	unlockFunc := func() {
		err := pvLock.Unlock()
		if err != nil {
			klog.ErrorS(err, "failed to unlock device", "disk", devLabelPath)
		}
	}
	defer unlockFunc()
	if len(existingSymlinks) > 0 { // already claimed
		for _, path := range existingSymlinks {
			if path == symlinkPath { // symlinked in this folder, ensure the PV exists
				return common.CreateLocalPV(
					obj,
					r.runtimeConfig,
					storageClass,
					mountPointMap,
					r.Client,
					symlinkPath,
					dev.KName,
					idExists,
					map[string]string{},
				)
			}
		}
		err := fmt.Errorf("found existing symlinks for device %s in %s", dev.KName, filepath.Dir(symLinkDir))
		klog.ErrorS(err, "skipping provisioning of PV")
		return err
	} else if err != nil || !pvLocked { // locking failed for some other reasion
		klog.ErrorS(err, "not provisioning, could not get lock", "disk", devLabelPath)
		return err
	}

	klog.InfoS("symlinking", "sourcePath", symlinkSourcePath, "targetPath", symlinkPath)
	// create symlink
	err = os.Symlink(symlinkSourcePath, symlinkPath)
	if os.IsExist(err) {
		fileInfo, statErr := os.Stat(symlinkSourcePath)
		if statErr != nil {
			return fmt.Errorf("could not create symlink: %v,%w", err, statErr)

			// existing file is symlink
		} else if fileInfo.Mode() == os.ModeSymlink {
			valid, evalErr := internal.PathEvalsToDiskLabel(symlinkSourcePath, dev.Name)
			if evalErr != nil {
				return fmt.Errorf("existing symlink not valid: %v,%w", err, evalErr)
				// existing file evals to disk
			} else if valid {
				// if file exists and is accurate symlink, create pv
				return common.CreateLocalPV(
					obj,
					r.runtimeConfig,
					storageClass,
					mountPointMap,
					r.Client,
					symlinkPath,
					dev.KName,
					idExists,
					map[string]string{},
				)
			}
		}
	} else if err != nil {
		return err
	}
	return common.CreateLocalPV(
		obj,
		r.runtimeConfig,
		storageClass,
		mountPointMap,
		r.Client,
		symlinkPath,
		dev.KName,
		idExists,
		map[string]string{},
	)
}

type LocalVolumeSetReconciler struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	Client        client.Client
	Scheme        *runtime.Scheme
	nodeName      string
	eventReporter *eventReporter
	cacheSynced   bool
	// map from KNAME of device to time when the device was first observed since the process started
	deviceAgeMap *ageMap

	// static-provisioner stuff
	cleanupTracker *provDeleter.CleanupStatusTracker
	runtimeConfig  *provCommon.RuntimeConfig
	deleter        *provDeleter.Deleter
}

func NewLocalVolumeSetReconciler(client client.Client, scheme *runtime.Scheme, time timeInterface, cleanupTracker *provDeleter.CleanupStatusTracker, rc *provCommon.RuntimeConfig) *LocalVolumeSetReconciler {
	deleter := provDeleter.NewDeleter(rc, cleanupTracker)
	eventReporter := newEventReporter(rc.Recorder)
	lvsReconciler := &LocalVolumeSetReconciler{
		Client:         client,
		Scheme:         scheme,
		nodeName:       nodeName,
		eventReporter:  eventReporter,
		deviceAgeMap:   newAgeMap(time),
		cleanupTracker: cleanupTracker,
		runtimeConfig:  rc,
		deleter:        deleter,
	}

	return lvsReconciler
}

func (r *LocalVolumeSetReconciler) WithManager(mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		// set to 1 explicitly, despite it being the default, as the reconciler is not thread-safe.
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		For(&localv1alpha1.LocalVolumeSet{}).
		Watches(
			&corev1.ConfigMap{},
			handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), &localv1.LocalVolume{})).
		// update owned-pv cache used by provisioner/deleter libs and enequeue owning lvset
		// only the cache is touched by
		Watches(&corev1.PersistentVolume{}, &handler.Funcs{
			GenericFunc: func(ctx context.Context, e event.GenericEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				pv, ok := e.Object.(*corev1.PersistentVolume)
				if ok && common.IsLocalVolumeSetPV(pv) {
					common.HandlePVChange(r.runtimeConfig, pv, q, watchNamespace, false)
				}
			},
			CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				pv, ok := e.Object.(*corev1.PersistentVolume)
				if ok && common.IsLocalVolumeSetPV(pv) {
					common.HandlePVChange(r.runtimeConfig, pv, q, watchNamespace, false)
				}
			},
			UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				pv, ok := e.ObjectNew.(*corev1.PersistentVolume)
				if ok && common.IsLocalVolumeSetPV(pv) {
					common.HandlePVChange(r.runtimeConfig, pv, q, watchNamespace, false)
				}
			},
			DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
				pv, ok := e.Object.(*corev1.PersistentVolume)
				if ok && common.IsLocalVolumeSetPV(pv) {
					common.HandlePVChange(r.runtimeConfig, pv, q, watchNamespace, true)
				}
			},
		}).
		Complete(r)

	return err
}
