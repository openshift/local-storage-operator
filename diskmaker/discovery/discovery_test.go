package discovery

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/diskmaker"
	"github.com/openshift/local-storage-operator/internal"
	"github.com/stretchr/testify/assert"
)

var lsblkOut string
var blkidOut string

// helperCommand returns a fake exec.Cmd for unit tests
func helperCommand(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1", fmt.Sprintf("COMMAND=%s", command),
		fmt.Sprintf("LSBLKOUT=%s", lsblkOut), fmt.Sprintf("BLKIDOUT=%s", blkidOut)}
	return cmd
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	defer os.Exit(0)
	switch os.Getenv("COMMAND") {
	case "lsblk":
		fmt.Fprintf(os.Stdout, os.Getenv("LSBLKOUT"))
	case "blkid":
		fmt.Fprintf(os.Stdout, os.Getenv("BLKIDOUT"))
	}
}

func TestDiscoverDevices(t *testing.T) {
	testcases := []struct {
		deviceDiscovery    *DeviceDiscovery
		fakelsblkCmdOutput string
		fakeblkidCmdOutput string
		fakeGlobfunc       func(string) ([]string, error)
		errMessage         error
	}{
		{
			deviceDiscovery:    getFakeDeviceDiscovery(),
			fakeblkidCmdOutput: "",
			fakelsblkCmdOutput: `NAME="sda" KNAME="sda" ROTA="1" TYPE="disk" SIZE="62914560000" MODEL="VBOX HARDDISK" VENDOR="ATA" RO="0" RM="0" STATE="running" SERIAL=""` + "\n" +
				`NAME="sda1" KNAME="sda1" ROTA="1" TYPE="part" SIZE="62913494528" MODEL="" VENDOR="" RO="0" RM="0" STATE="" SERIAL=""`,
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"removable", "subsytem", "sda"}, nil
			},
			errMessage: nil,
		},
	}

	for _, tc := range testcases {
		lsblkOut = tc.fakelsblkCmdOutput
		blkidOut = tc.fakeblkidCmdOutput
		internal.ExecCommand = helperCommand
		internal.FilePathGlob = tc.fakeGlobfunc
		defer func() {
			internal.FilePathGlob = filepath.Glob
			internal.ExecCommand = exec.Command
		}()
		err := tc.deviceDiscovery.discoverDevices()
		assert.NoError(t, err)
	}

}
func TestDiscoverDevicesFail(t *testing.T) {
	testcases := []struct {
		deviceDiscovery    *DeviceDiscovery
		mockClient         *diskmaker.MockAPIUpdater
		fakeLsblkCmdOutput string
		fakeGlobfunc       func(string) ([]string, error)
		errMessage         error
	}{
		{
			deviceDiscovery: getFakeDeviceDiscovery(),
			mockClient: &diskmaker.MockAPIUpdater{
				MockUpdateDiscoveryResultStatus: func(lvdr *v1alpha1.LocalVolumeDiscoveryResult) error {
					return fmt.Errorf("failed to update status")
				},
			},
			fakeLsblkCmdOutput: `NAME="sda" KNAME="sda" ROTA="1" TYPE="disk" SIZE="62914560000" MODEL="VBOX HARDDISK" VENDOR="ATA" RO="1" RM="0" STATE="running" FSTYPE="" SERIAL=""` + "\n" +
				`NAME="sda1" KNAME="sda1" ROTA="1" TYPE="part" SIZE="62913494528" MODEL="" VENDOR="" RO="0" RM="0" STATE="" SERIAL=""`,
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"removable", "subsytem"}, nil
			},
			errMessage: nil,
		},
	}

	for _, tc := range testcases {
		lsblkOut = tc.fakeLsblkCmdOutput
		internal.ExecCommand = helperCommand
		internal.FilePathGlob = tc.fakeGlobfunc
		defer func() {
			internal.FilePathGlob = filepath.Glob
			internal.ExecCommand = exec.Command
		}()
		tc.deviceDiscovery.apiClient = tc.mockClient
		err := tc.deviceDiscovery.discoverDevices()
		assert.Error(t, err)
	}
}

