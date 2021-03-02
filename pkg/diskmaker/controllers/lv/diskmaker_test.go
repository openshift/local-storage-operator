package lv

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestFindMatchingDisk(t *testing.T) {
	d := getFakeDiskMaker(t, nil, "/tmp/foo", "/mnt/local-storage")
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
	lv := &localv1.LocalVolume{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "local.storage.openshift.io",
			Kind:       "LocalVolume",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foobar",
			Namespace: "default",
		},
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

	d := getFakeDiskMaker(t, lv, filename, "/mnt/local-storage")
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

	lv := &localv1.LocalVolume{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "local.storage.openshift.io",
			Kind:       "LocalVolume",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foobar",
			Namespace: "default",
		},
	}
	d := getFakeDiskMaker(t, lv, "", tmpSymLinkTargetDir)
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

	lv := &localv1.LocalVolume{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "local.storage.openshift.io",
			Kind:       "LocalVolume",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foobar",
			Namespace: "default",
		},
	}

	d := getFakeDiskMaker(t, lv, "", tmpSymLinkTargetDir)
	diskLocation := DiskLocation{fakeDisk.Name(), ""}
	d.createSymLink(diskLocation, tmpSymLinkTargetDir)

	// assert that target symlink is created for disk name when no disk ID is available
	assert.Truef(t, hasFile(t, tmpSymLinkTargetDir, "diskName"), "failed to find symlink with disk name in %s directory", tmpSymLinkTargetDir)
}

func getFakeDiskMaker(t *testing.T, objs runtime.Object, configLocation, symlinkLocation string) *ReconcileLocalVolume {
	r := &ReconcileLocalVolume{configLocation: configLocation, symlinkLocation: symlinkLocation}
	scheme, err := localv1.SchemeBuilder.Build()
	assert.NoErrorf(t, err, "creating scheme")
	err = corev1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding corev1 to scheme")

	err = appsv1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding appsv1 to scheme")
	if objs != nil {
		r.client = fake.NewFakeClientWithScheme(scheme, objs)
	} else {
		r.client = fake.NewFakeClientWithScheme(scheme)
	}

	r.scheme = scheme
	//apis.AddToScheme(r.scheme)
	r.eventSync = newEventReporter(record.NewFakeRecorder(10))
	return r
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
