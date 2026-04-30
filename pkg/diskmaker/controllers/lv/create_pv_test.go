package lv

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	localv1 "github.com/openshift/local-storage-operator/api/v1"
	"github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/diskmaker/diskmakertest"
	"github.com/openshift/local-storage-operator/pkg/internal"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
	provUtil "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/util"
)

func TestCreatePV(t *testing.T) {
	reclaimPolicyDelete := corev1.PersistentVolumeReclaimDelete
	testTable := []struct {
		desc      string
		shouldErr bool
		lv        localv1.LocalVolume
		node      corev1.Node
		sc        storagev1.StorageClass
		// device stuff
		symlinkpath     string
		actualVolMode   string
		desiredVolMode  string
		deviceName      string
		deviceCapacity  int64
		mountPoints     sets.Set[string]
		extraDirEntries []*provUtil.FakeDirEntry
	}{
		{
			desc: "basic creation: block on block",
			lv: localv1.LocalVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "lv-a",
					// Namespace: "a",
				},
			},
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "nodename-a",
					Labels: map[string]string{corev1.LabelHostname: "node-hostname-a"},
				},
			},
			sc: storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "storageclass-a",
				},
				ReclaimPolicy: &reclaimPolicyDelete,
			},
			actualVolMode:  string(localv1.PersistentVolumeBlock),
			desiredVolMode: string(localv1.PersistentVolumeBlock),
			mountPoints:    sets.New[string](),
			symlinkpath:    "/mnt/local-storage/storageclass-a/device-a",
			deviceCapacity: 10 * common.GiB,
			deviceName:     "device-a",
		},
		{
			desc:      "basic creation: block on fs",
			shouldErr: true,
			lv: localv1.LocalVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "lv-a",
					// Namespace: "a",
				},
			},
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "nodename-a",
					Labels: map[string]string{corev1.LabelHostname: "node-hostname-a"},
				},
			},
			sc: storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "storageclass-a",
				},
				ReclaimPolicy: &reclaimPolicyDelete,
			},
			actualVolMode:  string(localv1.PersistentVolumeFilesystem),
			desiredVolMode: string(localv1.PersistentVolumeBlock),
			mountPoints:    sets.New[string](),
			symlinkpath:    "/mnt/local-storage/storageclass-a/device-a",
			deviceCapacity: 10 * common.GiB,
			deviceName:     "device-a",
		},
		{
			desc: "basic creation: fs on block",
			lv: localv1.LocalVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "lv-a",
					// Namespace: "a",
				},
			},
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "nodename-a",
					Labels: map[string]string{corev1.LabelHostname: "node-hostname-a"},
				},
			},
			sc: storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "storageclass-a",
				},
				ReclaimPolicy: &reclaimPolicyDelete,
			},
			actualVolMode:  string(localv1.PersistentVolumeBlock),
			desiredVolMode: string(localv1.PersistentVolumeFilesystem),
			mountPoints:    sets.New[string](),
			symlinkpath:    "/mnt/local-storage/storageclass-a/device-a",
			deviceCapacity: 10 * common.GiB,
			deviceName:     "device-a",
		},
		{
			desc: "basic creation: fs",
			lv: localv1.LocalVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "lv-a",
					// Namespace: "a",
				},
			},
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "nodename-a",
					Labels: map[string]string{corev1.LabelHostname: "node-hostname-a"},
				},
			},
			sc: storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "storageclass-a",
				},
				ReclaimPolicy: &reclaimPolicyDelete,
			},
			actualVolMode:  string(localv1.PersistentVolumeFilesystem),
			desiredVolMode: string(localv1.PersistentVolumeFilesystem),
			mountPoints:    sets.New[string]("/mnt/local-storage/storageclass-a/device-a"),
			symlinkpath:    "/mnt/local-storage/storageclass-a/device-a",
			deviceCapacity: 10 * common.GiB,
			deviceName:     "device-a",
		},
		{
			desc:      "actual volume mode is fs, but is not mountpoint",
			shouldErr: true,
			lv: localv1.LocalVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "lv-b",
					// Namespace: "a",
				},
			},
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "nodename-b",
					Labels: map[string]string{corev1.LabelHostname: "node-hostname-b"},
				},
			},
			sc: storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "storageclass-b",
				},
				ReclaimPolicy: &reclaimPolicyDelete,
			},
			actualVolMode:  string(localv1.PersistentVolumeFilesystem),
			desiredVolMode: string(localv1.PersistentVolumeFilesystem),
			mountPoints:    sets.New[string]("a", "b"), // device not present
			symlinkpath:    "/mnt/local-storage/storageclass-b/device-b",
			deviceCapacity: 10 * common.GiB,
			deviceName:     "device-b",
		},
	}
	// iterate through testcases
	for i, tc := range testTable {
		t.Run(tc.desc, func(t *testing.T) {
			t.Logf("Test Case #%d: %q", i, tc.desc)

			// fake setup
			if tc.lv.Namespace == "" {
				tc.lv.Namespace = "default"
			}
			tc.lv.Kind = localv1.LocalVolumeKind
			r, testConfig := getFakeDiskMaker(t, "/mnt/local-storage", &tc.lv, &tc.node, &tc.sc)
			testConfig.runtimeConfig.Node = &tc.node
			testConfig.runtimeConfig.Name = common.GetProvisionedByValue(tc.node)
			testConfig.runtimeConfig.DiscoveryMap[tc.sc.Name] = provCommon.MountConfig{VolumeMode: tc.desiredVolMode}

			fakeMap := map[string]string{
				string(corev1.PersistentVolumeFilesystem): provUtil.FakeEntryFile,
				string(corev1.PersistentVolumeBlock):      provUtil.FakeEntryBlock,
			}
			if len(tc.extraDirEntries) == 0 {
				tc.extraDirEntries = make([]*provUtil.FakeDirEntry, 0)
			}

			tc.extraDirEntries = append(tc.extraDirEntries, &provUtil.FakeDirEntry{
				Name:       tc.deviceName,
				Capacity:   tc.deviceCapacity,
				VolumeType: fakeMap[tc.actualVolMode],
			})
			dirFiles := map[string][]*provUtil.FakeDirEntry{
				tc.sc.Name: tc.extraDirEntries,
			}
			testConfig.fakeVolUtil.AddNewDirEntries("/mnt/local-storage/", dirFiles)
			oldReadLink := internal.Readlink
			defer func() {
				internal.Readlink = oldReadLink
			}()
			internal.Readlink = func(symlinkPath string) (string, error) {
				return "/dev/disk/by-id/wwn-null", nil
			}

			err := common.SyncLVAndLVDL(t.Context(), common.SyncLVAndLVDLArgs{
				LocalVolumeLikeObject: &tc.lv,
				RuntimeConfig:         r.runtimeConfig,
				StorageClass:          tc.sc,
				MountPointMap:         tc.mountPoints,
				Client:                r.Client,
				ClientReader:          r.ClientReader,
				SymLinkPath:           tc.symlinkpath,
				BlockDevice:           internal.BlockDevice{KName: filepath.Base(tc.deviceName)},
				CacheWriter:           r.pvLinkCache,
				ExtraLabelsForPV:      map[string]string{},
			})
			if tc.shouldErr {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}

			if tc.shouldErr {
				return
			}
			pv := &corev1.PersistentVolume{}
			err = r.Client.Get(context.TODO(), types.NamespacedName{Name: common.GeneratePVName(filepath.Base(tc.symlinkpath), tc.node.GetName(), tc.sc.GetName())}, pv)

			// provisioned-by annotation accurate
			actualProvName, found := pv.ObjectMeta.Annotations[provCommon.AnnProvisionedBy]
			assert.True(t, found)
			assert.Equal(t, testConfig.runtimeConfig.Name, actualProvName)

			// capacity accurate
			pvCapacity, found := pv.Spec.Capacity["storage"]
			assert.True(t, found)
			expectedCapacity := resource.MustParse(fmt.Sprint(common.RoundDownCapacityPretty(tc.deviceCapacity)))

			assert.Truef(t, pvCapacity.Equal(expectedCapacity), "actual: %s,expected: %s", pvCapacity, expectedCapacity)

			// pvName accurate
			assert.Equal(t, common.GeneratePVName(filepath.Base(tc.symlinkpath), tc.node.Name, tc.sc.Name), pv.Name)

			// symlinkPath accurate
			assert.NotNil(t, pv.Spec.Local)
			assert.Equal(t, tc.symlinkpath, pv.Spec.Local.Path)

			// storageclass accurate
			assert.Equal(t, tc.sc.Name, pv.Spec.StorageClassName)

			// reclaimPolicy accurate,
			assert.Equal(t, *tc.sc.ReclaimPolicy, pv.Spec.PersistentVolumeReclaimPolicy)

			// test idempotency by running again
			err = common.SyncLVAndLVDL(t.Context(), common.SyncLVAndLVDLArgs{
				LocalVolumeLikeObject: &tc.lv,
				RuntimeConfig:         r.runtimeConfig,
				StorageClass:          tc.sc,
				MountPointMap:         tc.mountPoints,
				Client:                r.Client,
				ClientReader:          r.ClientReader,
				SymLinkPath:           tc.symlinkpath,
				BlockDevice:           internal.BlockDevice{KName: filepath.Base(tc.deviceName)},
				CacheWriter:           r.pvLinkCache,
				ExtraLabelsForPV:      map[string]string{},
			})
			assert.Nil(t, err)

		})
	}

}

