package localvolumeset

import (
	"context"
	"fmt"
	"testing"

	operatorv1 "github.com/openshift/api/operator/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/controllers/nodedaemon"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

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

			err := fakeReconciler.updateDaemonSetsCondition(context.TODO(), req)
			assert.NoErrorf(t, err, "updateDaemonSetsCondition")

			reconciledLVSet := &localv1alpha1.LocalVolumeSet{}
			err = fakeReconciler.Client.Get(context.TODO(), lvsetKey, reconciledLVSet)
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
