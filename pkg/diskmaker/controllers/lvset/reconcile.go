package lvset

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/go-logr/logr"
	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/diskmaker"
	"github.com/openshift/local-storage-operator/pkg/internal"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	corev1helper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
	staticProvisioner "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
)

// Reconcile reads that state of the cluster for a LocalVolumeSet object and makes changes based on the state read
// and what is in the LocalVolumeSet.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileLocalVolumeSet) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling LocalVolumeSet")

	// Fetch the LocalVolumeSet instance
	lvset := &localv1alpha1.LocalVolumeSet{}
	err := r.client.Get(context.TODO(), request.NamespacedName, lvset)
	if err != nil {
		if kerrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// get the node and determine if the localvolumeset selects this node
	r.runtimeConfig.Node = &corev1.Node{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: r.nodeName}, r.runtimeConfig.Node)
	if err != nil {
		return reconcile.Result{}, err
	}

	// ignore LocalVolmeSets whose LabelSelector doesn't match this node
	// NodeSelectorTerms.MatchExpressions are ORed
	matches, err := nodeSelectorMatchesNodeLabels(r.runtimeConfig.Node, lvset.Spec.NodeSelector)
	if err != nil {
		reqLogger.Error(err, "failed to match nodeSelector to node labels")
		return reconcile.Result{}, err
	}

	if !matches {
		return reconcile.Result{}, nil
	}

	storageClassName := lvset.Spec.StorageClassName

	// get associated storageclass
	storageClass := &storagev1.StorageClass{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: storageClassName}, storageClass)
	if err != nil {
		reqLogger.Error(err, "could not get storageclass")
		return reconcile.Result{}, err
	}

	// get associated provisioner config
	cm := &corev1.ConfigMap{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: common.ProvisionerConfigMapName, Namespace: request.Namespace}, cm)
	if err != nil {
		reqLogger.Error(err, "could not get provisioner configmap")
		return reconcile.Result{}, err
	}

	// read provisioner config
	provisionerConfig := staticProvisioner.ProvisionerConfiguration{}
	staticProvisioner.ConfigMapDataToVolumeConfig(cm.Data, &provisionerConfig)

	r.runtimeConfig.DiscoveryMap = provisionerConfig.StorageClassConfig
	r.runtimeConfig.NodeLabelsForPV = provisionerConfig.NodeLabelsForPV
	r.runtimeConfig.UseAlphaAPI = provisionerConfig.UseAlphaAPI
	r.runtimeConfig.UseJobForCleaning = provisionerConfig.UseJobForCleaning
	r.runtimeConfig.MinResyncPeriod = provisionerConfig.MinResyncPeriod
	r.runtimeConfig.UseNodeNameOnly = provisionerConfig.UseNodeNameOnly
	r.runtimeConfig.Namespace = request.Namespace
	r.runtimeConfig.LabelsForPV = provisionerConfig.LabelsForPV
	r.runtimeConfig.SetPVOwnerRef = provisionerConfig.SetPVOwnerRef
	r.runtimeConfig.Name = getProvisionedByValue(*r.runtimeConfig.Node)

	// initialize the deleter's pv cache on the first run
	if !r.firstRunOver {
		log.Info("initializing PV cache")
		pvList := &corev1.PersistentVolumeList{}
		err := r.client.List(context.TODO(), pvList)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to initialize PV cache")
		}
		for _, pv := range pvList.Items {
			// skip non-owned PVs
			name, found := pv.Annotations[provCommon.AnnProvisionedBy]
			if !found || name != r.runtimeConfig.Name {
				continue
			}
			addOrUpdatePV(r.runtimeConfig, pv)
		}

		r.firstRunOver = true
	}

	// get symlinkdir
	symLinkConfig, ok := provisionerConfig.StorageClassConfig[storageClassName]
	if !ok {
		return reconcile.Result{}, fmt.Errorf("could not find storageclass entry %q in provisioner config: %+v", storageClassName, provisionerConfig)
	}
	symLinkDir := symLinkConfig.HostDir

	// list block devices
	blockDevices, badRows, err := internal.ListBlockDevices()
	if err != nil {
		r.eventReporter.Report(lvset, newDiskEvent(diskmaker.ErrorRunningBlockList, "failed to list block devices", "", corev1.EventTypeWarning))
		reqLogger.Error(err, "could not list block devices", "lsblk.BadRows", badRows)
		return reconcile.Result{}, err
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

		symlinkSourcePath, symlinkPath, _, err := getSymLinkSourceAndTarget(blockDevice, symLinkDir)
		if err != nil {
			devLogger.Error(err, "error while discovering symlink source and target")
			continue
		}

		// validate MaxDeviceCount
		var alreadyProvisionedCount int
		var currentDeviceSymlinked bool
		alreadyProvisionedCount, currentDeviceSymlinked, noMatch, err = getAlreadySymlinked(symLinkDir, blockDevice, blockDevices)
		_ = currentDeviceSymlinked
		if err != nil && lvset.Spec.MaxDeviceCount != nil {
			r.eventReporter.Report(lvset, newDiskEvent(ErrorListingExistingSymlinks, "error determining already provisioned disks", "", corev1.EventTypeWarning))
			return reconcile.Result{}, fmt.Errorf("could not determine how many devices are already provisioned: %w", err)
		}
		withinMax := true
		if lvset.Spec.MaxDeviceCount != nil {
			withinMax = int32(alreadyProvisionedCount) < *lvset.Spec.MaxDeviceCount
		}
		// skip this device if this device is not already symlinked and provisioning it would exceed the maxDeviceCount
		if !(withinMax || currentDeviceSymlinked) {
			break
		}

		// Retrieve list of mount points to iterate through discovered paths (aka files) below
		mountPoints, err := r.runtimeConfig.Mounter.List()
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("error retrieving mountpoints: %w", err)
		}
		// Put mount points into set for faster checks below
		type empty struct{}
		mountPointMap := sets.NewString()
		for _, mp := range mountPoints {
			mountPointMap.Insert(mp.Path)
		}

		devLogger.Info("provisioning PV")
		r.eventReporter.Report(lvset, newDiskEvent(diskmaker.FoundMatchingDisk, "provisioning matching disk", blockDevice.KName, corev1.EventTypeNormal))
		err = r.provisionPV(lvset, devLogger, blockDevice, *storageClass, mountPointMap, symlinkSourcePath, symlinkPath)
		if err != nil {
			r.eventReporter.Report(lvset, newDiskEvent(diskmaker.ErrorProvisioningDisk, "provisioning failed", blockDevice.KName, corev1.EventTypeWarning))
			return reconcile.Result{}, fmt.Errorf("could not provision disk: %w", err)
		}
		devLogger.Info("provisioning succeeded")

	}
	if len(noMatch) > 0 {
		reqLogger.Info("found stale symLink Entries", "storageClass.Name", storageClassName, "paths.List", noMatch, "directory", symLinkDir)
	}

	r.deleter.DeletePVs()

	// shorten the requeueTime if there are delayed devices
	requeueTime := time.Minute
	if len(delayedDevices) > 1 {
		requeueTime = deviceMinAge / 2
	}

	return reconcile.Result{Requeue: true, RequeueAfter: requeueTime}, nil
}

