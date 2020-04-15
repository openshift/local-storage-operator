package lvset

import (
	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_localvolumeset")

// Add adds a new nodeside lvset controller to mgr
func Add(mgr manager.Manager) error {
	r := &ReconcileLocalVolumeSet{client: mgr.GetClient(), scheme: mgr.GetScheme()}
	// Create a new controller
	c, err := controller.New("localvolumeset-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource LocalVolumeSet
	err = c.Watch(&source.Kind{Type: &localv1alpha1.LocalVolumeSet{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	// Watch for changes to secondary resource Pods and requeue the owner LocalVolumeSet
	err = c.Watch(&source.Kind{Type: &corev1.ConfigMap{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &localv1alpha1.LocalVolumeSet{},
	})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileLocalVolumeSet implements reconcile.Reconciler

// ReconcileLocalVolumeSet reconciles a LocalVolumeSet object
type ReconcileLocalVolumeSet struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

var _ reconcile.Reconciler = &ReconcileLocalVolumeSet{}
