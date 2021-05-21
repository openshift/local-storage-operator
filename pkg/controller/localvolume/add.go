package localvolume

import (
	"k8s.io/apimachinery/pkg/types"

	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	"github.com/openshift/local-storage-operator/pkg/common"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	componentName       = "local-storage-operator"
	localDiskLocation   = "/mnt/local-storage"
	ownerNamespaceLabel = "local.storage.openshift.io/owner-namespace"
	ownerNameLabel      = "local.storage.openshift.io/owner-name"

	localVolumeFinalizer = "storage.openshift.com/local-volume-protection"
)

// ReconcileLocalVolume reconciles a LocalVolume object
type ReconcileLocalVolume struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client            client.Client
	apiClient         apiUpdater
	lvMap             *common.StorageClassOwnerMap
	controllerVersion string
}

// Add creates a LocalVolume Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {

	r := &ReconcileLocalVolume{
		client:    mgr.GetClient(),
		apiClient: newAPIUpdater(mgr),
		lvMap:     &common.StorageClassOwnerMap{},
	}

	// create a new controller
	c, err := controller.New("localvolume-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}
	err = c.Watch(&source.Kind{Type: &localv1.LocalVolume{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	err = c.Watch(&source.Kind{Type: &appsv1.DaemonSet{}}, &handler.EnqueueRequestForOwner{OwnerType: &localv1.LocalVolume{}})
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

			names := r.lvMap.GetStorageClassOwners(pv.Spec.StorageClassName)
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

	// Watch for changes to owned resource PersistentVolume and enqueue the LocalVolume
	// so that the controller can update the status and finalizer based on the owned PVs
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

			// skip LocalVolumeSet owned PVs
			ownerKind, found := pv.Labels[common.PVOwnerKindLabel]
			if ownerKind != localv1.LocalVolumeKind || !found {
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
