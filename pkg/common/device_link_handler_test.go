package common

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	v1 "github.com/openshift/local-storage-operator/api/v1"
	"github.com/openshift/local-storage-operator/pkg/internal"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	utilexec "k8s.io/utils/exec"
	testingexec "k8s.io/utils/exec/testing"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testNodeName = "worker-a"

// createTempFile creates an empty regular file at path (for use as a fake device).
func createTempFile(t *testing.T, path string) error {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return f.Close()
}

// createSymlink creates a symlink at linkPath pointing to target.
func createSymlink(t *testing.T, target, linkPath string) error {
	t.Helper()
	return os.Symlink(target, linkPath)
}

// mkdirAll creates the directory path and all parents.
func mkdirAll(t *testing.T, path string) error {
	t.Helper()
	return os.MkdirAll(path, 0755)
}

// readlink returns the target of the symlink at path.
func readlink(path string) (string, error) {
	return os.Readlink(path)
}

func newLocalVolume(name, namespace string, uid types.UID) *v1.LocalVolume {
	return &v1.LocalVolume{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1.LocalVolumeKind,
			APIVersion: v1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       uid,
		},
	}
}

func newLVDL(name, namespace, pvName string) *v1.LocalVolumeDeviceLink {
	return &v1.LocalVolumeDeviceLink{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1.LocalVolumeDeviceLinkSpec{
			PersistentVolumeName: pvName,
			NodeName:             testNodeName,
			Policy:               v1.DeviceLinkPolicyNone,
		},
	}
}

func newPV(name string) *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
}

// newFakeDeviceLinkClient builds a controller-runtime fake client registered
// with the v1 scheme (which includes LocalVolumeDeviceLink).
// WithStatusSubresource is set so that the fake client enforces the status
// subresource boundary: client.Update() will not persist status fields, and
// only client.Status().Update() will — matching real cluster behaviour.
func newFakeDeviceLinkClient(t *testing.T, objs ...runtime.Object) *fake.ClientBuilder {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding v1 to scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding corev1 to scheme: %v", err)
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1.LocalVolumeDeviceLink{}).
		WithRuntimeObjects(objs...)
}

// fakeBlkidExecutor returns a FakeExec that makes blkid emit the given uuid.
func fakeBlkidExecutor(uuid string) *testingexec.FakeExec {
	blkidAction := func(cmd string, args ...string) utilexec.Cmd {
		return &testingexec.FakeCmd{
			CombinedOutputScript: []testingexec.FakeAction{
				func() ([]byte, []byte, error) {
					return []byte(uuid), nil, nil
				},
			},
		}
	}
	return &testingexec.FakeExec{
		CommandScript: []testingexec.FakeCommandAction{blkidAction},
	}
}

// fakeBlkidEmptyExecutor returns a FakeExec where blkid produces no output.
func fakeBlkidEmptyExecutor() *testingexec.FakeExec {
	return fakeBlkidExecutor("")
}

