package lv

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	localv1 "github.com/openshift/local-storage-operator/api/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/internal"
	test "github.com/openshift/local-storage-operator/test/framework"
	"github.com/openshift/local-storage-operator/test/framework/util"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/mount"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	provCache "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/cache"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
	provDeleter "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/deleter"
	provUtil "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/util"
)

//go:embed *
var f embed.FS

const (
	emptyFile = 0
	tinyFile  = 1024 * 1024 // bytes

	storageClassName = "local-sc"
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
	err = os.WriteFile(filename, []byte(yaml), 0755)
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
	fakeDisk := createTmpFile(t, "", "diskName", emptyFile)
	fakeDiskByID := createTmpFile(t, "", "diskID", emptyFile)
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
	diskLocation := DiskLocation{fakeDisk.Name(), fakeDiskByID.Name(), fakeDisk.Name(), internal.BlockDevice{}, false}

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

func TestWipeDeviceWhenCreateSymLinkByDeviceName(t *testing.T) {
	tests := []struct {
		name      string
		forceWipe bool
	}{
		{
			name:      "forceWipeDevicesAndDestroyAllData is False",
			forceWipe: false,
		},
		{
			name:      "forceWipeDevicesAndDestroyAllData is True",
			forceWipe: true,
		},
	}

	for i := range tests {
		test := tests[i]
		volHelper := util.NewVolumeHelper()
		t.Run(test.name, func(t *testing.T) {
			tmpSymLinkTargetDir := createTmpDir(t, "", "target")
			fakeDisk := createTmpFile(t, "", "diskName", tinyFile)
			fname := fakeDisk.Name()
			volHelper.FormatAsExt4(t, fname)
			defer os.Remove(fname)
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
			diskLocation := DiskLocation{fname, "", fname, internal.BlockDevice{}, test.forceWipe}
			d.createSymlink(diskLocation, fname, path.Join(tmpSymLinkTargetDir, "diskName"), false)

			// assert that target symlink is created for disk name when no disk ID is available
			assert.Truef(t, hasFile(t, tmpSymLinkTargetDir, "diskName"), "failed to find symlink with disk name in %s directory", tmpSymLinkTargetDir)

			// assert that disk was (or wasn't) wiped
			assert.Truef(t, volHelper.HasExt4(t, fname) != test.forceWipe, "unexpected wiping disk %s", fname)
		})
	}
}

func getFakeDiskMaker(t *testing.T, symlinkLocation string, objs ...runtime.Object) (*LocalVolumeReconciler, *testContext) {
	scheme, err := localv1.SchemeBuilder.Build()
	assert.NoErrorf(t, err, "creating scheme")

	err = localv1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "creating scheme")

	err = localv1alpha1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "creating scheme")

	err = corev1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding corev1 to scheme")

	err = storagev1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding storagev1 to scheme")
	err = appsv1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding appsv1 to scheme")
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()

	fakeRecorder := record.NewFakeRecorder(20)

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
	tmpDir, err := os.MkdirTemp(dir, prefix)
	if err != nil {
		t.Fatalf("error creating temp directory : %v", err)
	}
	return tmpDir
}

func createTmpFile(t *testing.T, dir, pattern string, size int64) *os.File {
	tmpFile, err := os.CreateTemp(dir, pattern)
	if err != nil {
		t.Fatalf("error creating tmp file: %v", err)
	}
	err = tmpFile.Truncate(size)
	if err != nil {
		t.Fatalf("error truncating tmp file: %v", err)
	}
	return tmpFile
}

