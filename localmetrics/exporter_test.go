package localmetrics

import (
	"context"
	"testing"

	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var fakeLabels = map[string]string{"key1": "value1", "key2": "value2"}

func getFakeClient(t *testing.T) client.Client {
	scheme, err := localv1alpha1.SchemeBuilder.Build()
	assert.NoErrorf(t, err, "creating scheme")

	err = monitoringv1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding monitoringv1 to scheme")

	err = corev1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding corev1 to scheme")

	return fake.NewFakeClientWithScheme(scheme)
}

func TestEnableService(t *testing.T) {
	fakeExporter := NewExporter(context.TODO(), getFakeClient(t), "test-service", "test-ns", "test-cert", []metav1.OwnerReference{}, fakeLabels)
	err := fakeExporter.enableService()
	assert.NoError(t, err)

	// assert that service was created with correct parameters.
	expectedServce := &corev1.Service{}
	err = fakeExporter.Client.Get(fakeExporter.Ctx,
		types.NamespacedName{Name: "test-service", Namespace: "test-ns"}, expectedServce)
	assert.NoError(t, err)
	assert.Equal(t, "test-service", expectedServce.Name)
	assert.Equal(t, fakeLabels, expectedServce.Labels)
	assert.Equal(t, fakeLabels, expectedServce.Spec.Selector)
}

func TestEnableServiceMonitor(t *testing.T) {
	fakeExporter := NewExporter(context.TODO(), getFakeClient(t), "test-service-monitor", "test-ns", "test-cert", []metav1.OwnerReference{}, fakeLabels)
	err := fakeExporter.enableServiceMonitor()
	assert.NoError(t, err)

	// assert that service monitor was created with correct parameters.
	expectedServce := &monitoringv1.ServiceMonitor{}

	err = fakeExporter.Client.Get(fakeExporter.Ctx,
		types.NamespacedName{Name: "test-service-monitor", Namespace: "test-ns"}, expectedServce)
	assert.NoError(t, err)
	assert.Equal(t, "test-service-monitor", expectedServce.Name)
	assert.Equal(t, fakeLabels, expectedServce.Labels)
	assert.Equal(t, fakeLabels, expectedServce.Spec.Selector.MatchLabels)
}
