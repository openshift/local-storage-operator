package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"k8s.io/klog/v2"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

var (
	ExecCommand          = exec.Command
	FilePathGlob         = filepath.Glob
	FilePathEvalSymLinks = filepath.EvalSymlinks
	mountFile            = "/proc/1/mountinfo"
)

const (
	// StateSuspended is a possible value of BlockDevice.State
	StateSuspended = "suspended"
	// DiskByIDDir is the path for symlinks to the device by id.
	DiskByIDDir = "/dev/disk/by-id/"
	// DiskDMDir is the path for symlinks of device mapper disks (e.g. mpath)
	DiskDMDir = "/dev/mapper/"
)

// IDPathNotFoundError indicates that a symlink to the device was not found in /dev/disk/by-id/
type IDPathNotFoundError struct {
	DeviceName string
}

func (e IDPathNotFoundError) Error() string {
	return fmt.Sprintf("IDPathNotFoundError: a symlink to  %q was not found in %q", e.DeviceName, DiskByIDDir)
}

// BlockDevice is the a block device as output by lsblk.
// All the fields are lsblk columns.
type BlockDevice struct {
	Name   string `json:"name"`
	KName  string `json:"kname"`
	Type   string `json:"type"`
	Model  string `json:"model,omitempty"`
	Vendor string `json:"vendor,omitempty"`
	State  string `json:"state,omitempty"`
	FSType string `json:"fsType"`
	Size   string `json:"size"`
	// Children   []BlockDevice `json:"children,omitempty"`
	Rotational string `json:"rota"`
	ReadOnly   string `json:"ro,omitempty"`
	Removable  string `json:"rm,omitempty"`
	PathByID   string `json:"pathByID,omitempty"`
	Serial     string `json:"serial,omitempty"`
	PartLabel  string `json:"partLabel,omitempty"`
}

// IDPathNotFoundError indicates that a symlink to the device was not found in /dev/disk/by-id/

// GetRotational as bool
func (b BlockDevice) GetRotational() (bool, error) {
	v, err := parseBitBool(b.Rotational)
	if err != nil {
		err = errors.Wrapf(err, "failed to parse rotational property %q as bool", b.Rotational)
	}
	return v, err
}

// GetReadOnly as bool
func (b BlockDevice) GetReadOnly() (bool, error) {
	v, err := parseBitBool(b.ReadOnly)
	if err != nil {
		err = errors.Wrapf(err, "failed to parse readOnly property %q as bool", b.ReadOnly)
	}
	return v, err
}

// GetRemovable as bool
func (b BlockDevice) GetRemovable() (bool, error) {
	v, err := parseBitBool(b.Removable)
	if err != nil {
		err = errors.Wrapf(err, "failed to parse removable property %q as bool", b.Removable)
	}
	return v, err
}

func parseBitBool(s string) (bool, error) {
	if s == "0" || s == "" {
		return false, nil
	} else if s == "1" {
		return true, nil
	}
	return false, fmt.Errorf("lsblk bool value not 0 or 1: %q", s)
}

// GetSize as int64
func (b BlockDevice) GetSize() (int64, error) {
	v, err := strconv.ParseInt(b.Size, 10, 64)
	if err != nil {
		err = errors.Wrapf(err, "failed to parse size property %q as int64", b.Size)
	}
	return v, err
}

// HasChildren check on BlockDevice
func (b BlockDevice) HasChildren() (bool, error) {
	sysDevDir := filepath.Join("/sys/block/", b.KName, "/*")
	paths, err := FilePathGlob(sysDevDir)
	if err != nil {
		return false, errors.Wrapf(err, "failed to check if device %q has partitions", b.KName)
	}
	for _, path := range paths {
		name := filepath.Base(path)
		if strings.HasPrefix(name, b.KName) {
			return true, nil
		}
	}
	return false, nil
}

// HasBindMounts checks for bind mounts and returns mount point for a device by parsing `proc/1/mountinfo`.
// HostPID should be set to true inside the POD spec to get details of host's mount points inside `proc/1/mountinfo`.
func (b *BlockDevice) HasBindMounts() (bool, string, error) {
	data, err := os.ReadFile(mountFile)
	if err != nil {
		return false, "", fmt.Errorf("failed to read file %s: %v", mountFile, err)
	}

	mountString := string(data)
	for _, mountInfo := range strings.Split(mountString, "\n") {
		if strings.Contains(mountInfo, b.KName) {
			mountInfoList := strings.Split(mountInfo, " ")
			if len(mountInfoList) >= 10 {
				// device source is 4th field for bind mounts and 10th for regular mounts
				if mountInfoList[3] == fmt.Sprintf("/%s", b.KName) || mountInfoList[9] == fmt.Sprintf("/dev/%s", b.KName) {
					return true, mountInfoList[4], nil
				}
			}
		}
	}

	return false, "", nil
}

