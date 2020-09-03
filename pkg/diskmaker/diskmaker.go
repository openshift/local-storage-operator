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
	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	"golang.org/x/sys/unix"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// DiskMaker returns a new instance of DiskMaker
func NewDiskMaker(configLocation, symLinkLocation string) *DiskMaker {
	t := &DiskMaker{}
	t.configLocation = configLocation
	t.symlinkLocation = symLinkLocation
	t.apiClient = newAPIUpdater()
	t.eventSync = newEventReporter(t.apiClient)
	return t
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
		msg := fmt.Sprintf("eror finding matching disks: %v", err)
		e := newEvent(ErrorFindingMatchingDisk, msg, "")
		d.eventSync.report(e, d.localVolume)
		klog.Errorf(msg)
		return
	}

	if len(deviceMap) == 0 {
		msg := "found empty matching device list"
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
			symLinkError := d.symLinkDeviceToPool(deviceNameLocation, symLinkPath)
			if symLinkError != nil {
				klog.Errorf("error symlinking %s to %s: %v", deviceNameLocation.diskNamePath, symLinkPath, symLinkError)
			}
		}
	}

}

func (d *DiskMaker) symLinkDeviceToPool(deviceNameLocation DiskLocation, symLinkPath string) error {
	baseDeviceName := filepath.Base(deviceNameLocation.diskNamePath)
	if fileExists(symLinkPath) {
		klog.V(4).Infof("symlink %s already exists", symLinkPath)
		return nil
	}
	// get PV creation lock which checks for existing symlinks to this device
	pvLock, existingSymlinks, err := getPVCreationLock(deviceNameLocation.diskNamePath, d.symlinkLocation)
	if err != nil {
		return fmt.Errorf("error acquiring exclusive lock on %s", deviceNameLocation.diskNamePath)
	}

	unlockFunc := func() {
		err := pvLock.unlock()
		if err != nil {
			klog.Errorf("failed to unlock device: %+v", err)
		}
	}
	defer unlockFunc()

	if len(existingSymlinks) > 0 {
		e := newEvent(DeviceSymlinkExists, "this device is already matched by another LocalVolume or LocalVolumeSet", symLinkPath)
		d.eventSync.report(e, d.localVolume)
		return fmt.Errorf("device %s already has been mapped to local-storage pool", deviceNameLocation.diskNamePath)
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
		return symLinkErr
	}

	successMsg := fmt.Sprintf("found matching disk %s", baseDeviceName)
	e := newSuccessEvent(FoundMatchingDisk, successMsg, deviceNameLocation.diskNamePath)
	d.eventSync.report(e, d.localVolume)
	return nil
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
				msg := fmt.Sprintf("unable to find matching disk %v", diskName)
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

// getPVCreationLock checks whether a PV can be created based on this device
// and Locks the device so that no PVs can be created on it while the lock is held.
// the PV lock will fail if:
// - another process holds an exclusive file lock on the device (using the syscall flock)
// - a symlink to this device exists in symlinkDirs
// returns:
// ExclusiveFileLock, must be unlocked regardless of success
// bool determines if flock was placed on device.
// existingLinkPaths is a list of existing symlinks. It is not exhaustive
// error
func getPVCreationLock(device string, symlinkDirs string) (exclusiveFileLock, []string, error) {
	lock := exclusiveFileLock{path: device}
	locked, err := lock.lock()
	if err != nil || !locked {
		return lock, []string{}, err
	}
	existingLinkPaths, err := getMatchingSymlinksInDirs(device, symlinkDirs)
	if err != nil || len(existingLinkPaths) > 0 {
		return lock, existingLinkPaths, err
	}
	return lock, existingLinkPaths, nil
}

// getMatchingSymlinksInDirs returns all the files in dir that are the same file as path after evaluating symlinks
// it works using `find -L dir1 dir2 dirn -samefile path`
func getMatchingSymlinksInDirs(path string, dirs string) ([]string, error) {
	cmd := exec.Command("find", "-L", dirs, "-samefile", path)
	output, err := executeCmdWithCombinedOutput(cmd)
	if err != nil {
		return []string{}, fmt.Errorf("failed to get symlinks in directories: %q for device path %q. %v", dirs, path, err)
	}
	links := make([]string, 0)
	output = strings.Trim(output, " \n")
	if len(output) < 1 {
		return links, nil
	}
	split := strings.Split(output, "\n")
	for _, entry := range split {
		link := strings.Trim(entry, " ")
		if len(link) != 0 {
			links = append(links, link)
		}
	}
	return links, nil
}

type exclusiveFileLock struct {
	path   string
	locked bool
	fd     int
}

// lock locks the file so other process cannot open the file
func (e *exclusiveFileLock) lock() (bool, error) {
	// fd, errno := unix.Open(e.Path, unix.O_RDONLY, 0)
	fd, errno := unix.Open(e.path, unix.O_RDONLY|unix.O_EXCL, 0)
	e.fd = fd
	if errno == unix.EBUSY {
		e.locked = false
		// device is in use
		return false, nil
	} else if errno != nil {
		return false, errno
	}
	e.locked = true
	return e.locked, nil

}

// unlock releases the lock. It is idempotent
func (e *exclusiveFileLock) unlock() error {
	if e.locked {
		err := unix.Close(e.fd)
		if err == nil {
			e.locked = false
			return nil
		}
		return fmt.Errorf("failed to unlock fd %q: %+v", e.fd, err)
	}
	return nil

}

func executeCmdWithCombinedOutput(cmd *exec.Cmd) (string, error) {
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}
