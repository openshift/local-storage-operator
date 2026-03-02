package nodedaemon

import (
	"testing"

	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/local-storage-operator/assets"
	"github.com/openshift/local-storage-operator/pkg/common"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// "reflect"
// "testing"

func TestMutateAggregatedSpecWithNilNodeSelector(t *testing.T) {
	ds := &appsv1.DaemonSet{}
	MutateAggregatedSpec(
		ds,
		[]corev1.Toleration{},
		[]metav1.OwnerReference{},
		nil,
		ds,
	)
	assert.Nilf(t, ds.Spec.Template.Spec.Affinity, "DaemonSet affinity should be nil if nodeSelector is nil")

	ds = &appsv1.DaemonSet{}
	nodeSelector := &corev1.NodeSelector{}
	MutateAggregatedSpec(
		ds,
		[]corev1.Toleration{},
		[]metav1.OwnerReference{},
		nodeSelector,
		ds,
	)
	assert.NotNilf(t, ds.Spec.Template.Spec.Affinity, "DaemonSet affinity should not be nil if nodeSelector is not nil")
}

func TestGetDiskMakerDSMutateFnTLSArgs(t *testing.T) {
	tlsMinVersion := "VersionTLS12"
	tlsCipherSuites := "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256"

	request := reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "test-ns"},
	}
	mutateFn := getDiskMakerDSMutateFn(request, nil, nil, nil, "hash", tlsMinVersion, tlsCipherSuites)
	ds := &appsv1.DaemonSet{}
	err := mutateFn(ds)
	assert.NoError(t, err, "mutateFn should not return an error")

	var proxyContainer *corev1.Container
	for i := range ds.Spec.Template.Spec.Containers {
		if ds.Spec.Template.Spec.Containers[i].Name == "kube-rbac-proxy" {
			proxyContainer = &ds.Spec.Template.Spec.Containers[i]
			break
		}
	}
	assert.NotNilf(t, proxyContainer, "kube-rbac-proxy container should be present")
	assert.Containsf(t, proxyContainer.Args, "--tls-min-version="+tlsMinVersion,
		"kube-rbac-proxy args should contain --tls-min-version")
	assert.Containsf(t, proxyContainer.Args, "--tls-cipher-suites="+tlsCipherSuites,
		"kube-rbac-proxy args should contain --tls-cipher-suites")
}

func TestGetDiskMakerDSMutateFnNoTLSArgs(t *testing.T) {
	request := reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "test-ns"},
	}
	// Empty TLS values (e.g. when APIServer is unreachable): mutateFn should still succeed.
	mutateFn := getDiskMakerDSMutateFn(request, nil, nil, nil, "hash", "", "")
	ds := &appsv1.DaemonSet{}
	err := mutateFn(ds)
	assert.NoError(t, err, "mutateFn should not return an error with empty TLS values")
}

func TestMutateAggregatedSpecTemplates(t *testing.T) {
	// Generate DaemonSet template by reading yaml asset
	dsBytes, err := assets.ReadFileAndReplace(
		common.DiskMakerManagerDaemonSetTemplate,
		[]string{
			"${OBJECT_NAMESPACE}", "new-namespace",
			"${CONTAINER_IMAGE}", common.GetDiskMakerImage(),
			"${RBAC_PROXY_IMAGE}", common.GetKubeRBACProxyImage(),
			"${PRIORITY_CLASS_NAME}", "",
		},
	)
	assert.Nil(t, err, "ReadFile should not return an error when reading the template")
	dsTemplate := resourceread.ReadDaemonSetV1OrDie(dsBytes)

	// Create basic DaemonSet and make sure the template is applied
	ds := &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "DaemonSet",
			APIVersion: "apps/v1",
		},
	}
	MutateAggregatedSpec(
		ds,
		nil,
		nil,
		nil,
		dsTemplate,
	)
	assert.Equalf(t, dsTemplate, ds, "DaemonSet should be equal to the DaemonSet template in the absence of other arguments")

	// If CreationTimestamp is set, we should not overwrite ObjectMeta fields
	ds.CreationTimestamp = metav1.Now()
	ds.ObjectMeta.Name = "test-name"
	ds.ObjectMeta.Namespace = "test-namespace"
	MutateAggregatedSpec(
		ds,
		nil,
		nil,
		nil,
		dsTemplate,
	)
	assert.Equalf(t, "test-name", ds.ObjectMeta.Name, "ObjectMeta.Name should not be overwritten when CreationTimestamp is set")
	assert.Equalf(t, "test-namespace", ds.ObjectMeta.Namespace, "ObjectMeta.Namespace should not be overwritten when CreationTimestamp is set")
}
