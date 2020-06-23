package controller

import (
	"github.com/openshift/local-storage-operator/pkg/controller/nodedaemon"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, nodedaemon.AddDaemonReconciler)
}
