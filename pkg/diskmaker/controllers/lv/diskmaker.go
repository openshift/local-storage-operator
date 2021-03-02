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

	"github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/internal"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"

	//	"github.com/prometheus/common/log"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog"
)

// DiskMaker is a small utility that reads configmap and
// creates and symlinks disks in location from which local-storage-provisioner can access.
// It also ensures that only stable device names are used.

var (
	checkDuration = 5 * time.Second
	diskByIDPath  = "/dev/disk/by-id/*"
)

const (
	ownerNamespaceLabel = "local.storage.openshift.io/owner-namespace"
	ownerNameLabel      = "local.storage.openshift.io/owner-name"
)

type DiskLocation struct {
	// diskNamePath stores full device name path - "/dev/sda"
	diskNamePath string
	diskID       string
}

func (r *ReconcileLocalVolume) createSymLink(deviceNameLocation DiskLocation, symLinkDirPath string) {

	var symLinkSource, symLinkTarget string
	var isSymLinkedByDeviceName bool
	if deviceNameLocation.diskID != "" {
		symLinkSource = deviceNameLocation.diskID
		symLinkTarget = getSymlinkTarget(deviceNameLocation.diskID, symLinkDirPath)
	} else {
		symLinkSource = deviceNameLocation.diskNamePath
		symLinkTarget = getSymlinkTarget(deviceNameLocation.diskNamePath, symLinkDirPath)
		isSymLinkedByDeviceName = true
	}

	// get PV creation lock which checks for existing symlinks to this device
	pvLock, pvLocked, existingSymlinks, err := internal.GetPVCreationLock(
		symLinkSource,
		r.symlinkLocation,
	)

	unlockFunc := func() {
		err := pvLock.Unlock()
		if err != nil {
			klog.Errorf("failed to unlock device: %+v", err)
		}
	}

	defer unlockFunc()

	if len(existingSymlinks) > 0 { // already claimed, fail silently
		return
	} else if err != nil || !pvLocked { // locking failed for some other reasion
		klog.Errorf("not symlinking, could not get lock: %v", err)
		return
	}

	err = os.MkdirAll(symLinkDirPath, 0755)
	if err != nil {
		msg := fmt.Sprintf("error creating symlink dir %s: %v", symLinkDirPath, err)
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, symLinkTarget, corev1.EventTypeWarning))
		klog.Errorf(msg)
		return
	}

	if fileExists(symLinkTarget) {
		klog.V(4).Infof("symlink %s already exists", symLinkTarget)
		return
	}

	err = os.Symlink(symLinkSource, symLinkTarget)
	if err != nil {
		msg := fmt.Sprintf("error creating symlink %s: %v", symLinkTarget, err)
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, symLinkSource, corev1.EventTypeWarning))
		klog.Errorf(msg)
		return
	}

	if isSymLinkedByDeviceName {
		msg := fmt.Sprintf("created symlink on device name %s with no disk/by-id. device name might not persist on reboot", symLinkSource)
		r.eventSync.Report(r.localVolume, newDiskEvent(SymLinkedOnDeviceName, msg, symLinkSource, corev1.EventTypeWarning))
		klog.Warningf(msg)
		return
	}
	successMsg := fmt.Sprintf("found matching disk %s with id %s", deviceNameLocation.diskNamePath, deviceNameLocation.diskID)
	r.eventSync.Report(r.localVolume, newDiskEvent(FoundMatchingDisk, successMsg, deviceNameLocation.diskNamePath, corev1.EventTypeNormal))
}
func diskMakerLabels(crName string) map[string]string {
	return map[string]string{
		"app": fmt.Sprintf("local-volume-diskmaker-%s", crName),
	}
}
func (r *ReconcileLocalVolume) generateConfig() *DiskConfig {
	configMapData := &DiskConfig{
		Disks:           map[string]*Disks{},
		OwnerName:       r.localVolume.Name,
		OwnerNamespace:  r.localVolume.Namespace,
		OwnerKind:       localv1.LocalVolumeKind,
		OwnerUID:        string(r.localVolume.UID),
		OwnerAPIVersion: localv1.SchemeGroupVersion.String(),
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
			APIVersion: localv1.SchemeGroupVersion.String(),
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
func (r *ReconcileLocalVolume) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("request.namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling LocalVolume")

	lv := &localv1.LocalVolume{}
	err := r.client.Get(context.TODO(), request.NamespacedName, lv)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	r.localVolume = lv

	// ignore LocalVolumes whose LabelSelector doesn't match this node
	// NodeSelectorTerms.MatchExpressions are ORed
	node := &corev1.Node{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: os.Getenv("MY_NODE_NAME")}, node)
	if err != nil {
		return reconcile.Result{}, err
	}

	matches, err := common.NodeSelectorMatchesNodeLabels(node, lv.Spec.NodeSelector)
	if err != nil {
		reqLogger.Error(err, "failed to match nodeSelector to node labels")
		return reconcile.Result{}, err
	}

	if !matches {
		return reconcile.Result{}, nil
	}

	err = os.MkdirAll(r.symlinkLocation, 0755)
	if err != nil {
		klog.Errorf("error creating local-storage directory %s: %v", r.symlinkLocation, err)
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
		msg := fmt.Sprintf("error running lsblk: %v", err)
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorRunningBlockList, msg, "", corev1.EventTypeWarning))
		klog.Errorf(msg)
		return reconcile.Result{}, err
	}

	// list block devices
	blockDevices, badRows, err := internal.ListBlockDevices()
	if err != nil {
		msg := fmt.Sprintf("failed to list block devices: %v", err)
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorRunningBlockList, msg, "", corev1.EventTypeWarning))
		reqLogger.Error(err, msg, "lsblk.BadRows", badRows)
		return reconcile.Result{}, err
	} else if len(badRows) > 0 {
		msg := fmt.Sprintf("error parsing rows: %+v", badRows)
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorRunningBlockList, msg, "", corev1.EventTypeWarning))
		klog.Errorf(msg, "could not parse all the lsblk rows", "lsblk.BadRows", badRows)
	}

	validBlockDevices := make([]internal.BlockDevice, 0)
	for _, blockDevice := range blockDevices {
		if ignoreDevices(blockDevice) {
			continue
		}
		validBlockDevices = append(validBlockDevices, blockDevice)
	}

	if len(validBlockDevices) == 0 {
		klog.V(3).Infof("unable to find any new disks")
		return reconcile.Result{}, nil
	}

	allDiskIds, err := filepath.Glob(diskByIDPath)
	if err != nil {
		msg := fmt.Sprintf("error listing disks in /dev/disk/by-id: %v", err)
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorListingDeviceID, msg, "", corev1.EventTypeWarning))
		klog.Errorf(msg)
		return reconcile.Result{}, nil
	}

	deviceMap, err := r.findMatchingDisks(diskConfig, validBlockDevices, allDiskIds)
	if err != nil {
		msg := fmt.Sprintf("error finding matching disks: %v", err)
		r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, "", corev1.EventTypeWarning))
		klog.Errorf(msg)
		return reconcile.Result{}, nil
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
		klog.Errorf(msg)
		return reconcile.Result{}, nil
	}

	for storageClass, deviceArray := range deviceMap {
		for _, deviceNameLocation := range deviceArray {
			symLinkDirPath := path.Join(r.symlinkLocation, storageClass)
			r.createSymLink(deviceNameLocation, symLinkDirPath)
		}
	}

	return reconcile.Result{Requeue: true, RequeueAfter: checkDuration}, nil
}

