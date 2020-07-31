package localvolumediscovery

import (
	"context"
	"testing"

	operatorv1 "github.com/openshift/api/operator/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	name      = "auto-discover-devices"
	namespace = "local-storage"
)

func newFakeLocalVolumeDiscoveryReconciler(t *testing.T, objs ...runtime.Object) *ReconcileLocalVolumeDiscovery {
	scheme, err := localv1alpha1.SchemeBuilder.Build()
	assert.NoErrorf(t, err, "creating scheme")

	err = corev1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding corev1 to scheme")

	err = appsv1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding appsv1 to scheme")

	client := fake.NewFakeClientWithScheme(scheme, objs...)

	return &ReconcileLocalVolumeDiscovery{
		client: client,
		scheme: scheme,
	}
}

func TestDiscoveryReconciler(t *testing.T) {

	testcases := []struct {
		label                        string
		discoveryDaemonCreated       bool
		discoveryDesiredDaemonsCount int32
		discoveryReadyDaemonsCount   int32
		expectedPhase                localv1alpha1.DiscoveryPhase
		conditionType                string
		conditionStatus              operatorv1.ConditionStatus
	}{
		{
			label:                        "case 1",
			discoveryDaemonCreated:       true,
			discoveryDesiredDaemonsCount: 1,
			discoveryReadyDaemonsCount:   1,
			expectedPhase:                localv1alpha1.Discovering,
			conditionType:                "Available",
			conditionStatus:              operatorv1.ConditionTrue,
		},
		{
			label:                        "case 2",
			discoveryDaemonCreated:       true,
			discoveryDesiredDaemonsCount: 100,
			discoveryReadyDaemonsCount:   100,
			expectedPhase:                localv1alpha1.Discovering,
			conditionType:                "Available",
			conditionStatus:              operatorv1.ConditionTrue,
		},
		{
			label:                        "case 3",
			discoveryDaemonCreated:       true,
			discoveryDesiredDaemonsCount: 100,
			discoveryReadyDaemonsCount:   80,
			expectedPhase:                localv1alpha1.Discovering,
			conditionType:                "Progressing",
			conditionStatus:              operatorv1.ConditionFalse,
		},
		{
			label:                        "case 4",
			discoveryDaemonCreated:       true,
			discoveryDesiredDaemonsCount: 0,
			discoveryReadyDaemonsCount:   0,
			expectedPhase:                localv1alpha1.DiscoveryFailed,
			conditionType:                "Degraded",
			conditionStatus:              operatorv1.ConditionFalse,
		},

		{
			label:                        "case 5",
			discoveryDaemonCreated:       false,
			discoveryDesiredDaemonsCount: 0,
			discoveryReadyDaemonsCount:   0,
			expectedPhase:                localv1alpha1.DiscoveryFailed,
			conditionType:                "Degraded",
			conditionStatus:              operatorv1.ConditionFalse,
		},
	}

	for _, tc := range testcases {
		diskmaker := &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      DiskMakerDiscovery,
				Namespace: namespace,
			},
			Status: appsv1.DaemonSetStatus{
				NumberReady:            tc.discoveryReadyDaemonsCount,
				DesiredNumberScheduled: tc.discoveryDesiredDaemonsCount,
			},
		}

		discoveryObj := &localv1alpha1.LocalVolumeDiscovery{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			TypeMeta: metav1.TypeMeta{
				Kind: "LocalVolumeDiscovery",
			},
		}
		objects := []runtime.Object{
			discoveryObj,
		}

		if tc.discoveryDaemonCreated {
			objects = append(objects, diskmaker)
		}

		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      discoveryObj.Name,
				Namespace: discoveryObj.Namespace,
			},
		}
		fakeReconciler := newFakeLocalVolumeDiscoveryReconciler(t, objects...)
		_, err := fakeReconciler.Reconcile(req)
		err = fakeReconciler.client.Get(context.TODO(), types.NamespacedName{Name: discoveryObj.Name, Namespace: discoveryObj.Namespace}, discoveryObj)
		assert.NoError(t, err)
		assert.Equalf(t, tc.expectedPhase, discoveryObj.Status.Phase, "[%s] invalid phase", tc.label)
		assert.Equalf(t, tc.conditionType, discoveryObj.Status.Conditions[0].Type, "[%s] invalid condition type", tc.label)
		assert.Equalf(t, tc.conditionStatus, discoveryObj.Status.Conditions[0].Status, "[%s] invalid condition status", tc.label)
	}
}
