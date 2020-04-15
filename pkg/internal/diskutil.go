package internal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/pkg/errors"
)

const (
	// StateSuspended is a possible value of BlockDevice.State
	StateSuspended = "suspended"
	// DiskByIDDir is the path for symlinks to the device by id.
	DiskByIDDir = "/dev/disk/by-id/"
)

// BlockDevice is the a block device as output by lsblk.
// All the fields are lsblk columns.
type BlockDevice struct {
	Name       string        `json:"name"`
	Type       string        `json:"type"`
	Model      string        `json:"mode,omitempty"`
	Vendor     string        `json:"vendor,omitempty"`
	State      string        `json:"state,omitempty"`
	Size       int           `json:"size"`
	Rotational bool          `json:"rota"`
	ReadOnly   bool          `json:"ro,omitempty"`
	Removable  bool          `json:"rm,omitempty"`
	Children   []BlockDevice `json:"children,omitempty"`
	pathByID   string
}

// HasChildren check on BlockDevice
func (b BlockDevice) HasChildren() bool {
	if len(b.Children) > 0 {
		return true
	}
	return false
}

// GetPathByID check on BlockDevice
func (b BlockDevice) GetPathByID() (string, error) {

	// return if previously populated value is valid
	if len(b.pathByID) > 0 && filepath.HasPrefix(b.pathByID, DiskByIDDir) {
		evalsCorrectly, err := pathEvalsToDisk(b.pathByID, b.Name)
		if err == nil && evalsCorrectly {
			return b.pathByID, nil
		}
	}
	b.pathByID = ""

	paths, err := filepath.Glob(DiskByIDDir)
	if err != nil {
		return "", errors.Wrapf(err, "could not list files in %q", DiskByIDDir)
	}
	for _, path := range paths {
		isMatch, err := pathEvalsToDisk(path, b.Name)
		if err != nil {
			return "", err
		}
		if isMatch {
			b.pathByID = path
			return path, nil
		}
	}
	return "", fmt.Errorf("could not find symlink to %q in %q", b.Name, DiskByIDDir)
}

// pathEvalsToDisk checks if the path is a symplink to a file devName
func pathEvalsToDisk(path, devName string) (bool, error) {
	devPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, errors.Wrapf(err, "could not eval symLink %q", path)
	}
	if filepath.Base(devPath) == devName {
		return true, nil
	}
	return false, nil
}

// ListBlockDevices using lsblk
func ListBlockDevices() ([]BlockDevice, error) {
	var output bytes.Buffer
	var parsedOutput LsblkOutput

	columns := "NAME,ROTA,TYPE,SIZE,MODEL,VENDOR,RO,RM,STATE"
	args := []string{"-J", "-b", "-o", columns}
	cmd := exec.Command("lsblk", args...)
	cmd.Stdout = &output

	err := cmd.Run()
	if err != nil {
		return []BlockDevice{}, err
	}

	err = json.Unmarshal(output.Bytes(), &parsedOutput)
	if err != nil {
		return []BlockDevice{}, err
	}

	return parsedOutput.Blockdevices, nil
}

// LsblkOutput is the structured output of lsblk
type LsblkOutput struct {
	Blockdevices []BlockDevice `json:"blockdevices"`
}