// runs filters and matchers on the blockDeviceList and returns valid devices
// and devices that are not considered old enough to be valid yet
// i.e. if the device is younger than deviceMinAge
// if the waitingDevices list is nonempty, the operator should requeueue
func (r *ReconcileLocalVolumeSet) getValidDevices(
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

func (r *ReconcileLocalVolumeSet) provisionPV(
	obj *localv1alpha1.LocalVolumeSet,
	devLogger logr.Logger,
	dev internal.BlockDevice,
	storageClass storagev1.StorageClass,
	mountPointMap sets.String,
	symlinkSourcePath string,
	symlinkPath string,
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
				return r.createPV(obj, devLogger, storageClass, mountPointMap, symlinkPath)
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
				// if file exists and is accurate symlink, create pv
				return r.createPV(obj, devLogger, storageClass, mountPointMap, symlinkPath)
			}
		}
	} else if err != nil {
		return err
	}
	return r.createPV(obj, devLogger, storageClass, mountPointMap, symlinkPath)
}

func generatePVName(file, node, class string) string {
	h := fnv.New32a()
	h.Write([]byte(file))
	h.Write([]byte(node))
	h.Write([]byte(class))
	// This is the FNV-1a 32-bit hash
	return fmt.Sprintf("local-pv-%x", h.Sum32())
}

func nodeSelectorMatchesNodeLabels(node *corev1.Node, nodeSelector *corev1.NodeSelector) (bool, error) {
	if nodeSelector == nil {
		return true, nil
	}
	if node == nil {
		return false, fmt.Errorf("the node var is nil")
	}
	matches := corev1helper.MatchNodeSelectorTerms(nodeSelector.NodeSelectorTerms, node.Labels, fields.Set{
		"metadata.name": node.Name,
	})
	return matches, nil
}

func getSymLinkSourceAndTarget(dev internal.BlockDevice, symlinkDir string) (string, string, bool, error) {
	var source string
	var target string
	var idExists = true
	var err error

	// get /dev/KNAME path
	devLabelPath, err := dev.GetDevPath()
	if err != nil {
		return source, target, false, err
	}
	// determine symlink source
	source, err = dev.GetPathByID()
	if errors.As(err, &internal.IDPathNotFoundError{}) {
		// no disk-by-id
		idExists = false
		source = devLabelPath
	} else if err != nil {
		return source, target, false, err
	}
	target = path.Join(symlinkDir, filepath.Base(source))
	return source, target, idExists, err

}

func getNodeNameEnvVar() string {
	return os.Getenv("MY_NODE_NAME")
}
