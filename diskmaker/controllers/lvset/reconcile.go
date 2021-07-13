package lvset

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/go-logr/logr"
	localv1 "github.com/openshift/local-storage-operator/api/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/common"
	"github.com/openshift/local-storage-operator/diskmaker"
	"github.com/openshift/local-storage-operator/internal"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/mount"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	provCache "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/cache"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
	staticProvisioner "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
	provDeleter "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/deleter"
	provUtil "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/util"
)

const (
	// ComponentName for lvset symlinker
	ComponentName = "localvolumeset-symlink-controller"

	pvOwnerKey = "pvOwner"
)

var log = logf.Log.WithName(ComponentName)

// Reconcile reads that state of the cluster for a LocalVolumeSet object and makes changes based on the state read
// and what is in the LocalVolumeSet.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *LocalVolumeSetReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling LocalVolumeSet")

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

	// don't provision for deleted lvsets
	if !lvset.DeletionTimestamp.IsZero() {
		reqLogger.Info("Lvset is deleted, Marking available PVs as released")
		pvOwnerLabels := labels.Set{
			common.PVOwnerKindLabel: lvset.Kind,
			common.PVOwnerNameLabel: lvset.Name,
		}
		labelSelector := labels.SelectorFromSet(pvOwnerLabels)
		err := common.ReleaseAvailablePVs(ctx, r.Client, labelSelector)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// get the node and determine if the localvolumeset selects this node
	r.runtimeConfig.Node = &corev1.Node{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: r.nodeName}, r.runtimeConfig.Node)
	if err != nil {
		return ctrl.Result{}, err
	}

	// ignore LocalVolmeSets whose LabelSelector doesn't match this node
	// NodeSelectorTerms.MatchExpressions are ORed
	matches, err := common.NodeSelectorMatchesNodeLabels(r.runtimeConfig.Node, lvset.Spec.NodeSelector)
	if err != nil {
		reqLogger.Error(err, "failed to match nodeSelector to node labels")
		return ctrl.Result{}, err
	}

	if !matches {
		return ctrl.Result{}, nil
	}

	storageClassName := lvset.Spec.StorageClassName

	// get associated storageclass
	storageClass := &storagev1.StorageClass{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: storageClassName}, storageClass)
	if err != nil {
		reqLogger.Error(err, "could not get storageclass")
		return ctrl.Result{}, err
	}

	// get associated provisioner config
	cm := &corev1.ConfigMap{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: common.ProvisionerConfigMapName, Namespace: request.Namespace}, cm)
	if err != nil {
		reqLogger.Error(err, "could not get provisioner configmap")
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

	// get symlinkdir
	symLinkConfig, ok := provisionerConfig.StorageClassConfig[storageClassName]
	if !ok {
		return ctrl.Result{}, fmt.Errorf("could not find storageclass entry %q in provisioner config: %+v", storageClassName, provisionerConfig)
	}
	symLinkDir := symLinkConfig.HostDir

	// list block devices
	blockDevices, badRows, err := internal.ListBlockDevices()
	if err != nil {
		r.eventReporter.Report(lvset, newDiskEvent(diskmaker.ErrorRunningBlockList, "failed to list block devices", "", corev1.EventTypeWarning))
		reqLogger.Error(err, "could not list block devices", "lsblk.BadRows", badRows)
		return ctrl.Result{}, err
	} else if len(badRows) > 0 {
		r.eventReporter.Report(lvset, newDiskEvent(diskmaker.ErrorRunningBlockList, fmt.Sprintf("error parsing rows: %+v", badRows), "", corev1.EventTypeWarning))
		reqLogger.Error(fmt.Errorf("bad rows"), "could not parse all the lsblk rows", "lsblk.BadRows", badRows)
	}

	// find disks that match lvset filters and matchers
	validDevices, delayedDevices := r.getValidDevices(reqLogger, lvset, blockDevices)

	// process valid devices
	var noMatch []string
	for _, blockDevice := range validDevices {
		devLogger := reqLogger.WithValues("Device.Name", blockDevice.Name)

		symlinkSourcePath, symlinkPath, idExists, err := common.GetSymLinkSourceAndTarget(blockDevice, symLinkDir)
		if err != nil {
			devLogger.Error(err, "error while discovering symlink source and target")
			continue
		}

		if !idExists {
			devLogger.Info("Using real device path, this could have problems if device name changes")
		}

		// validate MaxDeviceCount
		var alreadyProvisionedCount int
		var currentDeviceSymlinked bool
		alreadyProvisionedCount, currentDeviceSymlinked, noMatch, err = getAlreadySymlinked(symLinkDir, blockDevice, blockDevices)
		_ = currentDeviceSymlinked
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

		devLogger.Info("provisioning PV")
		r.eventReporter.Report(lvset, newDiskEvent(diskmaker.FoundMatchingDisk, "provisioning matching disk", blockDevice.KName, corev1.EventTypeNormal))
		err = r.provisionPV(lvset, devLogger, blockDevice, *storageClass, mountPointMap, symlinkSourcePath, symlinkPath, idExists)
		if err != nil {
			r.eventReporter.Report(lvset, newDiskEvent(diskmaker.ErrorProvisioningDisk, "provisioning failed", blockDevice.KName, corev1.EventTypeWarning))
			return ctrl.Result{}, fmt.Errorf("could not provision disk: %w", err)
		}
		devLogger.Info("provisioning succeeded")

	}
	if len(noMatch) > 0 {
		reqLogger.Info("found stale symLink Entries", "storageClass.Name", storageClassName, "paths.List", noMatch, "directory", symLinkDir)
	}

	// shorten the requeueTime if there are delayed devices
	requeueTime := time.Minute
	if len(delayedDevices) > 1 {
		requeueTime = deviceMinAge / 2
	}

	return ctrl.Result{Requeue: true, RequeueAfter: requeueTime}, nil
}

