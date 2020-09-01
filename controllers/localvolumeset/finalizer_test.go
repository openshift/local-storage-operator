package localvolumeset

import (
	"context"
	"fmt"
	"testing"
	"time"

	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/common"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestPVProtectionFinalizer(t *testing.T) {
	// finalizer should be removed only if and only if:-
	// - no PVs owned by the localvolumeset are a in "Bound" state

	// test table definition:

	// an association between a localVolumeSet and expectedFinalizers given
	// a list of PVs in various states.
	type lvSetResult struct {
		lvSet           localv1alpha1.LocalVolumeSet
		expectFinalizer bool
	}

	// knownResult defines one testCase
	type knownResult struct {
		existingPVs  []corev1.PersistentVolume
		lvSetResults []lvSetResult
		desc         string // small description
	}

	// StorageClass names for populating testcases
	nameA := "a"
	nameB := "b"
	nameC := "c"

	// when newPV is called, only the name,namespace,kind and storageclass of the LVSet matters, the deleted value does not
	pvNames := make([]string, 0)
	// utility method for populating `existingPVs` based on storageClass, phase, and reclaimPolicy
	newPV := func(lvset localv1alpha1.LocalVolumeSet, phase corev1.PersistentVolumePhase) corev1.PersistentVolume {
		pvName := fmt.Sprintf("pv-%d", len(pvNames))
		pvNames = append(pvNames, pvName)
		return corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: pvName,
				Labels: map[string]string{
					common.PVOwnerKindLabel:      lvset.Kind,
					common.PVOwnerNamespaceLabel: lvset.Namespace,
					common.PVOwnerNameLabel:      lvset.Name,
				},
			},
			// Name not generated, rely on ObjectMeta.GenerateName
			Spec: corev1.PersistentVolumeSpec{
				StorageClassName:              lvset.Spec.StorageClassName,
				PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
			},
			Status: corev1.PersistentVolumeStatus{
				Phase: phase,
			},
		}
	}

	// utility for populating lvsetresults
	newLV := func(deleted bool, name string) localv1alpha1.LocalVolumeSet {

		lvSet := localv1alpha1.LocalVolumeSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "test",
			},
			TypeMeta: metav1.TypeMeta{
				Kind: localv1alpha1.LocalVolumeSetKind,
			},
			Spec: localv1alpha1.LocalVolumeSetSpec{
				StorageClassName: "test-sc",
			},
		}

		if deleted {
			now := metav1.Now()
			lvSet.SetDeletionTimestamp(&now)
		}

		return lvSet

	}

	// test cases:
	testTable := []knownResult{
		// no bound pvs, no deletion timestamp, expectFinalizer to be created
		{
			desc: "1a: no deletion, simple",
			existingPVs: []corev1.PersistentVolume{
				// has bound
				newPV(newLV(false, nameA), corev1.VolumeAvailable),
				newPV(newLV(false, nameA), corev1.VolumeFailed),
				newPV(newLV(false, nameA), corev1.VolumePending),
				newPV(newLV(false, nameA), corev1.VolumeReleased),
				newPV(newLV(false, nameA), corev1.VolumeBound),
			},
			lvSetResults: []lvSetResult{
				{
					lvSet:           newLV(false, nameA),
					expectFinalizer: true,
				},
			},
		},
		{
			desc: "1b: no deletion, crowded",
			existingPVs: []corev1.PersistentVolume{
				// has bound
				newPV(newLV(false, nameA), corev1.VolumeAvailable),
				newPV(newLV(false, nameA), corev1.VolumeAvailable),
				newPV(newLV(false, nameA), corev1.VolumeFailed),
				newPV(newLV(false, nameA), corev1.VolumePending),
				newPV(newLV(false, nameA), corev1.VolumeReleased),
				newPV(newLV(false, nameA), corev1.VolumeBound),
				// no bound
				newPV(newLV(false, nameB), corev1.VolumeAvailable),
				// has bound
				newPV(newLV(false, nameC), corev1.VolumeAvailable),
				newPV(newLV(false, nameC), corev1.VolumeBound),
			},
			lvSetResults: []lvSetResult{
				{
					lvSet:           newLV(false, nameA),
					expectFinalizer: true,
				},
				{
					lvSet:           newLV(false, nameB),
					expectFinalizer: true,
				},
				{
					lvSet:           newLV(false, nameC),
					expectFinalizer: true,
				},
			},
		},
		// no bound pvs, no deletion timestamp, expectFinalizer to be created
		{
			desc: "2a: deletion unblocked, simple",
			existingPVs: []corev1.PersistentVolume{
				// no bound
				newPV(newLV(false, nameA), corev1.VolumeAvailable),
				newPV(newLV(false, nameA), corev1.VolumeFailed),
				newPV(newLV(false, nameA), corev1.VolumePending),
			},
			lvSetResults: []lvSetResult{
				{
					lvSet:           newLV(true, nameA),
					expectFinalizer: false,
				},
			},
		},
		{
			desc: "2b: deletion unblocked, crowded",
			existingPVs: []corev1.PersistentVolume{
				// no bound
				newPV(newLV(false, nameA), corev1.VolumeAvailable),
				newPV(newLV(false, nameA), corev1.VolumeAvailable),
				newPV(newLV(false, nameA), corev1.VolumeFailed),
				newPV(newLV(false, nameA), corev1.VolumePending),
				// has bound
				newPV(newLV(false, nameB), corev1.VolumeAvailable),
				newPV(newLV(false, nameB), corev1.VolumeBound),
				// no bound
				newPV(newLV(false, nameC), corev1.VolumeAvailable),
			},
			lvSetResults: []lvSetResult{
				{
					lvSet:           newLV(true, nameA),
					expectFinalizer: false,
				},
				{
					lvSet:           newLV(true, nameB),
					expectFinalizer: true,
				},
				{
					lvSet:           newLV(true, nameC),
					expectFinalizer: false,
				},
			},
		},
		// no bound pvs, no deletion timestamp, expectFinalizer to be created
		{
			desc: "3a: deletion blocked, simple",
			existingPVs: []corev1.PersistentVolume{
				// all types
				newPV(newLV(false, nameA), corev1.VolumeAvailable),
				newPV(newLV(false, nameA), corev1.VolumeFailed),
				newPV(newLV(false, nameA), corev1.VolumePending),
				newPV(newLV(false, nameA), corev1.VolumeReleased),
				newPV(newLV(false, nameA), corev1.VolumeBound),
			},
			lvSetResults: []lvSetResult{
				{
					lvSet:           newLV(true, nameA),
					expectFinalizer: true,
				},
			},
		},
		{
			desc: "3b: deletion blocked, crowded",
			existingPVs: []corev1.PersistentVolume{
				// all types
				newPV(newLV(false, nameA), corev1.VolumeAvailable),
				newPV(newLV(false, nameA), corev1.VolumeAvailable),
				newPV(newLV(false, nameA), corev1.VolumeFailed),
				newPV(newLV(false, nameA), corev1.VolumePending),
				newPV(newLV(false, nameA), corev1.VolumeReleased),
				newPV(newLV(false, nameA), corev1.VolumeBound),
				// no bound
				newPV(newLV(false, nameB), corev1.VolumeAvailable),
				newPV(newLV(false, nameC), corev1.VolumeAvailable),
				// no bound
				newPV(newLV(false, nameC), corev1.VolumeBound),
			},
			lvSetResults: []lvSetResult{
				{
					lvSet:           newLV(true, nameA),
					expectFinalizer: true,
				},
				{
					lvSet:           newLV(true, nameB),
					expectFinalizer: false,
				},
				{
					lvSet:           newLV(true, nameC),
					expectFinalizer: true,
				},
			},
		},
		// no bound pvs, no deletion timestamp, expectFinalizer to be created
		{
			desc: "3c: deletion blocked,  released PV",
			existingPVs: []corev1.PersistentVolume{
				// all types
				newPV(newLV(false, nameA), corev1.VolumeAvailable),
				newPV(newLV(false, nameA), corev1.VolumeFailed),
				newPV(newLV(false, nameA), corev1.VolumePending),
				newPV(newLV(false, nameA), corev1.VolumeReleased),
			},
			lvSetResults: []lvSetResult{
				{
					lvSet:           newLV(true, nameA),
					expectFinalizer: true,
				},
			},
		},
	}

	for _, testCase := range testTable {
		t.Logf("testCase: %s", testCase.desc)

		// set up mocks
		objs := make([]runtime.Object, 0)
		// add existingPVs to fake client
		for _, pv := range testCase.existingPVs {
			objs = append(objs, pv.DeepCopyObject())
		}
		for _, result := range testCase.lvSetResults {
			objs = append(objs, result.lvSet.DeepCopyObject())
		}
		reconciler := newFakeLocalVolumeSetReconciler(t, objs...)
		for _, result := range testCase.lvSetResults {

			lvSetKey := types.NamespacedName{Name: result.lvSet.GetName(), Namespace: result.lvSet.GetNamespace()}

			// to debug specific test cases uncomment this and change desc and lvset name and place breakpoints
			if testCase.desc == "2a: deletion unblocked, simple" {
				time.Sleep(0)
				if result.lvSet.Name == "s" {
					time.Sleep(0)
				}
			}

			// reconcile successfully
			_, err := reconciler.reconcile(context.TODO(), reconcile.Request{NamespacedName: lvSetKey})
			assert.Nilf(t, err, "expected reconciler to reconciler successfully")

			lvSet := &localv1alpha1.LocalVolumeSet{}
			err = reconciler.Client.Get(context.TODO(), lvSetKey, lvSet)
			if !result.expectFinalizer {
				assert.Truef(t, errors.IsNotFound(err), "expected lvset to be deleted")
			} else {
				assert.Nilf(t, err, "expected lvset to be found via fake client")
				exists := common.ContainsFinalizer(lvSet.ObjectMeta, common.LocalVolumeProtectionFinalizer)
				assert.Equalf(t, result.expectFinalizer, exists, "expect finalizer result to match for lv: %q", lvSet.Name)
			}

		}

	}

}
