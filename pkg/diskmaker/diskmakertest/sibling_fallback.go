// Package diskmakertest holds shared test fixtures for diskmaker controller tests.
package diskmakertest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	localv1 "github.com/openshift/local-storage-operator/api/v1"
	"github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/internal"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	provUtil "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/util"
)

// SiblingFallbackConfig configures the stale by-id / sibling LVDL scenario.
type SiblingFallbackConfig struct {
	TestNodeName    string
	TestNamespace   string
	SCName          string
	OldByID         string
	NewByID         string
	SiblingByID     string
	BlockDevKName   string
	SymlinkBaseName string
	BlkidUUID       string
	TempDirPrefix   string
}

// DefaultSiblingFallbackConfig returns the standard values used across LV and LVSet tests.
func DefaultSiblingFallbackConfig() SiblingFallbackConfig {
	return SiblingFallbackConfig{
		TestNodeName:    "node-relink",
		TestNamespace:   "default",
		SCName:          "local-sc",
		OldByID:         "/dev/disk/by-id/wwn-old",
		NewByID:         "/dev/disk/by-id/wwn-new",
		SiblingByID:     "/dev/disk/by-id/scsi-sibling",
		BlockDevKName:   "sdb",
		SymlinkBaseName: "old-symlink-name",
		BlkidUUID:       "uuid-relink-1234",
		TempDirPrefix:   "sibling-fallback",
	}
}

// SiblingFallbackFixture is the on-disk layout plus derived PV name for sibling fallback tests.
type SiblingFallbackFixture struct {
	Config         SiblingFallbackConfig
	TmpRoot        string
	SymLinkDir     string
	SymlinkPath    string
	ExpectedPVName string
}

// mergeSiblingFallbackConfig fills zero fields from defaults.
func mergeSiblingFallbackConfig(cfg SiblingFallbackConfig) SiblingFallbackConfig {
	def := DefaultSiblingFallbackConfig()
	if cfg.TestNodeName == "" {
		cfg.TestNodeName = def.TestNodeName
	}
	if cfg.TestNamespace == "" {
		cfg.TestNamespace = def.TestNamespace
	}
	if cfg.SCName == "" {
		cfg.SCName = def.SCName
	}
	if cfg.OldByID == "" {
		cfg.OldByID = def.OldByID
	}
	if cfg.NewByID == "" {
		cfg.NewByID = def.NewByID
	}
	if cfg.SiblingByID == "" {
		cfg.SiblingByID = def.SiblingByID
	}
	if cfg.BlockDevKName == "" {
		cfg.BlockDevKName = def.BlockDevKName
	}
	if cfg.SymlinkBaseName == "" {
		cfg.SymlinkBaseName = def.SymlinkBaseName
	}
	if cfg.BlkidUUID == "" {
		cfg.BlkidUUID = def.BlkidUUID
	}
	if cfg.TempDirPrefix == "" {
		cfg.TempDirPrefix = def.TempDirPrefix
	}
	return cfg
}