func hasFile(t *testing.T, dir, file string) bool {
	dentries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("error reading directory %s : %v", dir, err)
	}
	for _, d := range dentries {
		if strings.Contains(d.Name(), file) {
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

type NodeConfigParams struct {
	NodeName string
	NodeUID  types.UID
}

func makeNodeList(params []NodeConfigParams) *corev1.NodeList {
	mockNodeList := corev1.NodeList{
		TypeMeta: metav1.TypeMeta{
			Kind: "NodeList",
		},
		Items: []corev1.Node{},
	}

	for _, p := range params {
		node := corev1.Node{
			TypeMeta: metav1.TypeMeta{
				Kind: "Node",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: p.NodeName,
				UID:  p.NodeUID,
			},
		}
		mockNodeList.Items = append(mockNodeList.Items, node)
	}

	return &mockNodeList
}

type PVConfigParams struct {
	PVName          string
	PVAnnotation    string
	PVReclaimPolicy corev1.PersistentVolumeReclaimPolicy
	PVPhase         corev1.PersistentVolumePhase
}

func makePersistentVolumeList(symLinkDir string, params []PVConfigParams) *corev1.PersistentVolumeList {

	mockPersistentVolumeList := corev1.PersistentVolumeList{
		TypeMeta: metav1.TypeMeta{
			Kind: "PersistentVolumeList",
		},
		Items: nil,
	}

	if params == nil {
		return &mockPersistentVolumeList
	}

	for _, p := range params {
		pv := corev1.PersistentVolume{
			TypeMeta: metav1.TypeMeta{
				Kind: "PersistentVolume",
			},
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					provCommon.AnnProvisionedBy: p.PVAnnotation,
				},
				Name: p.PVName,
			},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeReclaimPolicy: p.PVReclaimPolicy,
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					Local: &corev1.LocalVolumeSource{
						Path: filepath.Join(symLinkDir, storageClassName, p.PVName),
					},
				},
				StorageClassName: storageClassName, //Has to match with ConfigMap (data.storageClassMap)
			},
			Status: corev1.PersistentVolumeStatus{
				Phase: p.PVPhase,
			},
		}
		mockPersistentVolumeList.Items = append(mockPersistentVolumeList.Items, pv)
	}

	return &mockPersistentVolumeList
}

