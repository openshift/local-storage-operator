package localmetrics

import (
	"testing"

	monitoringv1 "github.com/coreos/prometheus-operator/pkg/apis/monitoring/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestMetricsExporterService(t *testing.T) {
	fakeExporter := NewExporter(getFakeClient(t), "test-service", "test-ns", []metav1.OwnerReference{}, fakeLabels)
	service, err := fakeExporter.createOrUpdateService()
	assert.NoError(t, err)
	assert.Equal(t, "test-service", service.Name)
	assert.Equal(t, fakeLabels, service.Labels)
	assert.Equal(t, fakeLabels, service.Spec.Selector)
}

func TestMetricsExporterServiceMonitor(t *testing.T) {
	fakeExporter := NewExporter(getFakeClient(t), "test-service-monitor", "test-ns", []metav1.OwnerReference{}, fakeLabels)
	serviceMonitor, err := fakeExporter.createOrUpdateServiceMonitor()
	assert.NoError(t, err)
	assert.Equal(t, "test-service-monitor", serviceMonitor.Name)
	assert.Equal(t, fakeLabels, serviceMonitor.Labels)
	assert.Equal(t, fakeLabels, serviceMonitor.Spec.Selector.MatchLabels)
}