func TestDeviceLinkHandler_UpdateStatusAndPV(t *testing.T) {
	testCases := []struct {
		name           string
		pvName         string
		namespace      string
		currentSymlink string
		blockDevice    internal.BlockDevice
		ownerObj       runtime.Object
		existing       *v1.LocalVolumeDeviceLink
		existingPV     *corev1.PersistentVolume
		symlinkPath    string
		globLinks      []string
		filesystemUUID string
		expectedLVDL   *v1.LocalVolumeDeviceLink
		verifyOwnerRef bool
	}{
		{
			name:           "populates status with symlink targets and filesystem uuid",
			pvName:         "local-pv-statustest",
			namespace:      "default",
			currentSymlink: "/dev/disk/by-id/wwn-current",
			blockDevice:    internal.BlockDevice{KName: "sda", PathByID: "/dev/disk/by-id/wwn-preferred"},
			ownerObj:       newLocalVolume("lv-statustest", "default", "11111111-aaaa-bbbb-cccc-111111111111"),
			existing:       newLVDL("local-pv-statustest", "default", "local-pv-statustest"),
			existingPV:     newPV("local-pv-statustest"),
			symlinkPath:    "/mnt/local-storage/mysc/mylink",
			globLinks:      []string{"/dev/disk/by-id/wwn-preferred", "/dev/disk/by-id/scsi-abcde"},
			filesystemUUID: "550e8400-e29b-41d4-a716-446655440000",
			expectedLVDL: &v1.LocalVolumeDeviceLink{
				ObjectMeta: metav1.ObjectMeta{Name: "local-pv-statustest", Namespace: "default"},
				Spec:       v1.LocalVolumeDeviceLinkSpec{PersistentVolumeName: "local-pv-statustest", NodeName: testNodeName},
				Status: v1.LocalVolumeDeviceLinkStatus{
					CurrentLinkTarget:           "/dev/disk/by-id/wwn-current",
					PreferredLinkTarget:         "/dev/disk/by-id/wwn-preferred",
					FilesystemUUID:              "550e8400-e29b-41d4-a716-446655440000",
					ValidLinkTargets:            []string{"/dev/disk/by-id/wwn-preferred", "/dev/disk/by-id/scsi-abcde"},
					PersistentVolumeSymlinkPath: "/mnt/local-storage/mysc/mylink",
				},
			},
		},
		{
			name:           "populates symlinkPath and UUID if it was missing",
			pvName:         "local-pv-statustest",
			namespace:      "default",
			currentSymlink: "/dev/disk/by-id/wwn-current",
			blockDevice:    internal.BlockDevice{KName: "sda", PathByID: "/dev/disk/by-id/wwn-preferred"},
			ownerObj:       newLocalVolume("lv-statustest", "default", "11111111-aaaa-bbbb-cccc-111111111111"),
			existing: &v1.LocalVolumeDeviceLink{
				ObjectMeta: metav1.ObjectMeta{Name: "local-pv-statustest", Namespace: "default"},
				Spec:       v1.LocalVolumeDeviceLinkSpec{PersistentVolumeName: "local-pv-statustest", NodeName: testNodeName},
				Status: v1.LocalVolumeDeviceLinkStatus{
					CurrentLinkTarget:   "/dev/disk/by-id/wwn-current",
					PreferredLinkTarget: "/dev/disk/by-id/wwn-preferred",
					ValidLinkTargets:    []string{"/dev/disk/by-id/wwn-preferred", "/dev/disk/by-id/scsi-abcde"},
				},
			},
			existingPV:     newPV("local-pv-statustest"),
			symlinkPath:    "/mnt/local-storage/mysc/mylink",
			globLinks:      []string{"/dev/disk/by-id/wwn-preferred", "/dev/disk/by-id/scsi-abcde"},
			filesystemUUID: "550e8400-e29b-41d4-a716-446655440000",
			expectedLVDL: &v1.LocalVolumeDeviceLink{
				ObjectMeta: metav1.ObjectMeta{Name: "local-pv-statustest", Namespace: "default"},
				Spec:       v1.LocalVolumeDeviceLinkSpec{PersistentVolumeName: "local-pv-statustest", NodeName: testNodeName},
				Status: v1.LocalVolumeDeviceLinkStatus{
					CurrentLinkTarget:           "/dev/disk/by-id/wwn-current",
					PreferredLinkTarget:         "/dev/disk/by-id/wwn-preferred",
					FilesystemUUID:              "550e8400-e29b-41d4-a716-446655440000",
					ValidLinkTargets:            []string{"/dev/disk/by-id/wwn-preferred", "/dev/disk/by-id/scsi-abcde"},
					PersistentVolumeSymlinkPath: "/mnt/local-storage/mysc/mylink",
				},
			},
		},
		{
			name:           "handles no by-id links and no filesystem uuid",
			pvName:         "local-pv-nolinks",
			namespace:      "default",
			currentSymlink: "/dev/sdb",
			blockDevice:    internal.BlockDevice{KName: "sdb"},
			ownerObj:       newLocalVolume("lv-nolinks", "default", "22222222-aaaa-bbbb-cccc-222222222222"),
			existing:       newLVDL("local-pv-nolinks", "default", "local-pv-nolinks"),
			existingPV:     newPV("local-pv-nolinks"),
			symlinkPath:    "/mnt/local-storage/mysc/mylink",
			expectedLVDL: &v1.LocalVolumeDeviceLink{
				ObjectMeta: metav1.ObjectMeta{Name: "local-pv-nolinks", Namespace: "default"},
				Spec:       v1.LocalVolumeDeviceLinkSpec{PersistentVolumeName: "local-pv-nolinks", NodeName: testNodeName},
				Status: v1.LocalVolumeDeviceLinkStatus{
					CurrentLinkTarget:           "/dev/sdb",
					PreferredLinkTarget:         "",
					FilesystemUUID:              "",
					ValidLinkTargets:            []string{},
					PersistentVolumeSymlinkPath: "/mnt/local-storage/mysc/mylink",
				},
			},
		},
		{
			name:           "create then update full flow",
			pvName:         "local-pv-fullflow",
			namespace:      "openshift-local-storage",
			currentSymlink: "/dev/disk/by-id/scsi-current",
			blockDevice:    internal.BlockDevice{KName: "sdc", PathByID: "/dev/disk/by-id/wwn-preferred"},
			ownerObj:       newLocalVolume("lv-fullflow", "openshift-local-storage", "33333333-aaaa-bbbb-cccc-333333333333"),
			existing:       newLVDL("local-pv-fullflow", "openshift-local-storage", "local-pv-fullflow"),
			existingPV:     newPV("local-pv-fullflow"),
			symlinkPath:    "/mnt/local-storage/mysc/mylink",
			globLinks:      []string{"/dev/disk/by-id/wwn-preferred"},
			expectedLVDL: &v1.LocalVolumeDeviceLink{
				ObjectMeta: metav1.ObjectMeta{Name: "local-pv-fullflow", Namespace: "openshift-local-storage"},
				Spec:       v1.LocalVolumeDeviceLinkSpec{PersistentVolumeName: "local-pv-fullflow", NodeName: testNodeName},
				Status: v1.LocalVolumeDeviceLinkStatus{
					CurrentLinkTarget:           "/dev/disk/by-id/scsi-current",
					PreferredLinkTarget:         "/dev/disk/by-id/wwn-preferred",
					FilesystemUUID:              "",
					ValidLinkTargets:            []string{"/dev/disk/by-id/wwn-preferred"},
					PersistentVolumeSymlinkPath: "/mnt/local-storage/mysc/mylink",
				},
			},
		},
		{
			name:        "returns without updating when pv does not exist",
			pvName:      "local-pv-missing",
			namespace:   "default",
			symlinkPath: "/mnt/local-storage/mysc/mylink",
			blockDevice: internal.BlockDevice{KName: "sdd"},
			ownerObj:    newLocalVolume("lv-missing-pv", "default", "44444444-aaaa-bbbb-cccc-444444444444"),
			existing:    newLVDL("local-pv-missing", "default", "local-pv-missing"),
		},
		{
			name:           "creates lvdl during status update when pv exists and create was skipped",
			pvName:         "local-pv-lvdl-missing",
			namespace:      "default",
			currentSymlink: "/dev/sde",
			blockDevice:    internal.BlockDevice{KName: "sde"},
			ownerObj:       newLocalVolume("lv-create-on-update", "default", "55555555-aaaa-bbbb-cccc-555555555555"),
			existingPV:     newPV("local-pv-lvdl-missing"),
			symlinkPath:    "/mnt/local-storage/mysc/mylink",
			expectedLVDL: &v1.LocalVolumeDeviceLink{
				ObjectMeta: metav1.ObjectMeta{Name: "local-pv-lvdl-missing", Namespace: "default"},
				Spec: v1.LocalVolumeDeviceLinkSpec{
					PersistentVolumeName: "local-pv-lvdl-missing",
					NodeName:             testNodeName,
					Policy:               v1.DeviceLinkPolicyNone,
				},
				Status: v1.LocalVolumeDeviceLinkStatus{
					CurrentLinkTarget:           "/dev/sde",
					PreferredLinkTarget:         "",
					FilesystemUUID:              "",
					ValidLinkTargets:            []string{},
					PersistentVolumeSymlinkPath: "/mnt/local-storage/mysc/mylink",
				},
			},
			verifyOwnerRef: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var runtimeObjects []runtime.Object
			if tc.existing != nil {
				runtimeObjects = append(runtimeObjects, tc.existing.DeepCopy())
			}
			if tc.existingPV != nil {
				runtimeObjects = append(runtimeObjects, tc.existingPV.DeepCopy())
			}

			fakeClient := newFakeDeviceLinkClient(t, runtimeObjects...).Build()
			pvCache := NewLocalVolumeDeviceLinkCache(nil, nil, testNodeName)
			handler := NewDeviceLinkHandler(fakeClient, fakeClient, record.NewFakeRecorder(10), pvCache, testNodeName)

			origGlob := internal.FilePathGlob
			origEval := internal.FilePathEvalSymLinks
			origExec := internal.CmdExecutor
			t.Cleanup(func() {
				internal.FilePathGlob = origGlob
				internal.FilePathEvalSymLinks = origEval
				internal.CmdExecutor = origExec
			})

			internal.FilePathGlob = func(pattern string) ([]string, error) {
				return tc.globLinks, nil
			}
			internal.FilePathEvalSymLinks = func(path string) (string, error) {
				return "/dev/" + tc.blockDevice.KName, nil
			}
			if tc.filesystemUUID == "" {
				internal.CmdExecutor = fakeBlkidEmptyExecutor()
			} else {
				internal.CmdExecutor = fakeBlkidExecutor(tc.filesystemUUID)
			}

			updated, err := handler.ApplyStatus(context.TODO(), tc.pvName, tc.namespace, tc.blockDevice, tc.ownerObj, tc.currentSymlink, tc.symlinkPath)
			if err != nil {
				t.Fatalf("UpdateStatusAndPV returned unexpected error: %v", err)
			}
			if tc.expectedLVDL == nil {
				assert.Nil(t, updated)
				assert.Empty(t, pvCache.localDeviceInfos, "cache should not be updated when no LVDL is returned")
				return
			}

			assert.Equal(t, tc.expectedLVDL.Name, updated.Name)
			assert.Equal(t, tc.expectedLVDL.Namespace, updated.Namespace)
			assert.Equal(t, tc.expectedLVDL.Spec.PersistentVolumeName, updated.Spec.PersistentVolumeName)
			assert.Equal(t, tc.expectedLVDL.Spec.NodeName, updated.Spec.NodeName)
			assert.Equal(t, tc.expectedLVDL.Status.CurrentLinkTarget, updated.Status.CurrentLinkTarget)
			assert.Equal(t, tc.expectedLVDL.Status.PreferredLinkTarget, updated.Status.PreferredLinkTarget)
			assert.Equal(t, tc.expectedLVDL.Status.FilesystemUUID, updated.Status.FilesystemUUID)
			assert.Equal(t, tc.expectedLVDL.Status.PersistentVolumeSymlinkPath, updated.Status.PersistentVolumeSymlinkPath)
			assert.ElementsMatch(t, tc.expectedLVDL.Status.ValidLinkTargets, updated.Status.ValidLinkTargets)

			fetched := &v1.LocalVolumeDeviceLink{}
			if err := fakeClient.Get(context.TODO(), types.NamespacedName{Name: tc.pvName, Namespace: tc.namespace}, fetched); err != nil {
				t.Fatalf("Get after UpdateStatusAndPV failed: %v", err)
			}
			assert.Equal(t, tc.expectedLVDL.Name, fetched.Name)
			assert.Equal(t, tc.expectedLVDL.Namespace, fetched.Namespace)
			assert.Equal(t, tc.expectedLVDL.Spec.PersistentVolumeName, fetched.Spec.PersistentVolumeName)
			assert.Equal(t, tc.expectedLVDL.Spec.NodeName, fetched.Spec.NodeName)
			assert.Equal(t, tc.expectedLVDL.Status.CurrentLinkTarget, fetched.Status.CurrentLinkTarget)
			assert.Equal(t, tc.expectedLVDL.Status.PreferredLinkTarget, fetched.Status.PreferredLinkTarget)
			assert.Equal(t, tc.expectedLVDL.Status.FilesystemUUID, fetched.Status.FilesystemUUID)
			assert.Equal(t, tc.expectedLVDL.Status.PersistentVolumeSymlinkPath, fetched.Status.PersistentVolumeSymlinkPath)
			assert.ElementsMatch(t, tc.expectedLVDL.Status.ValidLinkTargets, fetched.Status.ValidLinkTargets)
			// Verify write-through cache was updated with the correct LVDL.
			for _, target := range tc.expectedLVDL.Status.ValidLinkTargets {
				info, found, err := pvCache.FindStalePVs(target, tc.blockDevice)
				assert.NoError(t, err)
				if assert.True(t, found, "cache should contain entry for valid link target %s", target) {
					cached := info.lvdls[tc.expectedLVDL.Name]
					if assert.NotNil(t, cached, "cache entry should contain LVDL %s", tc.expectedLVDL.Name) {
						assert.Equal(t, tc.expectedLVDL.Status.CurrentLinkTarget, cached.Status.CurrentLinkTarget)
						assert.Equal(t, tc.expectedLVDL.Status.PreferredLinkTarget, cached.Status.PreferredLinkTarget)
					}
				}
			}

			if tc.verifyOwnerRef {
				assert.Len(t, fetched.OwnerReferences, 1)
				assert.Equal(t, tc.ownerObj.GetObjectKind().GroupVersionKind().Kind, fetched.OwnerReferences[0].Kind)
				assert.Equal(t, tc.ownerObj.GetObjectKind().GroupVersionKind().GroupVersion().String(), fetched.OwnerReferences[0].APIVersion)
				assert.Equal(t, tc.ownerObj.(metav1.Object).GetName(), fetched.OwnerReferences[0].Name)
				assert.Equal(t, tc.ownerObj.(metav1.Object).GetUID(), fetched.OwnerReferences[0].UID)
			}
		})
	}
}

