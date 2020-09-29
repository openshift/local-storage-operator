package internal

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

var lsblkOut string
var blkidOut string

const (
	lsblkOutput1 = `NAME="sda" KNAME="sda" ROTA="1" TYPE="disk" SIZE="62914560000" MODEL="VBOX HARDDISK" VENDOR="ATA" RO="0" RM="0" STATE="running" SERIAL="" PARTLABEL=""
NAME="sda1" KNAME="sda1" ROTA="1" TYPE="part" SIZE="62913494528" MODEL="" VENDOR="" RO="0" RM="0" STATE="" SERIAL="" PARTLABEL="BIOS-BOOT"
`
	lsblkOutput2 = `NAME="sdc" KNAME="sdc" ROTA="1" TYPE="disk" SIZE="62914560000" MODEL="VBOX HARDDISK" VENDOR="ATA" RO="0" RM="1" STATE="running" SERIAL=""
NAME="sdc3" KNAME="sdc3" ROTA="1" TYPE="part" SIZE="62913494528" MODEL="" VENDOR="" RO="0" RM="1" STATE="" SERIAL=""
`
	blkIDOutput1 = `/dev/sdc: TYPE="ext4"
/dev/sdc3: TYPE="ext2"
`
)

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

func TestListBlockDevices(t *testing.T) {
	testcases := []struct {
		label             string
		lsblkOutput       string
		blkIDOutput       string
		totalBlockDevices int
		totalBadRows      int
		expected          []BlockDevice
	}{
		{
			label:             "Case 1: block devices with no filesystems",
			lsblkOutput:       lsblkOutput1,
			blkIDOutput:       "",
			totalBlockDevices: 2,
			totalBadRows:      0,
			expected: []BlockDevice{
				{
					Name:       "sda",
					FSType:     "",
					Type:       "disk",
					Size:       "62914560000",
					Model:      "VBOX HARDDISK",
					Vendor:     "ATA",
					Serial:     "",
					Rotational: "1",
					ReadOnly:   "0",
					Removable:  "0",
					State:      "running",
					PartLabel:  "",
				},
				{

					Name:       "sda1",
					FSType:     "",
					Type:       "part",
					Size:       "62913494528",
					Model:      "",
					Vendor:     "",
					Serial:     "",
					Rotational: "1",
					ReadOnly:   "0",
					Removable:  "0",
					State:      "running",
					PartLabel:  "BIOS-BOOT",
				},
			},
		},
		{
			label:             "Case 2: block devices with filesystems",
			lsblkOutput:       lsblkOutput2,
			blkIDOutput:       blkIDOutput1,
			totalBlockDevices: 2,
			totalBadRows:      0,
			expected: []BlockDevice{
				{
					Name:       "sdc",
					FSType:     "ext4",
					Type:       "disk",
					Size:       "62914560000",
					Model:      "VBOX HARDDISK",
					Vendor:     "ATA",
					Serial:     "",
					Rotational: "1",
					ReadOnly:   "0",
					Removable:  "1",
					State:      "running",
					PartLabel:  "",
				},
				{

					Name:       "sdc3",
					FSType:     "ext2",
					Type:       "part",
					Size:       "62913494528",
					Model:      "",
					Vendor:     "",
					Serial:     "",
					Rotational: "1",
					ReadOnly:   "0",
					Removable:  "1",
					State:      "running",
					PartLabel:  "",
				},
			},
		},
		{
			label:             "Case 3: empty lsblk output",
			lsblkOutput:       "",
			totalBlockDevices: 0,
			totalBadRows:      0,
			expected:          []BlockDevice{},
		},
	}

	for _, tc := range testcases {
		lsblkOut = tc.lsblkOutput
		blkidOut = tc.blkIDOutput
		ExecCommand = helperCommand
		defer func() { ExecCommand = exec.Command }()
		blockDevices, badRows, err := ListBlockDevices()
		assert.NoError(t, err)
		assert.Equalf(t, tc.totalBadRows, len(badRows), "[%s] total bad rows list didn't match", tc.label)
		assert.Equalf(t, tc.totalBlockDevices, len(blockDevices), "[%s] total block device list didn't match", tc.label)
		for i := 0; i < len(blockDevices); i++ {
			assert.Equalf(t, tc.expected[i].Name, blockDevices[i].Name, "[%q: Device: %d]: invalid block device name", tc.label, i+1)
			assert.Equalf(t, tc.expected[i].Type, blockDevices[i].Type, "[%q: Device: %d]: invalid block device type", tc.label, i+1)
			assert.Equalf(t, tc.expected[i].FSType, blockDevices[i].FSType, "[%q: Device: %d]: invalid block device file system", tc.label, i+1)
			assert.Equalf(t, tc.expected[i].Size, blockDevices[i].Size, "[%q: Device: %d]: invalid block device size", tc.label, i+1)
			assert.Equalf(t, tc.expected[i].Vendor, blockDevices[i].Vendor, "[%q: Device: %d]: invalid block device vendor", tc.label, i+1)
			assert.Equalf(t, tc.expected[i].Model, blockDevices[i].Model, "[%q: Device: %d]: invalid block device Model", tc.label, i+1)
			assert.Equalf(t, tc.expected[i].Serial, blockDevices[i].Serial, "[%q: Device: %d]: invalid block device serial", tc.label, i+1)
			assert.Equalf(t, tc.expected[i].Rotational, blockDevices[i].Rotational, "[%q: Device: %d]: invalid block device rotational property", tc.label, i+1)
			assert.Equalf(t, tc.expected[i].ReadOnly, blockDevices[i].ReadOnly, "[%q: Device: %d]: invalid block device read only value", tc.label, i+1)
			assert.Equalf(t, tc.expected[i].PartLabel, blockDevices[i].PartLabel, "[%q: Device: %d]: invalid block device PartLabel value", tc.label, i+1)
		}
	}

}

