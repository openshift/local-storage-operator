package common

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	v1api "github.com/openshift/local-storage-operator/api/v1"
	"github.com/openshift/local-storage-operator/pkg/internal"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	cacheLocalNode = "worker-a"
	cacheOtherNode = "worker-b"
)

func TestCurrentBlockDeviceInfoGetSymlinkTargetPath(t *testing.T) {
	const (
		symlinkDir = "/mnt/local-storage/sc-a"
		sourcePath = "/dev/disk/by-id/wwn-1234"
	)

	tests := []struct {
		name            string
		setup           func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string)
		symlinkEvalFunc func(path string) (string, error)
		apiObjects      []client.Object
		wantPath        string
		wantErr         string
	}{
		{
			name: "errors when more than one LVDL maps source",
			setup: func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string) {
				lvdl1 := newCacheLVDL("pv-a", cacheLocalNode, "/tmp/current-a", v1api.DeviceLinkPolicyPreferredLinkTarget, sourcePath)
				lvdl2 := newCacheLVDL("pv-b", cacheLocalNode, "/tmp/current-b", v1api.DeviceLinkPolicyPreferredLinkTarget, sourcePath)
				c.addOrUpdateLVDL(lvdl1)
				c.addOrUpdateLVDL(lvdl2)
				info := c.localDeviceInfos[sourcePath]
				return info, sourcePath
			},
			wantErr: "more than one LocalVolumeDevicelink found",
		},
		{
			name: "errors when lvdl set is unexpectedly empty",
			setup: func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string) {
				lvdl := newCacheLVDL("pv-empty", cacheLocalNode, "/tmp/current-empty", v1api.DeviceLinkPolicyPreferredLinkTarget, sourcePath)
				c.addOrUpdateLVDL(lvdl)
				info := c.localDeviceInfos[sourcePath]
				c.removeLVDL(lvdl)
				return info, sourcePath
			},
			wantErr: "unexpected empty lvdl set",
		},
		{
			name: "errors when policy is not preferred link target",
			setup: func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string) {
				lvdl := newCacheLVDL("pv-none", cacheLocalNode, "/tmp/current-none", v1api.DeviceLinkPolicyNone, sourcePath)
				c.addOrUpdateLVDL(lvdl)
				info := c.localDeviceInfos[sourcePath]
				return info, sourcePath
			},
			apiObjects: []client.Object{
				localPV("pv-none", "/mnt/local-storage/sc-a/current-none"),
			},
			wantErr: "found stale symlink link",
		},
		{
			name: "errors when current link still resolves",
			setup: func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string) {
				lvdl := newCacheLVDL("pv-resolves", cacheLocalNode, "/tmp/current-resolves", v1api.DeviceLinkPolicyPreferredLinkTarget, sourcePath)
				c.addOrUpdateLVDL(lvdl)
				info := c.localDeviceInfos[sourcePath]
				return info, sourcePath
			},
			symlinkEvalFunc: func(path string) (string, error) {
				if path == "/tmp/current-resolves" {
					return "/dev/sda", nil
				}
				return "", os.ErrNotExist
			},
			apiObjects: []client.Object{
				localPV("pv-resolves", "/mnt/local-storage/sc-a/current-resolves"),
			},
			wantErr: "currentSymlink /tmp/current-resolves still resolves",
		},
		{
			name: "returns recomputed symlink path when preferred policy and unresolved current",
			setup: func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string) {
				lvdl := newCacheLVDL("pv-success", cacheLocalNode, "/dev/disk/by-id/yyy", v1api.DeviceLinkPolicyPreferredLinkTarget, sourcePath)
				c.addOrUpdateLVDL(lvdl)
				info := c.localDeviceInfos[sourcePath]
				return info, sourcePath
			},
			symlinkEvalFunc: func(path string) (string, error) {
				return "", os.ErrNotExist
			},
			apiObjects: []client.Object{
				localPV("pv-success", "/mnt/local-storage/sc-a/xxx"),
			},
			// old behavior derived basename from currentLinkTarget ("yyy"), but we must derive
			// the final symlink name from the PV local path ("xxx").
			wantPath: filepath.Join(symlinkDir, "xxx"),
		},
		{
			name: "returns symlink path with warning when source is not in validLinkTargets",
			setup: func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string) {
				lvdl := newCacheLVDL("pv-invalid-target", cacheLocalNode, "/tmp/current-invalid", v1api.DeviceLinkPolicyPreferredLinkTarget, "/dev/disk/by-id/wwn-other")
				c.addOrUpdateLVDL(lvdl)
				return c.localDeviceInfos["/dev/disk/by-id/wwn-other"], sourcePath
			},
			symlinkEvalFunc: func(path string) (string, error) {
				return "", os.ErrNotExist
			},
			apiObjects: []client.Object{
				localPV("pv-invalid-target", "/mnt/local-storage/sc-a/current-invalid"),
			},
			wantPath: filepath.Join(symlinkDir, "current-invalid"),
		},
		{
			name: "uses symlinkPath from LVDL status when available",
			setup: func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string) {
				lvdl := newCacheLVDL("pv-status-symlink", cacheLocalNode, "/tmp/current-status", v1api.DeviceLinkPolicyPreferredLinkTarget, sourcePath)
				lvdl.Status.PersistentVolumeSymlinkPath = "/mnt/local-storage/sc-a/from-lvdl-status"
				c.addOrUpdateLVDL(lvdl)
				return c.localDeviceInfos[sourcePath], sourcePath
			},
			symlinkEvalFunc: func(path string) (string, error) {
				return "", os.ErrNotExist
			},
			// pvObjectsForInfo would imply basename "current-status"; status SymlinkPath basename wins in getLVDLAndSymlinkPath.
			wantPath: filepath.Join(symlinkDir, "from-lvdl-status"),
		},
		{
			name: "uses symlinkPath from PV when LVDL has none",
			setup: func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string) {
				lvdl := newCacheLVDL("pv-local", cacheLocalNode, "/tmp/current-status", v1api.DeviceLinkPolicyPreferredLinkTarget, sourcePath)
				lvdl.Status.PersistentVolumeSymlinkPath = ""
				c.addOrUpdateLVDL(lvdl)
				return c.localDeviceInfos[sourcePath], sourcePath
			},
			symlinkEvalFunc: func(path string) (string, error) {
				return "", os.ErrNotExist
			},
			apiObjects: []client.Object{
				&corev1.PersistentVolume{
					ObjectMeta: metav1.ObjectMeta{Name: "pv-local"},
					Spec: corev1.PersistentVolumeSpec{
						PersistentVolumeSource: corev1.PersistentVolumeSource{
							Local: &corev1.LocalVolumeSource{Path: "/mnt/local-storage/sc-a/from-pv-local"},
						},
					},
				},
			},
			wantPath: filepath.Join(symlinkDir, "from-pv-local"),
		},
		{
			name: "prefers symlinkPath from LVDL when both LVDL and PV have one",
			setup: func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string) {
				lvdl := newCacheLVDL("pv-local", cacheLocalNode, "/tmp/current-status", v1api.DeviceLinkPolicyPreferredLinkTarget, sourcePath)
				lvdl.Status.PersistentVolumeSymlinkPath = "/mnt/local-storage/sc-a/from-lvdl-status"
				c.addOrUpdateLVDL(lvdl)
				return c.localDeviceInfos[sourcePath], sourcePath
			},
			symlinkEvalFunc: func(path string) (string, error) {
				return "", os.ErrNotExist
			},
			apiObjects: []client.Object{
				localPV("pv-local", "/mnt/local-storage/sc-a/from-pv-local"),
			},
			wantPath: filepath.Join(symlinkDir, "from-lvdl-status"),
		},
		{
			name: "errors when LVDL symlinkPath is empty and associated PV is missing",
			setup: func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string) {
				lvdl := newCacheLVDL("pv-missing", cacheLocalNode, "/tmp/missing", v1api.DeviceLinkPolicyPreferredLinkTarget, sourcePath)
				lvdl.Status.PersistentVolumeSymlinkPath = ""
				c.addOrUpdateLVDL(lvdl)
				return c.localDeviceInfos[sourcePath], sourcePath
			},
			symlinkEvalFunc: func(path string) (string, error) {
				return "", os.ErrNotExist
			},
			wantErr: "error getting associated pv object pv-missing",
		},
		{
			name: "errors when LVDL symlinkPath is empty and PV has empty local path",
			setup: func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string) {
				lvdl := newCacheLVDL("pv-empty-local", cacheLocalNode, "/tmp/empty-local", v1api.DeviceLinkPolicyPreferredLinkTarget, sourcePath)
				lvdl.Status.PersistentVolumeSymlinkPath = ""
				c.addOrUpdateLVDL(lvdl)
				return c.localDeviceInfos[sourcePath], sourcePath
			},
			symlinkEvalFunc: func(path string) (string, error) {
				return "", os.ErrNotExist
			},
			apiObjects: []client.Object{
				localPV("pv-empty-local", ""),
			},
			wantErr: "pv pv-empty-local has empty local path",
		},
		{
			name: "errors when LVDL symlinkPath is empty and PV has no Local volume source",
			setup: func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string) {
				lvdl := newCacheLVDL("pv-csi", cacheLocalNode, "/tmp/nil-local", v1api.DeviceLinkPolicyPreferredLinkTarget, sourcePath)
				lvdl.Status.PersistentVolumeSymlinkPath = ""
				c.addOrUpdateLVDL(lvdl)
				return c.localDeviceInfos[sourcePath], sourcePath
			},
			symlinkEvalFunc: func(path string) (string, error) {
				return "", os.ErrNotExist
			},
			apiObjects: []client.Object{
				csiPV("pv-csi", "example.com/csi", "volume-handle"),
			},
			wantErr: "pv pv-csi has empty local path",
		},
		{
			name: "errors when LVDL symlinkPath is for a different storage class",
			setup: func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string) {
				lvdl := newCacheLVDL("pv-empty-local", cacheLocalNode, "/tmp/empty-local", v1api.DeviceLinkPolicyPreferredLinkTarget, sourcePath)
				lvdl.Status.PersistentVolumeSymlinkPath = "/mnt/local-storage/sc-b/from-lvdl-status"
				c.addOrUpdateLVDL(lvdl)
				return c.localDeviceInfos[sourcePath], sourcePath
			},
			symlinkEvalFunc: func(path string) (string, error) {
				return "", os.ErrNotExist
			},
			apiObjects: []client.Object{},
			wantErr:    "does not match expected symlink directory",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			origEval := internal.FilePathEvalSymLinks
			t.Cleanup(func() {
				internal.FilePathEvalSymLinks = origEval
			})

			if tc.symlinkEvalFunc != nil {
				internal.FilePathEvalSymLinks = tc.symlinkEvalFunc
			} else {
				internal.FilePathEvalSymLinks = func(path string) (string, error) {
					return "", os.ErrNotExist
				}
			}

			cache := NewLocalVolumeDeviceLinkCache(nil, nil, cacheLocalNode)
			info, source := tc.setup(t, cache)
			fakeClient := fakeClientForObjects(t, tc.apiObjects...)

			gotPath, err := info.GetSymlinkTargetPath(context.TODO(), symlinkDir, source, fakeClient)

			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				assert.Empty(t, gotPath)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tc.wantPath, gotPath)
		})
	}
}