// newLVDLWithPolicy creates an LVDL with the given policy and status fields.
func newLVDLWithPolicy(name, namespace string, policy v1.DeviceLinkPolicy, currentTarget, preferredTarget string) *v1.LocalVolumeDeviceLink {
	return &v1.LocalVolumeDeviceLink{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1.LocalVolumeDeviceLinkSpec{
			PersistentVolumeName: name,
			NodeName:             testNodeName,
			Policy:               policy,
		},
		Status: v1.LocalVolumeDeviceLinkStatus{
			CurrentLinkTarget:   currentTarget,
			PreferredLinkTarget: preferredTarget,
		},
	}
}

// saveAndRestoreGlobals saves the current values of the injectable package-level
// variables and restores them via t.Cleanup. Call this at the start of any test
// that overrides internal.FilePathGlob, internal.FilePathEvalSymLinks, or internal.CmdExecutor.
func saveAndRestoreGlobals(t *testing.T) {
	t.Helper()
	origGlob := internal.FilePathGlob
	origEval := internal.FilePathEvalSymLinks
	origExec := internal.CmdExecutor
	origTimeNow := timeNow
	t.Cleanup(func() {
		internal.FilePathGlob = origGlob
		internal.FilePathEvalSymLinks = origEval
		internal.CmdExecutor = origExec
		timeNow = origTimeNow
	})
}