func TestDeleteReconcile(t *testing.T) {
	tests := []struct {
		name           string
		dirEntries     []*provUtil.FakeDirEntry
		nodeList       *corev1.NodeList
		targetNodeName string
		initialPVs     []PVConfigParams
		expectedPVs    []PVConfigParams
	}{
		{
			name:       "Reconcile does not delete a PV (Reclaim policy: Retain)",
			dirEntries: nil,
			nodeList: makeNodeList([]NodeConfigParams{
				{
					NodeName: "Node1", NodeUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				},
			}),
			// Name of a node that deleter Reconcile() will see, normally it would get it from env var - it's the name of the node the code runs on.
			targetNodeName: "Node1",
			initialPVs: []PVConfigParams{
				{
					PVName:          "PV-1",
					PVAnnotation:    "local-volume-provisioner-Node1-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
					PVReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
					PVPhase:         corev1.VolumeReleased,
				},
			},
			expectedPVs: []PVConfigParams{
				{
					PVName:          "PV-1",
					PVAnnotation:    "local-volume-provisioner-Node1-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
					PVReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
					PVPhase:         corev1.VolumeReleased,
				},
			},
		},
		{
			name: "Reconcile deletes only PVs that belong to its node (Reclaim policy: Delete)",
			dirEntries: []*provUtil.FakeDirEntry{
				{
					Name:       "PV-1",
					VolumeType: provUtil.FakeEntryFile,
				},
				{
					Name:       "PV-2",
					VolumeType: provUtil.FakeEntryFile,
				},
			},
			nodeList: makeNodeList([]NodeConfigParams{
				{
					NodeName: "Node1", NodeUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				},
			}),
			targetNodeName: "Node1",
			initialPVs: []PVConfigParams{
				{
					PVName:          "PV-1",
					PVAnnotation:    "local-volume-provisioner-Node1-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
					PVReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					PVPhase:         corev1.VolumeReleased,
				},
				{
					PVName:          "PV-2",
					PVAnnotation:    "local-volume-provisioner-Node2-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
					PVReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					PVPhase:         corev1.VolumeReleased,
				},
			},
			expectedPVs: []PVConfigParams{
				{
					PVName:          "PV-2",
					PVAnnotation:    "local-volume-provisioner-Node2-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
					PVReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					PVPhase:         corev1.VolumeReleased,
				},
			},
		},
		{
			name: "Reconcile deletes a PV with UID (Reclaim policy: Delete)",
			dirEntries: []*provUtil.FakeDirEntry{
				{
					Name:       "PV-1",
					VolumeType: provUtil.FakeEntryFile,
				},
			},
			nodeList: makeNodeList([]NodeConfigParams{
				{
					NodeName: "Node1", NodeUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				},
			}),
			targetNodeName: "Node1",
			initialPVs: []PVConfigParams{
				{
					PVName:          "PV-1",
					PVAnnotation:    "local-volume-provisioner-Node1-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
					PVReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					PVPhase:         corev1.VolumeReleased,
				},
			},
			expectedPVs: nil,
		},
		{
			name: "Reconcile deletes a PV without UID (Reclaim policy: Delete)",
			dirEntries: []*provUtil.FakeDirEntry{
				{
					Name:       "PV-1",
					VolumeType: provUtil.FakeEntryFile,
				},
			},
			nodeList: makeNodeList([]NodeConfigParams{
				{
					NodeName: "Node1", NodeUID: "",
				},
			}),
			targetNodeName: "Node1",
			initialPVs: []PVConfigParams{
				{
					PVName:          "PV-1",
					PVAnnotation:    "local-volume-provisioner-Node1",
					PVReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					PVPhase:         corev1.VolumeReleased,
				},
			},
			expectedPVs: nil,
		},
		{
			name: "Reconcile deletes a PV if node UID does not match PV annotation (Reclaim policy: Delete)",
			dirEntries: []*provUtil.FakeDirEntry{
				{
					Name:       "PV-1",
					VolumeType: provUtil.FakeEntryFile,
				},
			},
			nodeList: makeNodeList([]NodeConfigParams{
				{
					NodeName: "Node1", NodeUID: "5991475c-f876-11ec-b939-0242ac120002",
				},
			}),
			targetNodeName: "Node1",
			initialPVs: []PVConfigParams{
				{
					PVName:          "PV-1",
					PVAnnotation:    "local-volume-provisioner-Node1-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
					PVReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					PVPhase:         corev1.VolumeReleased,
				},
			},
			expectedPVs: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpSymLinkTargetDir := createTmpDir(t, "", "target")

			cmBytes, err := f.ReadFile("testfiles/provisioner_conf.yaml")
			if err != nil {
				t.Fatalf("Failed to load config map: %v", err)
			}
			cmBytes = bytes.ReplaceAll(cmBytes, []byte("/mnt/local-storage"), []byte(tmpSymLinkTargetDir))
			configMap := resourceread.ReadConfigMapV1OrDie(cmBytes)
			cmNamespace := configMap.GetObjectMeta().GetNamespace()
			cmName := configMap.GetObjectMeta().GetName()

			lv := &localv1.LocalVolume{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "local.storage.openshift.io",
					Kind:       "LocalVolume",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      cmName,
					Namespace: cmNamespace,
				},
			}

			initialPVs := makePersistentVolumeList(tmpSymLinkTargetDir, test.initialPVs)
			expectedPVs := makePersistentVolumeList(tmpSymLinkTargetDir, test.expectedPVs)

			objects := []runtime.Object{
				test.nodeList,
				initialPVs,
				lv,
			}

			err = os.Setenv("MY_NODE_NAME", test.targetNodeName)
			if err != nil {
				t.Fatalf("Failed to set MY_NODE_NAME: %v", err)
			}

			r, tc := getFakeDiskMaker(t, tmpSymLinkTargetDir, objects...)
			r.localVolume = lv

			err = tc.fakeClient.Create(context.TODO(), configMap)
			if err != nil {
				t.Fatalf("Failed to create ConfigMap: %v", err)
			}

			// Rewrite nodeName which is set in init() of the module (from env var).
			nodeName = test.targetNodeName
			reconRequest := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name: cmName, Namespace: cmNamespace},
			}

			// Create fake directory entries.
			dirFiles := map[string][]*provUtil.FakeDirEntry{
				storageClassName: test.dirEntries,
			}
			tc.fakeVolUtil.AddNewDirEntries(tmpSymLinkTargetDir, dirFiles)

			// Run Reconcile() attempts evaluating state of PVs in each iteration.
			retries := 5
			retryWait := 1 * time.Second
			checkPassed := false
			for i := retries; i >= 0; i-- {
				if !checkPassed {
					result, err := r.Reconcile(context.TODO(), reconRequest)
					if err != nil {
						t.Fatalf("Reconcile failed: %v", err)
					}
					if result.Requeue {
						time.Sleep(result.RequeueAfter)
						result, err = r.Reconcile(context.TODO(), reconRequest)
					}
					ok, msg := evaluate(tc, expectedPVs)
					if !ok {
						t.Logf("Reconcile evaluation did not pass with message: %v attempts remaining: %v", msg, i)
						t.Logf("Retry after %v", retryWait)
						time.Sleep(retryWait)
						continue
					} else {
						checkPassed = true
						t.Log("Reconcile check passed!")
					}

				}
			}
			if !checkPassed {
				t.Fatalf("Reconcile check failed!")
			}
		})
	}
}