// GetDevPath for block device (/dev/sdx)
func (b BlockDevice) GetDevPath() (path string, err error) {
	if b.KName == "" {
		path = ""
		err = fmt.Errorf("empty KNAME")
	}

	path = filepath.Join("/dev/", b.KName)

	return
}

// GetPathByID check on BlockDevice
func (b *BlockDevice) GetPathByID() (string, error) {

	// return if previously populated value is valid
	if len(b.PathByID) > 0 && strings.HasPrefix(b.PathByID, DiskByIDDir) {
		evalsCorrectly, err := PathEvalsToDiskLabel(b.PathByID, b.KName)
		if err == nil && evalsCorrectly {
			return b.PathByID, nil
		}
	}
	b.PathByID = ""
	allDisks, err := FilePathGlob(filepath.Join(DiskByIDDir, "/*"))
	if err != nil {
		return "", fmt.Errorf("error listing files in %s: %v", DiskByIDDir, err)
	}
	preferredPatterns := []string{"wwn", "scsi", "nvme", ""}

	// sortedSymlinks sorts symlinks in 4 buckets.
	// 	- [0] - syminks that match wwn
	//	- [1] - symlinks that match scsi
	//	- [2] - symlinks that match nvme
	//	- [3] - symlinks that does not any of these
	sortedSymlinks := make([][]string, len(preferredPatterns))

	for _, path := range allDisks {
		for i, pattern := range preferredPatterns {
			symLinkName := filepath.Base(path)
			if strings.HasPrefix(symLinkName, pattern) {
				sortedSymlinks[i] = append(sortedSymlinks[i], path)
				break
			}
		}
	}

	for _, groupedLink := range sortedSymlinks {
		for _, path := range groupedLink {
			isMatch, err := PathEvalsToDiskLabel(path, b.KName)
			if err != nil {
				return "", err
			}
			if isMatch {
				b.PathByID = path
				return path, nil
			}
		}
	}

	devPath, err := b.GetDevPath()
	if err != nil {
		return "", err
	}
	// return path by label and error
	return devPath, IDPathNotFoundError{DeviceName: b.KName}
}

// PathEvalsToDiskLabel checks if the path is a symplink to a file devName
func PathEvalsToDiskLabel(path, devName string) (bool, error) {
	devPath, err := FilePathEvalSymLinks(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("could not eval symLink %q:%w", devPath, err)
	}
	if filepath.Base(devPath) == devName {
		return true, nil
	}
	return false, nil
}

// ListBlockDevices using the lsblk command
func ListBlockDevices(devices []string) ([]BlockDevice, []string, error) {
	// var output bytes.Buffer
	var blockDevices []BlockDevice

	deviceFSMap, err := GetDeviceFSMap(devices)
	if err != nil {
		return []BlockDevice{}, []string{}, errors.Wrap(err, "failed to list block devices")
	}

	columns := "NAME,ROTA,TYPE,SIZE,MODEL,VENDOR,RO,RM,STATE,KNAME,SERIAL,PARTLABEL"
	args := []string{"--pairs", "-b", "-o", columns}
	cmd := ExecCommand("lsblk", args...)
	klog.Infof("Executing command: %#v", cmd)
	output, err := executeCmdWithCombinedOutput(cmd)
	if err != nil {
		return []BlockDevice{}, []string{output}, err
	}
	badRows := make([]string, 0)
	// convert to json and then Marshal.
	outputMapList := make([]map[string]interface{}, 0)
	rowList := strings.Split(output, "\n")
	for _, row := range rowList {
		if len(strings.Trim(row, " ")) == 0 {
			break
		}
		outputMap := make(map[string]interface{})
		// split by `" ` to avoid splitting on spaces in MODEL,VENDOR
		keyValues := strings.Split(row, `" `)
		for _, keyValue := range keyValues {
			keyValueList := strings.Split(keyValue, "=")
			if len(keyValueList) != 2 {
				continue
			}
			key := strings.ToLower(keyValueList[0])
			value := strings.Replace(keyValueList[1], `"`, "", -1)
			outputMap[key] = strings.TrimSpace(value)
		}

		// only use device if name is populated, and non-empty
		v, found := outputMap["name"]
		if !found {
			badRows = append(badRows, row)
			break
		}
		name := v.(string)
		if len(strings.Trim(name, " ")) == 0 {
			badRows = append(badRows, row)
			break
		}
		if len(badRows) > 0 {
			klog.Warningf("failed to parse all the lsblk rows. Bad rows: %+v", badRows)
		}

		// Update device filesystem using `blkid`
		if fs, ok := deviceFSMap[fmt.Sprintf("/dev/%s", name)]; ok {
			outputMap["fsType"] = fs
		}

		outputMapList = append(outputMapList, outputMap)
	}

	if len(badRows) == len(rowList) {
		return []BlockDevice{}, badRows, fmt.Errorf("could not parse any of the lsblk rows")
	}

	jsonBytes, err := json.Marshal(outputMapList)
	if err != nil {
		return []BlockDevice{}, badRows, err
	}

	err = json.Unmarshal(jsonBytes, &blockDevices)
	if err != nil {
		return []BlockDevice{}, badRows, err
	}

	return blockDevices, badRows, nil
}