// runs filters and matchers on the blockDeviceList and returns valid devices
// and devices that are not considered old enough to be valid yet
// i.e. if the device is younger than deviceMinAge
// if the waitingDevices list is nonempty, the operator should requeueue
func (r *LocalVolumeSetReconciler) getValidDevices(
	reqLogger logr.Logger,
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

		devLogger := reqLogger.WithValues("Device.Name", blockDevice.Name)
		for name, filter := range FilterMap {
			var valid bool
			var err error
			filterLogger := devLogger.WithValues("filter.Name", name)
			valid, err = filter(blockDevice, nil)
			if err != nil {
				filterLogger.Error(err, "filter error")
				valid = false
				continue DeviceLoop
			} else if !valid {
				filterLogger.Info("filter negative")
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
			matcherLogger := devLogger.WithValues("matcher.Name", name)
			valid, err := matcher(blockDevice, lvset.Spec.DeviceInclusionSpec)
			if err != nil {
				matcherLogger.Error(err, "match error")
				valid = false
				continue DeviceLoop
			} else if !valid {
				matcherLogger.Info("match negative")
				continue DeviceLoop
			}
		}
		devLogger.Info("matched disk")
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

func (r *LocalVolumeSetReconciler) provisionPV(
	obj *localv1alpha1.LocalVolumeSet,
	devLogger logr.Logger,
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
			devLogger.Error(err, "failed to unlock device")
		}
	}
	defer unlockFunc()
	if len(existingSymlinks) > 0 { // already claimed
		for _, path := range existingSymlinks {
			if path == symlinkPath { // symlinked in this folder, ensure the PV exists
				return common.CreateLocalPV(
					obj,
					r.runtimeConfig,
					r.cleanupTracker,
					devLogger,
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
		return nil
	} else if err != nil || !pvLocked { // locking failed for some other reasion
		devLogger.Error(err, "not provisioning, could not get lock")
		return err
	}

	devLogger.Info("symlinking", "sourcePath", symlinkSourcePath, "targetPath", symlinkPath)
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
				// if file exists but the localvolumeset is deleted
				// remove the symlink
				if !obj.DeletionTimestamp.IsZero() {
					devLogger.Info("lvset deleted, removing symlink")
					cmd := exec.Command("rm", symlinkPath)
					err = cmd.Start()
					if err != nil {
						return err
					}
				}
				// if file exists and is accurate symlink, create pv
				return common.CreateLocalPV(
					obj,
					r.runtimeConfig,
					r.cleanupTracker,
					devLogger,
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
		r.cleanupTracker,
		devLogger,
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
	// map from KNAME of device to time when the device was first observed since the process started
	deviceAgeMap *ageMap

	// static-provisioner stuff
	cleanupTracker *provDeleter.CleanupStatusTracker
	runtimeConfig  *provCommon.RuntimeConfig
	deleter        *provDeleter.Deleter
}

var watchNamespace string
var nodeName string

func init() {
	nodeName = common.GetNodeNameEnvVar()
	watchNamespace, _ = common.GetWatchNamespace()
}

func (r *LocalVolumeSetReconciler) SetupWithManager(mgr ctrl.Manager, cleanupTracker *provDeleter.CleanupStatusTracker, pvCache *provCache.VolumeCache) error {

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
	clock := &wallTime{}

	r.nodeName = nodeName
	r.eventReporter = newEventReporter(mgr.GetEventRecorderFor(ComponentName))
	r.deviceAgeMap = newAgeMap(clock)
	r.cleanupTracker = cleanupTracker
	r.runtimeConfig = runtimeConfig
	r.deleter = provDeleter.NewDeleter(runtimeConfig, cleanupTracker)
	return ctrl.NewControllerManagedBy(mgr).
		// set to 1 explicitly, despite it being the default, as the reconciler is not thread-safe.
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		For(&localv1alpha1.LocalVolumeSet{}).
		Watches(&source.Kind{Type: &corev1.ConfigMap{}}, &handler.EnqueueRequestForOwner{
			OwnerType: &localv1.LocalVolume{},
		}).
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
	ownerNamespace, found := pv.Labels[common.PVOwnerNameLabel]
	if !found {
		return
	}
	ownerKind, found := pv.Labels[common.PVOwnerKindLabel]
	if ownerKind != localv1.LocalVolumeKind || !found {
		return
	}

	if isDelete {
		// delayed reconcile so that the cleanup tracker has time to mark the PV cleaned up
		time.Sleep(time.Second * 10)
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: ownerName, Namespace: ownerNamespace}})
	} else {
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: ownerName, Namespace: ownerNamespace}})
	}

}
