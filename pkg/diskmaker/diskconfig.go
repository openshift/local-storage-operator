package diskmaker

import (
	"fmt"
	"strings"

	"github.com/ghodss/yaml"
	"k8s.io/apimachinery/pkg/util/sets"
)

// Disks defines disks to be used for local volumes
type Disks struct {
	DevicePaths []string `json:"devicePaths,omitempty"`
}

// DeviceNames returns devices which are used by name.
// Such as - /dev/sda, /dev/xvdba
func (d *Disks) DeviceNames() sets.String {
	deviceNames := sets.NewString()
	for _, disk := range d.DevicePaths {
		deviceParts := strings.Split(disk, "/")
		if len(deviceParts) == 3 && (deviceParts[1] == "dev") {
			deviceNames.Insert(disk)
		}
	}
	return deviceNames
}

// DeviceIDs returns devices which are specified by ids.
// For example - /dev/disk/by-id/abcde
func (d *Disks) DeviceIDs() sets.String {
	deviceNames := d.DeviceNames()
	allDevicePaths := sets.NewString(d.DevicePaths...)
	return allDevicePaths.Difference(deviceNames)
}

// DiskConfig stores a mapping between StorageClass Name and disks that the storageclass
// will use on each matached node.
type DiskConfig struct {
	Disks           map[string]*Disks `json:"disks,omitempty"`
	OwnerName       string            `json:"ownerName,omitempty"`
	OwnerNamespace  string            `json:"ownerNamespace,omitempty"`
	OwnerKind       string            `json:"ownerKind,omitempty"`
	OwnerUID        string            `json:"ownerUID,omitempty"`
	OwnerAPIVersion string            `json:ownerAPIVersion,omitempty`
}

// ToYAML returns yaml representation of diskconfig
func (d *DiskConfig) ToYAML() (string, error) {
	y, err := yaml.Marshal(d)
	if err != nil {
		return "", fmt.Errorf("error marshaling to yaml: %v", err)
	}
	return string(y), nil
}