// filePathGlobWithPreferred returns a glob func that returns preferredTarget
// for /dev/disk/by-id/* patterns and delegates to filepath.Glob otherwise.
func filePathGlobWithPreferred(preferredTarget string) func(string) ([]string, error) {
	return func(pattern string) ([]string, error) {
		if pattern == internal.DiskByIDDir+"*" || pattern == filepath.Join(internal.DiskByIDDir, "/*") {
			return []string{preferredTarget}, nil
		}
		return filepath.Glob(pattern)
	}
}

type recreateSymlinkTestEnv struct {
	tmpDir         string
	physicalDevice string
	// preferred path in /dev/disk/by-id (well in real code anyways)
	preferredTarget string
	// current path in /dev/somewhere
	currentTarget string
	// current path in /mnt/local-storage/somewhere
	symLinkPath string
	// we would prefer to use preferredTarget but somehow
	// this is claimed by another symlink /mnt/local-storage/otherdevice
	claimingSymlink string
}

func newRecreateSymlinkTestEnv(t *testing.T) *recreateSymlinkTestEnv {
	t.Helper()

	tmpDir := t.TempDir()
	physicalDevice := filepath.Join(tmpDir, "sdb")
	if err := createTempFile(t, physicalDevice); err != nil {
		t.Fatalf("failed to create physical device file: %v", err)
	}

	// Create a by-id directory under tmpDir so that test symlinks
	// mimic the real /dev/disk/by-id/<link> structure.
	byIDDir := filepath.Join(tmpDir, "by-id")
	if err := mkdirAll(t, byIDDir); err != nil {
		t.Fatalf("failed to create by-id dir: %v", err)
	}

	scDir := filepath.Join(tmpDir, "sc")
	if err := mkdirAll(t, scDir); err != nil {
		t.Fatalf("failed to create sc dir: %v", err)
	}

	return &recreateSymlinkTestEnv{
		tmpDir:          tmpDir,
		physicalDevice:  physicalDevice,
		preferredTarget: filepath.Join(byIDDir, "preferred"),
		currentTarget:   filepath.Join(byIDDir, "current"),
		symLinkPath:     filepath.Join(scDir, "device"),
		claimingSymlink: filepath.Join(scDir, "other-device"),
	}
}