func TestFindStalePVs(t *testing.T) {
	const (
		directSymlink  = "/dev/disk/by-id/wwn-direct"
		siblingSymlink = "/dev/disk/by-id/scsi-sibling"
		unknownSymlink = "/dev/disk/by-id/wwn-unknown"
	)

	tests := []struct {
		name          string
		localNodeName string
		// symlink is the primary key passed to FindStalePVs
		symlink string
		// seededLVDLs are pre-populated into the cache
		seededLVDLs []*v1api.LocalVolumeDeviceLink
		// filePathGlob mocks the /dev/disk/by-id/ listing
		filePathGlob func(pattern string) ([]string, error)
		// filePathEvalSymLinks mocks symlink resolution
		filePathEvalSymLinks func(path string) (string, error)
		// blockDevice is the device passed to FindStalePVs
		blockDevice internal.BlockDevice
		wantFound   bool
		wantErr     string
		// wantLVDLNames are the expected LVDL names in the result
		wantLVDLNames []string
	}{
		{
			name:    "direct match by symlink name",
			symlink: directSymlink,
			seededLVDLs: []*v1api.LocalVolumeDeviceLink{
				newCacheLVDL("pv-direct", cacheLocalNode, "/dev/disk/by-id/old-gone", v1api.DeviceLinkPolicyPreferredLinkTarget, directSymlink),
			},
			localNodeName: cacheLocalNode,
			blockDevice:   internal.BlockDevice{Name: "sda", KName: "sda"},
			// No glob/eval needed — direct match short-circuits
			wantFound:     true,
			wantLVDLNames: []string{"pv-direct"},
		},
		{
			name:    "fallback match via sibling by-id symlink",
			symlink: unknownSymlink,
			seededLVDLs: []*v1api.LocalVolumeDeviceLink{
				// LVDL was recorded with siblingSymlink, not unknownSymlink
				newCacheLVDL("pv-sibling", cacheLocalNode, "/dev/disk/by-id/old-gone", v1api.DeviceLinkPolicyPreferredLinkTarget, siblingSymlink),
			},
			localNodeName: cacheLocalNode,
			blockDevice:   internal.BlockDevice{Name: "sdb", KName: "sdb"},
			filePathGlob: func(pattern string) ([]string, error) {
				return []string{unknownSymlink, siblingSymlink}, nil
			},
			filePathEvalSymLinks: func(path string) (string, error) {
				// both symlinks resolve to the same device
				if path == unknownSymlink || path == siblingSymlink {
					return "/dev/sdb", nil
				}
				return "", os.ErrNotExist
			},
			wantFound:     true,
			wantLVDLNames: []string{"pv-sibling"},
		},
		{
			name:          "no match when device has no cached LVDL",
			symlink:       unknownSymlink,
			localNodeName: cacheLocalNode,
			blockDevice:   internal.BlockDevice{Name: "sdc", KName: "sdc"},
			filePathGlob: func(pattern string) ([]string, error) {
				return []string{unknownSymlink}, nil
			},
			filePathEvalSymLinks: func(path string) (string, error) {
				if path == unknownSymlink {
					return "/dev/sdc", nil
				}
				return "", os.ErrNotExist
			},
			wantFound: false,
		},
		{
			name:          "error when GetValidByIDSymlinks fails",
			symlink:       unknownSymlink,
			localNodeName: cacheLocalNode,
			blockDevice:   internal.BlockDevice{Name: "sdd", KName: "sdd"},
			filePathGlob: func(pattern string) ([]string, error) {
				return nil, fmt.Errorf("permission denied")
			},
			wantErr: "error listing valid symlinks",
		},
		{
			name:    "merges LVDLs from multiple sibling symlinks",
			symlink: unknownSymlink,
			seededLVDLs: []*v1api.LocalVolumeDeviceLink{
				newCacheLVDL("pv-link-a", cacheLocalNode, "/dev/disk/by-id/old-a", v1api.DeviceLinkPolicyPreferredLinkTarget, directSymlink),
				newCacheLVDL("pv-link-b", cacheLocalNode, "/dev/disk/by-id/old-b", v1api.DeviceLinkPolicyPreferredLinkTarget, siblingSymlink),
			},
			localNodeName: cacheLocalNode,
			blockDevice:   internal.BlockDevice{Name: "sde", KName: "sde"},
			filePathGlob: func(pattern string) ([]string, error) {
				return []string{directSymlink, siblingSymlink, unknownSymlink}, nil
			},
			filePathEvalSymLinks: func(path string) (string, error) {
				// all three symlinks resolve to the same device
				return "/dev/sde", nil
			},
			wantFound:     true,
			wantLVDLNames: []string{"pv-link-a", "pv-link-b"},
		},
		{
			name:          "ignores direct match from different node",
			symlink:       directSymlink,
			localNodeName: cacheLocalNode,
			seededLVDLs: []*v1api.LocalVolumeDeviceLink{
				newCacheLVDL("pv-remote", cacheOtherNode, "/dev/disk/by-id/old-gone", v1api.DeviceLinkPolicyPreferredLinkTarget, directSymlink),
			},
			blockDevice: internal.BlockDevice{Name: "sdf", KName: "sdf"},
			wantFound:   false,
		},
		{
			name:          "merges only local node lvdl entries when sibling targets overlap",
			symlink:       unknownSymlink,
			localNodeName: cacheLocalNode,
			seededLVDLs: []*v1api.LocalVolumeDeviceLink{
				newCacheLVDL("pv-local-a", cacheLocalNode, "/dev/disk/by-id/old-a", v1api.DeviceLinkPolicyPreferredLinkTarget, directSymlink),
				newCacheLVDL("pv-local-b", cacheLocalNode, "/dev/disk/by-id/old-b", v1api.DeviceLinkPolicyPreferredLinkTarget, siblingSymlink),
				newCacheLVDL("pv-remote", cacheOtherNode, "/dev/disk/by-id/old-remote", v1api.DeviceLinkPolicyPreferredLinkTarget, directSymlink, siblingSymlink),
			},
			blockDevice: internal.BlockDevice{Name: "sdg", KName: "sdg"},
			filePathGlob: func(pattern string) ([]string, error) {
				return []string{directSymlink, siblingSymlink, unknownSymlink}, nil
			},
			filePathEvalSymLinks: func(path string) (string, error) {
				return "/dev/sdg", nil
			},
			wantFound:     true,
			wantLVDLNames: []string{"pv-local-a", "pv-local-b"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			origGlob := internal.FilePathGlob
			origEval := internal.FilePathEvalSymLinks
			t.Cleanup(func() {
				internal.FilePathGlob = origGlob
				internal.FilePathEvalSymLinks = origEval
			})

			if tc.filePathGlob != nil {
				internal.FilePathGlob = tc.filePathGlob
			}
			if tc.filePathEvalSymLinks != nil {
				internal.FilePathEvalSymLinks = tc.filePathEvalSymLinks
			}

			c := NewLocalVolumeDeviceLinkCache(nil, nil, tc.localNodeName)
			for _, lvdl := range tc.seededLVDLs {
				c.addOrUpdateLVDL(lvdl)
			}

			info, found, err := c.FindStalePVs(tc.symlink, tc.blockDevice)

			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tc.wantFound, found, "found mismatch")

			if !tc.wantFound {
				assert.Empty(t, info.lvdls)
				return
			}

			gotNames := make([]string, 0, len(info.lvdls))
			for name := range info.lvdls {
				gotNames = append(gotNames, name)
			}
			assert.ElementsMatch(t, tc.wantLVDLNames, gotNames)
		})
	}
}

