package diskmaker

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

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
	apiClient       apiUpdater
	localVolume     *localv1.LocalVolume
	eventSync       *eventReporter
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
	apiUpdater, err := newAPIUpdater(scheme)
	if err != nil {
		log.Error(err, "failed to create new APIUpdater")
		return &DiskMaker{}, err
	}

	t := &DiskMaker{}
	t.configLocation = configLocation
	t.symlinkLocation = symLinkLocation
	t.apiClient = apiUpdater
	t.eventSync = newEventReporter(t.apiClient)
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

func (d *DiskMaker) symLinkDisks(diskConfig *DiskConfig) {
	// run command lsblk --all --noheadings --pairs --output "KNAME,PKNAME,TYPE,MOUNTPOINT"
	// the reason we are using KNAME instead of NAME is because for lvm disks(and may be others)
	// the NAME and device file in /dev directory do not match.
	cmd := exec.Command("lsblk", "--all", "--noheadings", "--pairs", "--output", "KNAME,PKNAME,TYPE,MOUNTPOINT")
	var out bytes.Buffer
	var err error
	cmd.Stdout = &out
	err = cmd.Run()
	if err != nil {
		msg := fmt.Sprintf("error running lsblk: %v", err)
		e := newEvent(ErrorRunningBlockList, msg, "")
		d.eventSync.report(e, d.localVolume)
		klog.Errorf(msg)
		return
	}
	deviceSet := d.findNewDisks(out.String())

	if len(deviceSet) == 0 {
		klog.V(3).Infof("unable to find any new disks")
		return
	}

	// read all available disks from /dev/disk/by-id/*
	allDiskIds, err := filepath.Glob(diskByIDPath)
	if err != nil {
		msg := fmt.Sprintf("error listing disks in /dev/disk/by-id: %v", err)
		e := newEvent(ErrorListingDeviceID, msg, "")
		d.eventSync.report(e, d.localVolume)
		klog.Errorf(msg)
		return
	}

	deviceMap, err := d.findMatchingDisks(diskConfig, deviceSet, allDiskIds)
	if err != nil {
		msg := fmt.Sprintf("error finding matching disks: %v", err)
		e := newEvent(ErrorFindingMatchingDisk, msg, "")
		d.eventSync.report(e, d.localVolume)
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
		e := newEvent(ErrorFindingMatchingDisk, msg, "")
		d.eventSync.report(e, d.localVolume)
		klog.Errorf(msg)
		return
	}

	for storageClass, deviceArray := range deviceMap {
		for _, deviceNameLocation := range deviceArray {
			symLinkDirPath := path.Join(d.symlinkLocation, storageClass)
			err := os.MkdirAll(symLinkDirPath, 0755)
			if err != nil {
				msg := fmt.Sprintf("error creating symlink dir %s: %v", symLinkDirPath, err)
				e := newEvent(ErrorFindingMatchingDisk, msg, "")
				d.eventSync.report(e, d.localVolume)
				klog.Errorf(msg)
				continue
			}
			baseDeviceName := filepath.Base(deviceNameLocation.diskNamePath)
			symLinkPath := path.Join(symLinkDirPath, baseDeviceName)
			if fileExists(symLinkPath) {
				klog.V(4).Infof("symlink %s already exists", symLinkPath)
				continue
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
				e := newEvent(ErrorFindingMatchingDisk, msg, deviceNameLocation.diskNamePath)
				d.eventSync.report(e, d.localVolume)
				klog.Errorf(msg)
			}

			successMsg := fmt.Sprintf("found matching disk %s", baseDeviceName)
			e := newSuccessEvent(FoundMatchingDisk, successMsg, deviceNameLocation.diskNamePath)
			d.eventSync.report(e, d.localVolume)
		}
	}

}

func (d *DiskMaker) findMatchingDisks(diskConfig *DiskConfig, deviceSet sets.String, allDiskIds []string) (map[string][]DiskLocation, error) {
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
			if hasExactDisk(deviceSet, baseDeviceName) {
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
				e := newEvent(ErrorFindingMatchingDisk, msg, diskName)
				d.eventSync.report(e, d.localVolume)
				klog.Errorf(msg)
			}
		}

		deviceIds := disks.DeviceIDs().List()
		// handle DeviceIDs
		for _, deviceID := range deviceIds {
			matchedDeviceID, matchedDiskName, err := d.findDeviceByID(deviceID)
			if err != nil {
				msg := fmt.Sprintf("unable to add disk-id %s to local disk pool: %v", deviceID, err)
				e := newEvent(ErrorFindingMatchingDisk, msg, deviceID)
				d.eventSync.report(e, d.localVolume)
				klog.Errorf(msg)
				continue
			}
			baseDeviceName := filepath.Base(matchedDiskName)
			// We need to make sure that requested device is not already mounted.
			if hasExactDisk(deviceSet, baseDeviceName) {
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
		return "", "", fmt.Errorf("unable to find device with id %s", deviceID)
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

func (d *DiskMaker) findNewDisks(content string) sets.String {
	deviceSet := sets.NewString()
	fullDiskTable := newDiskTable()
	fullDiskTable.parse(content)
	usableDisks := fullDiskTable.filterUsableDisks()

	for _, disk := range usableDisks {
		diskName := disk["KNAME"]
		deviceSet.Insert(diskName)
	}
	return deviceSet
}

func hasExactDisk(disks sets.String, device string) bool {
	for _, disk := range disks.List() {
		if disk == device {
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
