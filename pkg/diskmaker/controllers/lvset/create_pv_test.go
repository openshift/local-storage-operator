package lvset

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	localv1 "github.com/openshift/local-storage-operator/api/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/internal"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		lvset     localv1alpha1.LocalVolumeSet
		node      corev1.Node
		sc        storagev1.StorageClass
		// device stuff
		symlinkpath     string
		actualVolMode   string
		desiredVolMode  string
		deviceName      string
		deviceCapacity  int64
		mountPoints     sets.String
		extraDirEntries []*provUtil.FakeDirEntry
	}{
		{
			desc: "basic creation: block on block",
			lvset: localv1alpha1.LocalVolumeSet{
				ObjectMeta: metav1.ObjectMeta{
					Name: "lvset-a",
					// Namespace: "a",
				},
				Spec: localv1alpha1.LocalVolumeSetSpec{
					StorageClassName: "storageclass-a",
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
			mountPoints:    sets.NewString(),
			symlinkpath:    "/mnt/local-storage/storageclass-a/device-a",
			deviceCapacity: 10 * common.GiB,
			deviceName:     "device-a",
		},
		{
			desc:      "basic creation: block on fs",
			shouldErr: true,
			lvset: localv1alpha1.LocalVolumeSet{
				ObjectMeta: metav1.ObjectMeta{
					Name: "lvset-a",
					// Namespace: "a",
				},
				Spec: localv1alpha1.LocalVolumeSetSpec{
					StorageClassName: "storageclass-a",
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
			mountPoints:    sets.NewString(),
			symlinkpath:    "/mnt/local-storage/storageclass-a/device-a",
			deviceCapacity: 10 * common.GiB,
			deviceName:     "device-a",
		},
		{
			desc: "basic creation: fs on block",
			lvset: localv1alpha1.LocalVolumeSet{
				ObjectMeta: metav1.ObjectMeta{
					Name: "lvset-a",
					// Namespace: "a",
				},
				Spec: localv1alpha1.LocalVolumeSetSpec{
					StorageClassName: "storageclass-a",
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
			mountPoints:    sets.NewString(),
			symlinkpath:    "/mnt/local-storage/storageclass-a/device-a",
			deviceCapacity: 10 * common.GiB,
			deviceName:     "device-a",
		},
		{
			desc: "basic creation: fs",
			lvset: localv1alpha1.LocalVolumeSet{
				ObjectMeta: metav1.ObjectMeta{
					Name: "lvset-a",
					// Namespace: "a",
				},
				Spec: localv1alpha1.LocalVolumeSetSpec{
					StorageClassName: "storageclass-a",
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
			mountPoints:    sets.NewString("/mnt/local-storage/storageclass-a/device-a"),
			symlinkpath:    "/mnt/local-storage/storageclass-a/device-a",
			deviceCapacity: 10 * common.GiB,
			deviceName:     "device-a",
		},
		{
			desc:      "actual volume mode is fs, but is not mountpoint",
			shouldErr: true,
			lvset: localv1alpha1.LocalVolumeSet{
				ObjectMeta: metav1.ObjectMeta{
					Name: "lvset-b",
					// Namespace: "a",
				},
				Spec: localv1alpha1.LocalVolumeSetSpec{
					StorageClassName: "storageclass-b",
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
			mountPoints:    sets.NewString("a", "b"), // device not present
			symlinkpath:    "/mnt/local-storage/storageclass-b/device-b",
			deviceCapacity: 10 * common.GiB,
			deviceName:     "device-b",
		},
	}
	// iterate through testcases
	for i, tc := range testTable {
		t.Logf("Test Case #%d: %q", i, tc.desc)

		// fake setup
		tc.lvset.Spec.VolumeMode = localv1.PersistentVolumeMode(tc.desiredVolMode)
		if tc.lvset.Namespace == "" {
			tc.lvset.Namespace = "default"
		}
		tc.lvset.Kind = localv1alpha1.LocalVolumeSetKind
		r, testConfig := newFakeLocalVolumeSetReconciler(t, &tc.lvset, &tc.node, &tc.sc)
		r.nodeName = tc.node.Name
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

		err := common.CreateLocalPV(t.Context(), common.CreateLocalPVArgs{
			LocalVolumeLikeObject: &tc.lvset,
			RuntimeConfig:         r.runtimeConfig,
			StorageClass:          tc.sc,
			MountPointMap:         tc.mountPoints,
			Client:                r.Client,
			SymLinkPath:           tc.symlinkpath,
			DevicePath:            tc.deviceName,
			IDExists:              true,
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
		err = common.CreateLocalPV(t.Context(), common.CreateLocalPVArgs{
			LocalVolumeLikeObject: &tc.lvset,
			RuntimeConfig:         r.runtimeConfig,
			StorageClass:          tc.sc,
			MountPointMap:         tc.mountPoints,
			Client:                r.Client,
			SymLinkPath:           tc.symlinkpath,
			DevicePath:            tc.deviceName,
			IDExists:              true,
			ExtraLabelsForPV:      map[string]string{},
		})
		assert.Nil(t, err)

	}

}

func TestCreatePV_SetsLVDLOwnerRefToLocalVolumeSet(t *testing.T) {
	reclaimPolicyDelete := corev1.PersistentVolumeReclaimDelete
	lvset := localv1alpha1.LocalVolumeSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       localv1alpha1.LocalVolumeSetKind,
			APIVersion: localv1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lvset-ownerref",
			Namespace: "default",
			UID:       "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		},
		Spec: localv1alpha1.LocalVolumeSetSpec{
			StorageClassName: "storageclass-ownerref",
		},
	}
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "nodename-ownerref",
			Labels: map[string]string{corev1.LabelHostname: "node-hostname-ownerref"},
		},
	}
	sc := storagev1.StorageClass{
		ObjectMeta:    metav1.ObjectMeta{Name: "storageclass-ownerref"},
		ReclaimPolicy: &reclaimPolicyDelete,
	}

	r, testConfig := newFakeLocalVolumeSetReconciler(t, &lvset, &node, &sc)
	r.nodeName = node.Name
	testConfig.runtimeConfig.Node = &node
	testConfig.runtimeConfig.Name = common.GetProvisionedByValue(node)
	testConfig.runtimeConfig.Namespace = lvset.Namespace
	testConfig.runtimeConfig.DiscoveryMap[sc.Name] = provCommon.MountConfig{
		VolumeMode: string(localv1.PersistentVolumeBlock),
	}

	symLinkPath := "/mnt/local-storage/" + sc.Name + "/device-ownerref"
	testConfig.fakeVolUtil.AddNewDirEntries("/mnt/local-storage/", map[string][]*provUtil.FakeDirEntry{
		sc.Name: {
			{Name: "device-ownerref", Capacity: 10 * common.GiB, VolumeType: provUtil.FakeEntryBlock},
		},
	})

	origGlob := internal.FilePathGlob
	origEval := internal.FilePathEvalSymLinks
	origExec := internal.ExecCommand
	t.Cleanup(func() {
		internal.FilePathGlob = origGlob
		internal.FilePathEvalSymLinks = origEval
		internal.ExecCommand = origExec
	})
	internal.FilePathGlob = func(pattern string) ([]string, error) {
		return []string{"/dev/disk/by-id/wwn-ownerref"}, nil
	}
	internal.FilePathEvalSymLinks = func(path string) (string, error) {
		return "/dev/device-ownerref", nil
	}
	internal.ExecCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("bash", "-c", "echo uuid-ownerref")
	}

	err := common.CreateLocalPV(t.Context(), common.CreateLocalPVArgs{
		LocalVolumeLikeObject: &lvset,
		RuntimeConfig:         r.runtimeConfig,
		StorageClass:          sc,
		MountPointMap:         sets.NewString(),
		Client:                r.Client,
		SymLinkPath:           symLinkPath,
		DevicePath:            "/dev/device-ownerref",
		KName:                 "device-ownerref",
		IDExists:              true,
		ExtraLabelsForPV:      map[string]string{},
	})
	assert.NoError(t, err)

	pvName := common.GeneratePVName(filepath.Base(symLinkPath), node.Name, sc.Name)
	lvdl := &localv1.LocalVolumeDeviceLink{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: pvName, Namespace: lvset.Namespace}, lvdl)
	assert.NoError(t, err)
	if assert.Len(t, lvdl.OwnerReferences, 1, "LVDL should carry owner reference to LocalVolumeSet") {
		assert.Equal(t, localv1alpha1.GroupVersion.String(), lvdl.OwnerReferences[0].APIVersion)
		assert.Equal(t, localv1alpha1.LocalVolumeSetKind, lvdl.OwnerReferences[0].Kind)
		assert.Equal(t, lvset.Name, lvdl.OwnerReferences[0].Name)
		assert.Equal(t, lvset.UID, lvdl.OwnerReferences[0].UID)
	}
}