func (env *recreateSymlinkTestEnv) createPreferredSymlink(t *testing.T) {
	t.Helper()
	if err := createSymlink(t, env.physicalDevice, env.preferredTarget); err != nil {
		t.Fatalf("failed to create preferred symlink: %v", err)
	}
}

func (env *recreateSymlinkTestEnv) createCurrentSymlink(t *testing.T) {
	t.Helper()
	if err := createSymlink(t, env.physicalDevice, env.currentTarget); err != nil {
		t.Fatalf("failed to create current symlink: %v", err)
	}
}

func (env *recreateSymlinkTestEnv) createClaimingSymlink(t *testing.T) {
	t.Helper()
	if err := createSymlink(t, env.physicalDevice, env.claimingSymlink); err != nil {
		t.Fatalf("failed to create claiming symlink: %v", err)
	}
}

func (env *recreateSymlinkTestEnv) createExistingSymlink(t *testing.T, target string) {
	t.Helper()
	if err := createSymlink(t, target, env.symLinkPath); err != nil {
		t.Fatalf("failed to create existing symlink: %v", err)
	}
}

func assertSingleConditionReason(t *testing.T, lvdl *v1.LocalVolumeDeviceLink, reason string) {
	t.Helper()
	if !assert.Len(t, lvdl.Status.Conditions, 1) {
		return
	}
	assert.Equal(t, DeviceSymlinkErrorType, lvdl.Status.Conditions[0].Type)
	assert.Equal(t, operatorv1.ConditionTrue, lvdl.Status.Conditions[0].Status)
	assert.Equal(t, reason, lvdl.Status.Conditions[0].Reason)
}