func TestAddOrUpdateLVDL_RemovesStaleTargets(t *testing.T) {
	c := NewLocalVolumeDeviceLinkCache(nil, nil, cacheLocalNode)

	// Seed with targets A and B.
	lvdl := newCacheLVDL("pv-1", cacheLocalNode, "/old", v1api.DeviceLinkPolicyPreferredLinkTarget, "/dev/disk/by-id/A", "/dev/disk/by-id/B")
	c.addOrUpdateLVDL(lvdl)

	// Verify both entries exist.
	_, okA := c.localDeviceInfos["/dev/disk/by-id/A"]
	_, okB := c.localDeviceInfos["/dev/disk/by-id/B"]
	assert.True(t, okA, "expected entry for target A")
	assert.True(t, okB, "expected entry for target B")

	// Update the same LVDL so ValidLinkTargets changes from [A, B] to [A, C].
	lvdlUpdated := newCacheLVDL("pv-1", cacheLocalNode, "/new", v1api.DeviceLinkPolicyPreferredLinkTarget, "/dev/disk/by-id/A", "/dev/disk/by-id/C")
	c.addOrUpdateLVDL(lvdlUpdated)

	// A should still exist, B should be gone, C should be added.
	_, okA = c.localDeviceInfos["/dev/disk/by-id/A"]
	_, okB = c.localDeviceInfos["/dev/disk/by-id/B"]
	_, okC := c.localDeviceInfos["/dev/disk/by-id/C"]
	assert.True(t, okA, "expected entry for target A to remain")
	assert.False(t, okB, "expected stale entry for target B to be removed")
	assert.True(t, okC, "expected entry for target C to be added")

	// Verify the LVDL pointer was updated in existing entries.
	assert.Equal(t, "/new", c.localDeviceInfos["/dev/disk/by-id/A"].lvdls["pv-1"].Status.CurrentLinkTarget)
}