func TestGetDeviceFSMap(t *testing.T) {
	testcases := []struct {
		label       string
		blkIDOutput string
		expected    map[string]string
	}{
		{
			label:       "Case 1: Empty blkid response",
			blkIDOutput: "",
			expected:    map[string]string{},
		},
		{
			label:       "Case 2: Valid blkid response",
			blkIDOutput: blkIDOutput1,
			expected: map[string]string{
				"/dev/sdc":  "ext4",
				"/dev/sdc3": "ext2",
			},
		},
	}

	for _, tc := range testcases {
		blkidOut = tc.blkIDOutput
		ExecCommand = helperCommand
		defer func() { ExecCommand = exec.Command }()

		actual, err := GetDeviceFSMap()
		assert.NoError(t, err)
		assert.Equalf(t, tc.expected, actual, "[%s]: failed to get device filesystem map", tc.label)
	}
}

func TestHasChildren(t *testing.T) {
	testcases := []struct {
		label        string
		blockDevice  BlockDevice
		fakeGlobfunc func(string) ([]string, error)
		expected     bool
	}{
		{
			label:       "Case 1: device with partitions",
			blockDevice: BlockDevice{Name: "sdb", KName: "sdb"},
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"removable", "subsytem", "sdb1"}, nil
			},
			expected: true,
		},
		{
			label:       "Case 2: device with partitions",
			blockDevice: BlockDevice{Name: "sdb", KName: "sdb"},
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"removable", "subsytem", "sdb2"}, nil
			},
			expected: true,
		},
		{
			label:       "Case 3: device with no partitions",
			blockDevice: BlockDevice{Name: "sdb", KName: "sdb"},
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"removable", "subsytem"}, nil
			},
			expected: false,
		},
	}

	for _, tc := range testcases {
		FilePathGlob = tc.fakeGlobfunc
		defer func() { FilePathGlob = filepath.Glob }()
		actual, err := tc.blockDevice.HasChildren()
		assert.NoError(t, err)
		assert.Equalf(t, tc.expected, actual, "[%s]: failed to check if devie %q has child partitions", tc.label, tc.blockDevice.Name)
	}
}