func TestIgnoreDevices(t *testing.T) {
	testcases := []struct {
		label        string
		blockDevice  internal.BlockDevice
		fakeGlobfunc func(string) ([]string, error)
		expected     bool
		errMessage   error
	}{
		{
			label: "Case 1: don't ignore disk type",
			blockDevice: internal.BlockDevice{
				Name:     "sdb",
				KName:    "sdb",
				ReadOnly: "0",
				State:    "running",
				Type:     "disk",
			},
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"removable", "subsytem"}, nil
			},
			expected:   false,
			errMessage: fmt.Errorf("ignored wrong device"),
		},
		{
			label: "Case 2: don't ignore lvm type",
			blockDevice: internal.BlockDevice{
				Name:     "sdb",
				KName:    "sdb",
				ReadOnly: "0",
				State:    "running",
				Type:     "lvm",
			},
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"removable", "subsytem"}, nil
			},
			expected:   false,
			errMessage: fmt.Errorf("ignored wrong device"),
		},
		{
			label: "Case 3: ignore read only devices",
			blockDevice: internal.BlockDevice{
				Name:     "sdb",
				KName:    "sdb",
				ReadOnly: "1",
				State:    "running",
				Type:     "disk",
			},
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"removable", "subsytem"}, nil
			},
			expected:   true,
			errMessage: fmt.Errorf("failed to ignore read only device"),
		},
		{
			label: "Case 4: ignore devices in suspended state",
			blockDevice: internal.BlockDevice{
				Name:     "sdb",
				KName:    "sdb",
				ReadOnly: "0",
				State:    "suspended",
				Type:     "disk",
			},
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"removable", "subsytem"}, nil
			},
			expected:   true,
			errMessage: fmt.Errorf("ignored wrong suspended device"),
		},
		{
			label: "Case 5: ignore root device with children",
			blockDevice: internal.BlockDevice{
				Name:     "sdb",
				KName:    "sdb",
				ReadOnly: "0",
				State:    "running",
				Type:     "disk",
			},
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"removable", "subsytem", "sdb"}, nil
			},
			expected:   true,
			errMessage: fmt.Errorf("failed to ignore root device with children"),
		},
	}

	for _, tc := range testcases {
		internal.FilePathGlob = tc.fakeGlobfunc
		defer func() {
			internal.FilePathGlob = filepath.Glob
		}()

		actual := ignoreDevices(tc.blockDevice)
		assert.Equalf(t, tc.expected, actual, "[%s]: %s", tc.label, tc.errMessage)
	}
}

