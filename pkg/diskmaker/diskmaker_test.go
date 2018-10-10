package diskmaker

import (
	"testing"
)

func TestFindMatchingDisk(t *testing.T) {
	d := NewDiskMaker("/tmp/foo", "/mnt/local-storage")
	deviceSet, err := d.findNewDisks(getData())
	if err != nil {
		t.Fatalf("error getting data %v", err)
	}
	if len(deviceSet) != 7 {
		t.Errorf("expected 7 devices got %d", len(deviceSet))
	}
	diskConfig := map[string]*Disks{
		"foo": &Disks{
			DiskNames: []string{"vda"},
		},
		"bar": &Disks{
			DiskPatterns: []string{"vd*"},
		},
	}
	deviceMap, err := d.findMatchingDisks(diskConfig, deviceSet)
	if err != nil {
		t.Fatalf("error finding matchin device %v", err)
	}
	if len(deviceMap) != 2 {
		t.Errorf("expected 2 elements in map got %d", len(deviceMap))
	}
}

func getData() string {
	return `
sda
sda1 /boot
sda2 [SWAP]
sda3 /
vda
vdb
vdc
vdd
vde
vdf`
}
