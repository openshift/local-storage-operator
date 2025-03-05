package localvolumeset

import (
	"fmt"
	"testing"

	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	fakeClient "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// This file should contain all the mocking required for LocalVolumeSetReconciler to run in a unit test.

const (
	testNamespace = "default"
)

func pvSCIndexer(obj client.Object) []string {
	pv, ok := obj.(*corev1.PersistentVolume)
	if !ok {
		panic(fmt.Errorf("indexer function for type %T's spec.storageClassName field received"+
			" object of type %T, this should never happen", corev1.PersistentVolume{}, obj))
	}
	return []string{pv.Spec.StorageClassName}
}

func newFakeLocalVolumeSetReconciler(t *testing.T, objs ...runtime.Object) *LocalVolumeSetReconciler {
	scheme, err := localv1alpha1.SchemeBuilder.Build()
	assert.NoErrorf(t, err, "creating scheme")

	err = corev1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding corev1 to scheme")

	err = appsv1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding appsv1 to scheme")
	err = storagev1.AddToScheme(scheme)
	assert.NoErrorf(t, err, "adding appsv1 to scheme")

	crsWithStatus := []client.Object{
		&localv1alpha1.LocalVolumeSet{},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(crsWithStatus...).WithRuntimeObjects(objs...).WithIndex(&corev1.PersistentVolume{}, "spec.storageClassName", pvSCIndexer).Build()
	apiUpdater := newFakeAPIUpdater(fakeClient.NewSimpleClientset())

	return &LocalVolumeSetReconciler{
		Client:    client,
		apiClient: apiUpdater,
		Scheme:    scheme,
		LvSetMap:  &common.StorageClassOwnerMap{},
	}
}

func newFakeAPIUpdater(clientset kubernetes.Interface) *sdkAPIUpdater {
	return &sdkAPIUpdater{
		clientset: clientset,
	}
}
