package diskmaker

import (
	"github.com/openshift/local-storage-operator/pkg/internal"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
)

func TestFindMatchingDisk(t *testing.T) {
	d := getFakeDiskMaker("/tmp/foo", "/mnt/local-storage")
	blockDevices := []internal.BlockDevice{
		{
			Name:  "sdb1",
			KName: "sdb1",
		},
		{
			Name:  "sdb2",
			KName: "sdb2",
		},
	}
	if len(blockDevices) != 2 {
		t.Errorf("expected 2 devices got %d", len(blockDevices))
	}
	diskConfig := &DiskConfig{
		Disks: map[string]*Disks{
			"foo": &Disks{
				DevicePaths: []string{"/dev/sdb1", "/dev/sdb2"},
			},
		},
	}
	allDiskIds := getDeiveIDs()
	deviceMap, err := d.findMatchingDisks(diskConfig, blockDevices, allDiskIds)
	if err != nil {
		t.Fatalf("error finding matchin device %v", err)
	}
	if len(deviceMap) != 1 {
		t.Errorf("expected 1 elements in map got %d", len(deviceMap))
	}
}

func TestLoadConfig(t *testing.T) {
	tempDir, err := ioutil.TempDir("", "diskmaker")
	if err != nil {
		t.Fatalf("error creating temp directory : %v", err)
	}

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

func getFakeDiskMaker(configLocation, symlinkLocation string) *DiskMaker {
	d := &DiskMaker{configLocation: configLocation, symlinkLocation: symlinkLocation}
	d.apiClient = &MockAPIUpdater{}
	d.eventSync = NewEventReporter(d.apiClient)
	return d
}

func getDeiveIDs() []string {
	return []string{
		"/dev/disk/by-id/xyz",
	}
}
