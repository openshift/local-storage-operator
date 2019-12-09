package diskmaker

import (
	"reflect"
	"testing"
)

func TestDiskTableParse(t *testing.T) {
	output := getRawOutput()
	d := newDiskTable()
	d.parse(output)
	expectedDiskMap := []blockDevice{
		map[string]string{"KNAME": "sda", "MOUNTPOINT": "", "PKNAME": "", "TYPE": "disk"},
		map[string]string{"KNAME": "sda1", "MOUNTPOINT": "/boot/efi", "PKNAME": "sda", "TYPE": "part"},
		map[string]string{"KNAME": "sda2", "MOUNTPOINT": "[SWAP]", "PKNAME": "sda", "TYPE": "part"},
		map[string]string{"KNAME": "sda3", "MOUNTPOINT": "/", "PKNAME": "sda", "TYPE": "part"},
		map[string]string{"KNAME": "sdb", "MOUNTPOINT": "", "PKNAME": "", "TYPE": "disk"},
		map[string]string{"KNAME": "sdb1", "MOUNTPOINT": "", "PKNAME": "sdb", "TYPE": "part"},
		map[string]string{"KNAME": "sdb2", "MOUNTPOINT": "", "PKNAME": "sdb", "TYPE": "part"},
		map[string]string{"KNAME": "sdc", "MOUNTPOINT": "", "PKNAME": "", "TYPE": "disk"},
		map[string]string{"KNAME": "sdc1", "MOUNTPOINT": "", "PKNAME": "sdc", "TYPE": "part"},
		map[string]string{"KNAME": "sdc2", "MOUNTPOINT": "", "PKNAME": "sdc", "TYPE": "part"},
		map[string]string{"KNAME": "sdc3", "MOUNTPOINT": "", "PKNAME": "sdc", "TYPE": "part"},
	}
	if !reflect.DeepEqual(expectedDiskMap, d.disks) {
		t.Errorf("Expexted disk map to be: %+v got %+v", expectedDiskMap, d.disks)
	}
	expectedUsableDisks := []blockDevice{
		map[string]string{"KNAME": "sdb1", "MOUNTPOINT": "", "PKNAME": "sdb", "TYPE": "part"},
		map[string]string{"KNAME": "sdb2", "MOUNTPOINT": "", "PKNAME": "sdb", "TYPE": "part"},
		map[string]string{"KNAME": "sdc1", "MOUNTPOINT": "", "PKNAME": "sdc", "TYPE": "part"},
		map[string]string{"KNAME": "sdc2", "MOUNTPOINT": "", "PKNAME": "sdc", "TYPE": "part"},
		map[string]string{"KNAME": "sdc3", "MOUNTPOINT": "", "PKNAME": "sdc", "TYPE": "part"},
	}
	usableDisks := d.filterUsableDisks()
	if !reflect.DeepEqual(expectedUsableDisks, usableDisks) {
		t.Errorf("expected usable disks to be: %+v got %+v", expectedUsableDisks, usableDisks)
	}

}

func getRawOutput() string {
	return `
KNAME="sda" PKNAME="" TYPE="disk" MOUNTPOINT=""
KNAME="sda1" PKNAME="sda" TYPE="part" MOUNTPOINT="/boot/efi"
KNAME="sda2" PKNAME="sda" TYPE="part" MOUNTPOINT="[SWAP]"
KNAME="sda3" PKNAME="sda" TYPE="part" MOUNTPOINT="/"
KNAME="sdb" PKNAME="" TYPE="disk" MOUNTPOINT=""
KNAME="sdb1" PKNAME="sdb" TYPE="part" MOUNTPOINT=""
KNAME="sdb2" PKNAME="sdb" TYPE="part" MOUNTPOINT=""
KNAME="sdc" PKNAME="" TYPE="disk" MOUNTPOINT=""
KNAME="sdc1" PKNAME="sdc" TYPE="part" MOUNTPOINT=""
KNAME="sdc2" PKNAME="sdc" TYPE="part" MOUNTPOINT=""
KNAME="sdc3" PKNAME="sdc" TYPE="part" MOUNTPOINT=""
`
}