// TestSyncLVAndLVDL_DeviceLinkArgOrder verifies that SyncLVAndLVDL passes KName
// to the by-id symlink matcher and DevicePath to blkid — and not the other way
// around.  A previous bug had these two arguments swapped in the
// ApplyStatus call inside SyncLVAndLVDL.
//
// The test uses deliberately distinct values for KName and DevicePath so that
// swapping them would produce wrong results:
//
//   - FilePathGlob / FilePathEvalSymLinks are mocked to recognise only KName
//     when resolving by-id links → ValidLinkTargets must be non-empty.
//   - ExecCommand (blkid) is mocked to return a UUID only when its last
//     argument equals DevicePath → FilesystemUUID must equal fakeUUID.
//
// If the arguments were swapped:
//   - blkid would receive KName ("sda") instead of DevicePath ("/dev/sda")
//     and would produce an empty UUID, causing the FilesystemUUID assertion
//     to fail.
//   - The symlink matcher would receive DevicePath and filepath.Base of that
//     would still be "sda", so to make the test more robust the mock checks
//     the exact string passed to FilePathEvalSymLinks as its target.
func TestSyncLVAndLVDL_DeviceLinkArgOrder(t *testing.T) {
	reclaimPolicyDelete := corev1.PersistentVolumeReclaimDelete
	kName := "sda"
	devPath := "/dev/" + kName // deliberately different from kName
	fakeUUID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	fakeByIDLink := "/dev/disk/by-id/wwn-0x" + kName

	lv := localv1.LocalVolume{
		TypeMeta: metav1.TypeMeta{Kind: localv1.LocalVolumeKind},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lv-argorder",
			Namespace: "openshift-local-storage",
		},
	}
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-argorder",
			Labels: map[string]string{corev1.LabelHostname: "node-hostname-argorder"},
		},
	}
	sc := storagev1.StorageClass{
		ObjectMeta:    metav1.ObjectMeta{Name: "sc-argorder"},
		ReclaimPolicy: &reclaimPolicyDelete,
	}

	symLinkPath := "/mnt/local-storage/" + sc.Name + "/" + kName
	pvName := common.GeneratePVName(filepath.Base(symLinkPath), node.Name, sc.Name)

	r, testConfig := getFakeDiskMaker(t, "/mnt/local-storage", &lv, &node, &sc)
	testConfig.runtimeConfig.Node = &node
	testConfig.runtimeConfig.Name = common.GetProvisionedByValue(node)
	testConfig.runtimeConfig.Namespace = lv.Namespace
	testConfig.runtimeConfig.DiscoveryMap[sc.Name] = provCommon.MountConfig{
		VolumeMode: string(localv1.PersistentVolumeBlock),
	}
	testConfig.fakeVolUtil.AddNewDirEntries("/mnt/local-storage/", map[string][]*provUtil.FakeDirEntry{
		sc.Name: {
			{Name: kName, Capacity: 10 * common.GiB, VolumeType: provUtil.FakeEntryBlock},
		},
	})

	var resolvedTarget string
	diskmakertest.WithInternalMocks(t, func() {
		internal.Readlink = func(symlinkPath string) (string, error) {
			return "/dev/disk/by-id/wwn-null", nil
		}
		internal.FilePathGlob = func(pattern string) ([]string, error) {
			return []string{fakeByIDLink}, nil
		}
		internal.FilePathEvalSymLinks = func(path string) (string, error) {
			resolvedTarget = path
			return "/dev/" + kName, nil
		}
		internal.CmdExecutor = diskmakertest.BlkidForDevicePathFakeExec(devPath, fakeUUID)
	})

	err := common.SyncLVAndLVDL(t.Context(), common.SyncLVAndLVDLArgs{
		LocalVolumeLikeObject: &lv,
		RuntimeConfig:         r.runtimeConfig,
		StorageClass:          sc,
		MountPointMap:         sets.New[string](),
		Client:                r.Client,
		ClientReader:          r.ClientReader,
		SymLinkPath:           symLinkPath,
		BlockDevice:           internal.BlockDevice{KName: kName, PathByID: fakeByIDLink},
		CacheWriter:           r.pvLinkCache,
		ExtraLabelsForPV:      map[string]string{},
	})
	assert.NoError(t, err)

	// Fetch the LocalVolumeDeviceLink that SyncLVAndLVDL must have created.
	lvdl := &localv1.LocalVolumeDeviceLink{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: pvName, Namespace: lv.Namespace}, lvdl)
	assert.NoError(t, err, "LVDL should have been created by SyncLVAndLVDL")
	if assert.Len(t, lvdl.OwnerReferences, 1, "LVDL should carry owner reference to LocalVolume") {
		assert.Equal(t, localv1.GroupVersion.String(), lvdl.OwnerReferences[0].APIVersion)
		assert.Equal(t, localv1.LocalVolumeKind, lvdl.OwnerReferences[0].Kind)
		assert.Equal(t, lv.Name, lvdl.OwnerReferences[0].Name)
	}

	// ValidLinkTargets must be populated — proving KName was used for the
	// by-id glob matching (not DevicePath).
	assert.NotEmpty(t, lvdl.Status.ValidLinkTargets,
		"ValidLinkTargets should be populated; KName must have been passed to the symlink matcher")
	assert.Contains(t, lvdl.Status.ValidLinkTargets, fakeByIDLink)

	// FilesystemUUID must equal fakeUUID — proving DevicePath was passed to
	// blkid (not KName).  If the args were swapped, blkid would receive "sda"
	// instead of "/dev/sda" and would produce an empty string.
	assert.Equal(t, fakeUUID, lvdl.Status.FilesystemUUID,
		"FilesystemUUID should match fakeUUID; DevicePath must have been passed to blkid")

	// Sanity check: the symlink evaluator was called with the by-id link,
	// confirming the glob result was actually used.
	assert.Equal(t, fakeByIDLink, resolvedTarget,
		"FilePathEvalSymLinks should have been called with the by-id link")
}