func ignoreDevices(dev internal.BlockDevice) bool {
	if hasBindMounts, _, err := dev.HasBindMounts(); err != nil || hasBindMounts {
		klog.Infof("ignoring mount device %q", dev.Name)
		return true
	}

	if hasChildren, err := dev.HasChildren(); err != nil || hasChildren {
		klog.Infof("ignoring root device %q", dev.Name)
		return true
	}

	return false
}

func (r *ReconcileLocalVolume) findMatchingDisks(diskConfig *DiskConfig, blockDevices []internal.BlockDevice, allDiskIds []string) (map[string][]DiskLocation, error) {

	// blockDeviceMap is a map of storageclass and device locations
	blockDeviceMap := make(map[string][]DiskLocation)

	addDiskToMap := func(scName, stableDeviceID, diskName string) {
		deviceArray, ok := blockDeviceMap[scName]
		if !ok {
			deviceArray = []DiskLocation{}
		}
		deviceArray = append(deviceArray, DiskLocation{diskName, stableDeviceID})
		blockDeviceMap[scName] = deviceArray
	}
	for storageClass, disks := range diskConfig.Disks {
		// handle diskNames
		deviceNames := disks.DeviceNames().List()
		for _, diskName := range deviceNames {
			baseDeviceName := filepath.Base(diskName)

			if hasExactDisk(blockDevices, baseDeviceName) {
				matchedDeviceID, err := r.findStableDeviceID(baseDeviceName, allDiskIds)
				// This means no /dev/disk/by-id entry was created for requested device.
				if err != nil {
					klog.V(4).Infof("unable to find disk ID %s for local pool %v", diskName, err)
					addDiskToMap(storageClass, "", diskName)
					continue
				}
				addDiskToMap(storageClass, matchedDeviceID, diskName)
				continue
			} else {
				if !fileExists(diskName) {
					msg := fmt.Sprintf("no file exists for specified device %v", diskName)
					klog.Errorf(msg)
					continue
				}
				fileMode, err := os.Stat(diskName)
				if err != nil {
					klog.Errorf("error attempting to examine %v, %v", diskName, err)
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
				//	e := NewEvent(ErrorFindingMatchingDisk, msg, diskName)
				r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, diskName, corev1.EventTypeWarning))
				klog.Errorf(msg)
			}
		}

		deviceIds := disks.DeviceIDs().List()
		// handle DeviceIDs
		for _, deviceID := range deviceIds {
			matchedDeviceID, matchedDiskName, err := r.findDeviceByID(deviceID)
			if err != nil {
				msg := fmt.Sprintf("unable to add disk-id %s to local disk pool: %v", deviceID, err)
				//	e := NewEvent(ErrorFindingMatchingDisk, msg, deviceID)
				r.eventSync.Report(r.localVolume, newDiskEvent(ErrorFindingMatchingDisk, msg, deviceID, corev1.EventTypeWarning))
				klog.Errorf(msg)
				continue
			}
			baseDeviceName := filepath.Base(matchedDiskName)
			// We need to make sure that requested device is not already mounted.
			if hasExactDisk(blockDevices, baseDeviceName) {
				addDiskToMap(storageClass, matchedDeviceID, matchedDiskName)
			}
		}
	}
	return blockDeviceMap, nil
}

// findDeviceByID finds device ID and return device name(such as sda, sdb) and complete deviceID path
func (r *ReconcileLocalVolume) findDeviceByID(deviceID string) (string, string, error) {
	diskDevPath, err := filepath.EvalSymlinks(deviceID)
	if err != nil {
		return "", "", fmt.Errorf("unable to find device at path %s: %v", deviceID, err)
	}
	return deviceID, diskDevPath, nil
}

func (r *ReconcileLocalVolume) findStableDeviceID(diskName string, allDisks []string) (string, error) {
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

func hasExactDisk(blockDevices []internal.BlockDevice, device string) bool {
	for _, blockDevice := range blockDevices {
		if blockDevice.KName == device {
			return true
		}
	}
	return false
}

// fileExists checks if a file exists
func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return true
}

func getSymlinkTarget(device, symLinkDirPath string) string {
	baseDeviceName := filepath.Base(device)
	return path.Join(symLinkDirPath, baseDeviceName)
}