func TestValidBlockDevices(t *testing.T) {
	testcases := []struct {
		label                        string
		blockDevices                 []internal.BlockDevice
		fakeLsblkCmdOutput           string
		fakeblkidCmdOutput           string
		fakeGlobfunc                 func(string) ([]string, error)
		expectedDiscoveredDeviceSize int
		errMessage                   error
	}{
		{
			label:              "Case 1: ignore readonly device sda",
			fakeblkidCmdOutput: "",
			fakeLsblkCmdOutput: `NAME="sda" KNAME="sda" ROTA="1" TYPE="disk" SIZE="62914560000" MODEL="VBOX HARDDISK" VENDOR="ATA" RO="1" RM="0" STATE="running" SERIAL=""` + "\n" +
				`NAME="sda1" KNAME="sda1" ROTA="1" TYPE="part" SIZE="62913494528" MODEL="" VENDOR="" RO="0" RM="0" STATE=""  SERIAL=""`,
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"removable", "subsytem"}, nil
			},
			expectedDiscoveredDeviceSize: 1,
			errMessage:                   fmt.Errorf("failed to ignore readonly device sda"),
		},
		{
			label:              "Case 2: ignore root device sda",
			fakeblkidCmdOutput: "",
			fakeLsblkCmdOutput: `NAME="sda" KNAME="sda" ROTA="1" TYPE="disk" SIZE="62914560000" MODEL="VBOX HARDDISK" VENDOR="ATA" RO="0" RM="0" STATE="running" SERIAL=""` + "\n" +
				`NAME="sda1" KNAME="sda1" ROTA="1" TYPE="part" SIZE="62913494528" MODEL="" VENDOR="" RO="0" RM="0" STATE="" SERIAL=""`,
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"removable", "subsytem", "sda"}, nil
			},
			expectedDiscoveredDeviceSize: 1,
			errMessage:                   fmt.Errorf("failed to ignore root device sda with partition"),
		},
		{
			label:              "Case 3: ignore loop device",
			fakeblkidCmdOutput: "",
			fakeLsblkCmdOutput: `NAME="sda" KNAME="sda" ROTA="1" TYPE="loop" SIZE="62914560000" MODEL="VBOX HARDDISK" VENDOR="ATA" RO="0" RM="0" STATE="running" SERIAL=""` + "\n" +
				`NAME="sda1" KNAME="sda1" ROTA="1" TYPE="part" SIZE="62913494528" MODEL="" VENDOR="" RO="0" RM="0" STATE="" SERIAL=""`,
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"removable", "subsytem"}, nil
			},
			expectedDiscoveredDeviceSize: 1,
			errMessage:                   fmt.Errorf("failed to ignore device sda with type loop"),
		},
		{
			label:              "Case 4: ignore device is suspended state",
			fakeblkidCmdOutput: "",
			fakeLsblkCmdOutput: `NAME="sda" KNAME="sda" ROTA="1" TYPE="disk" SIZE="62914560000" MODEL="VBOX HARDDISK" VENDOR="ATA" RO="0" RM="0" STATE="running"  SERIAL=""` + "\n" +
				`NAME="sda1" KNAME="sda1" ROTA="1" TYPE="part" SIZE="62913494528" MODEL="" VENDOR="" RO="0" RM="0" STATE="suspended" SERIAL=""`,
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"removable", "subsytem"}, nil
			},
			expectedDiscoveredDeviceSize: 1,
			errMessage:                   fmt.Errorf("failed to ignore child device sda1 in suspended state"),
		},
	}

	for _, tc := range testcases {
		lsblkOut = tc.fakeLsblkCmdOutput
		blkidOut = tc.fakeblkidCmdOutput
		internal.ExecCommand = helperCommand
		internal.FilePathGlob = tc.fakeGlobfunc
		defer func() {
			internal.FilePathGlob = filepath.Glob
			internal.ExecCommand = exec.Command
		}()
		actual, err := getValidBlockDevices()
		assert.NoError(t, err)
		assert.Equalf(t, tc.expectedDiscoveredDeviceSize, len(actual), "[%s]: %s", tc.label, tc.errMessage)
	}
}

