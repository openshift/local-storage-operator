package lv

import (
	"github.com/openshift/local-storage-operator/pkg/apis"
	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	//event "github.com/openshift/local-storage-operator/pkg/diskmaker"
)

const (
	// ComponentName for lv symlinker
	ComponentName = "localvolume-symlink-controller"
)

var log = logf.Log.WithName(ComponentName)

// Add adds a new nodeside lv controller to mgr
func Add(mgr manager.Manager) error {
	apis.AddToScheme(mgr.GetScheme())

	r := &ReconcileLocalVolume{
		client:          mgr.GetClient(),
		scheme:          mgr.GetScheme(),
		eventSync:       newEventReporter(mgr.GetEventRecorderFor(ComponentName)),
		symlinkLocation: "/mnt/local-storage",
	}
	// Create a new controller
	//	apis.AddToScheme(r.scheme)
	c, err := controller.New(ComponentName, mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	err = c.Watch(&source.Kind{Type: &corev1.ConfigMap{}}, &handler.EnqueueRequestForOwner{
		OwnerType: &localv1.LocalVolume{},
	})

	err = c.Watch(&source.Kind{Type: &localv1.LocalVolume{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

type ReconcileLocalVolume struct {
	client          client.Client
	scheme          *runtime.Scheme
	symlinkLocation string
	localVolume     *localv1.LocalVolume
	eventSync       *eventReporter
}

var _ reconcile.Reconciler = &ReconcileLocalVolume{}