// SetupSiblingFallback creates temp dirs, a dangling symlink to OldByID, installs internal
// mocks (by-id glob/eval, blkid), and registers t.Cleanup to restore globals and remove tmpdir.
func SetupSiblingFallback(t *testing.T, cfg SiblingFallbackConfig) *SiblingFallbackFixture {
	t.Helper()
	cfg = mergeSiblingFallbackConfig(cfg)

	tmpRoot := TempDir(t, cfg.TempDirPrefix)
	symLinkDir := filepath.Join(tmpRoot, cfg.SCName)
	if err := os.MkdirAll(symLinkDir, 0o755); err != nil {
		t.Fatalf("mkdir symLinkDir: %v", err)
	}
	symlinkPath := filepath.Join(symLinkDir, cfg.SymlinkBaseName)
	if err := os.Symlink(cfg.OldByID, symlinkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	expectedPVName := common.GeneratePVName(cfg.SymlinkBaseName, cfg.TestNodeName, cfg.SCName)

	f := &SiblingFallbackFixture{
		Config:         cfg,
		TmpRoot:        tmpRoot,
		SymLinkDir:     symLinkDir,
		SymlinkPath:    symlinkPath,
		ExpectedPVName: expectedPVName,
	}

	WithInternalMocks(t, func() {
		internal.FilePathGlob = func(pattern string) ([]string, error) {
			if strings.HasPrefix(pattern, internal.DiskByIDDir) {
				return []string{cfg.NewByID, cfg.SiblingByID}, nil
			}
			return filepath.Glob(pattern)
		}
		internal.FilePathEvalSymLinks = func(p string) (string, error) {
			switch p {
			case cfg.NewByID, cfg.SiblingByID:
				return filepath.Join("/dev", cfg.BlockDevKName), nil
			case cfg.OldByID:
				return "", os.ErrNotExist
			default:
				if p == symlinkPath {
					return filepath.Join("/dev", cfg.BlockDevKName), nil
				}
				return filepath.EvalSymlinks(p)
			}
		}
		internal.CmdExecutor = BlkidAlwaysFakeExec(cfg.BlkidUUID, nil)
	})

	return f
}

// PersistentVolume returns a PV pointing at the local-storage symlink path.
func (f *SiblingFallbackFixture) PersistentVolume() *corev1.PersistentVolume {
	reclaimDelete := corev1.PersistentVolumeReclaimDelete
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: f.ExpectedPVName},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: reclaimDelete,
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				Local: &corev1.LocalVolumeSource{Path: f.SymlinkPath},
			},
			StorageClassName: f.Config.SCName,
		},
	}
}

// LocalVolumeDeviceLink returns an LVDL with PreferredLinkTarget policy and stale by-id status.
func (f *SiblingFallbackFixture) LocalVolumeDeviceLink() *localv1.LocalVolumeDeviceLink {
	return &localv1.LocalVolumeDeviceLink{
		ObjectMeta: metav1.ObjectMeta{
			Name:      f.ExpectedPVName,
			Namespace: f.Config.TestNamespace,
		},
		Spec: localv1.LocalVolumeDeviceLinkSpec{
			PersistentVolumeName: f.ExpectedPVName,
			NodeName:             f.Config.TestNodeName,
			Policy:               localv1.DeviceLinkPolicyPreferredLinkTarget,
		},
		Status: localv1.LocalVolumeDeviceLinkStatus{
			CurrentLinkTarget: f.Config.OldByID,
			ValidLinkTargets:  []string{f.Config.OldByID, f.Config.SiblingByID},
		},
	}
}

// StorageClass returns a no-provisioner storage class matching the fixture.
func (f *SiblingFallbackFixture) StorageClass() *storagev1.StorageClass {
	reclaimDelete := corev1.PersistentVolumeReclaimDelete
	return &storagev1.StorageClass{
		ObjectMeta:    metav1.ObjectMeta{Name: f.Config.SCName},
		ReclaimPolicy: &reclaimDelete,
		Provisioner:   "kubernetes.io/no-provisioner",
	}
}

// RuntimeObjects returns PV, LVDL, and StorageClass for a fake client.
func (f *SiblingFallbackFixture) RuntimeObjects() []runtime.Object {
	return []runtime.Object{
		f.PersistentVolume(),
		f.LocalVolumeDeviceLink(),
		f.StorageClass(),
	}
}

// AddFakeVolumeDirEntries registers the symlink basename as a block entry under TmpRoot for FakeVolumeUtil.
func (f *SiblingFallbackFixture) AddFakeVolumeDirEntries(volUtil *provUtil.FakeVolumeUtil) {
	volUtil.AddNewDirEntries(f.TmpRoot, map[string][]*provUtil.FakeDirEntry{
		f.Config.SCName: {
			{Name: f.Config.SymlinkBaseName, Capacity: 10 * common.GiB, VolumeType: provUtil.FakeEntryBlock},
		},
	})
}
