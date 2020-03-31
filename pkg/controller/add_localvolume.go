package controller

import (
	localv1 "github.com/openshift/local-storage-operator/pkg/controller/localvolume"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, localv1.Add)
}
