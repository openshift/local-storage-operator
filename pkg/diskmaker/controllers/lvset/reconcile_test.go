package lvset

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/client-go/security/clientset/versioned/scheme"
	v1api "github.com/openshift/local-storage-operator/api/v1"
	v1alphav1api "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/internal"
	test "github.com/openshift/local-storage-operator/test/framework"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/exec"
	testingexec "k8s.io/utils/exec/testing"
	"k8s.io/utils/mount"

	"sigs.k8s.io/controller-runtime/pkg/client"
	crFake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	provCache "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/cache"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
	provDeleter "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/deleter"
	provUtil "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/util"
)

const (
	testNamespace = "default"
)

// testConfig allows manipulating the fake objects for ReconcileLocalVolumeSet
type testContext struct {
	fakeClient    client.Client
	fakeRecorder  *record.FakeRecorder
	eventStream   chan string
	fakeClock     *fakeClock
	fakeMounter   *mount.FakeMounter
	fakeVolUtil   *provUtil.FakeVolumeUtil
	fakeDirFiles  map[string][]*provUtil.FakeDirEntry
	runtimeConfig *provCommon.RuntimeConfig
}

func newFakeLocalVolumeSetReconciler(t *testing.T, objs ...runtime.Object) (*LocalVolumeSetReconciler, *testContext) {
	scheme := scheme.Scheme

	err := v1api.AddToScheme(scheme)
	assert.NoErrorf(t, err, "creating scheme")

	err = v1alphav1api.AddToScheme(scheme)
	assert.NoErrorf(t, err, "creating scheme")

	err = corev1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding corev1 to scheme")

	err = appsv1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding appsv1 to scheme")

	err = storagev1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding storagev1 to scheme")

	fakeClient := crFake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1api.LocalVolumeDeviceLink{}).WithRuntimeObjects(objs...).Build()

	fakeRecorder := record.NewFakeRecorder(20)
	eventChannel := fakeRecorder.Events
	fakeClock := &fakeClock{}
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
		eventStream:   eventChannel,
		fakeClock:     fakeClock,
		fakeMounter:   mounter,
		runtimeConfig: runtimeConfig,
		fakeVolUtil:   fakeVolUtil,
	}

	lvsReconciler := NewLocalVolumeSetReconciler(
		fakeClient,
		fakeClient,
		scheme,
		fakeClock,
		&provDeleter.CleanupStatusTracker{ProcTable: provDeleter.NewProcTable()},
		runtimeConfig,
	)

	return lvsReconciler, tc
}

func createTempDir(t *testing.T, prefix string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", prefix)
	assert.NoError(t, err)
	return dir
}

func fakeCommandExecutor(findOutput, blkidOutput string, blkidErr error) *testingexec.FakeExec {
	action := func(cmd string, args ...string) exec.Cmd {
		switch cmd {
		case "find":
			return &testingexec.FakeCmd{
				CombinedOutputScript: []testingexec.FakeAction{
					func() ([]byte, []byte, error) {
						return []byte(findOutput), nil, nil
					},
				},
			}
		case "blkid":
			return &testingexec.FakeCmd{
				CombinedOutputScript: []testingexec.FakeAction{
					func() ([]byte, []byte, error) {
						return []byte(blkidOutput), nil, blkidErr
					},
				},
			}
		default:
			return &testingexec.FakeCmd{
				CombinedOutputScript: []testingexec.FakeAction{
					func() ([]byte, []byte, error) {
						return nil, nil, fmt.Errorf("unexpected command %s", cmd)
					},
				},
			}
		}
	}
	commandScript := make([]testingexec.FakeCommandAction, 0, 16)
	for i := 0; i < 16; i++ {
		commandScript = append(commandScript, action)
	}
	return &testingexec.FakeExec{
		CommandScript: commandScript,
	}
}