func evaluate(tc *testContext, expectedPVs *corev1.PersistentVolumeList) (bool, string) {
	// Get PVs after reconcile.
	currentPVs := &corev1.PersistentVolumeList{}
	err := tc.fakeClient.List(context.TODO(), currentPVs)
	if err != nil {
		msg := fmt.Sprintf("Failed to get PVs: %v", err)
		return false, msg
	}

	var actualPVsValues []corev1.PersistentVolume
	actualPVsValues = currentPVs.Items

	actualPVsValuesCopy := make([]corev1.PersistentVolume, len(actualPVsValues))
	copy(actualPVsValuesCopy, actualPVsValues)

	actualPVsValuesCopy2 := make([]corev1.PersistentVolume, len(actualPVsValues))
	copy(actualPVsValuesCopy2, actualPVsValues)

	// Test that there are no extra PVs left apart from expected ones.
	for a, actualPV := range actualPVsValuesCopy {
		for _, expectedPV := range expectedPVs.Items {
			actualPV.SetResourceVersion("")
			expectedPV.SetResourceVersion("")
			if reflect.DeepEqual(actualPV, expectedPV) {
				actualPVsValuesCopy = RemoveIndex(actualPVsValuesCopy, a)
			}

		}
	}

	if len(expectedPVs.Items) == 0 && len(actualPVsValuesCopy) != 0 {
		msg := fmt.Sprintf("\nExpected to find no PVs after reconcile but some were still found: %v", actualPVsValuesCopy)
		return false, msg
	}

	if len(actualPVsValuesCopy) != 0 {
		msg := fmt.Sprintf("\nFound PVs that were not expected!\nThese PVs are actually present but should not be:\n%v", actualPVsValuesCopy)
		return false, msg
	}

	// Test that every expected PV is found.
	expectedPVsValuesCopy := make([]corev1.PersistentVolume, len(expectedPVs.Items))
	copy(expectedPVsValuesCopy, expectedPVs.Items)

	if len(expectedPVs.Items) != 0 {
		for _, actualPV := range actualPVsValuesCopy2 {
			for e, expectedPV := range expectedPVsValuesCopy {
				actualPV.SetResourceVersion("")
				expectedPV.SetResourceVersion("")
				if reflect.DeepEqual(actualPV, expectedPV) {
					expectedPVsValuesCopy = RemoveIndex(expectedPVsValuesCopy, e)
				}
			}
		}
	}

	if len(expectedPVsValuesCopy) != 0 {
		msg := fmt.Sprintf("\nNot all expected PVs found!\nThese PVs were expected but not found:\n%v", expectedPVsValuesCopy)
		return false, msg
	}

	return true, ""
}

func RemoveIndex(s []corev1.PersistentVolume, index int) []corev1.PersistentVolume {
	ret := make([]corev1.PersistentVolume, 0)
	ret = append(ret, s[:index]...)
	return append(ret, s[index+1:]...)
}
