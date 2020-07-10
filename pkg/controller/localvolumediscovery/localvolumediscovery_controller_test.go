package localvolumediscovery

import (
	"context"
	"testing"

	"github.com/openshift/local-storage-operator/pkg/apis"
	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	name      = "auto-discover-devices"
	namespace = "local-storage"
	nodeName  = "node1"
)

func TestLocalVolumeDiscoveryReconciler(t *testing.T) {
	cr := getFakeDiscoveryObj()
	objects := []runtime.Object{
		cr,
		&appsv1.DaemonSet{},
	}

	r := createFakeDiscoveryReconciler(t, objects...)

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      cr.Name,
			Namespace: cr.Namespace,
		},
	}

	res, err := r.Reconcile(req)

	assert.NoError(t, err)
	assert.False(t, res.Requeue)
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, cr)
	assert.NoError(t, err)
	assert.Equal(t, localv1alpha1.Discovering, cr.Status.Phase)

}

func getFakeDiscoveryObj() *localv1alpha1.LocalVolumeDiscovery {
	return &localv1alpha1.LocalVolumeDiscovery{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		TypeMeta: metav1.TypeMeta{
			Kind: "LocalVolumeDiscovery",
		},
		Spec: localv1alpha1.LocalVolumeDiscoverySpec{
			NodeSelector: &v1.NodeSelector{
				NodeSelectorTerms: []v1.NodeSelectorTerm{
					{
						MatchFields: []v1.NodeSelectorRequirement{
							{Key: "kubernetes.io/hostname", Operator: v1.NodeSelectorOpIn, Values: []string{nodeName}},
						},
					},
				},
			},
			Tolerations: []v1.Toleration{},
		},
	}
}

func createFakeDiscoveryReconciler(t *testing.T, objects ...runtime.Object) *ReconcileLocalVolumeDiscovery {
	s := createFakeScheme(t, objects...)
	cl := fake.NewFakeClientWithScheme(s, objects...)
	r := &ReconcileLocalVolumeDiscovery{client: cl, scheme: s}
	return r
}

func createFakeScheme(t *testing.T, obj ...runtime.Object) *runtime.Scheme {
	registerObjs := obj
	registerObjs = append(registerObjs)
	localv1alpha1.SchemeBuilder.Register(registerObjs...)
	scheme, err := localv1alpha1.SchemeBuilder.Build()
	if err != nil {
		assert.Fail(t, "unable to build scheme")
	}
	err = apis.AddToScheme(scheme)
	if err != nil {
		assert.Fail(t, "failed to add scheme")
	}

	return scheme

}
