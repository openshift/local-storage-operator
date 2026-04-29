package lvset

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/client-go/security/clientset/versioned/scheme"
	v1api "github.com/openshift/local-storage-operator/api/v1"
	v1alphav1api "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/diskmaker/diskmakertest"
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

	pvLinkCache := common.NewLocalVolumeDeviceLinkCache(fakeClient, nil, "test-node")
	pvLinkCache.MarkSyncedForTests()

	lvsReconciler := NewLocalVolumeSetReconciler(
		fakeClient,
		fakeClient,
		scheme,
		fakeClock,
		&provDeleter.CleanupStatusTracker{ProcTable: provDeleter.NewProcTable()},
		runtimeConfig,
		pvLinkCache,
	)

	return lvsReconciler, tc
}

func TestGetAlreadySymlinked(t *testing.T) {
	tmpDir := diskmakertest.TempDir(t, "already-symlinked-")

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
			tmpDir := diskmakertest.TempDir(t, "process-new-symlink-")

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
			r.pvLinkCache = common.NewLocalVolumeDeviceLinkCache(r.Client, nil, node.Name)
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

			diskmakertest.WithInternalMocks(t, func() {
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
				internal.CmdExecutor = diskmakertest.FindAndBlkidFakeExec("", "", nil)
			})

			result, err := r.processNewSymlink(
				t.Context(),
				lvset,
				device,
				[]internal.BlockDevice{device},
				*sc,
				sets.New[string](),
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

// TestProcessNewSymlink_SiblingFallback_LVSet mirrors the LocalVolume sibling-fallback
// scenario: stale dangling symlink and LVDL valid targets include a sibling by-id path.
// Exercises FindStalePVs → syncExistingPVAndLVDL → PVAndLVDLSyncer with a LocalVolumeSet owner.
func TestProcessNewSymlink_SiblingFallback_LVSet(t *testing.T) {
	testCases := []struct {
		name          string
		lvsetName     string
		policy        v1api.DeviceLinkPolicy
		expectUpdated bool
	}{
		{
			name:          "PreferredLinkTarget policy resolves sibling fallback",
			lvsetName:     "lvset-sibling-fallback",
			policy:        v1api.DeviceLinkPolicyPreferredLinkTarget,
			expectUpdated: true,
		},
		{
			name:          "policy None cannot fix stale symlink via sibling fallback",
			lvsetName:     "lvset-sibling-fallback-none",
			policy:        v1api.DeviceLinkPolicyNone,
			expectUpdated: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := diskmakertest.SetupSiblingFallback(t, diskmakertest.DefaultSiblingFallbackConfig())
			cfg := fixture.Config

			lvset := &v1alphav1api.LocalVolumeSet{
				TypeMeta: metav1.TypeMeta{
					Kind:       v1alphav1api.LocalVolumeSetKind,
					APIVersion: v1alphav1api.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      tc.lvsetName,
					Namespace: cfg.TestNamespace,
				},
				Spec: v1alphav1api.LocalVolumeSetSpec{
					StorageClassName: cfg.SCName,
				},
			}
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   cfg.TestNodeName,
					Labels: map[string]string{corev1.LabelHostname: cfg.TestNodeName},
				},
			}

			lvdl := fixture.LocalVolumeDeviceLink()
			lvdl.Spec.Policy = tc.policy
			lvdl.Spec.NodeName = node.Name

			objs := []runtime.Object{lvset, node, fixture.PersistentVolume(), lvdl, fixture.StorageClass()}
			r, testCtx := newFakeLocalVolumeSetReconciler(t, objs...)
			r.pvLinkCache = common.NewLocalVolumeDeviceLinkCache(r.Client, nil, node.Name)
			r.pvLinkCache.MarkSyncedForTests()
			r.pvLinkCache.SeedForTests(lvdl)
			r.nodeName = node.Name
			r.runtimeConfig.Node = node
			r.runtimeConfig.Name = common.GetProvisionedByValue(*node)
			r.runtimeConfig.Namespace = cfg.TestNamespace
			r.runtimeConfig.DiscoveryMap[cfg.SCName] = provCommon.MountConfig{
				VolumeMode: string(corev1.PersistentVolumeBlock),
			}

			fixture.AddFakeVolumeDirEntries(testCtx.fakeVolUtil)

			sc := fixture.StorageClass()
			device := internal.BlockDevice{Name: cfg.BlockDevKName, KName: cfg.BlockDevKName}

			result, err := r.processNewSymlink(
				context.Background(),
				lvset,
				device,
				[]internal.BlockDevice{device},
				*sc,
				sets.New[string](),
				fixture.SymLinkDir,
			)
			assert.NotNil(t, result)
			assert.NoError(t, err)

			gotLinkTarget, err := os.Readlink(fixture.SymlinkPath)
			assert.NoError(t, err)
			if !tc.expectUpdated {
				assert.Equal(t, cfg.OldByID, gotLinkTarget,
					"symlink should remain unchanged when sibling fallback is not allowed to repair it")

				gotLVDL := &v1api.LocalVolumeDeviceLink{}
				err = r.Client.Get(context.Background(), types.NamespacedName{Name: fixture.ExpectedPVName, Namespace: cfg.TestNamespace}, gotLVDL)
				assert.NoError(t, err)
				assert.Equal(t, cfg.NewByID, gotLVDL.Status.PreferredLinkTarget,
					"PreferredLinkTarget should be updated even when policy prevents symlink recreation")
				assert.ElementsMatch(t, []string{cfg.NewByID, cfg.SiblingByID}, gotLVDL.Status.ValidLinkTargets,
					"ValidLinkTargets should reflect current device state")
				return
			}

			assert.Equal(t, cfg.NewByID, gotLinkTarget,
				"symlink under local-storage should be updated to the current preferred by-id path")

			pv := &corev1.PersistentVolume{}
			err = r.Client.Get(context.Background(), types.NamespacedName{Name: fixture.ExpectedPVName}, pv)
			assert.NoError(t, err, "provisioned PV should exist after sibling fallback")
			assert.Equal(t, fixture.SymlinkPath, pv.Spec.Local.Path,
				"PV local path should be preserved from the original symlink name")
			assert.Equal(t, v1api.LocalVolumeSetKind, pv.Labels[common.PVOwnerKindLabel])

			gotLVDL := &v1api.LocalVolumeDeviceLink{}
			err = r.Client.Get(context.Background(), types.NamespacedName{Name: fixture.ExpectedPVName, Namespace: cfg.TestNamespace}, gotLVDL)
			assert.NoError(t, err)
			assert.Equal(t, cfg.NewByID, gotLVDL.Status.CurrentLinkTarget)
			assert.Equal(t, cfg.NewByID, gotLVDL.Status.PreferredLinkTarget)
			assert.ElementsMatch(t, []string{cfg.NewByID, cfg.SiblingByID}, gotLVDL.Status.ValidLinkTargets)
		})
	}
}