func TestGetDiscoveredDevices(t *testing.T) {
	testcases := []struct {
		label               string
		blockDevices        []internal.BlockDevice
		expected            []v1alpha1.DiscoveredDevice
		fakeGlobfunc        func(string) ([]string, error)
		fakeEvalSymlinkfunc func(string) (string, error)
	}{
		{
			label: "Case 1: discovering device with fstype as NotAvailable",
			blockDevices: []internal.BlockDevice{
				{
					Name:       "sdb",
					KName:      "sdb",
					FSType:     "ext4",
					Type:       "disk",
					Size:       "62914560000",
					Model:      "VBOX HARDDISK",
					Vendor:     "ATA",
					Serial:     "DEVICE_SERIAL_NUMBER",
					Rotational: "1",
					ReadOnly:   "0",
					Removable:  "0",
					State:      "running",
				},
			},
			expected: []v1alpha1.DiscoveredDevice{
				{
					DeviceID: "/dev/disk/by-id/sdb",
					Path:     "/dev/sdb",
					Model:    "VBOX HARDDISK",
					Type:     "disk",
					Vendor:   "ATA",
					Serial:   "DEVICE_SERIAL_NUMBER",
					Size:     int64(62914560000),
					Property: "Rotational",
					FSType:   "ext4",
					Status:   v1alpha1.DeviceStatus{State: "NotAvailable"},
				},
			},
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"/dev/disk/by-id/sdb"}, nil
			},
			fakeEvalSymlinkfunc: func(path string) (string, error) {
				return "/dev/disk/by-id/sdb", nil
			},
		},

		{
			label: "Case 2: discovering device with fstype as NotAvailable",
			blockDevices: []internal.BlockDevice{
				{
					Name:       "sda1",
					KName:      "sda1",
					FSType:     "ext4",
					Type:       "part",
					Size:       "62913494528",
					Model:      "",
					Vendor:     "",
					Serial:     "",
					Rotational: "0",
					ReadOnly:   "0",
					Removable:  "0",
					State:      "running",
				},
			},
			expected: []v1alpha1.DiscoveredDevice{
				{
					DeviceID: "/dev/disk/by-id/sda1",
					Path:     "/dev/sda1",
					Model:    "",
					Type:     "part",
					Vendor:   "",
					Serial:   "",
					Size:     int64(62913494528),
					Property: "NonRotational",
					FSType:   "ext4",
					Status:   v1alpha1.DeviceStatus{State: "NotAvailable"},
				},
			},
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"/dev/disk/by-id/sda1"}, nil
			},
			fakeEvalSymlinkfunc: func(path string) (string, error) {
				return "/dev/disk/by-id/sda1", nil
			},
		},
		{
			label: "Case 3: discovering device with BIOS-BOOT part-label as NotAvailable",
			blockDevices: []internal.BlockDevice{
				{
					Name:       "sda1",
					KName:      "sda1",
					FSType:     "",
					Type:       "part",
					Size:       "62913494528",
					Model:      "",
					Vendor:     "",
					Serial:     "",
					Rotational: "0",
					ReadOnly:   "0",
					Removable:  "0",
					State:      "running",
					PartLabel:  "BIOS-BOOT",
				},
			},
			expected: []v1alpha1.DiscoveredDevice{
				{
					DeviceID: "/dev/disk/by-id/sda1",
					Path:     "/dev/sda1",
					Model:    "",
					Type:     "part",
					Vendor:   "",
					Serial:   "",
					Size:     int64(62913494528),
					Property: "NonRotational",
					FSType:   "",
					Status:   v1alpha1.DeviceStatus{State: "NotAvailable"},
				},
			},
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"/dev/disk/by-id/sda1"}, nil
			},
			fakeEvalSymlinkfunc: func(path string) (string, error) {
				return "/dev/disk/by-id/sda1", nil
			},
		},
		{
			label: "Case 4: discovering device with vfat fstype as NotAvailable",
			blockDevices: []internal.BlockDevice{
				{
					Name:       "sda1",
					KName:      "sda1",
					FSType:     "vfat",
					Type:       "part",
					Size:       "62913494528",
					Model:      "",
					Vendor:     "",
					Serial:     "",
					Rotational: "0",
					ReadOnly:   "0",
					Removable:  "0",
					State:      "running",
					PartLabel:  "EFI-SYSTEM",
				},
			},
			expected: []v1alpha1.DiscoveredDevice{
				{
					DeviceID: "/dev/disk/by-id/sda1",
					Path:     "/dev/sda1",
					Model:    "",
					Type:     "part",
					Vendor:   "",
					Serial:   "",
					Size:     int64(62913494528),
					Property: "NonRotational",
					FSType:   "vfat",
					Status:   v1alpha1.DeviceStatus{State: "NotAvailable"},
				},
			},
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"/dev/disk/by-id/sda1"}, nil
			},
			fakeEvalSymlinkfunc: func(path string) (string, error) {
				return "/dev/disk/by-id/sda1", nil
			},
		},
	}

	for _, tc := range testcases {
		internal.FilePathGlob = tc.fakeGlobfunc
		internal.FilePathEvalSymLinks = tc.fakeEvalSymlinkfunc
		defer func() {
			internal.FilePathGlob = filepath.Glob
			internal.FilePathEvalSymLinks = filepath.EvalSymlinks
		}()

		actual := getDiscoverdDevices(tc.blockDevices)
		for i := 0; i < len(tc.expected); i++ {
			assert.Equalf(t, tc.expected[i].DeviceID, actual[i].DeviceID, "[%s: Discovered Device: %d]: invalid device ID", tc.label, i+1)
			assert.Equalf(t, tc.expected[i].Path, actual[i].Path, "[%s: Discovered Device: %d]: invalid device path", tc.label, i+1)
			assert.Equalf(t, tc.expected[i].Model, actual[i].Model, "[%s: Discovered Device: %d]: invalid device model", tc.label, i+1)
			assert.Equalf(t, tc.expected[i].Type, actual[i].Type, "[%s: Discovered Device: %d]: invalid device type", tc.label, i+1)
			assert.Equalf(t, tc.expected[i].Vendor, actual[i].Vendor, "[%s: Discovered Device: %d]: invalid device vendor", tc.label, i+1)
			assert.Equalf(t, tc.expected[i].Serial, actual[i].Serial, "[%s: Discovered Device: %d]: invalid device serial", tc.label, i+1)
			assert.Equalf(t, tc.expected[i].Size, actual[i].Size, "[%s: Discovered Device: %d]: invalid device size", tc.label, i+1)
			assert.Equalf(t, tc.expected[i].Property, actual[i].Property, "[%s: Discovered Device: %d]: invalid device property", tc.label, i+1)
			assert.Equalf(t, tc.expected[i].FSType, actual[i].FSType, "[%s: Discovered Device: %d]: invalid device filesystem", tc.label, i+1)
			assert.Equalf(t, tc.expected[i].Status, actual[i].Status, "[%s: Discovered Device: %d]: invalid device status", tc.label, i+1)
		}
	}
}

