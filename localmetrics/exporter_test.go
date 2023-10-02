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

var (
	appLabel       = "fakeLabel"
	expectedLabels = map[string]string{"app": appLabel}
)

func getFakeClient(t *testing.T) client.Client {
	scheme, err := localv1alpha1.SchemeBuilder.Build()
	assert.NoErrorf(t, err, "creating scheme")

	err = monitoringv1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding monitoringv1 to scheme")

	err = corev1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding corev1 to scheme")

	return fake.NewClientBuilder().WithScheme(scheme).Build()
}

func TestEnableService(t *testing.T) {
	fakeExporter := NewExporter(context.TODO(), getFakeClient(t), "test-service", "test-ns", "test-cert", []metav1.OwnerReference{}, appLabel)
	err := fakeExporter.enableService()
	assert.NoError(t, err)

	// assert that service was created with correct parameters.
	actual := &corev1.Service{}
	err = fakeExporter.Client.Get(fakeExporter.Ctx,
		types.NamespacedName{Name: "test-service", Namespace: "test-ns"}, actual)
	assert.NoError(t, err)
	assert.Equal(t, "test-service", actual.Name)
	assert.Equal(t, expectedLabels, actual.Labels)
	assert.Equal(t, expectedLabels, actual.Spec.Selector)
}

func TestEnableServiceMonitor(t *testing.T) {
	fakeExporter := NewExporter(context.TODO(), getFakeClient(t), "test-service-monitor", "test-ns", "test-cert", []metav1.OwnerReference{}, appLabel)
	err := fakeExporter.enableServiceMonitor()
	assert.NoError(t, err)

	// assert that service monitor was created with correct parameters.
	actual := &monitoringv1.ServiceMonitor{}
	err = fakeExporter.Client.Get(fakeExporter.Ctx,
		types.NamespacedName{Name: "test-service-monitor", Namespace: "test-ns"}, actual)
	assert.NoError(t, err)
	assert.Equal(t, "test-service-monitor", actual.Name)
	assert.Equal(t, expectedLabels, actual.Labels)
	assert.Equal(t, expectedLabels, actual.Spec.Selector.MatchLabels)
}
