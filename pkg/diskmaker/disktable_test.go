package diskmaker

import (
	"testing"
)

func TestDiskTableParse(t *testing.T) {
	output := getRawOutput()
	d := newDiskTable()
	d.parse(output)
	if len(d.disks) != 11 {
		t.Errorf("expected 11 disks got %d", len(d.disks))
	}

	usableDisks := d.filterUsableDisks()
	if len(usableDisks) != 5 {
		t.Errorf("expected 5 usable disks, got %d", len(usableDisks))
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
