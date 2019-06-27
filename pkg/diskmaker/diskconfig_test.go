package diskmaker

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
)

func TestDeviceNames(t *testing.T) {
	disks := &Disks{
		DevicePaths: []string{
			"/dev/sda",
			"/dev/xvdba",
			"/dev/disk/by-id/foobar",
			"/dev/disk/by-uuid/baz",
		},
	}

	deviceNames := disks.DeviceNames()
	expectedDeviceNames := sets.NewString("/dev/sda", "/dev/xvdba")
	if !expectedDeviceNames.Equal(deviceNames) {
		t.Fatalf("expected device names to be : %v got %v", expectedDeviceNames, deviceNames)
	}

	deviceIds := disks.DeviceIDs()
	expectedDeviceIds := sets.NewString("/dev/disk/by-id/foobar", "/dev/disk/by-uuid/baz")

	if !expectedDeviceIds.Equal(deviceIds) {
		t.Fatalf("expected device ids to be %v got %v", expectedDeviceIds, deviceIds)
	}
}
