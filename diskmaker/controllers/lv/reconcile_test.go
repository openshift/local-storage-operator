package lv

import (
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openshift/local-storage-operator/test-framework"

	"github.com/openshift/local-storage-operator/internal"

	localv1 "github.com/openshift/local-storage-operator/api/v1"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/mount"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	provCache "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/cache"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
	provDeleter "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/deleter"
	provUtil "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/util"
)

func TestFindMatchingDisk(t *testing.T) {
	tests := []struct {
		name                  string
		availableBlockDevices []internal.BlockDevice
		fakeGlobFunc          func(string) ([]string, error)
		fakeEvalSymlink       func(string) (string, error)
		userSpecifiedDisk     []string
		matchingDevices       []DiskLocation
	}{
		{
			name: "when devices match by their device names",
			fakeGlobFunc: func(string) ([]string, error) {
				return []string{
					"/dev/disk/by-id/abcde",
					"/dev/disk/by-id/wwn-abcde",
					"/dev/disk/by-id/wwn-xyz",
				}, nil
			},
			fakeEvalSymlink: func(path string) (string, error) {
				switch path {
				case "/dev/disk/by-id/wwn-abcde":
					return "/dev/sdc1", nil
				case "/dev/disk/by-id/wwn-xyz":
					return "/dev/sdc2", nil
				default:
					return "", nil
				}
			},
			availableBlockDevices: []internal.BlockDevice{
				{
					Name:  "sdc1",
					KName: "sdc1",
				},
				{
					Name:  "sdc2",
					KName: "sdc2",
				},
			},
			userSpecifiedDisk: []string{"/dev/sdc1", "/dev/sdc2"},
			matchingDevices: []DiskLocation{
				{
					diskNamePath:     "/dev/sdc1",
					userProvidedPath: "/dev/sdc1",
					diskID:           "/dev/disk/by-id/wwn-abcde",
				},
				{
					diskNamePath:     "/dev/sdc2",
					userProvidedPath: "/dev/sdc2",
					diskID:           "/dev/disk/by-id/wwn-xyz",
				},
			},
		},
	}

	for i := range tests {
		test := tests[i]
		t.Run(test.name, func(t *testing.T) {
			d, _ := getFakeDiskMaker(t, "/mnt/local-storage")
			var diskConfig = &DiskConfig{
				Disks: map[string]*Disks{
					"foo": {
						DevicePaths: test.userSpecifiedDisk,
					},
				},
			}
			d.fsInterface = FakeFileSystemInterface{}
			internal.FilePathGlob = test.fakeGlobFunc
			internal.FilePathEvalSymLinks = test.fakeEvalSymlink
			defer func() {
				internal.FilePathGlob = filepath.Glob
				internal.FilePathEvalSymLinks = filepath.EvalSymlinks
			}()

			deviceMap, err := d.findMatchingDisks(diskConfig, test.availableBlockDevices)
			if err != nil {
				t.Fatalf("error finding matchin device %v", err)
			}
			if len(test.matchingDevices) > 0 {
				foundDevices, ok := deviceMap["foo"]
				if !ok {
					t.Fatalf("expected devices for storageclass foo, found none")
				}

				for _, expectedDiskLocation := range test.matchingDevices {
					matchFound := false
					for _, foundLocation := range foundDevices {
						if foundLocation.diskNamePath == expectedDiskLocation.diskNamePath &&
							foundLocation.diskID == expectedDiskLocation.diskID &&
							foundLocation.userProvidedPath == expectedDiskLocation.userProvidedPath {
							matchFound = true
						}
					}
					if !matchFound {
						t.Errorf("expected device %v found none", expectedDiskLocation)
					}
				}
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	tempDir := createTmpDir(t, "", "diskmaker")
	defer os.RemoveAll(tempDir)
	diskConfig := &DiskConfig{
		Disks: map[string]*Disks{
			"foo": {
				DevicePaths: []string{"xyz"},
			},
		},
		OwnerName:       "foobar",
		OwnerNamespace:  "default",
		OwnerKind:       localv1.LocalVolumeKind,
		OwnerUID:        "foobar",
		OwnerAPIVersion: localv1.GroupVersion.String(),
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

	d, _ := getFakeDiskMaker(t, "/mnt/local-storage", lv)
	d.localVolume = lv
	diskConfigFromDisk := d.generateConfig()

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
	sc := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "foobar",
		},
	}
	d, _ := getFakeDiskMaker(t, tmpSymLinkTargetDir, lv, sc)
	d.fsInterface = FakeFileSystemInterface{}
	diskLocation := DiskLocation{fakeDisk.Name(), fakeDiskByID.Name(), fakeDisk.Name(), internal.BlockDevice{}}

	d.runtimeConfig = &provCommon.RuntimeConfig{
		UserConfig: &provCommon.UserConfig{
			DiscoveryMap: map[string]provCommon.MountConfig{
				sc.ObjectMeta.Name: {
					FsType: string(corev1.PersistentVolumeBlock),
				},
			},
			Node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "nodename-a",
					Labels: map[string]string{corev1.LabelHostname: "node-hostname-a"},
				},
			},
		},
	}
	d.createSymlink(diskLocation, fakeDiskByID.Name(), path.Join(tmpSymLinkTargetDir, "diskID"), true)

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
	sc := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "foobar",
		},
	}

	d, _ := getFakeDiskMaker(t, tmpSymLinkTargetDir, lv, sc)
	d.fsInterface = FakeFileSystemInterface{}
	diskLocation := DiskLocation{fakeDisk.Name(), "", fakeDisk.Name(), internal.BlockDevice{}}
	d.createSymlink(diskLocation, fakeDisk.Name(), path.Join(tmpSymLinkTargetDir, "diskName"), false)

	// assert that target symlink is created for disk name when no disk ID is available
	assert.Truef(t, hasFile(t, tmpSymLinkTargetDir, "diskName"), "failed to find symlink with disk name in %s directory", tmpSymLinkTargetDir)
}