// TestHasMismatchingSymlink verifies the HasMismatchingSymlink helper for various policies and states.
func TestHasMismatchingSymlink(t *testing.T) {
	testCases := []struct {
		name        string
		lvdl        *v1.LocalVolumeDeviceLink
		blockDevice internal.BlockDevice
		expected    bool
	}{
		{
			name:        "nil lvdl",
			lvdl:        nil,
			blockDevice: internal.BlockDevice{PathByID: "/preferred"},
			expected:    false,
		},
		{
			name:        "policy None",
			lvdl:        newLVDLWithPolicy("pv", "ns", v1.DeviceLinkPolicyNone, "/current", "/preferred"),
			blockDevice: internal.BlockDevice{PathByID: "/preferred"},
			expected:    false,
		},
		{
			name:        "policy CurrentLinkTarget",
			lvdl:        newLVDLWithPolicy("pv", "ns", v1.DeviceLinkPolicyCurrentLinkTarget, "/current", "/preferred"),
			blockDevice: internal.BlockDevice{PathByID: "/preferred"},
			expected:    false,
		},
		{
			name:        "policy PreferredLinkTarget with empty preferred from device",
			lvdl:        newLVDLWithPolicy("pv", "ns", v1.DeviceLinkPolicyPreferredLinkTarget, "/current", ""),
			blockDevice: internal.BlockDevice{},
			expected:    false,
		},
		{
			name:        "policy PreferredLinkTarget with matching targets",
			lvdl:        newLVDLWithPolicy("pv", "ns", v1.DeviceLinkPolicyPreferredLinkTarget, "/same", "/same"),
			blockDevice: internal.BlockDevice{PathByID: "/same"},
			expected:    false,
		},
		{
			name:        "policy PreferredLinkTarget with mismatching targets",
			lvdl:        newLVDLWithPolicy("pv", "ns", v1.DeviceLinkPolicyPreferredLinkTarget, "/current", "/dev/disk/by-id/preferred"),
			blockDevice: internal.BlockDevice{KName: "sda", PathByID: "/dev/disk/by-id/preferred"},
			expected:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			saveAndRestoreGlobals(t)
			internal.FilePathEvalSymLinks = func(path string) (string, error) {
				return "/dev/sda", nil
			}
			assert.Equal(t, tc.expected, HasMismatchingSymlink(tc.lvdl, tc.blockDevice))
		})
	}
}

