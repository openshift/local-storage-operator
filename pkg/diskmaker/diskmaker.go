package diskmaker

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/openshift/local-storage-operator/pkg/internal"

	"github.com/ghodss/yaml"
	"github.com/openshift/local-storage-operator/pkg/apis"
	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	"github.com/prometheus/common/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog"
)

// DiskMaker is a small utility that reads configmap and
// creates and symlinks disks in location from which local-storage-provisioner can access.
// It also ensures that only stable device names are used.

var (
	checkDuration = 5 * time.Second
	diskByIDPath  = "/dev/disk/by-id/*"
)

type DiskMaker struct {
	configLocation  string
	symlinkLocation string
	apiClient       ApiUpdater
	localVolume     *localv1.LocalVolume
	eventSync       *EventReporter
}

type DiskLocation struct {
	// diskNamePath stores full device name path - "/dev/sda"
	diskNamePath string
	diskID       string
}

// NewDiskMaker returns a new instance of DiskMaker
func NewDiskMaker(configLocation, symLinkLocation string) (*DiskMaker, error) {
	scheme := scheme.Scheme
	apis.AddToScheme(scheme)
	apiUpdater, err := NewAPIUpdater(scheme)
	if err != nil {
		log.Error(err, "failed to create new APIUpdater")
		return &DiskMaker{}, err
	}

	t := &DiskMaker{}
	t.configLocation = configLocation
	t.symlinkLocation = symLinkLocation
	t.apiClient = apiUpdater
	t.eventSync = NewEventReporter(t.apiClient)
	return t, nil
}

func (d *DiskMaker) loadConfig() (*DiskConfig, error) {
	var err error
	content, err := ioutil.ReadFile(d.configLocation)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %v", d.configLocation, err)
	}
	var diskConfig DiskConfig
	err = yaml.Unmarshal(content, &diskConfig)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling %s: %v", d.configLocation, err)
	}

	lv := &localv1.LocalVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:      diskConfig.OwnerName,
			Namespace: diskConfig.OwnerNamespace,
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       diskConfig.OwnerKind,
			APIVersion: diskConfig.OwnerAPIVersion,
		},
	}
	lv, err = d.apiClient.getLocalVolume(lv)

	localKey := fmt.Sprintf("%s/%s", diskConfig.OwnerNamespace, diskConfig.OwnerName)
	if err != nil {
		return nil, fmt.Errorf("error fetching local volume %s: %v", localKey, err)
	}
	d.localVolume = lv

	return &diskConfig, nil
}

// Run and create disk config
func (d *DiskMaker) Run(stop <-chan struct{}) {
	ticker := time.NewTicker(checkDuration)
	defer ticker.Stop()

	err := os.MkdirAll(d.symlinkLocation, 0755)
	if err != nil {
		klog.Errorf("error creating local-storage directory %s: %v", d.symlinkLocation, err)
		os.Exit(-1)
	}

	for {
		select {
		case <-ticker.C:
			diskConfig, err := d.loadConfig()
			if err != nil {
				klog.Errorf("error loading configuration: %v", err)
				break
			}
			d.symLinkDisks(diskConfig)
		case <-stop:
			klog.Infof("exiting, received message on stop channel")
			os.Exit(0)
		}
	}
}

func (d *DiskMaker) createSymLink(deviceNameLocation DiskLocation, symLinkPath string, symLinkDirPath string, baseDeviceName string) {
	// get PV creation lock which checks for existing symlinks to this device
	pvLock, pvLocked, existingSymlinks, err := internal.GetPVCreationLock(
		deviceNameLocation.diskNamePath,
		d.symlinkLocation,
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
		e := NewEvent(ErrorFindingMatchingDisk, msg, symLinkPath)
		d.eventSync.Report(e, d.localVolume)
		klog.Errorf(msg)
		return
	}

	if fileExists(symLinkPath) {
		klog.V(4).Infof("symlink %s already exists", symLinkPath)
		return
	}

	var symLinkErr error
	if deviceNameLocation.diskID != "" {
		klog.V(3).Infof("symlinking to %s to %s", deviceNameLocation.diskID, symLinkPath)
		symLinkErr = os.Symlink(deviceNameLocation.diskID, symLinkPath)
	} else {
		klog.V(3).Infof("symlinking to %s to %s", deviceNameLocation.diskNamePath, symLinkPath)
		symLinkErr = os.Symlink(deviceNameLocation.diskNamePath, symLinkPath)
	}
	if symLinkErr != nil {
		msg := fmt.Sprintf("error creating symlink %s: %v", symLinkPath, symLinkErr)
		e := NewEvent(ErrorFindingMatchingDisk, msg, deviceNameLocation.diskNamePath)
		d.eventSync.Report(e, d.localVolume)
		klog.Errorf(msg)
		return
	}

	successMsg := fmt.Sprintf("found matching disk %s", baseDeviceName)
	e := NewSuccessEvent(FoundMatchingDisk, successMsg, deviceNameLocation.diskNamePath)
	d.eventSync.Report(e, d.localVolume)

}

