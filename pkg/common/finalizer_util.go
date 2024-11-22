package common

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetOwnedPVs returns a list of PV's owned by the object
func GetOwnedPVs(obj runtime.Object, c client.Client) ([]corev1.PersistentVolume, error) {
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return nil, fmt.Errorf("could not get object metadata accessor from obj: %+v", obj)
	}

	name := accessor.GetName()
	namespace := accessor.GetNamespace()
	kind := obj.GetObjectKind().GroupVersionKind().Kind
	if len(name) == 0 || len(namespace) == 0 || len(kind) == 0 {
		return nil, fmt.Errorf("name: %q, namespace: %q, or  kind: %q is empty for obj: %+v", name, namespace, kind, obj)
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
		return nil, fmt.Errorf("failed to list persistent volumes: %w", err)
	}
	return pvList.Items, nil
}

// ReleaseAvailablePVs releases available PV's owned by the object.
func ReleaseAvailablePVs(obj runtime.Object, c client.Client) error {
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return fmt.Errorf("could not get object metadata accessor from obj: %+v", obj)
	}

	name := accessor.GetName()
	namespace := accessor.GetNamespace()
	kind := obj.GetObjectKind().GroupVersionKind().Kind
	if len(name) == 0 || len(namespace) == 0 || len(kind) == 0 {
		return fmt.Errorf("name: %q, namespace: %q, or  kind: %q is empty for obj: %+v", name, namespace, kind, obj)
	}

	// fetch PVs that match the owner
	pvList := &corev1.PersistentVolumeList{}
	ownerSelector := client.MatchingLabels{
		PVOwnerKindLabel:      kind,
		PVOwnerNameLabel:      name,
		PVOwnerNamespaceLabel: namespace,
	}
	err = c.List(context.TODO(), pvList, ownerSelector)
	if err != nil {
		return fmt.Errorf("failed to list persistent volumes: %w", err)
	}

	deletionBlocked := false
	for _, pv := range pvList.Items {
		if pv.Spec.PersistentVolumeReclaimPolicy == corev1.PersistentVolumeReclaimRetain {
			klog.Infof("PV %s has Retain policy, blocking deletion of %s object %s/%s", pv.Name, kind, name, namespace)
			deletionBlocked = true
			continue
		}
		if pv.Status.Phase == corev1.VolumeAvailable && pv.Spec.ClaimRef == nil {
			klog.InfoS("Releasing unbound available PV", "pvName", pv.Name)
			pvCopy := pv.DeepCopy()
			// Set ClaimRef to a non-existing object so KCM will set
			// pv.Status.Phase to the Released state.
			uid := uuid.NewString()
			pvCopy.Spec.ClaimRef = &corev1.ObjectReference{
				UID:       types.UID(uid),
				Namespace: namespace,
				Name:      "release-" + uid,
			}
			err := c.Update(context.TODO(), pvCopy)
			if err != nil {
				return fmt.Errorf("failed to set ClaimRef for persistent volume %s: %w", pv.Name, err)
			}
		}
	}
	// If one or more PV's had the Retain policy, we still release Available PV's
	// but report an error here to generate an event and try again later.
	if deletionBlocked {
		return fmt.Errorf("%s object %s/%s has persistent volumes with Retain reclaim policy blocking deletion", kind, namespace, name)
	}
	return nil
}
