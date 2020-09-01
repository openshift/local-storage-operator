package localvolumeset

import (
	"context"
	"fmt"

	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/common"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *LocalVolumeSetReconciler) syncFinalizer(lvSet localv1alpha1.LocalVolumeSet) error {
	lvSetExisting := &localv1alpha1.LocalVolumeSet{}
	lvSet.DeepCopyInto(lvSetExisting)
	// finalizer should exist and be removed only when deleting
	setFinalizer := true

	// handle deletion
	if !lvSet.DeletionTimestamp.IsZero() {
		r.ReqLogger.Info("deletionTimeStamp found, waiting for 0 bound PVs")
		// if obect is deleted, finalizer should be unset only when no boundPVs are found
		boundPVs, releasedPVs, err := common.GetBoundAndReleasedPVs(&lvSet, r.Client)
		if err != nil {
			return fmt.Errorf("could not list bound PVs: %w", err)
		}

		// if we add support for other reclaimPolicy's we can avoid appending releasedPVs here only bound PVs
		pendingPVs := append(boundPVs, releasedPVs...)
		if len(pendingPVs) == 0 {
			setFinalizer = false
			r.ReqLogger.Info("no bound/released PVs found, removing finalizer")
		} else {
			pvNames := ""
			for _, pv := range pendingPVs {
				pvNames += fmt.Sprintf(" %v", pv.Name)
			}
			r.ReqLogger.Info("bound/released PVs found, not removing finalizer", "pvNames", pvNames)
		}
	}

	if setFinalizer {
		controllerutil.AddFinalizer(&lvSet, common.LocalVolumeProtectionFinalizer)
	} else {
		controllerutil.RemoveFinalizer(&lvSet, common.LocalVolumeProtectionFinalizer)
	}

	// update finalizer in lvset and make client call only if the value changed
	if !equality.Semantic.DeepEqual(lvSetExisting, lvSet) {
		return r.Client.Update(context.TODO(), &lvSet)
	}

	return nil

}
