package cache

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

func TestCurrentBlockDeviceInfoGetSymlinkTargetPath(t *testing.T) {
	const (
		symlinkDir = "/mnt/local-storage/sc-a"
		sourcePath = "/dev/disk/by-id/wwn-1234"
	)

	tests := []struct {
		name            string
		setup           func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string)
		symlinkEvalFunc func(path string) (string, error)
		wantPath        string
		wantErr         string
	}{
		{
			name: "errors when more than one LVDL maps source",
			setup: func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string) {
				lvdl1 := newLVDL("pv-a", "/tmp/current-a", v1api.DeviceLinkPolicyPreferredLinkTarget, sourcePath)
				lvdl2 := newLVDL("pv-b", "/tmp/current-b", v1api.DeviceLinkPolicyPreferredLinkTarget, sourcePath)
				c.addOrUpdateLVDL(lvdl1)
				c.addOrUpdateLVDL(lvdl2)
				info, found := c.GetCurrentDeviceInfo(sourcePath)
				assert.True(t, found)
				return info, sourcePath
			},
			wantErr: "more than one LocalVolumeDevicelink found",
		},
		{
			name: "errors when lvdl set is unexpectedly empty",
			setup: func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string) {
				lvdl := newLVDL("pv-empty", "/tmp/current-empty", v1api.DeviceLinkPolicyPreferredLinkTarget, sourcePath)
				c.addOrUpdateLVDL(lvdl)
				info, found := c.GetCurrentDeviceInfo(sourcePath)
				assert.True(t, found)
				c.removeLVDL(lvdl)
				return info, sourcePath
			},
			wantErr: "unexpected empty lvdl set",
		},
		{
			name: "errors when policy is not preferred link target",
			setup: func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string) {
				lvdl := newLVDL("pv-none", "/tmp/current-none", v1api.DeviceLinkPolicyNone, sourcePath)
				c.addOrUpdateLVDL(lvdl)
				info, found := c.GetCurrentDeviceInfo(sourcePath)
				assert.True(t, found)
				return info, sourcePath
			},
			wantErr: "found stale symlink link",
		},
		{
			name: "errors when current link still resolves",
			setup: func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string) {
				lvdl := newLVDL("pv-resolves", "/tmp/current-resolves", v1api.DeviceLinkPolicyPreferredLinkTarget, sourcePath)
				c.addOrUpdateLVDL(lvdl)
				info, found := c.GetCurrentDeviceInfo(sourcePath)
				assert.True(t, found)
				return info, sourcePath
			},
			symlinkEvalFunc: func(path string) (string, error) {
				if path == "/tmp/current-resolves" {
					return "/dev/sda", nil
				}
				return "", os.ErrNotExist
			},
			wantErr: "currentSymlink /tmp/current-resolves still resolves",
		},
		{
			name: "returns recomputed symlink path when preferred policy and unresolved current",
			setup: func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string) {
				lvdl := newLVDL("pv-success", "/dev/disk/by-id/yyy", v1api.DeviceLinkPolicyPreferredLinkTarget, sourcePath)
				c.addOrUpdateLVDL(lvdl)
				info, found := c.GetCurrentDeviceInfo(sourcePath)
				assert.True(t, found)
				return info, sourcePath
			},
			symlinkEvalFunc: func(path string) (string, error) {
				return "", os.ErrNotExist
			},
			// old behavior derived basename from currentLinkTarget ("yyy"), but we must derive
			// the final symlink name from the PV local path ("xxx").
			wantPath: filepath.Join(symlinkDir, "xxx"),
		},
		{
			name: "errors when source is not a valid link target",
			setup: func(t *testing.T, c *LocalVolumeDeviceLinkCache) (CurrentBlockDeviceInfo, string) {
				lvdl := newLVDL("pv-invalid-target", "/tmp/current-invalid", v1api.DeviceLinkPolicyPreferredLinkTarget, "/dev/disk/by-id/wwn-other")
				c.addOrUpdateLVDL(lvdl)
				info, found := c.GetCurrentDeviceInfo("/dev/disk/by-id/wwn-other")
				assert.True(t, found)
				return info, sourcePath
			},
			symlinkEvalFunc: func(path string) (string, error) {
				return "", os.ErrNotExist
			},
			wantErr: fmt.Sprintf("symlink source %s is not a valid symlink target", sourcePath),
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

			cache := NewLocalVolumeDeviceLinkCache(nil, nil)
			info, source := tc.setup(t, cache)
			fakeClient := fakeClientForInfo(t, info)

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

func newLVDL(name, current string, policy v1api.DeviceLinkPolicy, validTargets ...string) *v1api.LocalVolumeDeviceLink {
	return &v1api.LocalVolumeDeviceLink{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1api.LocalVolumeDeviceLinkSpec{
			PersistentVolumeName: name,
			Policy:               policy,
		},
		Status: v1api.LocalVolumeDeviceLinkStatus{
			CurrentLinkTarget: current,
			ValidLinkTargets:  validTargets,
		},
	}
}

func fakeClientForInfo(t *testing.T, info CurrentBlockDeviceInfo) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	assert.NoError(t, corev1.AddToScheme(scheme))

	objects := make([]client.Object, 0, len(info.lvdls))
	for name, lvdl := range info.lvdls {
		localPath := filepath.Join("/mnt/local-storage/sc-a", filepath.Base(lvdl.Status.CurrentLinkTarget))
		if name == "pv-success" {
			localPath = "/mnt/local-storage/sc-a/xxx"
		}
		objects = append(objects, &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					Local: &corev1.LocalVolumeSource{Path: localPath},
				},
			},
		})
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}
