package diskmaker

import (
	"github.com/openshift/local-storage-operator/pkg/diskmaker/controllers/deleter"
	"github.com/openshift/local-storage-operator/pkg/diskmaker/controllers/lv"
	"github.com/openshift/local-storage-operator/pkg/diskmaker/controllers/lvset"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"sigs.k8s.io/controller-runtime/pkg/manager"
	provCache "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/cache"
	provDeleter "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/deleter"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, lvset.Add)
	AddToManagerFuncs = append(AddToManagerFuncs, lv.Add)
	AddToManagerFuncs = append(AddToManagerFuncs, deleter.Add)
}

// AddToManagerFuncs is a list of functions to add all Controllers to the Manager and pass shared resources for the static provisioner library
// The cache populated by LVS will also be read by LV
var AddToManagerFuncs []func(manager.Manager, *provDeleter.CleanupStatusTracker, *provCache.VolumeCache) error

// AddToManager adds all Controllers to the Manager
func AddToManager(m manager.Manager) error {
	for _, f := range AddToManagerFuncs {
		if err := f(m, &provDeleter.CleanupStatusTracker{ProcTable: provDeleter.NewProcTable()}, provCache.NewVolumeCache()); err != nil {
			logf.Log.Error(err, "failed to add controller")
			return err
		}
	}
	return nil
}
