package lv

import (
	"context"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openshift/local-storage-operator/pkg/internal"

	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/util/mount"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	provCache "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/cache"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
	provDeleter "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/deleter"
	provUtil "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/util"
)

func TestFindMatchingDisk(t *testing.T) {
	d, _ := getFakeDiskMaker(t, "/mnt/local-storage")
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
	diskLocation := DiskLocation{fakeDisk.Name(), fakeDiskByID.Name(), internal.BlockDevice{}}

	d.runtimeConfig = &provCommon.RuntimeConfig{
		UserConfig: &provCommon.UserConfig{
			DiscoveryMap: map[string]provCommon.MountConfig{
				sc.ObjectMeta.Name: provCommon.MountConfig{
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
	d.createSymlink(diskLocation, fakeDiskByID.Name(), path.Join(tmpSymLinkTargetDir, "diskID"), log, true)

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
	diskLocation := DiskLocation{fakeDisk.Name(), "", internal.BlockDevice{}}
	d.createSymlink(diskLocation, fakeDisk.Name(), path.Join(tmpSymLinkTargetDir, "diskName"), log, false)

	// assert that target symlink is created for disk name when no disk ID is available
	assert.Truef(t, hasFile(t, tmpSymLinkTargetDir, "diskName"), "failed to find symlink with disk name in %s directory", tmpSymLinkTargetDir)
}

func getFakeDiskMaker(t *testing.T, symlinkLocation string, objs ...runtime.Object) (*ReconcileLocalVolume, *testContext) {
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
	fakeEventSync := newEventReporter(fakeRecorder)
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
		APIUtil:  apiUtil{client: fakeClient},
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
	cleanupTracker := &provDeleter.CleanupStatusTracker{ProcTable: provDeleter.NewProcTable()}
	return &ReconcileLocalVolume{
		symlinkLocation: symlinkLocation,
		client:          fakeClient,
		scheme:          scheme,
		eventSync:       fakeEventSync,
		cleanupTracker:  cleanupTracker,
		runtimeConfig:   runtimeConfig,
		deleter:         provDeleter.NewDeleter(runtimeConfig, cleanupTracker),
	}, tc

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
type apiUtil struct {
	client client.Client
}

// Create PersistentVolume object
func (a apiUtil) CreatePV(pv *v1.PersistentVolume) (*v1.PersistentVolume, error) {
	return pv, a.client.Create(context.TODO(), pv)
}

// Delete PersistentVolume object
func (a apiUtil) DeletePV(pvName string) error {
	pv := &corev1.PersistentVolume{}
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: pvName}, pv)
	if kerrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	err = a.client.Delete(context.TODO(), pv)
	return err
}

// CreateJob Creates a Job execution.
func (a apiUtil) CreateJob(job *batchv1.Job) error {
	return a.client.Create(context.TODO(), job)
}

// DeleteJob deletes specified Job by its name and namespace.
func (a apiUtil) DeleteJob(jobName string, namespace string) error {
	job := &batchv1.Job{}
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: jobName}, job)
	if kerrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	err = a.client.Delete(context.TODO(), job)
	return err
}
