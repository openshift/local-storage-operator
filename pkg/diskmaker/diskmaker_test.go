package diskmaker

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
)

func TestFindMatchingDisk(t *testing.T) {
	d := getFakeDiskMaker("/tmp/foo", "/mnt/local-storage")
	deviceSet := d.findNewDisks(getRawOutput())
	if len(deviceSet) != 5 {
		t.Errorf("expected 7 devices got %d", len(deviceSet))
	}
	diskConfig := &DiskConfig{
		Disks: map[string]*Disks{
			"foo": &Disks{
				DevicePaths: []string{"xyz"},
			},
		},
	}
	allDiskIds := getDeiveIDs()
	deviceMap, err := d.findMatchingDisks(diskConfig, deviceSet, allDiskIds)
	if err != nil {
		t.Fatalf("error finding matchin device %v", err)
	}
	if len(deviceMap) != 0 {
		t.Errorf("expected 0 elements in map got %d", len(deviceMap))
	}
}

func TestLoadConfig(t *testing.T) {
	tempDir := createTmpDir(t, "", "diskmaker")
	defer os.RemoveAll(tempDir)
	diskConfig := &DiskConfig{
		Disks: map[string]*Disks{
			"foo": &Disks{
				DevicePaths: []string{"xyz"},
			},
		},
		OwnerName:       "foobar",
		OwnerNamespace:  "default",
		OwnerKind:       localv1.LocalVolumeKind,
		OwnerUID:        "foobar",
		OwnerAPIVersion: localv1.SchemeGroupVersion.String(),
	}
	yaml, err := diskConfig.ToYAML()
	if err != nil {
		t.Fatalf("error marshalling yaml : %v", err)
	}
	filename := filepath.Join(tempDir, "config")
	err = ioutil.WriteFile(filename, []byte(yaml), 0755)
	if err != nil {
		t.Fatalf("error writing yaml to disk : %v", err)
	}

	d := getFakeDiskMaker(filename, "/mnt/local-storage")
	diskConfigFromDisk, err := d.loadConfig()
	if err != nil {
		t.Fatalf("error loading diskconfig from disk : %v", err)
	}
	if diskConfigFromDisk == nil {
		t.Fatalf("expected a diskconfig got nil")
	}
	if d.localVolume == nil {
		t.Fatalf("expected localvolume got nil")
	}

	if d.localVolume.Name != diskConfig.OwnerName {
		t.Fatalf("expected owner name to be %s got %s", diskConfig.OwnerName, d.localVolume.Name)
	}
}

func TestCreateSymLinkByDeviceID(t *testing.T) {
	tmpSymLinkTargetDir := createTmpDir(t, "", "target")
	fakeDisk := createTmpFile(t, "", "diskName")
	fakeDiskByID := createTmpFile(t, "", "diskID")
	defer os.RemoveAll(tmpSymLinkTargetDir)
	defer os.Remove(fakeDisk.Name())
	defer os.Remove(fakeDiskByID.Name())

	d := getFakeDiskMaker("", tmpSymLinkTargetDir)
	diskLocation := DiskLocation{fakeDisk.Name(), fakeDiskByID.Name()}

	d.createSymLink(diskLocation, tmpSymLinkTargetDir)

	// assert that target symlink is created for disk ID when both disk name and disk by-id are available
	assert.Truef(t, hasFile(t, tmpSymLinkTargetDir, "diskID"), "failed to find symlink with disk ID in %s directory", tmpSymLinkTargetDir)
}

func TestCreateSymLinkByDeviceName(t *testing.T) {
	tmpSymLinkTargetDir := createTmpDir(t, "", "target")
	fakeDisk := createTmpFile(t, "", "diskName")
	defer os.Remove(fakeDisk.Name())
	defer os.RemoveAll(tmpSymLinkTargetDir)

	d := getFakeDiskMaker("", tmpSymLinkTargetDir)
	diskLocation := DiskLocation{fakeDisk.Name(), ""}
	d.createSymLink(diskLocation, tmpSymLinkTargetDir)

	// assert that target symlink is created for disk name when no disk ID is available
	assert.Truef(t, hasFile(t, tmpSymLinkTargetDir, "diskName"), "failed to find symlink with disk name in %s directory", tmpSymLinkTargetDir)
}

func getFakeDiskMaker(configLocation, symlinkLocation string) *DiskMaker {
	d := &DiskMaker{configLocation: configLocation, symlinkLocation: symlinkLocation}
	d.apiClient = &MockAPIUpdater{}
	return d
}

func getDeiveIDs() []string {
	return []string{
		"/dev/disk/by-id/xyz",
	}
}

func createTmpDir(t *testing.T, dir, prefix string) string {
	tmpDir, err := ioutil.TempDir(dir, prefix)
	if err != nil {
		t.Fatalf("error creating temp directory : %v", err)
	}
	return tmpDir
}

func createTmpFile(t *testing.T, dir, pattern string) *os.File {
	tmpFile, err := ioutil.TempFile(dir, pattern)
	if err != nil {
		t.Fatalf("error creating tmp file: %v", err)
	}
	return tmpFile
}

func hasFile(t *testing.T, dir, file string) bool {
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		t.Fatalf("error reading directory %s : %v", dir, err)
	}
	for _, f := range files {
		if strings.Contains(f.Name(), file) {
			return true
		}
	}
	return false
}
