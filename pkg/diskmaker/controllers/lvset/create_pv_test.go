package lvset

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
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
		r, testConfig := newFakeLocalVolumeSetReconciler(t, &tc.lvset, &tc.node, &tc.sc)
		r.nodeName = tc.node.Name
		testConfig.runtimeConfig.Node = &tc.node
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

		err := r.createPV(
			&tc.lvset,
			log.WithName("testLogger"),
			tc.sc,
			tc.mountPoints,
			tc.symlinkpath,
		)
		if tc.shouldErr {
			assert.NotNil(t, err)
		} else {
			assert.Nil(t, err)
		}

		if tc.shouldErr {
			return
		}
		pv := &corev1.PersistentVolume{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: generatePVName(tc.symlinkpath, tc.node.GetName(), tc.sc.GetName())}, pv)

		// capacity accurate
		pvCapacity, found := pv.Spec.Capacity["storage"]
		assert.True(t, found)
		expectedCapacity := resource.MustParse(fmt.Sprint(common.RoundDownCapacityPretty(tc.deviceCapacity)))

		assert.Truef(t, pvCapacity.Equal(expectedCapacity), "actual: %s,expected: %s", pvCapacity, expectedCapacity)

		// pvName accurate
		assert.Equal(t, generatePVName(tc.symlinkpath, tc.node.Name, tc.sc.Name), pv.Name)

		// symlinkPath accurate
		assert.NotNil(t, pv.Spec.Local)
		assert.Equal(t, tc.symlinkpath, pv.Spec.Local.Path)

		// storageclass accurate
		assert.Equal(t, tc.sc.Name, pv.Spec.StorageClassName)

		// reclaimPolicy accurate,
		assert.Equal(t, *tc.sc.ReclaimPolicy, pv.Spec.PersistentVolumeReclaimPolicy)

		// test idempotency by running again
		err = r.createPV(
			&tc.lvset,
			log.WithName("testLogger"),
			tc.sc,
			tc.mountPoints,
			tc.symlinkpath,
		)
		assert.Nil(t, err)

	}

}