func TestHasBindMounts(t *testing.T) {
	tempDir, err := ioutil.TempDir("", "discovery")
	if err != nil {
		t.Fatalf("error creating temp directory : %v", err)
	}
	defer os.RemoveAll(tempDir)

	testcases := []struct {
		label              string
		blockDevice        BlockDevice
		mountInfo          string
		expected           bool
		expectedMountPoint string
	}{
		{
			label:              "Case 1: device with bind mounts",
			blockDevice:        BlockDevice{Name: "sdc"},
			mountInfo:          "5595 121 0:6 /sdc /var/lib/kubelet/plugins/kubernetes.io~local-volume/volumeDevices/local-pv-343bdd9/6d9d33ae-408e-4bac-81f7-c0bc347a9667 rw shared:23 - devtmpfs devtmpfs rw,seclabel,size=32180404k,nr_inodes=8045101,mode=755",
			expected:           true,
			expectedMountPoint: "/var/lib/kubelet/plugins/kubernetes.io~local-volume/volumeDevices/local-pv-343bdd9/6d9d33ae-408e-4bac-81f7-c0bc347a9667",
		},
		{
			label:              "Case 2: device with regular mounts",
			blockDevice:        BlockDevice{Name: "sdc"},
			mountInfo:          "121 98 259:1 / /boot rw,relatime shared:65 - ext4 /dev/sdc rw,seclabel",
			expected:           true,
			expectedMountPoint: "/boot",
		},
		{
			label:              "Case 3: device with no mount points",
			blockDevice:        BlockDevice{Name: "sdd"},
			mountInfo:          "5595 121 0:6 /sdc /var/lib/kubelet/plugins/kubernetes.io~local-volume/volumeDevices/local-pv-343bdd9/6d9d33ae-408e-4bac-81f7-c0bc347a9667 rw shared:23 - devtmpfs devtmpfs rw,seclabel,size=32180404k,nr_inodes=8045101,mode=755",
			expected:           false,
			expectedMountPoint: "",
		},
		{
			label:              "Case 4: device with no mount point",
			blockDevice:        BlockDevice{Name: "sdc"},
			mountInfo:          "",
			expected:           false,
			expectedMountPoint: "",
		},
	}

	for _, tc := range testcases {
		filename := filepath.Join(tempDir, "mountfile")
		err = ioutil.WriteFile(filename, []byte(tc.mountInfo), 0755)
		if err != nil {
			t.Fatalf("error writing mount info to file : %v", err)
		}
		mountFile = filename
		actual, mountPoint, err := tc.blockDevice.HasBindMounts()
		assert.NoError(t, err)
		assert.Equalf(t, tc.expected, actual, "[%s]: failed to check bind mounts", tc.label)
		assert.Equalf(t, tc.expectedMountPoint, mountPoint, "[%s]: failed to get correct mount point", tc.label)
	}
}

func TestHasChildrenFail(t *testing.T) {
	testcases := []struct {
		label        string
		blockDevice  BlockDevice
		fakeGlobfunc func(string) ([]string, error)
		expected     bool
	}{
		{
			label:       "Case 1: filepath.Glob command failure",
			blockDevice: BlockDevice{Name: "sdb"},
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{}, fmt.Errorf("failed to list matching files")
			},
			expected: false,
		},
	}

	for _, tc := range testcases {
		FilePathGlob = tc.fakeGlobfunc
		defer func() { FilePathGlob = filepath.Glob }()
		actual, err := tc.blockDevice.HasChildren()
		assert.Error(t, err)
		assert.Equalf(t, tc.expected, actual, "[%s]: failed to check if devie %q has child partitions", tc.label, tc.blockDevice.Name)
	}
}