func TestSyncExistingPVAndLVDL(t *testing.T) {
	reclaimPolicyDelete := corev1.PersistentVolumeReclaimDelete

	testCases := []struct {
		name            string
		symlinkTarget   string
		expectDeviceID  bool
		removeSymlink   bool
		mockReadlink    bool
		expectCurrLink  string
		expectErrSubstr string
	}{
		{
			name:           "preserves by-id annotation when symlink points to by-id path",
			symlinkTarget:  "/dev/disk/by-id/wwn-null",
			expectDeviceID: true,
			expectCurrLink: "/dev/disk/by-id/wwn-null",
		},
		{
			name:           "does not set device-id annotation for plain device path",
			symlinkTarget:  "/dev/null",
			expectCurrLink: "/dev/null",
		},
		{
			name:            "returns error when symlink is missing",
			symlinkTarget:   "/dev/null",
			removeSymlink:   true,
			mockReadlink:    true,
			expectErrSubstr: "unable to resolve symlink",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := diskmakertest.TempDir(t, "existing-pv-")

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

			diskmakertest.WithInternalMocks(t, func() {
				internal.CmdExecutor = diskmakertest.FindAndBlkidFakeExec("", "", nil)
				if tc.mockReadlink {
					baseReadlink := internal.Readlink
					internal.Readlink = func(path string) (string, error) {
						if path == symlinkPath {
							if tc.removeSymlink {
								return "", os.ErrNotExist
							}
							return tc.symlinkTarget, nil
						}
						return baseReadlink(path)
					}
				}
			})

			err := r.syncExistingPVAndLVDL(
				t.Context(),
				lvset,
				internal.BlockDevice{Name: "null", KName: "null"},
				*sc,
				sets.New[string](),
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
			assert.Equal(t, tc.expectCurrLink, lvdl.Status.CurrentLinkTarget)
			assert.Equal(t, "", lvdl.Status.FilesystemUUID)
		})
	}
}

