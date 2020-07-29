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
	lvSetMap := &lvSetMapStore{}

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

	// Watch for changes to secondary resource PersistentVolume and requeue the LocalVolumeSet
	err = c.Watch(&source.Kind{Type: &corev1.PersistentVolume{}}, &handler.EnqueueRequestsFromMapFunc{
		ToRequests: handler.ToRequestsFunc(func(obj handler.MapObject) []reconcile.Request {
			pv, ok := obj.Object.(*corev1.PersistentVolume)
			if !ok {
				return []reconcile.Request{}
			}
			names := lvSetMap.getLocalVolumeSets(pv.Spec.StorageClassName)
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

	return nil
}
