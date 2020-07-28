package controller

import (
	localv1 "github.com/openshift/local-storage-operator/pkg/controller/localvolume"
	"github.com/openshift/local-storage-operator/pkg/controller/localvolumeset"
	"github.com/openshift/local-storage-operator/pkg/controller/nodedaemon"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// AddToManagerFuncs is a list of functions to add all Controllers to the Manager
var AddToManagerFuncs = []func(manager.Manager) error{
	localv1.Add,
	localvolumeset.AddLocalVolumeSetReconciler,
	nodedaemon.AddDaemonReconciler,
}

// AddToManager adds all Controllers to the Manager
func AddToManager(m manager.Manager) error {
	for _, f := range AddToManagerFuncs {
		if err := f(m); err != nil {
			return err
		}
	}
	return nil
}
