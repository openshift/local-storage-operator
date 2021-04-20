package nodedaemon

import (
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// "reflect"
// "testing"

func TestMutateAggregatedSpecWithNilNodeSelector(t *testing.T) {
	ds := &appsv1.DaemonSet{}
	MutateAggregatedSpec(
		ds,
		reconcile.Request{},
		[]corev1.Toleration{},
		[]metav1.OwnerReference{},
		nil,
		"",
	)
	assert.Nilf(t, ds.Spec.Template.Spec.Affinity, "DaemonSet affinity should be nil if nodeSelector is nil")

	ds = &appsv1.DaemonSet{}
	nodeSelector := &corev1.NodeSelector{}
	MutateAggregatedSpec(
		ds,
		reconcile.Request{},
		[]corev1.Toleration{},
		[]metav1.OwnerReference{},
		nodeSelector,
		"",
	)
	assert.NotNilf(t, ds.Spec.Template.Spec.Affinity, "DaemonSet affinity should not be nil if nodeSelector is not nil")

}
