package diskmaker

import (
	"fmt"

	"github.com/ghodss/yaml"
)

// Disks defines disks to be used for local volumes
type Disks struct {
	DiskPatterns []string `json:"diskPatterns"`
	DiskNames    []string `json:"disks"`
}

type DiskConfig map[string]*Disks

// ToYAML returns yaml representation of diskconfig
func (d *DiskConfig) ToYAML() (string, error) {
	y, err := yaml.Marshal(d)
	if err != nil {
		return "", fmt.Errorf("error marshaling to yaml: %v", err)
	}
	return string(y), nil
}
