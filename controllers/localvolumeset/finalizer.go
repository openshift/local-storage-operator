package localvolumeset

import (
	"context"
	"fmt"

	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/common"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *LocalVolumeSetReconciler) cleanupLocalVolumeSetDeployment(ctx context.Context, lvSet localv1alpha1.LocalVolumeSet) error {
	lvSetExisting := &localv1alpha1.LocalVolumeSet{}
	lvSet.DeepCopyInto(lvSetExisting)

	r.Log.Info("deletionTimeStamp found, waiting for 0 bound PVs")
	// if obect is deleted, finalizer should be unset only when no boundPVs are found
	boundPVs, releasedPVs, err := common.GetBoundAndReleasedPVs(&lvSet, r.Client)
	if err != nil {
		return fmt.Errorf("could not list bound PVs: %w", err)
	}

	// if we add support for other reclaimPolicy's we can avoid appending releasedPVs here only bound PVs
	pendingPVs := append(boundPVs, releasedPVs...)
	if len(pendingPVs) == 0 {
		err := r.removeUnExpectedStorageClasses(ctx, &lvSet, sets.NewString())
		if err != nil {
			return err
		}
		r.Log.Info("no bound/released PVs found, removing finalizer")
		err = r.syncFinalizer(ctx, lvSet, !setFinalizer)
	} else {
		pvNames := ""
		for i, pv := range pendingPVs {
			pvNames += fmt.Sprintf(" %v", pv.Name)
			// only print up to 10 PV names
			if i >= 9 {
				pvNames += "..."
				break
			}
		}
		r.Log.Info("bound/released PVs found, not removing finalizer", "pvNames", pvNames)
		err = r.syncFinalizer(ctx, lvSet, setFinalizer)
	}

	return err

}
func (r *LocalVolumeSetReconciler) syncFinalizer(ctx context.Context, lvSet localv1alpha1.LocalVolumeSet, setFinalizer bool) error {
	lvSetExisting := &localv1alpha1.LocalVolumeSet{}
	lvSet.DeepCopyInto(lvSetExisting)

	if setFinalizer {
		controllerutil.AddFinalizer(&lvSet, common.LocalVolumeProtectionFinalizer)
	} else {
		controllerutil.RemoveFinalizer(&lvSet, common.LocalVolumeProtectionFinalizer)
	}

	// update finalizer in lvset and make client call only if the value changed
	if !equality.Semantic.DeepEqual(lvSetExisting, lvSet) {
		return r.Client.Update(ctx, &lvSet)
	}

	return nil

}
