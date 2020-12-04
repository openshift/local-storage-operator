package localvolumeset

import (
	"context"
	"fmt"

	operatorv1 "github.com/openshift/api/operator/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/controller/nodedaemon"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func (r *LocalVolumeSetReconciler) updateDaemonSetsCondition(request reconcile.Request) error {
	var diskMakerMessage string
	diskMakerFound := true
	conditionType := DaemonSetsAvailableAndConfigured
	conditionStatus := operatorv1.ConditionTrue
	diskMakerDS := &appsv1.DaemonSet{}
	key := types.NamespacedName{Name: nodedaemon.DiskMakerName, Namespace: request.Namespace}
	err := r.client.Get(context.TODO(), key, diskMakerDS)
	if kerrors.IsNotFound(err) {
		diskMakerFound = false
	} else if err != nil {
		return fmt.Errorf("failed to get %q: %w", key, err)
	}
	if !diskMakerFound {
		diskMakerMessage = "Not found."
		conditionStatus = operatorv1.ConditionFalse
	} else if diskMakerDS.Status.NumberUnavailable > 0 {
		diskMakerMessage = fmt.Sprintf("%d/%d Unavailable.", diskMakerDS.Status.NumberUnavailable, diskMakerDS.Status.CurrentNumberScheduled)
		conditionStatus = operatorv1.ConditionFalse
	} else {
		diskMakerMessage = "Available"
	}
	conditionMessage := fmt.Sprintf("DiskMaker: %s", diskMakerMessage)

	lvSet := &localv1alpha1.LocalVolumeSet{}
	err = r.client.Get(context.TODO(), request.NamespacedName, lvSet)
	if err != nil {
		if kerrors.IsNotFound(err) {
			r.lvSetMap.deregisterLocalVolumeSet(lvSet.Spec.StorageClassName, request.NamespacedName)
			return nil
		}
		return fmt.Errorf("failed to get localvolumeset: %w", err)
	}

	changed := SetCondition(&lvSet.Status.Conditions, conditionType, conditionMessage, conditionStatus)
	if changed {
		err := r.client.Status().Update(context.TODO(), lvSet)
		if err != nil {
			r.reqLogger.Error(err, "failed to update localvolumeset condition", conditionType, conditionStatus, "message", conditionMessage)
			return err
		}
	}

	return nil
}

func (r *LocalVolumeSetReconciler) updateTotalProvisionedDeviceCountStatus(request reconcile.Request) error {

	lvSet := &localv1alpha1.LocalVolumeSet{}
	err := r.client.Get(context.TODO(), request.NamespacedName, lvSet)
	if err != nil {
		if kerrors.IsNotFound(err) {
			r.lvSetMap.deregisterLocalVolumeSet(lvSet.Spec.StorageClassName, request.NamespacedName)
			return nil
		}
		return fmt.Errorf("failed to get localvolumeset: %w", err)
	}

	// fetch PVs that match the storageclass
	pvs := &corev1.PersistentVolumeList{}
	err = r.client.List(context.TODO(), pvs, client.MatchingFields{pvStorageClassField: lvSet.Spec.StorageClassName})
	if err != nil {
		return fmt.Errorf("failed to list persistent volumes: %w", err)
	}

	totalPVCount := int32(len(pvs.Items))
	lvSet.Status.TotalProvisionedDeviceCount = &totalPVCount
	lvSet.Status.ObservedGeneration = lvSet.Generation
	err = r.client.Status().Update(context.TODO(), lvSet)
	if err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	return nil
}

func (r *LocalVolumeSetReconciler) addAvailabilityConditions(request reconcile.Request, result reconcile.Result, reconcileError error) (reconcile.Result, error) {
	// can't set conditions if lvset can't be fetched
	lvSet := &localv1alpha1.LocalVolumeSet{}
	err := r.client.Get(context.TODO(), request.NamespacedName, lvSet)
	if err != nil {
		if kerrors.IsNotFound(err) {
			r.lvSetMap.deregisterLocalVolumeSet(lvSet.Spec.StorageClassName, request.NamespacedName)
			return result, err
		}
		return result, fmt.Errorf("failed to get localvolumeset: %w", err)
	}

	// success values
	conditionType := operatorv1.OperatorStatusTypeAvailable
	conditionStatus := operatorv1.ConditionTrue
	conditionMessage := "Operator reconciled successfully."

	// failure values
	if reconcileError != nil {
		r.reqLogger.Error(reconcileError, "reconcile error")
		conditionStatus = operatorv1.ConditionFalse
		conditionMessage = fmt.Sprintf("Operator error: %+v", reconcileError)
	}
	changed := SetCondition(&lvSet.Status.Conditions, conditionType, conditionMessage, conditionStatus)
	if changed {
		err := r.client.Status().Update(context.TODO(), lvSet)
		if err != nil {
			r.reqLogger.Error(err, "failed to update localvolumeset condition", conditionType, conditionStatus, "message", conditionMessage)
			return result, err
		}
	}
	return result, reconcileError
}
