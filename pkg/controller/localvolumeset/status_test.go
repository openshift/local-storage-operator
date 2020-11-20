package localvolumeset

import (
	"context"
	"fmt"
	"testing"

	operatorv1 "github.com/openshift/api/operator/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/controller/nodedaemon"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	testNamespace = "default"
)

func newFakeLocalVolumeSetReconciler(t *testing.T, objs ...runtime.Object) *LocalVolumeSetReconciler {
	scheme, err := localv1alpha1.SchemeBuilder.Build()
	assert.NoErrorf(t, err, "creating scheme")

	err = corev1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding corev1 to scheme")

	err = appsv1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding appsv1 to scheme")

	client := fake.NewFakeClientWithScheme(scheme, objs...)

	return &LocalVolumeSetReconciler{
		client:   client,
		scheme:   scheme,
		lvSetMap: &lvSetMapStore{},
	}
}

func TestDaemonSetCondition(t *testing.T) {
	type knownResult struct {
		diskMakerFound            bool
		diskMakerUnavailableCount int32
		condition                 bool
	}

	testTable := []knownResult{
		{
			diskMakerFound:            true,
			diskMakerUnavailableCount: 0,
			condition:                 true,
		},
		{
			diskMakerFound:            false,
			diskMakerUnavailableCount: 5,
			condition:                 false,
		},
		{
			diskMakerFound:            false,
			diskMakerUnavailableCount: 1,
			condition:                 false,
		},
	}

	for _, kr := range testTable {
		diskmaker := &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      nodedaemon.DiskMakerName,
				Namespace: testNamespace,
			},
			Status: appsv1.DaemonSetStatus{
				NumberUnavailable: kr.diskMakerUnavailableCount,
			},
		}

		var objs []runtime.Object
		if kr.diskMakerFound {
			objs = append(objs, diskmaker)
		}
		var lvsets []*localv1alpha1.LocalVolumeSet

		for i := 0; i <= 5; i++ {
			lvset := &localv1alpha1.LocalVolumeSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("s-%d", i),
					Namespace: testNamespace,
				},
			}
			lvsets = append(lvsets, lvset)
			objs = append(objs, lvset)
		}

		fakeReconciler := newFakeLocalVolumeSetReconciler(t, objs...)

		for _, lvset := range lvsets {
			lvsetKey := types.NamespacedName{Name: lvset.GetName(), Namespace: lvset.GetNamespace()}
			req := reconcile.Request{lvsetKey}

			err := fakeReconciler.updateDaemonSetsCondition(req)
			assert.NoErrorf(t, err, "updateDaemonSetsCondition")

			reconciledLVSet := &localv1alpha1.LocalVolumeSet{}
			err = fakeReconciler.client.Get(context.TODO(), lvsetKey, reconciledLVSet)
			assert.NoErrorf(t, err, "get lvset from fake client")

			conditions := reconciledLVSet.Status.Conditions

			conditionFound := false
			expectedConditionStatus := operatorv1.ConditionFalse
			if kr.condition {
				expectedConditionStatus = operatorv1.ConditionTrue
			}

			for _, condition := range conditions {
				if condition.Type == DaemonSetsAvailableAndConfigured {
					conditionFound = true
					assert.Equalf(t, expectedConditionStatus, condition.Status, "match condtion status with known result")
				}
			}
			assert.Truef(t, conditionFound, "condition should be set")
		}

	}
}