func TestGetPathByID(t *testing.T) {
	testcases := []struct {
		label               string
		blockDevice         BlockDevice
		fakeGlobfunc        func(string) ([]string, error)
		fakeEvalSymlinkfunc func(string) (string, error)
		expected            string
	}{
		{
			label:       "Case 1: pathByID is already available",
			blockDevice: BlockDevice{Name: "sdb", KName: "sdb", PathByID: "/dev/disk/by-id/sdb"},
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"/dev/disk/by-id/dm-home", "/dev/disk/by-id/dm-uuid-LVM-6p00g8KptCD", "/dev/disk/by-id/sdb"}, nil
			},
			fakeEvalSymlinkfunc: func(path string) (string, error) {
				return "/dev/disk/by-id/sdb", nil
			},
			expected: "/dev/disk/by-id/sdb",
		},

		{
			label:       "Case 2: pathByID is not available",
			blockDevice: BlockDevice{Name: "sdb", KName: "sdb", PathByID: ""},
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"/dev/disk/by-id/sdb"}, nil
			},
			fakeEvalSymlinkfunc: func(path string) (string, error) {
				return "/dev/disk/by-id/sdb", nil
			},
			expected: "/dev/disk/by-id/sdb",
		},
	}

	for _, tc := range testcases {
		FilePathGlob = tc.fakeGlobfunc
		FilePathEvalSymLinks = tc.fakeEvalSymlinkfunc
		defer func() {
			FilePathGlob = filepath.Glob
			FilePathEvalSymLinks = filepath.EvalSymlinks
		}()

		actual, err := tc.blockDevice.GetPathByID()
		assert.NoError(t, err)
		assert.Equalf(t, tc.expected, actual, "[%s] failed to get device path by ID", tc.label)

	}
}

func TestGetPathByIDFail(t *testing.T) {
	testcases := []struct {
		label               string
		blockDevice         BlockDevice
		fakeGlobfunc        func(string) ([]string, error)
		fakeEvalSymlinkfunc func(string) (string, error)
		expected            string
	}{
		{
			label:       "Case 1: filepath.Glob command failure",
			blockDevice: BlockDevice{Name: "sdb"},
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{}, fmt.Errorf("failed to list matching files")
			},
			fakeEvalSymlinkfunc: func(path string) (string, error) {
				return "/dev/disk/by-id/sdb", nil
			},
			expected: "",
		},

		{
			label:       "Case 2: filepath.EvalSymlinks command failure",
			blockDevice: BlockDevice{Name: "sdb", PathByID: ""},
			fakeGlobfunc: func(name string) ([]string, error) {
				return []string{"/dev/disk/by-id/sdb"}, nil
			},
			fakeEvalSymlinkfunc: func(path string) (string, error) {
				return "", fmt.Errorf("failed to evaluate symlink")
			},
			expected: "",
		},
	}

	for _, tc := range testcases {
		FilePathGlob = tc.fakeGlobfunc
		FilePathEvalSymLinks = tc.fakeEvalSymlinkfunc
		defer func() {
			FilePathGlob = filepath.Glob
			FilePathEvalSymLinks = filepath.EvalSymlinks
		}()

		actual, err := tc.blockDevice.GetPathByID()
		assert.Error(t, err)
		assert.Equalf(t, tc.expected, actual, "[%s] failed to get device path by ID", tc.label)

	}
}

func TestParseBitBool(t *testing.T) {
	testcases := []struct {
		label    string
		input    string
		expected bool
	}{
		{
			label:    "Case 1: prasing 0",
			input:    "0",
			expected: false,
		},

		{
			label:    "Case 2: parsing empty value",
			input:    "",
			expected: false,
		},

		{
			label:    "Case 1: parsing 1",
			input:    "1",
			expected: true,
		},
	}

	for _, tc := range testcases {
		actual, err := parseBitBool(tc.input)
		assert.Equalf(t, tc.expected, actual, "[%s]: invalid response", tc.label)
		assert.NoError(t, err)
	}
}