func TestParseDeviceType(t *testing.T) {
	testcases := []struct {
		label    string
		input    string
		expected v1alpha1.DiscoveredDeviceType
	}{
		{
			label:    "Case 1: disk type",
			input:    "disk",
			expected: v1alpha1.DiskType,
		},
		{
			label:    "Case 2: part type",
			input:    "part",
			expected: v1alpha1.PartType,
		},
		{
			label:    "Case 3: lvm type",
			input:    "lvm",
			expected: v1alpha1.LVMType,
		},
		{
			label:    "Case 4: loop device type",
			input:    "loop",
			expected: "",
		},
	}

	for _, tc := range testcases {
		actual := parseDeviceType(tc.input)
		assert.Equalf(t, tc.expected, actual, "[%s]: failed to parse device type", tc.label)
	}
}

func TestParseDeviceProperty(t *testing.T) {
	testcases := []struct {
		label    string
		input    string
		expected v1alpha1.DeviceMechanicalProperty
	}{
		{
			label:    "Case 1: rotational device",
			input:    "1",
			expected: v1alpha1.Rotational,
		},
		{
			label:    "Case 2: non-rotational device",
			input:    "0",
			expected: v1alpha1.NonRotational,
		},
		{
			label:    "Case 3: invalid rotational property",
			input:    "2",
			expected: "",
		},
	}

	for _, tc := range testcases {
		actual := parseDeviceProperty(tc.input)
		assert.Equalf(t, tc.expected, actual, "[%s]: failed to parse device mechanical property", tc.label)
	}
}

func getFakeDeviceDiscovery() *DeviceDiscovery {
	dd := &DeviceDiscovery{}
	dd.apiClient = &diskmaker.MockAPIUpdater{}
	dd.eventSync = diskmaker.NewEventReporter(dd.apiClient)
	dd.disks = []v1alpha1.DiscoveredDevice{}
	dd.localVolumeDiscovery = &v1alpha1.LocalVolumeDiscovery{}

	return dd
}

func setEnv() {
	os.Setenv("MY_NODE_NAME", "node1")
	os.Setenv("WATCH_NAMESPACE", "ns")
	os.Setenv("DISCOVERY_OBJECT_UID", "uid")
	os.Setenv("DISCOVERY_OBJECT_NAME", "auto-discover-devices")
}

func unsetEnv() {
	os.Unsetenv("MY_NODE_NAME")
	os.Unsetenv("WATCH_NAMESPACE")
	os.Unsetenv("DISCOVERY_OBJECT_UID")
	os.Unsetenv("DISCOVERY_OBJECT_NAME")
}
