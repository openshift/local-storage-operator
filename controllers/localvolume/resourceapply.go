package localvolume

import (
	"context"

	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	storageclientv1 "k8s.io/client-go/kubernetes/typed/storage/v1"
)

// ApplyStorageclass
func applyStorageClass(ctx context.Context, client storageclientv1.StorageClassesGetter, required *storagev1.StorageClass) (*storagev1.StorageClass, bool, error) {
	existing, err := client.StorageClasses().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		actual, err := client.StorageClasses().Create(ctx, required, metav1.CreateOptions{})
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}

	changed := false
	resourcemerge.EnsureObjectMeta(&changed, &existing.ObjectMeta, required.ObjectMeta)

	if !equality.Semantic.DeepEqual(required.MountOptions, existing.MountOptions) {
		changed = true
		existing.MountOptions = required.MountOptions
	}

	if !equality.Semantic.DeepEqual(existing.AllowedTopologies, required.AllowedTopologies) {
		changed = true
		existing.AllowedTopologies = required.AllowedTopologies
	}

	if !changed {
		return existing, false, nil
	}
	actual, err := client.StorageClasses().Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}
