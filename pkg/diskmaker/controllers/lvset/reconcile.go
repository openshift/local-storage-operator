package lvset

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/openshift/local-storage-operator/pkg/common"

	"github.com/go-logr/logr"
	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/diskmaker"
	"github.com/openshift/local-storage-operator/pkg/internal"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	corev1helper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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

	// get node name
	nodeName := getNodeNameEnvVar()
	// get node labels
	node := &corev1.Node{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: nodeName}, node)
	if err != nil {
		return reconcile.Result{}, err
	}

	// ignore LocalVolmeSets whose LabelSelector doesn't match this node
	// NodeSelectorTerms.MatchExpressions are ORed
	matches, err := nodeSelectorMatchesNodeLabels(node, lvset.Spec.NodeSelector)
	if err != nil {
		reqLogger.Error(err, "failed to match nodeSelector to node labels")
		return reconcile.Result{}, err
	}

	if !matches {
		return reconcile.Result{}, nil
	}

	storageClass := lvset.Spec.StorageClassName

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

	// get symlinkdir
	symLinkConfig, ok := provisionerConfig.StorageClassConfig[storageClass]
	if !ok {
		return reconcile.Result{}, fmt.Errorf("could not find storageclass entry %q in provisioner config: %+v", storageClass, provisionerConfig)
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

		// validate MaxDeviceCount
		var alreadyProvisionedCount int
		alreadyProvisionedCount, noMatch, err = getAlreadyProvisioned(symLinkDir, validDevices)
		if err != nil && lvset.Spec.MaxDeviceCount != nil {
			r.eventReporter.Report(lvset, newDiskEvent(ErrorListingExistingSymlinks, "error determining already provisioned disks", "", corev1.EventTypeWarning))
			return reconcile.Result{}, fmt.Errorf("could not determine how many devices are already provisioned: %w", err)
		}
		withinMax := true
		if lvset.Spec.MaxDeviceCount != nil {
			withinMax = int32(alreadyProvisionedCount) < *lvset.Spec.MaxDeviceCount
		}
		if !withinMax {
			break
		}

		devLogger.Info("symlinking")
		r.eventReporter.Report(lvset, newDiskEvent(diskmaker.FoundMatchingDisk, "symlinking matching disk", blockDevice.KName, corev1.EventTypeNormal))
		err = symLinkDisk(lvset, r.eventReporter, devLogger, blockDevice, symLinkDir)
		if err != nil {
			r.eventReporter.Report(lvset, newDiskEvent(diskmaker.ErrorCreatingSymLink, "symlinking failed", blockDevice.KName, corev1.EventTypeWarning))
			return reconcile.Result{}, fmt.Errorf("could not symlink disk: %w", err)
		}
		devLogger.Info("symlinking succeeded")

	}
	if len(noMatch) > 0 {
		reqLogger.Info("found stale symLink Entries", "storageClass.Name", storageClass, "paths.List", noMatch, "directory", symLinkDir)
	}

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

func getAlreadyProvisioned(symLinkDir string, validDevices []internal.BlockDevice) (int, []string, error) {
	count := 0
	noMatch := make([]string, 0)
	paths, err := filepath.Glob(filepath.Join(symLinkDir, "/*"))
	if err != nil {
		return 0, []string{}, err
	}

PathLoop:
	for _, path := range paths {
		for _, device := range validDevices {
			isMatch, err := internal.PathEvalsToDiskLabel(path, device.KName)
			if err != nil {
				return 0, []string{}, err
			}
			if isMatch {
				count++
				continue PathLoop
			}
		}
		noMatch = append(noMatch, path)
	}
	return count, noMatch, nil
}

func symLinkDisk(obj runtime.Object, eventReporter *eventReporter, devLogger logr.Logger, dev internal.BlockDevice, symLinkDir string) error {
	// get /dev/KNAME path
	devLabelPath, err := dev.GetDevPath()
	if err != nil {
		return err
	}

	// determine symlink source
	symlinkSourcePath, err := dev.GetPathByID()
	if errors.As(err, &internal.IDPathNotFoundError{}) {
		// no disk-by-id
		symlinkSourcePath = devLabelPath
	} else if err != nil {
		return err
	}
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
		eventReporter.Report(
			obj,
			newDiskEvent(
				diskmaker.DeviceSymlinkExists,
				"this device is already matched by another LocalVolume or LocalVolumeSet",
				dev.KName, corev1.EventTypeNormal,
			),
		)
		return nil
	} else if err != nil || !pvLocked { // locking failed for some other reasion
		devLogger.Error(err, "not symlinking, could not get lock")
		return err
	}
	unlockFunc()

	symLinkName := filepath.Base(symlinkSourcePath)
	symLinkPath := path.Join(symLinkDir, symLinkName)

	devLogger.Info("symlinking", "sourcePath", symlinkSourcePath, "targetPath", symLinkPath)
	// create symlink
	err = os.Symlink(symlinkSourcePath, symLinkPath)
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
				// if file exists and is accurate symlink, skip and return success
				return nil
			}
		}
	} else if err != nil {
		return err
	}

	return nil
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

func getNodeNameEnvVar() string {
	return os.Getenv("MY_NODE_NAME")
}