// GetDeviceFSMap returns mapping between disks and the filesystem using blkid
// It parses the output of `blkid -s TYPE`. Sample ouput format before parsing
// `/dev/sdc: TYPE="ext4"
// /dev/sdd: TYPE="ext2"`
// If devices is empty, it scans all disks, otherwise only devices.
func GetDeviceFSMap(devices []string) (map[string]string, error) {
	m := map[string]string{}
	args := append([]string{"-s", "TYPE"}, devices...)
	cmd := ExecCommand("blkid", args...)
	output, err := executeCmdWithCombinedOutput(cmd)
	if err != nil {
		// According to blkid man page, exit status 2 is returned
		// if no device found.
		if exiterr, ok := err.(*exec.ExitError); ok {
			if exiterr.ExitCode() == 2 {
				return map[string]string{}, nil
			}
		}
		return map[string]string{}, err
	}
	lines := strings.Split(output, "\n")
	for _, l := range lines {
		if len(l) <= 0 {
			// Ignore empty line.
			continue
		}

		values := strings.Split(l, ":")
		if len(values) != 2 {
			continue
		}

		fs := strings.Split(values[1], "=")
		if len(fs) != 2 {
			continue
		}

		m[values[0]] = strings.Trim(strings.TrimSpace(fs[1]), "\"")
	}

	return m, nil
}

// GetPVCreationLock checks whether a PV can be created based on this device
// and Locks the device so that no PVs can be created on it while the lock is held.
// the PV lock will fail if:
// - another process holds an exclusive file lock on the device (using the syscall flock)
// - a symlink to this device exists in symlinkDirs
// returns:
// ExclusiveFileLock, must be unlocked regardless of success
// bool determines if flock was placed on device.
// existingLinkPaths is a list of existing symlinks. It is not exhaustive
// error
func GetPVCreationLock(device string, symlinkDirs ...string) (ExclusiveFileLock, bool, []string, error) {
	lock := ExclusiveFileLock{Path: device}
	locked, err := lock.Lock()
	// If the device is busy, then we should continue and check for symlinks
	if err != nil && err != unix.EBUSY {
		return lock, locked, []string{}, err
	}
	existingLinkPaths, symErr := GetMatchingSymlinksInDirs(device, symlinkDirs...)
	// If symErr is not nil, there was an error fetching the symlinks
	if symErr != nil {
		return lock, locked, existingLinkPaths, symErr
	} else if len(existingLinkPaths) == 0 && !locked {
		// Alternatively, if we don't have any existing symlinks AND we can't get a lock,
		// then the device is likely busy, and return the original error.
		return lock, locked, existingLinkPaths, err
	}

	return lock, locked, existingLinkPaths, nil
}

// GetMatchingSymlinksInDirs returns all the files in dir that are the same file as path after evaluating symlinks
// it works using `find -L dir1 dir2 dirn -samefile path`
func GetMatchingSymlinksInDirs(path string, dirs ...string) ([]string, error) {
	cmd := exec.Command("find", "-L", strings.Join(dirs, " "), "-samefile", path)
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

// GetOrphanedSymlinks returns the devices that were symlinked previously, but didn't match the updated
// LocalVolumeSet deviceInclusionSpec or LocalVolume devicePaths
func GetOrphanedSymlinks(symlinkDir string, validDevices []BlockDevice) ([]string, error) {
	orphanedSymlinkDevices := []string{}
	paths, err := FilePathGlob(filepath.Join(symlinkDir, "/*"))
	if err != nil {
		return orphanedSymlinkDevices, err
	}

	for _, path := range paths {
		symlinkFound := false
		for _, device := range validDevices {
			isMatch, err := PathEvalsToDiskLabel(path, device.KName)
			if err != nil {
				return orphanedSymlinkDevices, err
			}
			if isMatch {
				symlinkFound = true
				break
			}
		}
		if !symlinkFound {
			orphanedSymlinkDevices = append(orphanedSymlinkDevices, path)
		}
	}

	return orphanedSymlinkDevices, nil
}

type ExclusiveFileLock struct {
	Path   string
	locked bool
	fd     int
}

// Lock locks the file so other process cannot open the file
func (e *ExclusiveFileLock) Lock() (bool, error) {
	// fd, errno := unix.Open(e.Path, unix.O_RDONLY, 0)
	fd, errno := unix.Open(e.Path, unix.O_RDONLY|unix.O_EXCL, 0)
	e.fd = fd
	if errno == unix.EBUSY {
		e.locked = false
		// device is in use
		return false, errno
	} else if errno != nil {
		return false, errno
	}
	e.locked = true
	return e.locked, nil

}

// Unlock releases the lock. It is idempotent
func (e *ExclusiveFileLock) Unlock() error {
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
		return string(output), err
	}
	return strings.TrimSpace(string(output)), nil
}
