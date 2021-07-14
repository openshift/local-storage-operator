package nodedaemon

import (
	"testing"

	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/local-storage-operator/assets"
	"github.com/openshift/local-storage-operator/common"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestMutateAggregatedSpecTemplates(t *testing.T) {
	// Generate DaemonSet template by reading yaml asset
	dsBytes, err := assets.ReadFileAndReplace(
		common.LocalProvisionerDaemonSetTemplate,
		[]string{
			"${OBJECT_NAMESPACE}", "test-namespace",
			"${CONTAINER_IMAGE}", common.GetLocalProvisionerImage(),
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

	// In the template, DeprecatedServiceAccount is set automatically based
	// on the ServiceAccountName defined in the template. Since this is
	// expected, we set DeprecatedServiceAccount to the same value in ds.
	// This line can be removed when DeprecatedServiceAccount is removed.
	ds.Spec.Template.Spec.DeprecatedServiceAccount = ds.Spec.Template.Spec.ServiceAccountName
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