func TestRecreateSymlinkIfNeeded(t *testing.T) {
	type recreateTestCase struct {
		name              string
		pvName            string
		blockDevice       func(env *recreateSymlinkTestEnv) internal.BlockDevice
		initialConditions func(env *recreateSymlinkTestEnv) []operatorv1.OperatorCondition
		setup             func(t *testing.T, env *recreateSymlinkTestEnv)
		configureEval     func(env *recreateSymlinkTestEnv) func(string) (string, error)
		configureGlob     func(env *recreateSymlinkTestEnv) func(string) ([]string, error)
		expectedReason    string
		// checkIdempotency, when true, verifies that LastTransitionTime was preserved
		checkIdempotency bool
		// expectCachePopulated, when true, verifies that the cache was populated
		// after the error condition was set. False when the device has no valid
		// by-id symlinks or when the status is unchanged (idempotent).
		expectCachePopulated bool
	}

	testCases := []recreateTestCase{
		{
			name:   "preferred target not found",
			pvName: "local-pv-preferred-not-found",
			blockDevice: func(env *recreateSymlinkTestEnv) internal.BlockDevice {
				return internal.BlockDevice{KName: "sda"}
			},
			setup: func(t *testing.T, env *recreateSymlinkTestEnv) {},
			configureGlob: func(env *recreateSymlinkTestEnv) func(string) ([]string, error) {
				return func(pattern string) ([]string, error) { return nil, nil }
			},
			expectedReason: "PreferredTargetNotFound",
		},
		{
			name:   "relinks when current target resolves differently",
			pvName: "local-pv-device-mismatch",
			blockDevice: func(env *recreateSymlinkTestEnv) internal.BlockDevice {
				// KName matches what preferredTarget resolves to, so GetPathByID succeeds.
				return internal.BlockDevice{KName: "sdb"}
			},
			setup: func(t *testing.T, env *recreateSymlinkTestEnv) {
				env.createPreferredSymlink(t)
			},
			configureEval: func(env *recreateSymlinkTestEnv) func(string) (string, error) {
				return func(path string) (string, error) {
					switch path {
					case env.preferredTarget:
						return "/dev/sdb", nil
					case env.currentTarget:
						return "/dev/sda", nil
					default:
						return path, nil
					}
				}
			},
			configureGlob: func(env *recreateSymlinkTestEnv) func(string) ([]string, error) {
				return filePathGlobWithPreferred(env.preferredTarget)
			},
		},
		{
			name:   "target already claimed",
			pvName: "local-pv-already-claimed",
			blockDevice: func(env *recreateSymlinkTestEnv) internal.BlockDevice {
				return internal.BlockDevice{KName: "sdb"}
			},
			setup: func(t *testing.T, env *recreateSymlinkTestEnv) {
				env.createPreferredSymlink(t)
				env.createCurrentSymlink(t)
				env.createClaimingSymlink(t)
			},
			configureEval: func(env *recreateSymlinkTestEnv) func(string) (string, error) {
				return func(path string) (string, error) {
					switch path {
					case env.preferredTarget, env.currentTarget, env.claimingSymlink:
						return env.physicalDevice, nil
					default:
						return path, nil
					}
				}
			},
			configureGlob: func(env *recreateSymlinkTestEnv) func(string) ([]string, error) {
				return filePathGlobWithPreferred(env.preferredTarget)
			},
			expectedReason:       "TargetAlreadyClaimed",
			expectCachePopulated: true,
		},
		{
			name:   "atomic swap success",
			pvName: "local-pv-swap-success",
			blockDevice: func(env *recreateSymlinkTestEnv) internal.BlockDevice {
				return internal.BlockDevice{KName: "sdb"}
			},
			initialConditions: func(env *recreateSymlinkTestEnv) []operatorv1.OperatorCondition {
				return []operatorv1.OperatorCondition{
					{
						Type:    DeviceSymlinkErrorType,
						Status:  operatorv1.ConditionTrue,
						Reason:  "PreferredTargetNotFound",
						Message: "stale error",
					},
				}
			},
			setup: func(t *testing.T, env *recreateSymlinkTestEnv) {
				env.createPreferredSymlink(t)
				env.createCurrentSymlink(t)
				env.createExistingSymlink(t, env.currentTarget)
			},
			configureEval: func(env *recreateSymlinkTestEnv) func(string) (string, error) {
				return func(path string) (string, error) {
					switch path {
					case env.preferredTarget, env.currentTarget:
						return env.physicalDevice, nil
					default:
						return path, nil
					}
				}
			},
			configureGlob: func(env *recreateSymlinkTestEnv) func(string) ([]string, error) {
				return filePathGlobWithPreferred(env.preferredTarget)
			},
		},
		{
			name:   "idempotent condition preserves LastTransitionTime",
			pvName: "local-pv-idempotent",
			blockDevice: func(env *recreateSymlinkTestEnv) internal.BlockDevice {
				// No PathByID and KName won't match any glob result →
				// GetPathByID returns internal.IDPathNotFoundError → PreferredTargetNotFound.
				return internal.BlockDevice{KName: "sda"}
			},
			setup: func(t *testing.T, env *recreateSymlinkTestEnv) {},
			configureGlob: func(env *recreateSymlinkTestEnv) func(string) ([]string, error) {
				return func(pattern string) ([]string, error) { return nil, nil }
			},
			// Pre-seed the exact condition that RecreateSymlinkIfNeeded would set.
			// setLVDLCondition should detect same reason/status/message and skip the update,
			// so the original LastTransitionTime is preserved.
			initialConditions: func(env *recreateSymlinkTestEnv) []operatorv1.OperatorCondition {
				idErr := internal.IDPathNotFoundError{DeviceName: "sda"}
				return []operatorv1.OperatorCondition{
					{
						Type:               DeviceSymlinkErrorType,
						Status:             operatorv1.ConditionTrue,
						Reason:             "PreferredTargetNotFound",
						Message:            fmt.Sprintf("couldn't find preferredLinkTarget for device  with currentLink %s: %v", env.currentTarget, idErr),
						LastTransitionTime: metav1.NewTime(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)),
					},
				}
			},
			expectedReason:   "PreferredTargetNotFound",
			checkIdempotency: true,
		},
		{
			name:   "current target gone",
			pvName: "local-pv-current-gone",
			blockDevice: func(env *recreateSymlinkTestEnv) internal.BlockDevice {
				return internal.BlockDevice{KName: "sdb"}
			},
			setup: func(t *testing.T, env *recreateSymlinkTestEnv) {
				env.currentTarget = filepath.Join(env.tmpDir, "by-id-current-gone")
				env.createPreferredSymlink(t)
				env.createExistingSymlink(t, env.currentTarget)
			},
			configureEval: func(env *recreateSymlinkTestEnv) func(string) (string, error) {
				return func(path string) (string, error) {
					if path == env.preferredTarget {
						return env.physicalDevice, nil
					}
					return "", fmt.Errorf("no such file or directory: %s", path)
				}
			},
			configureGlob: func(env *recreateSymlinkTestEnv) func(string) ([]string, error) {
				return filePathGlobWithPreferred(env.preferredTarget)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			saveAndRestoreGlobals(t)

			env := newRecreateSymlinkTestEnv(t)
			tc.setup(t, env)

			lvdl := newLVDLWithPolicy(tc.pvName, "default", v1.DeviceLinkPolicyPreferredLinkTarget, env.currentTarget, env.preferredTarget)
			if tc.initialConditions != nil {
				lvdl.Status.Conditions = tc.initialConditions(env)
			}

			fakeClient := newFakeDeviceLinkClient(t, lvdl).Build()
			pvCache := NewLocalVolumeDeviceLinkCache(nil, nil, testNodeName)
			pvCache.MarkSyncedForTests()
			handler := NewDeviceLinkHandler(fakeClient, fakeClient, record.NewFakeRecorder(10), pvCache, testNodeName)

			if tc.configureEval != nil {
				internal.FilePathEvalSymLinks = tc.configureEval(env)
			}
			if tc.configureGlob != nil {
				internal.FilePathGlob = tc.configureGlob(env)
			}
			internal.CmdExecutor = fakeBlkidEmptyExecutor()
			if tc.checkIdempotency {
				// Freeze TimeNow so getCondition produces a known timestamp
				// that differs from the pre-seeded one.
				frozenTime := metav1.NewTime(time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC))
				timeNow = func() metav1.Time { return frozenTime }
			}

			blockDevice := tc.blockDevice(env)
			result, err := handler.RecreateSymlinkIfNeeded(t.Context(), lvdl, env.symLinkPath, blockDevice)
			assert.NoError(t, err)

			fetched := &v1.LocalVolumeDeviceLink{}
			assert.NoError(t, fakeClient.Get(t.Context(), types.NamespacedName{Name: tc.pvName, Namespace: "default"}, fetched))

			if tc.expectedReason != "" {
				assertSingleConditionReason(t, result, tc.expectedReason)
				assertSingleConditionReason(t, fetched, tc.expectedReason)
				if tc.checkIdempotency {
					// setLVDLCondition should have preserved the original LastTransitionTime
					// (2020-01-01) instead of using the frozen TimeNow (2099-01-01).
					expected := tc.initialConditions(env)[0].LastTransitionTime.UTC()
					actual := fetched.Status.Conditions[0].LastTransitionTime.UTC()
					assert.Equal(t, expected, actual,
						"LastTransitionTime should be preserved when condition is unchanged")
				}
				// Cache is only populated when the device has valid by-id
				// symlinks that can be used as index keys, and when the
				// status actually changed (non-idempotent).
				if tc.expectCachePopulated {
					assert.NotEmpty(t, pvCache.localDeviceInfos, "cache should be updated when error condition is set")
				}
				return
			}

			assert.Empty(t, result.Status.Conditions)
			assert.Empty(t, fetched.Status.Conditions)

			target, readlinkErr := readlink(env.symLinkPath)
			assert.NoError(t, readlinkErr)
			assert.Equal(t, env.preferredTarget, target)

			// Verify write-through cache was updated after successful symlink recreation.
			assert.NotEmpty(t, pvCache.localDeviceInfos, "cache should be updated after successful symlink recreation")
			// Find the LVDL in the cache by checking its preferred target entry.
			info, found, err := pvCache.FindStalePVs(env.preferredTarget, blockDevice)
			if assert.NoError(t, err) && assert.True(t, found, "cache should contain entry for preferred target") {
				cached := info.lvdls[tc.pvName]
				if assert.NotNil(t, cached, "cache entry should contain LVDL %s", tc.pvName) {
					assert.Equal(t, env.preferredTarget, cached.Status.CurrentLinkTarget)
				}
			}
		})
	}
}