func getFakeDiskMaker(t *testing.T, symlinkLocation string, objs ...runtime.Object) (*LocalVolumeReconciler, *testContext) {
	scheme, err := localv1.SchemeBuilder.Build()
	assert.NoErrorf(t, err, "creating scheme")
	err = corev1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding corev1 to scheme")

	err = storagev1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding storagev1 to scheme")
	err = appsv1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding appsv1 to scheme")
	fakeClient := fake.NewFakeClientWithScheme(scheme, objs...)

	fakeRecorder := record.NewFakeRecorder(10)

	mounter := &mount.FakeMounter{
		MountPoints: []mount.MountPoint{},
	}

	fakeVolUtil := provUtil.NewFakeVolumeUtil(false /*deleteShouldFail*/, map[string][]*provUtil.FakeDirEntry{})

	runtimeConfig := &provCommon.RuntimeConfig{
		UserConfig: &provCommon.UserConfig{
			Node:         &corev1.Node{},
			DiscoveryMap: make(map[string]provCommon.MountConfig),
		},
		Cache:    provCache.NewVolumeCache(),
		VolUtil:  fakeVolUtil,
		APIUtil:  test.ApiUtil{Client: fakeClient},
		Recorder: fakeRecorder,
		Mounter:  mounter,
	}
	tc := &testContext{
		fakeClient:    fakeClient,
		fakeRecorder:  fakeRecorder,
		fakeMounter:   mounter,
		runtimeConfig: runtimeConfig,
		fakeVolUtil:   fakeVolUtil,
	}

	lvReconciler := NewLocalVolumeReconciler(
		fakeClient,
		scheme,
		symlinkLocation,
		&provDeleter.CleanupStatusTracker{ProcTable: provDeleter.NewProcTable()},
		runtimeConfig,
	)

	return lvReconciler, tc

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

type testContext struct {
	fakeClient    client.Client
	fakeRecorder  *record.FakeRecorder
	eventStream   chan string
	fakeMounter   *mount.FakeMounter
	fakeVolUtil   *provUtil.FakeVolumeUtil
	fakeDirFiles  map[string][]*provUtil.FakeDirEntry
	runtimeConfig *provCommon.RuntimeConfig
}
