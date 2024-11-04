package localvolumeset

import (
	"context"
	"fmt"

	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *LocalVolumeSetReconciler) syncFinalizer(lvSet *localv1alpha1.LocalVolumeSet) error {
	// finalizer should exist and be removed only when deleting
	setFinalizer := true

	// handle deletion
	if !lvSet.DeletionTimestamp.IsZero() {
		klog.Info("deletionTimeStamp found, waiting for 0 bound PVs")

		// Release all remaining 'Available' PV's owned by the LVS
		err := common.ReleaseAvailablePVs(lvSet, r.Client)
		if err != nil {
			msg := fmt.Sprintf("error releasing unbound persistent volumes for localvolumeset %s: %v", common.LocalVolumeSetKey(lvSet), err)
			return fmt.Errorf(msg)
		}

		// finalizer should be unset only when no owned PVs are found
		ownedPVs, err := common.GetOwnedPVs(lvSet, r.Client)
		if err != nil {
			return fmt.Errorf("could not list owned PVs: %w", err)
		}

		if len(ownedPVs) == 0 {
			setFinalizer = false
			klog.Info("no owned PVs found, removing finalizer")
		} else {
			pvNames := ""
			for i, pv := range ownedPVs {
				pvNames += fmt.Sprintf(" %v", pv.Name)
				// only print up to 10 PV names
				if i >= 9 {
					pvNames += "..."
					break
				}
			}
			klog.InfoS("owned PVs found, not removing finalizer", "pvNames", pvNames)
		}
	}

	finalizerUpdated := false
	if setFinalizer {
		finalizerUpdated = controllerutil.AddFinalizer(lvSet, common.LocalVolumeProtectionFinalizer)
	} else {
		finalizerUpdated = controllerutil.RemoveFinalizer(lvSet, common.LocalVolumeProtectionFinalizer)
	}

	// update finalizer in lvset and make client call only if the value changed
	if finalizerUpdated {
		return r.Client.Update(context.TODO(), lvSet)
	}

	return nil

}