func (d *DiskMaker) symLinkDisks(diskConfig *DiskConfig) {
	blockDevices, badRows, err := internal.ListBlockDevices()
	if err != nil {
		msg := fmt.Sprintf("failed to list block devices: %v", err)
		e := NewEvent(ErrorRunningBlockList, msg, "")
		d.eventSync.Report(e, d.localVolume)
		klog.Errorf(msg, "could not list block devices", "lsblk.BadRows", badRows)
		return
	} else if len(badRows) > 0 {
		msg := fmt.Sprintf("error parsing rows: %+v", badRows)
		e := NewEvent(ErrorRunningBlockList, msg, "")
		d.eventSync.Report(e, d.localVolume)
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
		return
	}

	// read all available disks from /dev/disk/by-id/*
	allDiskIds, err := filepath.Glob(diskByIDPath)
	if err != nil {
		msg := fmt.Sprintf("error listing disks in /dev/disk/by-id: %v", err)
		e := NewEvent(ErrorListingDeviceID, msg, "")
		d.eventSync.Report(e, d.localVolume)
		klog.Errorf(msg)
		return
	}

	deviceMap, err := d.findMatchingDisks(diskConfig, validBlockDevices, allDiskIds)
	if err != nil {
		msg := fmt.Sprintf("error finding matching disks: %v", err)
		e := NewEvent(ErrorFindingMatchingDisk, msg, "")
		d.eventSync.Report(e, d.localVolume)
		klog.Errorf(msg)
		return
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
		e := NewEvent(ErrorFindingMatchingDisk, msg, "")
		d.eventSync.Report(e, d.localVolume)
		klog.Errorf(msg)
		return
	}

	for storageClass, deviceArray := range deviceMap {
		for _, deviceNameLocation := range deviceArray {

			symLinkDirPath := path.Join(d.symlinkLocation, storageClass)
			baseDeviceName := filepath.Base(deviceNameLocation.diskNamePath)
			symLinkPath := path.Join(symLinkDirPath, baseDeviceName)

			d.createSymLink(deviceNameLocation, symLinkPath, symLinkDirPath, baseDeviceName)
		}
	}

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

func (d *DiskMaker) findMatchingDisks(diskConfig *DiskConfig, blockDevices []internal.BlockDevice, allDiskIds []string) (map[string][]DiskLocation, error) {
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
				matchedDeviceID, err := d.findStableDeviceID(baseDeviceName, allDiskIds)
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
				e := NewEvent(ErrorFindingMatchingDisk, msg, diskName)
				d.eventSync.Report(e, d.localVolume)
				klog.Errorf(msg)
			}
		}

		deviceIds := disks.DeviceIDs().List()
		// handle DeviceIDs
		for _, deviceID := range deviceIds {
			matchedDeviceID, matchedDiskName, err := d.findDeviceByID(deviceID)
			if err != nil {
				msg := fmt.Sprintf("unable to add disk-id %s to local disk pool: %v", deviceID, err)
				e := NewEvent(ErrorFindingMatchingDisk, msg, deviceID)
				d.eventSync.Report(e, d.localVolume)
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
func (d *DiskMaker) findDeviceByID(deviceID string) (string, string, error) {
	diskDevPath, err := filepath.EvalSymlinks(deviceID)
	if err != nil {
		return "", "", fmt.Errorf("unable to find device at path %s: %v", deviceID, err)
	}
	return deviceID, diskDevPath, nil
}

func (d *DiskMaker) findStableDeviceID(diskName string, allDisks []string) (string, error) {
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
