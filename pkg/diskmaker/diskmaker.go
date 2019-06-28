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
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
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
}

type DiskLocation struct {
	// diskNamePath stores full device name path - "/dev/sda"
	diskNamePath string
	diskID       string
}

// DiskMaker returns a new instance of DiskMaker
func NewDiskMaker(configLocation, symLinkLocation string) *DiskMaker {
	t := &DiskMaker{}
	t.configLocation = configLocation
	t.symlinkLocation = symLinkLocation
	return t
}

func (d *DiskMaker) loadConfig() (DiskConfig, error) {
	var err error
	content, err := ioutil.ReadFile(d.configLocation)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s with %v", d.configLocation, err)
	}
	var diskConfig DiskConfig
	err = yaml.Unmarshal(content, &diskConfig)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling %s with %v", d.configLocation, err)
	}
	return diskConfig, nil
}

// Run and create disk config
func (d *DiskMaker) Run(stop <-chan struct{}) {
	ticker := time.NewTicker(checkDuration)
	defer ticker.Stop()

	err := os.MkdirAll(d.symlinkLocation, 0755)
	if err != nil {
		logrus.Errorf("error creating local-storage directory %s with %v", d.symlinkLocation, err)
		os.Exit(-1)
	}

	for {
		select {
		case <-ticker.C:
			diskConfig, err := d.loadConfig()
			if err != nil {
				logrus.Errorf("error loading configuration with %v", err)
				break
			}
			d.symLinkDisks(diskConfig)
		case <-stop:
			logrus.Infof("exiting, received message on stop channel")
			os.Exit(0)
		}
	}
}

func (d *DiskMaker) symLinkDisks(diskConfig DiskConfig) {
	cmd := exec.Command("lsblk", "--list", "-o", "NAME,MOUNTPOINT", "--noheadings")
	var out bytes.Buffer
	var err error
	cmd.Stdout = &out
	err = cmd.Run()
	if err != nil {
		logrus.Errorf("error running lsblk %v", err)
		return
	}
	deviceSet, err := d.findNewDisks(out.String())
	if err != nil {
		logrus.Errorf("error unmrashalling json %v", err)
		return
	}

	if len(deviceSet) == 0 {
		logrus.Infof("unable to find any new disks")
		return
	}

	// read all available disks from /dev/disk/by-id/*
	allDiskIds, err := filepath.Glob(diskByIDPath)
	if err != nil {
		logrus.Errorf("error listing disks in /dev/disk/by-id : %v", err)
		return
	}

	deviceMap, err := d.findMatchingDisks(diskConfig, deviceSet, allDiskIds)
	if err != nil {
		logrus.Errorf("error matching finding disks : %v", err)
		return
	}

	if len(deviceMap) == 0 {
		logrus.Errorf("unable to find any matching disks")
		return
	}

	for storageClass, deviceArray := range deviceMap {
		for _, deviceNameLocation := range deviceArray {
			symLinkDirPath := path.Join(d.symlinkLocation, storageClass)
			err := os.MkdirAll(symLinkDirPath, 0755)
			if err != nil {
				logrus.Errorf("error creating symlink directory %s with %v", symLinkDirPath, err)
				continue
			}
			baseDeviceName := filepath.Base(deviceNameLocation.diskNamePath)
			symLinkPath := path.Join(symLinkDirPath, baseDeviceName)
			if fileExists(symLinkPath) {
				logrus.Infof("symlink %s already exists", symLinkPath)
				continue
			}
			var symLinkErr error
			if deviceNameLocation.diskID != "" {
				logrus.Infof("symlinking to %s to %s", deviceNameLocation.diskID, symLinkPath)
				symLinkErr = os.Symlink(deviceNameLocation.diskID, symLinkPath)
			} else {
				logrus.Infof("symlinking to %s to %s", deviceNameLocation.diskNamePath, symLinkPath)
				symLinkErr = os.Symlink(deviceNameLocation.diskNamePath, symLinkPath)
			}

			if symLinkErr != nil {
				logrus.Errorf("error creating symlink %s with %v", symLinkPath, err)
			}
		}
	}

}

func (d *DiskMaker) findMatchingDisks(diskConfig DiskConfig, deviceSet sets.String, allDiskIds []string) (map[string][]DiskLocation, error) {
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
	for storageClass, disks := range diskConfig {
		// handle diskNames
		deviceNames := disks.DeviceNames().List()
		for _, diskName := range deviceNames {
			baseDeviceName := filepath.Base(diskName)
			if hasExactDisk(deviceSet, baseDeviceName) {
				matchedDeviceID, err := d.findStableDeviceID(baseDeviceName, allDiskIds)
				if err != nil {
					logrus.Errorf("Unable to find disk ID %s for local pool %v", diskName, err)
					addDiskToMap(storageClass, "", diskName)
					continue
				}
				addDiskToMap(storageClass, matchedDeviceID, diskName)
				continue
			}
		}

		deviceIds := disks.DeviceIDs().List()
		// handle DeviceIDs
		for _, deviceID := range deviceIds {
			matchedDeviceID, matchedDiskName, err := d.findDeviceByID(deviceID)
			if err != nil {
				logrus.Errorf("unable to add disk-id %s to local disk pool %v", deviceID, err)
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

func (d *DiskMaker) findNewDisks(content string) (sets.String, error) {
	deviceSet := sets.NewString()
	deviceLines := strings.Split(content, "\n")
	for _, deviceLine := range deviceLines {
		deviceLine := strings.TrimSpace(deviceLine)
		deviceDetails := strings.Split(deviceLine, " ")
		// We only consider devices that are not mounted.
		// TODO: We should also consider checking for device partitions, so as
		// if a device has partitions then we do not consider the device. We only
		// consider partitions.
		if len(deviceDetails) == 1 && len(deviceDetails[0]) > 0 {
			deviceSet.Insert(deviceDetails[0])
		}
	}
	return deviceSet, nil
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