func TestParseBitBoolFail(t *testing.T) {
	testcases := []struct {
		label    string
		input    string
		expected bool
	}{
		{
			label:    "Case 1: parsing invalid input",
			input:    "invalid input",
			expected: false,
		},
	}

	for _, tc := range testcases {
		actual, err := parseBitBool(tc.input)
		assert.Equal(t, actual, tc.expected, "[%s]: invalid response", tc.label)
		assert.Error(t, err)
	}
}

func TestReadOnly(t *testing.T) {
	testcases := []struct {
		label       string
		blockDevice BlockDevice
		expected    bool
	}{
		{
			label:       "Case 1: not a readonly device",
			blockDevice: BlockDevice{ReadOnly: "0"},
			expected:    false,
		},
		{
			label:       "Case 2: readonly device",
			blockDevice: BlockDevice{ReadOnly: "1"},
			expected:    true,
		},
	}

	for _, tc := range testcases {
		actual, err := tc.blockDevice.GetReadOnly()
		assert.Equal(t, tc.expected, actual, "[%s]: invalid response", tc.label)
		assert.NoError(t, err)
	}

}

func TestReadOnlyFail(t *testing.T) {
	testcases := []struct {
		label       string
		blockDevice BlockDevice
		expected    bool
	}{
		{
			label:       "Case 1: invalid input",
			blockDevice: BlockDevice{ReadOnly: "invalid input"},
			expected:    false,
		},
	}

	for _, tc := range testcases {
		actual, err := tc.blockDevice.GetReadOnly()
		assert.Equal(t, tc.expected, actual, "[%s]: invalid response", tc.label)
		assert.Error(t, err)
	}

}

func TestGetRemovable(t *testing.T) {
	testcases := []struct {
		label       string
		blockDevice BlockDevice
		expected    bool
	}{
		{
			label:       "Case 1: non-removable device",
			blockDevice: BlockDevice{Removable: "0"},
			expected:    false,
		},
		{
			label:       "Case 2: removable device",
			blockDevice: BlockDevice{Removable: "1"},
			expected:    true,
		},
	}

	for _, tc := range testcases {
		actual, err := tc.blockDevice.GetRemovable()
		assert.Equal(t, tc.expected, actual, "[%s]: invalid response", tc.label)
		assert.NoError(t, err)
	}
}

func TestGetRemovableFail(t *testing.T) {
	testcases := []struct {
		label       string
		blockDevice BlockDevice
		expected    bool
	}{
		{
			label:       "Case 1: invalid input",
			blockDevice: BlockDevice{Removable: "invalid input"},
			expected:    false,
		},
	}

	for _, tc := range testcases {
		actual, err := tc.blockDevice.GetRemovable()
		assert.Equal(t, tc.expected, actual, "[%s]: invalid response", tc.label)
		assert.Error(t, err)
	}

}

func TestGetRotational(t *testing.T) {
	testcases := []struct {
		label       string
		blockDevice BlockDevice
		expected    bool
	}{
		{
			label:       "Case 1: non-rotational device",
			blockDevice: BlockDevice{Rotational: "0"},
			expected:    false,
		},
		{
			label:       "Case 2: rotationl device",
			blockDevice: BlockDevice{Rotational: "1"},
			expected:    true,
		},
	}

	for _, tc := range testcases {
		actual, err := tc.blockDevice.GetRotational()
		assert.Equal(t, tc.expected, actual, "[%s]: invalid response", tc.label)
		assert.NoError(t, err)
	}
}

func TestGetRotationalFail(t *testing.T) {
	testcases := []struct {
		label       string
		blockDevice BlockDevice
		expected    bool
	}{
		{
			label:       "Case 1: invalid input",
			blockDevice: BlockDevice{Rotational: "invalid input"},
			expected:    false,
		},
	}

	for _, tc := range testcases {
		actual, err := tc.blockDevice.GetRotational()
		assert.Equal(t, tc.expected, actual, "[%s]: invalid response", tc.label)
		assert.Error(t, err)
	}

}
