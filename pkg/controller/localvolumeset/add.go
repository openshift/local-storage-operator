// Package localvolumeset implements the two controllers for localvolumesets
// LocalVolumeSetReconciler reconciles LocalVolumeSets and
package localvolumeset

import (
	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/controller/nodedaemon"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	ComponentName       = "localvolumeset-controller"
	pvStorageClassField = "spec.storageClassName"
)

// AddLocalVolumeSetReconciler adds a new Controller to mgr with r as the reconcile.Reconciler
// this controller creates the child objects for the localvolumset CR
func AddLocalVolumeSetReconciler(mgr manager.Manager) error {

	// an association from storageclass to localvolumesets
	lvSetMap := &common.StorageClassOwnerMap{}

	r := &LocalVolumeSetReconciler{client: mgr.GetClient(), scheme: mgr.GetScheme(), lvSetMap: lvSetMap}
	// Create a new controller
	c, err := controller.New(ComponentName, mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Allows us to list PVs by a particular field selector. Handled and indexed by cache.
	err = mgr.GetFieldIndexer().IndexField(&corev1.PersistentVolume{}, pvStorageClassField, func(o runtime.Object) []string {
		pv := o.(*corev1.PersistentVolume)
		storageClassName := pv.Spec.StorageClassName
		if len(storageClassName) > 0 {
			return []string{storageClassName}
		}
		return []string{}
	})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource LocalVolumeSet
	err = c.Watch(&source.Kind{Type: &localv1alpha1.LocalVolumeSet{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// watch provisioner, diskmaker-manager daemonsets and enqueue owning object to update status.conditions
	err = c.Watch(&source.Kind{Type: &appsv1.DaemonSet{}}, &handler.EnqueueRequestForOwner{OwnerType: &localv1alpha1.LocalVolumeSet{}}, common.EnqueueOnlyLabeledSubcomponents(nodedaemon.DiskMakerName, nodedaemon.ProvisionerName))
	if err != nil {
		return err
	}

	//  watch for storageclass, enqueue owner
	err = c.Watch(&source.Kind{Type: &corev1.PersistentVolume{}}, &handler.EnqueueRequestsFromMapFunc{
		ToRequests: handler.ToRequestsFunc(func(obj handler.MapObject) []reconcile.Request {
			pv, ok := obj.Object.(*corev1.PersistentVolume)
			if !ok {
				return []reconcile.Request{}
			}

			names := lvSetMap.GetStorageClassOwners(pv.Spec.StorageClassName)
			reqs := make([]reconcile.Request, 0)
			for _, name := range names {
				reqs = append(reqs, reconcile.Request{NamespacedName: name})
			}
			return reqs
		}),
	})
	if err != nil {
		return err
	}

	// Watch for changes to owned resource PersistentVolume and enqueue the LocalVolumeSet
	// so that the controller can update the status and finalizer(TODO) based on the owned PVs
	err = c.Watch(&source.Kind{Type: &corev1.PersistentVolume{}}, &handler.EnqueueRequestsFromMapFunc{
		ToRequests: handler.ToRequestsFunc(func(obj handler.MapObject) []reconcile.Request {

			pv, ok := obj.Object.(*corev1.PersistentVolume)
			if !ok {
				return []reconcile.Request{}
			}

			// get owner
			ownerName, found := pv.Labels[common.PVOwnerNameLabel]
			if !found {
				return []reconcile.Request{}
			}
			ownerNamespace, found := pv.Labels[common.PVOwnerNamespaceLabel]
			if !found {
				return []reconcile.Request{}
			}

			// skip LocalVolume owned PVs
			ownerKind, found := pv.Labels[common.PVOwnerKindLabel]
			if ownerKind != localv1alpha1.LocalVolumeSetKind || !found {
				return []reconcile.Request{}
			}
			req := reconcile.Request{NamespacedName: types.NamespacedName{Name: ownerName, Namespace: ownerNamespace}}

			return []reconcile.Request{req}
		}),
	})
	if err != nil {
		return err
	}

	return nil
}
