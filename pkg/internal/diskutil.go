package internal

import (
	"bytes"
	"encoding/json"
	"os/exec"
)

const (
	// StateSuspended is a possible value of BlockDevice.State
	StateSuspended = "suspended"
)

// BlockDevice is the a block device as output by lsblk.
// All the fields are lsblk columns.
type BlockDevice struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Model      string `json:"mode,omitempty"`
	Vendor     string `json:"vendor,omitempty"`
	State      string `json:"state,omitempty"`
	Size       int    `json:"size"`
	Rotational bool   `json:"rota"`
	// Size in bytes
	ReadOnly  bool          `json:"ro,omitempty"`
	Removable bool          `json:"rm,omitempty"`
	Children  []BlockDevice `json:"children,omitempty"`
}

// HasChildren check on BlockDevice
func (b BlockDevice) HasChildren() bool {
	if len(b.Children) > 0 {
		return true
	}
	return false
}

// ListBlockDevices using lsblk
func ListBlockDevices() ([]BlockDevice, error) {
	var output bytes.Buffer
	var parsedOutput LsblkOutput

	columns := "NAME,ROTA,TYPE,SIZE,MODEL,VENDOR,RO,RM,STATE,"
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