func TestGetAlreadySymlinked(t *testing.T) {
	tmpDir := createTempDir(t, "already-symlinked-")
	defer os.RemoveAll(tmpDir)

	matching := filepath.Join(tmpDir, "matching")
	nonMatching := filepath.Join(tmpDir, "other")
	broken := filepath.Join(tmpDir, "broken")

	assert.NoError(t, os.Symlink("/dev/null", matching))
	assert.NoError(t, os.Symlink("/dev/zero", nonMatching))
	assert.NoError(t, os.Symlink(filepath.Join(tmpDir, "missing"), broken))

	count, noMatch, err := getAlreadySymlinked(tmpDir, []internal.BlockDevice{{KName: "null"}})
	assert.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.ElementsMatch(t, []string{nonMatching, broken}, noMatch)
}

func TestProcessNewSymlink(t *testing.T) {
	reclaimPolicyDelete := corev1.PersistentVolumeReclaimDelete
	maxCountOne := int32(1)

	testCases := []struct {
		name             string
		symLinkDir       string
		maxDeviceCount   *int32
		existingSymlink  bool
		existingReleased bool
		expectError      string
		expectFast       bool
		expectMax        bool
		expectPV         bool
	}{
		{
			name:       "provisions a new pv when below max count",
			symLinkDir: "sc-test",
			expectPV:   true,
		},
		{
			name:            "returns max count reached when existing symlink already matches",
			symLinkDir:      "sc-test",
			maxDeviceCount:  &maxCountOne,
			existingSymlink: true,
			expectMax:       true,
		},
		{
			name:             "requests fast requeue when pv is released",
			symLinkDir:       "sc-test",
			existingReleased: true,
			expectFast:       true,
			expectPV:         true,
		},
		{
			name:           "returns error when max count lookup cannot glob",
			symLinkDir:     "[",
			maxDeviceCount: &maxCountOne,
			expectError:    "could not determine how many devices are already provisioned",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := createTempDir(t, "process-new-symlink-")
			defer os.RemoveAll(tmpDir)

			lvset := &v1alphav1api.LocalVolumeSet{
				TypeMeta: metav1.TypeMeta{
					Kind:       v1alphav1api.LocalVolumeSetKind,
					APIVersion: v1alphav1api.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "lvset-a",
					Namespace: testNamespace,
				},
				Spec: v1alphav1api.LocalVolumeSetSpec{
					StorageClassName: "sc-test",
					MaxDeviceCount:   tc.maxDeviceCount,
				},
			}
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{corev1.LabelHostname: "node-hostname-a"},
				},
			}
			sc := &storagev1.StorageClass{
				ObjectMeta:    metav1.ObjectMeta{Name: "sc-test"},
				ReclaimPolicy: &reclaimPolicyDelete,
			}

			objs := []runtime.Object{lvset, node, sc}
			symLinkDirPath := tc.symLinkDir
			if tc.symLinkDir != "[" {
				symLinkDirPath = filepath.Join(tmpDir, tc.symLinkDir)
				assert.NoError(t, os.MkdirAll(symLinkDirPath, 0755))
			}

			fakeIDPath := "/dev/disk/by-id/wwn-null"

			device := internal.BlockDevice{
				Name:     "null",
				KName:    "null",
				PathByID: fakeIDPath,
			}
			targetName := filepath.Base(device.PathByID)
			pvName := common.GeneratePVName(targetName, node.Name, sc.Name)
			if tc.existingReleased {
				objs = append(objs, &corev1.PersistentVolume{
					ObjectMeta: metav1.ObjectMeta{Name: pvName},
					Status:     corev1.PersistentVolumeStatus{Phase: corev1.VolumeReleased},
				})
			}

			r, ctx := newFakeLocalVolumeSetReconciler(t, objs...)
			r.nodeName = node.Name
			r.runtimeConfig.Node = node
			r.runtimeConfig.Name = common.GetProvisionedByValue(*node)
			r.runtimeConfig.Namespace = lvset.Namespace
			r.runtimeConfig.DiscoveryMap[sc.Name] = provCommon.MountConfig{
				VolumeMode: string(corev1.PersistentVolumeBlock),
			}
			ctx.fakeVolUtil.AddNewDirEntries(tmpDir, map[string][]*provUtil.FakeDirEntry{
				sc.Name: {
					{Name: targetName, Capacity: 10 * common.GiB, VolumeType: provUtil.FakeEntryBlock},
				},
			})

			if tc.existingSymlink {
				assert.NoError(t, os.Symlink("/dev/null", filepath.Join(symLinkDirPath, "claimed")))
			}

			origEval := internal.FilePathEvalSymLinks
			origGlob := internal.FilePathGlob
			origExec := internal.CmdExecutor
			t.Cleanup(func() {
				internal.FilePathEvalSymLinks = origEval
				internal.FilePathGlob = origGlob
				internal.CmdExecutor = origExec
			})

			internal.FilePathEvalSymLinks = func(path string) (string, error) {
				if path == device.PathByID {
					return "/dev/null", nil
				}
				return filepath.EvalSymlinks(path)
			}
			internal.FilePathGlob = func(pattern string) ([]string, error) {
				if pattern == filepath.Join(internal.DiskByIDDir, "*") {
					return []string{fakeIDPath}, nil
				}
				return filepath.Glob(pattern)
			}
			internal.CmdExecutor = fakeCommandExecutor("", "", nil)

			result, err := r.processNewSymlink(
				t.Context(),
				lvset,
				device,
				[]internal.BlockDevice{device},
				*sc,
				sets.NewString(),
				symLinkDirPath,
			)

			if tc.expectError != "" {
				assert.Nil(t, result)
				assert.ErrorContains(t, err, tc.expectError)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, result)
			assert.Equal(t, tc.expectFast, result.fastRequeue)
			assert.Equal(t, tc.expectMax, result.maxCountReached)

			pv := &corev1.PersistentVolume{}
			err = r.Client.Get(t.Context(), types.NamespacedName{Name: pvName}, pv)
			if tc.expectPV {
				if !tc.existingReleased {
					assert.NoError(t, err)
					expectedLocalPath := filepath.Join(symLinkDirPath, targetName)
					assert.Equal(t, expectedLocalPath, pv.Spec.Local.Path)

					lvdl := &v1api.LocalVolumeDeviceLink{}
					err = r.Client.Get(t.Context(), types.NamespacedName{Name: pvName, Namespace: lvset.Namespace}, lvdl)
					if assert.NoError(t, err) {
						assert.Equal(t, pvName, lvdl.Spec.PersistentVolumeName)
						assert.Equal(t, v1api.DeviceLinkPolicyNone, lvdl.Spec.Policy)
						assert.Equal(t, device.PathByID, lvdl.Status.CurrentLinkTarget)
						assert.Equal(t, fakeIDPath, lvdl.Status.PreferredLinkTarget)
						assert.Equal(t, "", lvdl.Status.FilesystemUUID)
					}
				}
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestProvisionFromExistingPV(t *testing.T) {
	reclaimPolicyDelete := corev1.PersistentVolumeReclaimDelete

	testCases := []struct {
		name            string
		symlinkTarget   string
		expectDeviceID  bool
		removeSymlink   bool
		expectErrSubstr string
	}{
		{
			name:           "preserves by-id annotation when symlink points to by-id path",
			symlinkTarget:  "/dev/disk/by-id/wwn-null",
			expectDeviceID: true,
		},
		{
			name:          "does not set device-id annotation for plain device path",
			symlinkTarget: "/dev/null",
		},
		{
			name:            "returns readlink error when symlink is missing",
			removeSymlink:   true,
			expectErrSubstr: "no such file or directory",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := createTempDir(t, "existing-pv-")
			defer os.RemoveAll(tmpDir)

			lvset := &v1alphav1api.LocalVolumeSet{
				TypeMeta: metav1.TypeMeta{
					Kind:       v1alphav1api.LocalVolumeSetKind,
					APIVersion: v1alphav1api.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "lvset-existing",
					Namespace: testNamespace,
				},
				Spec: v1alphav1api.LocalVolumeSetSpec{StorageClassName: "sc-existing"},
			}
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-existing",
					Labels: map[string]string{corev1.LabelHostname: "node-hostname-existing"},
				},
			}
			sc := &storagev1.StorageClass{
				ObjectMeta:    metav1.ObjectMeta{Name: "sc-existing"},
				ReclaimPolicy: &reclaimPolicyDelete,
			}

			r, ctx := newFakeLocalVolumeSetReconciler(t, lvset, node, sc)
			r.nodeName = node.Name
			r.runtimeConfig.Node = node
			r.runtimeConfig.Name = common.GetProvisionedByValue(*node)
			r.runtimeConfig.Namespace = lvset.Namespace
			r.runtimeConfig.DiscoveryMap[sc.Name] = provCommon.MountConfig{
				VolumeMode: string(corev1.PersistentVolumeBlock),
			}

			symLinkDir := filepath.Join(tmpDir, sc.Name)
			assert.NoError(t, os.MkdirAll(symLinkDir, 0755))
			symlinkPath := filepath.Join(symLinkDir, "claimed")
			if !tc.removeSymlink {
				assert.NoError(t, os.Symlink(tc.symlinkTarget, symlinkPath))
			}

			ctx.fakeVolUtil.AddNewDirEntries(tmpDir, map[string][]*provUtil.FakeDirEntry{
				sc.Name: {
					{Name: "claimed", Capacity: 10 * common.GiB, VolumeType: provUtil.FakeEntryBlock},
				},
			})

			origExec := internal.CmdExecutor
			t.Cleanup(func() {
				internal.CmdExecutor = origExec
			})
			internal.CmdExecutor = fakeCommandExecutor("", "", nil)

			err := r.provisionFromExistingPV(
				t.Context(),
				lvset,
				internal.BlockDevice{Name: "null", KName: "null"},
				*sc,
				sets.NewString(),
				symlinkPath,
			)

			if tc.expectErrSubstr != "" {
				assert.ErrorContains(t, err, tc.expectErrSubstr)
				return
			}

			assert.NoError(t, err)
			pvName := common.GeneratePVName(filepath.Base(symlinkPath), node.Name, sc.Name)
			pv := &corev1.PersistentVolume{}
			assert.NoError(t, r.Client.Get(t.Context(), types.NamespacedName{Name: pvName}, pv))
			if assert.NotNil(t, pv.Spec.Local) {
				assert.Equal(t, symlinkPath, pv.Spec.Local.Path)
			}
			_, hasDeviceID := pv.Annotations[common.PVDeviceIDLabel]
			assert.Equal(t, tc.expectDeviceID, hasDeviceID)

			lvdl := &v1api.LocalVolumeDeviceLink{}
			assert.NoError(t, r.Client.Get(t.Context(), types.NamespacedName{Name: pvName, Namespace: lvset.Namespace}, lvdl))
			assert.Equal(t, pvName, lvdl.Spec.PersistentVolumeName)
			assert.Equal(t, v1api.DeviceLinkPolicyNone, lvdl.Spec.Policy)
			assert.Equal(t, tc.symlinkTarget, lvdl.Status.CurrentLinkTarget)
			assert.Equal(t, "", lvdl.Status.FilesystemUUID)
		})
	}
}

