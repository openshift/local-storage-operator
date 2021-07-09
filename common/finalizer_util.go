package common

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ContainsFinalizer checks if finalizer is found in metadata
func ContainsFinalizer(metadata metav1.ObjectMeta, finalizer string) bool {
	for _, f := range metadata.Finalizers {
		if f == finalizer {
			return true
		}
	}
	return false
}

// GetBoundAndReleasedPVs owned by the object
func GetBoundAndReleasedPVs(obj runtime.Object, c client.Client) ([]corev1.PersistentVolume, []corev1.PersistentVolume, error) {
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return nil, nil, fmt.Errorf("could not get object metadata accessor from obj: %+v", obj)
	}

	name := accessor.GetName()
	namespace := accessor.GetNamespace()
	kind := obj.GetObjectKind().GroupVersionKind().Kind
	if len(name) == 0 || len(namespace) == 0 || len(kind) == 0 {
		return nil, nil, fmt.Errorf("name: %q, namespace: %q, or  kind: %q is empty for obj: %+v", name, namespace, kind, obj)
	}

	// fetch PVs that match the LocalVolumeSet
	pvList := &corev1.PersistentVolumeList{}
	ownerSelector := client.MatchingLabels{
		PVOwnerKindLabel:      kind,
		PVOwnerNameLabel:      name,
		PVOwnerNamespaceLabel: namespace,
	}
	err = c.List(context.TODO(), pvList, ownerSelector)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list persistent volumes: %w", err)
	}
	var boundPVs = make([]corev1.PersistentVolume, 0, len(pvList.Items))
	var releasedPVs = make([]corev1.PersistentVolume, 0, len(pvList.Items))

	for _, pv := range pvList.Items {
		if pv.Status.Phase == corev1.VolumeBound {
			boundPVs = append(boundPVs, pv)
		} else if pv.Status.Phase == corev1.VolumeReleased {
			releasedPVs = append(releasedPVs, pv)
		}
	}
	return boundPVs, releasedPVs, nil
}