func TestProcessRejectedDevicesForDeviceLinks(t *testing.T) {
	testCases := []struct {
		name               string
		createSymlink      bool
		createClaimingLink bool
		danglingSymlink    bool
		seedCacheFromLVDL  bool
		initialPolicy      v1api.DeviceLinkPolicy
		initialCurrentLink string
		initialPreferLink  string
		expectCurrentLink  string
		expectPreferLink   string
		expectCondition    string
		expectSymlinkPath  string
	}{
		{
			name:              "updates LVDL current link when existing symlink resolves",
			createSymlink:     true,
			initialPolicy:     v1api.DeviceLinkPolicyNone,
			expectCurrentLink: "/dev/null",
			expectPreferLink:  "/dev/disk/by-id/wwn-null",
		},
		{
			name:             "skips devices without existing symlink",
			initialPolicy:    v1api.DeviceLinkPolicyNone,
			expectPreferLink: "",
		},
		{
			name:               "records TargetAlreadyClaimed when another symlink owns preferred target",
			createSymlink:      true,
			createClaimingLink: true,
			initialPolicy:      v1api.DeviceLinkPolicyPreferredLinkTarget,
			initialCurrentLink: "/dev/null",
			// stale preferred target should be replaced by resolved block-device target
			initialPreferLink: "/dev/disk/by-id/stale-preferred-target",
			expectCurrentLink: "/dev/null",
			expectPreferLink:  "/dev/disk/by-id/wwn-null",
			expectCondition:   "TargetAlreadyClaimed",
		},
		{
			name:               "relinks existing symlink to computed preferred target",
			createSymlink:      true,
			initialPolicy:      v1api.DeviceLinkPolicyPreferredLinkTarget,
			initialCurrentLink: "/dev/disk/by-id/legacy-null",
			initialPreferLink:  "/dev/disk/by-id/stale-preferred-target",
			expectCurrentLink:  "/dev/disk/by-id/wwn-null",
			expectPreferLink:   "/dev/disk/by-id/wwn-null",
			expectSymlinkPath:  "/dev/disk/by-id/wwn-null",
		},
		{
			name:               "recomputes symlink path for dangling link and relinks to updated pathByID",
			createSymlink:      true,
			danglingSymlink:    true,
			seedCacheFromLVDL:  true,
			initialPolicy:      v1api.DeviceLinkPolicyPreferredLinkTarget,
			initialCurrentLink: "claimed",
			initialPreferLink:  "/dev/disk/by-id/stale-preferred-target",
			expectCurrentLink:  "/dev/disk/by-id/wwn-null",
			expectPreferLink:   "/dev/disk/by-id/wwn-null",
			expectSymlinkPath:  "/dev/disk/by-id/wwn-null",
		},
		{
			name:               "updates PreferredLinkTarget even when policy is None",
			createSymlink:      false,
			seedCacheFromLVDL:  true,
			initialPolicy:      v1api.DeviceLinkPolicyNone,
			initialCurrentLink: "/dev/disk/by-id/old-link",
			initialPreferLink:  "/dev/disk/by-id/stale-preferred",
			expectCurrentLink:  "/dev/disk/by-id/old-link",
			expectPreferLink:   "/dev/disk/by-id/wwn-null",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := diskmakertest.TempDir(t, "rejected-lvset-")

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
					NodeName:             node.Name,
					Policy:               tc.initialPolicy,
				},
				Status: v1api.LocalVolumeDeviceLinkStatus{
					CurrentLinkTarget:   tc.initialCurrentLink,
					PreferredLinkTarget: tc.initialPreferLink,
				},
			}
			if tc.initialCurrentLink == "claimed" {
				lvdl.Status.CurrentLinkTarget = filepath.Join(tmpDir, lvset.Spec.StorageClassName, "claimed")
			}

			symLinkDir := filepath.Join(tmpDir, lvset.Spec.StorageClassName)
			assert.NoError(t, os.MkdirAll(symLinkDir, 0755))
			symlinkPath := filepath.Join(symLinkDir, "claimed")
			r, _ := newFakeLocalVolumeSetReconciler(t,
				lvset,
				node,
				&corev1.PersistentVolume{
					ObjectMeta: metav1.ObjectMeta{Name: pvName},
					Spec: corev1.PersistentVolumeSpec{
						PersistentVolumeSource: corev1.PersistentVolumeSource{
							Local: &corev1.LocalVolumeSource{Path: symlinkPath},
						},
					},
				},
				lvdl,
			)
			r.pvLinkCache = common.NewLocalVolumeDeviceLinkCache(r.Client, nil, node.Name)
			r.nodeName = node.Name
			r.runtimeConfig.Node = node
			r.runtimeConfig.Namespace = testNamespace

			if tc.createSymlink {
				target := "/dev/null"
				if tc.danglingSymlink {
					target = "/dev/disk/by-id/wwn-gone"
				}
				assert.NoError(t, os.Symlink(target, symlinkPath))
			}
			if tc.createClaimingLink {
				assert.NoError(t, os.Symlink("/dev/null", filepath.Join(symLinkDir, "zzz-already-claimed")))
			}
			if tc.seedCacheFromLVDL {
				lvdl.Status.ValidLinkTargets = []string{"/dev/disk/by-id/wwn-null"}
				r.pvLinkCache.SeedForTests(lvdl)
			}

			diskmakertest.WithInternalMocks(t, func() {
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
					if tc.danglingSymlink && path == symlinkPath {
						return "", os.ErrNotExist
					}
					return filepath.EvalSymlinks(path)
				}
				internal.CmdExecutor = diskmakertest.FindAndBlkidFakeExec("", "uuid-null", nil)
			})

			r.processRejectedDevicesForDeviceLinks(
				t.Context(),
				lvset,
				[]internal.BlockDevice{{Name: "null", KName: "null"}},
				symLinkDir,
				lvset.Spec.StorageClassName,
			)

			fetched := &v1api.LocalVolumeDeviceLink{}
			assert.NoError(t, r.Client.Get(t.Context(), types.NamespacedName{Name: pvName, Namespace: testNamespace}, fetched))

			assert.Equal(t, tc.expectCurrentLink, fetched.Status.CurrentLinkTarget)
			assert.Equal(t, tc.expectPreferLink, fetched.Status.PreferredLinkTarget)
			if tc.expectSymlinkPath != "" {
				currentTarget, err := os.Readlink(symlinkPath)
				assert.NoError(t, err)
				assert.Equal(t, tc.expectSymlinkPath, currentTarget)
			}

			if tc.expectCondition != "" {
				if assert.Len(t, fetched.Status.Conditions, 1) {
					assert.Equal(t, operatorv1.ConditionTrue, fetched.Status.Conditions[0].Status)
					assert.Equal(t, tc.expectCondition, fetched.Status.Conditions[0].Reason)
				}
				return
			}
			assert.Empty(t, fetched.Status.Conditions)
		})
	}
}