func TestProcessRejectedDevicesForDeviceLinks(t *testing.T) {
	testCases := []struct {
		name              string
		createSymlink     bool
		makeSymlinkAFile  bool
		mismatchingPolicy bool
		expectCurrentLink string
		expectCondition   string
	}{
		{
			name:              "updates lvdl status when existing symlink matches",
			createSymlink:     true,
			expectCurrentLink: "/dev/null",
		},
		{
			name: "skips devices without existing symlink",
		},
		{
			name:             "skips symlink entries that cannot be read",
			createSymlink:    true,
			makeSymlinkAFile: true,
		},
		{
			name:              "records condition when mismatching symlink must be recreated",
			createSymlink:     true,
			mismatchingPolicy: true,
			expectCondition:   "PreferredTargetNotSymlink",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := createTempDir(t, "rejected-lvset-")
			defer os.RemoveAll(tmpDir)

			lvset := &v1alphav1api.LocalVolumeSet{
				TypeMeta: metav1.TypeMeta{
					Kind:       v1alphav1api.LocalVolumeSetKind,
					APIVersion: v1alphav1api.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "lvset-rejected",
					Namespace: testNamespace,
				},
				Spec: v1alphav1api.LocalVolumeSetSpec{StorageClassName: "sc-rejected"},
			}
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-rejected",
					Labels: map[string]string{corev1.LabelHostname: "node-hostname-rejected"},
				},
			}
			pvName := common.GeneratePVName("claimed", node.Name, lvset.Spec.StorageClassName)
			lvdl := &v1api.LocalVolumeDeviceLink{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvName,
					Namespace: testNamespace,
				},
				Spec: v1api.LocalVolumeDeviceLinkSpec{
					PersistentVolumeName: pvName,
					Policy:               v1api.DeviceLinkPolicyNone,
				},
			}
			if tc.mismatchingPolicy {
				preferredFile := filepath.Join(tmpDir, "preferred-file")
				assert.NoError(t, os.WriteFile(preferredFile, []byte("not-a-symlink"), 0644))
				lvdl.Spec.Policy = v1api.DeviceLinkPolicyPreferredLinkTarget
				lvdl.Status.CurrentLinkTarget = "/dev/null"
				lvdl.Status.PreferredLinkTarget = preferredFile
			}

			r, _ := newFakeLocalVolumeSetReconciler(t,
				lvset,
				node,
				&corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: pvName}},
				lvdl,
			)
			r.nodeName = node.Name
			r.runtimeConfig.Node = node
			r.runtimeConfig.Namespace = testNamespace

			symLinkDir := filepath.Join(tmpDir, lvset.Spec.StorageClassName)
			assert.NoError(t, os.MkdirAll(symLinkDir, 0755))
			symlinkPath := filepath.Join(symLinkDir, "claimed")
			if tc.createSymlink {
				if tc.makeSymlinkAFile {
					assert.NoError(t, os.WriteFile(symlinkPath, []byte("plain-file"), 0644))
				} else {
					assert.NoError(t, os.Symlink("/dev/null", symlinkPath))
				}
			}

			origGlob := internal.FilePathGlob
			origEval := internal.FilePathEvalSymLinks
			origExec := internal.CmdExecutor
			t.Cleanup(func() {
				internal.FilePathGlob = origGlob
				internal.FilePathEvalSymLinks = origEval
				internal.CmdExecutor = origExec
			})

			internal.FilePathGlob = func(pattern string) ([]string, error) {
				if pattern == filepath.Join(internal.DiskByIDDir, "*") {
					return []string{"/dev/disk/by-id/wwn-null"}, nil
				}
				return filepath.Glob(pattern)
			}
			internal.FilePathEvalSymLinks = func(path string) (string, error) {
				if path == "/dev/disk/by-id/wwn-null" {
					return "/dev/null", nil
				}
				return filepath.EvalSymlinks(path)
			}
			internal.CmdExecutor = fakeCommandExecutor("", "uuid-null", nil)

			r.processRejectedDevicesForDeviceLinks(
				t.Context(),
				lvset,
				[]internal.BlockDevice{{Name: "null", KName: "null"}},
				symLinkDir,
				lvset.Spec.StorageClassName,
			)

			fetched := &v1api.LocalVolumeDeviceLink{}
			assert.NoError(t, r.Client.Get(context.TODO(), types.NamespacedName{Name: pvName, Namespace: testNamespace}, fetched))
			if tc.expectCurrentLink != "" {
				assert.Equal(t, tc.expectCurrentLink, fetched.Status.CurrentLinkTarget)
				return
			}
			if tc.expectCondition != "" {
				if assert.Len(t, fetched.Status.Conditions, 1) {
					assert.Equal(t, operatorv1.ConditionTrue, fetched.Status.Conditions[0].Status)
					assert.Equal(t, tc.expectCondition, fetched.Status.Conditions[0].Reason)
				}
				return
			}
			assert.Empty(t, fetched.Status.CurrentLinkTarget)
		})
	}
}