func TestSyncLVAndLVDL_DeviceLinkLifecycle(t *testing.T) {
	reclaimPolicyDelete := corev1.PersistentVolumeReclaimDelete
	fakeByIDLink := "/dev/disk/by-id/wwn-null"

	testCases := []struct {
		name                string
		existingLVDLFactory func(pvName, namespace, currentTarget, preferredTarget string) *localv1.LocalVolumeDeviceLink
		triggerRelink       bool
	}{
		{
			name: "creates lvdl after creating pv when one does not exist",
		},
		{
			name: "updates existing lvdl status when symlink already matches policy",
			existingLVDLFactory: func(pvName, namespace, currentTarget, preferredTarget string) *localv1.LocalVolumeDeviceLink {
				return &localv1.LocalVolumeDeviceLink{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pvName,
						Namespace: namespace,
					},
					Spec: localv1.LocalVolumeDeviceLinkSpec{
						PersistentVolumeName: pvName,
						Policy:               localv1.DeviceLinkPolicyNone,
					},
				}
			},
		},
		{
			name: "recreates mismatching symlink before pv creation",
			existingLVDLFactory: func(pvName, namespace, currentTarget, preferredTarget string) *localv1.LocalVolumeDeviceLink {
				return &localv1.LocalVolumeDeviceLink{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pvName,
						Namespace: namespace,
					},
					Spec: localv1.LocalVolumeDeviceLinkSpec{
						PersistentVolumeName: pvName,
						Policy:               localv1.DeviceLinkPolicyPreferredLinkTarget,
					},
					Status: localv1.LocalVolumeDeviceLinkStatus{
						CurrentLinkTarget:   currentTarget,
						PreferredLinkTarget: preferredTarget,
					},
				}
			},
			triggerRelink: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := diskmakertest.TempDir(t, "create-local-pv-lifecycle-")

			lv := localv1.LocalVolume{
				TypeMeta: metav1.TypeMeta{
					Kind:       localv1.LocalVolumeKind,
					APIVersion: localv1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "lv-lifecycle",
					Namespace: "openshift-local-storage",
					UID:       "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				},
			}
			node := corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-lifecycle",
					Labels: map[string]string{corev1.LabelHostname: "node-hostname-lifecycle"},
				},
			}
			sc := storagev1.StorageClass{
				ObjectMeta:    metav1.ObjectMeta{Name: "sc-lifecycle"},
				ReclaimPolicy: &reclaimPolicyDelete,
			}

			symLinkPath := filepath.Join(tmpDir, sc.Name, "claimed")
			pvName := common.GeneratePVName(filepath.Base(symLinkPath), node.Name, sc.Name)

			currentTarget := filepath.Join(tmpDir, "current-target")
			preferredTarget := filepath.Join(tmpDir, "preferred-target")

			assert.NoError(t, os.MkdirAll(filepath.Dir(symLinkPath), 0755))
			assert.NoError(t, os.Symlink("/dev/null", currentTarget))
			assert.NoError(t, os.Symlink("/dev/null", preferredTarget))

			objs := []runtime.Object{&lv, &node, &sc}
			if tc.existingLVDLFactory != nil {
				objs = append(objs, tc.existingLVDLFactory(pvName, lv.Namespace, currentTarget, preferredTarget))
			}

			r, testConfig := getFakeDiskMaker(t, tmpDir, objs...)
			testConfig.runtimeConfig.Node = &node
			testConfig.runtimeConfig.Name = common.GetProvisionedByValue(node)
			testConfig.runtimeConfig.Namespace = lv.Namespace
			testConfig.runtimeConfig.DiscoveryMap[sc.Name] = provCommon.MountConfig{
				VolumeMode: string(localv1.PersistentVolumeBlock),
			}
			testConfig.fakeVolUtil.AddNewDirEntries(tmpDir, map[string][]*provUtil.FakeDirEntry{
				sc.Name: {
					{Name: filepath.Base(symLinkPath), Capacity: 10 * common.GiB, VolumeType: provUtil.FakeEntryBlock},
				},
			})

			var expectedPreferredSymlink string

			// triggerRelink case triggers change of symlink to new preferredLinkTarget.
			// RecreateSymlinkIfNeeded uses blockDevice.GetPathByID() which returns
			// fakeByIDLink, so both current and preferred become fakeByIDLink after relink.
			if tc.triggerRelink {
				assert.NoError(t, os.Symlink(currentTarget, symLinkPath))
				expectedPreferredSymlink = fakeByIDLink
			}

			// effectiveCurrentSource is always resolved via internal.Readlink inside
			// SyncLVAndLVDL, which the stub returns as fakeByIDLink.
			expectedCurrentSymlink := fakeByIDLink

			diskmakertest.WithInternalMocks(t, func() {
				internal.Readlink = func(symlinkPath string) (string, error) {
					return "/dev/disk/by-id/wwn-null", nil
				}
				internal.FilePathGlob = func(pattern string) ([]string, error) {
					if pattern == filepath.Join(internal.DiskByIDDir, "*") {
						return []string{fakeByIDLink}, nil
					}
					return filepath.Glob(pattern)
				}
				internal.FilePathEvalSymLinks = func(path string) (string, error) {
					if path == fakeByIDLink {
						return "/dev/null", nil
					}
					return filepath.EvalSymlinks(path)
				}
				internal.CmdExecutor = diskmakertest.BlkidForDevicePathFakeExec("/dev/null", "uuid-lifecycle")
			})

			err := common.SyncLVAndLVDL(t.Context(), common.SyncLVAndLVDLArgs{
				LocalVolumeLikeObject: &lv,
				RuntimeConfig:         r.runtimeConfig,
				StorageClass:          sc,
				MountPointMap:         sets.New[string](),
				Client:                r.Client,
				ClientReader:          r.ClientReader,
				SymLinkPath:           symLinkPath,
				BlockDevice: internal.BlockDevice{
					Name:     "null",
					KName:    "null",
					PathByID: fakeByIDLink,
				},
				CacheWriter:      r.pvLinkCache,
				ExtraLabelsForPV: map[string]string{},
			})
			assert.NoError(t, err)

			pv := &corev1.PersistentVolume{}
			assert.NoError(t, r.Client.Get(context.TODO(), types.NamespacedName{Name: pvName}, pv))

			lvdl := &localv1.LocalVolumeDeviceLink{}
			assert.NoError(t, r.Client.Get(context.TODO(), types.NamespacedName{Name: pvName, Namespace: lv.Namespace}, lvdl))
			assert.Equal(t, expectedCurrentSymlink, lvdl.Status.CurrentLinkTarget)
			assert.Equal(t, "uuid-lifecycle", lvdl.Status.FilesystemUUID)
			assert.Equal(t, fakeByIDLink, lvdl.Status.PreferredLinkTarget)
			if tc.existingLVDLFactory == nil && assert.Len(t, lvdl.OwnerReferences, 1) {
				assert.Equal(t, lv.Name, lvdl.OwnerReferences[0].Name)
			}

			if tc.triggerRelink {
				target, readErr := os.Readlink(symLinkPath)
				assert.NoError(t, readErr)
				assert.Equal(t, expectedPreferredSymlink, target)
			}
		})
	}
}
