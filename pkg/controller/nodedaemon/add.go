// Package nodedaemon implements the controller that manages:
// - local-static-provisioner daemon, configmap
// - diskmaker-manager: a controller-runtime manager that runs on each node with a controller for LocalVolumeSet objects
//   that match that node
package nodedaemon

import (
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	controllerName = "localvolumesetdaemon-controller"
)

// AddDaemonReconciler adds a new Controller to mgr with r as the reconcile.Reconciler
// this controller manages creation and scheduling of the diskmaker manager and provisioner daemonsets
func AddDaemonReconciler(mgr manager.Manager) error {
	r := &DaemonReconciler{client: mgr.GetClient(), scheme: mgr.GetScheme()}
	// Create a new controller
	c, err := controller.New(controllerName, mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// The controller will ignore the name part of the enqueued request as
	// every reconcile gathers multiple resources an acts on a few one-per-namespace obects.
	enqueueOnlyNamespace := &handler.EnqueueRequestsFromMapFunc{
		ToRequests: handler.ToRequestsFunc(func(obj handler.MapObject) []reconcile.Request {
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: obj.Meta.GetNamespace()},
			}
			return []reconcile.Request{req}
		}),
	}

	// Watch for changes to primary resource LocalVolumeSet
	err = c.Watch(&source.Kind{Type: &localv1alpha1.LocalVolumeSet{}}, enqueueOnlyNamespace)
	if err != nil {
		return err
	}

	// watch provisioner, diskmaker-manager daemonsets
	err = c.Watch(&source.Kind{Type: &appsv1.DaemonSet{}}, enqueueOnlyNamespace, common.EnqueueOnlyLabeledSubcomponents(DiskMakerName, ProvisionerName))
	if err != nil {
		return err
	}

	// watch provisioner configmap
	err = c.Watch(&source.Kind{Type: &corev1.ConfigMap{}}, enqueueOnlyNamespace, common.EnqueueOnlyLabeledSubcomponents(ProvisionerConfigMapName))
	if err != nil {
		return err
	}

	return nil
}