func TestAddOrUpdateLVDL_IgnoresDifferentNodes(t *testing.T) {
	c := NewLocalVolumeDeviceLinkCache(nil, nil, cacheLocalNode)

	c.addOrUpdateLVDL(newCacheLVDL("pv-remote", cacheOtherNode, "/remote", v1api.DeviceLinkPolicyPreferredLinkTarget, "/dev/disk/by-id/remote"))
	assert.Empty(t, c.localDeviceInfos, "expected foreign-node LVDL to be ignored")

	c.addOrUpdateLVDL(newCacheLVDL("pv-local", cacheLocalNode, "/local", v1api.DeviceLinkPolicyPreferredLinkTarget, "/dev/disk/by-id/local"))
	assert.Contains(t, c.localDeviceInfos, "/dev/disk/by-id/local")
}

func newCacheLVDL(name, nodeName, current string, policy v1api.DeviceLinkPolicy, validTargets ...string) *v1api.LocalVolumeDeviceLink {
	return &v1api.LocalVolumeDeviceLink{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1api.LocalVolumeDeviceLinkSpec{
			PersistentVolumeName: name,
			NodeName:             nodeName,
			Policy:               policy,
		},
		Status: v1api.LocalVolumeDeviceLinkStatus{
			CurrentLinkTarget: current,
			ValidLinkTargets:  validTargets,
		},
	}
}

// fakeClientWithObjects builds a fake client from an explicit object list.
func fakeClientForObjects(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	assert.NoError(t, corev1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func localPV(name, localPath string) *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				Local: &corev1.LocalVolumeSource{Path: localPath},
			},
		},
	}
}

func csiPV(name, driver, handle string) *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{Driver: driver, VolumeHandle: handle},
			},
		},
	}
}
