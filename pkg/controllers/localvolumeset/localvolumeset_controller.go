/*
Copyright 2021 The Local Storage Operator Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package localvolumeset

import (
	"context"
	"fmt"

	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/controllers/nodedaemon"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ComponentName       = "localvolumeset-controller"
	pvStorageClassField = "spec.storageClassName"
)

// LocalVolumeSetReconciler reconciles a LocalVolumeSet object
type LocalVolumeSetReconciler struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	Client    client.Client
	apiClient apiUpdater
	Scheme    *runtime.Scheme
	LvSetMap  *common.StorageClassOwnerMap
}

// Reconcile reads that state of the cluster for a LocalVolumeSet object and makes changes based on the state read
// and what is in the LocalVolumeSet.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *LocalVolumeSetReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	result, err := r.reconcile(ctx, request)
	// sets conditions based on the exit status of reconcile
	return r.addAvailabilityConditions(ctx, request, result, err)
}

func (r *LocalVolumeSetReconciler) reconcile(ctx context.Context, request reconcile.Request) (ctrl.Result, error) {
	klog.InfoS("Reconciling LocalVolumeSet", "namespace", request.Namespace, "name", request.Name)
	// Fetch the LocalVolumeSet instance
	lvSet := &localv1alpha1.LocalVolumeSet{}
	err := r.Client.Get(ctx, request.NamespacedName, lvSet)
	if err != nil {
		if kerrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			r.LvSetMap.DeregisterStorageClassOwner(lvSet.Spec.StorageClassName, request.NamespacedName)
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		klog.ErrorS(err, "failed to get localvolumeset")
		return ctrl.Result{}, err
	}

	// store a one to many association from storageClass to LocalVolumeSet
	r.LvSetMap.RegisterStorageClassOwner(lvSet.Spec.StorageClassName, request.NamespacedName)

	// handle the LocalVolumeSet finalizer
	err = r.syncFinalizer(lvSet)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update localvolumeset finalizer: %w", err)
	}

	// The diskmaker daemonset, local-staic-provisioner daemonset and configmap are created in pkg/daemon
	// this way, there can be one daemonset for all LocalVolumeSets

	err = r.syncStorageClass(ctx, lvSet)
	if err != nil {
		klog.ErrorS(err, "failed to sync storageclass")
		return ctrl.Result{}, err
	}
	klog.Info("updating status")

	err = r.updateDaemonSetsCondition(ctx, request)
	if err != nil {
		klog.ErrorS(err, "failed to set condition")
		return ctrl.Result{}, err
	}

	err = r.updateTotalProvisionedDeviceCountStatus(ctx, request)
	if err != nil {
		klog.ErrorS(err, "failed to update status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *LocalVolumeSetReconciler) syncStorageClass(ctx context.Context, lvs *localv1alpha1.LocalVolumeSet) error {

	// remove storageClass if the finalizer is removed
	if !controllerutil.ContainsFinalizer(lvs, common.LocalVolumeProtectionFinalizer) {
		removeError := r.removeStorageClass(ctx, lvs)
		if removeError != nil {
			klog.ErrorS(removeError, "error removing storageClass", "scName", lvs.Spec.StorageClassName)
		}
		// do not block on storageClass removal
		return nil
	}

	deleteReclaimPolicy := corev1.PersistentVolumeReclaimDelete
	firstConsumerBinding := storagev1.VolumeBindingWaitForFirstConsumer
	storageClass := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: lvs.Spec.StorageClassName,
			Labels: map[string]string{
				common.OwnerNameLabel:      lvs.GetName(),
				common.OwnerNamespaceLabel: lvs.GetNamespace(),
				common.OwnerKindLabel:      localv1alpha1.LocalVolumeSetKind,
			},
		},
		Provisioner:       "kubernetes.io/no-provisioner",
		ReclaimPolicy:     &deleteReclaimPolicy,
		VolumeBindingMode: &firstConsumerBinding,
	}
	if _, _, err := r.apiClient.applyStorageClass(ctx, storageClass); err != nil {
		return fmt.Errorf("error syncing StorageClass %s: %w", storageClass.Name, err)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *LocalVolumeSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.apiClient = newAPIUpdater(mgr)
	// Allows us to list PVs by a particular field selector. Handled and indexed by cache.
	err := mgr.GetFieldIndexer().IndexField(context.TODO(), &corev1.PersistentVolume{}, pvStorageClassField,
		func(o client.Object) []string {
			pv := o.(*corev1.PersistentVolume)
			storageClassName := pv.Spec.StorageClassName
			if len(storageClassName) > 0 {
				return []string{storageClassName}
			}
			return []string{}
		})
	if err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&localv1alpha1.LocalVolumeSet{}).
		// watch provisioner, diskmaker-manager daemonsets and enqueue owning object to update status.conditions
		Watches(&appsv1.DaemonSet{}, handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), &localv1alpha1.LocalVolumeSet{}), builder.WithPredicates(common.EnqueueOnlyLabeledSubcomponents(nodedaemon.DiskMakerName, nodedaemon.ProvisionerName))).
		//  watch for storageclass, enqueue owner
		Watches(&corev1.PersistentVolume{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				pv, ok := obj.(*corev1.PersistentVolume)
				if !ok {
					return []reconcile.Request{}
				}
				names := r.LvSetMap.GetStorageClassOwners(pv.Spec.StorageClassName)
				reqs := make([]reconcile.Request, 0)
				for _, name := range names {
					reqs = append(reqs, reconcile.Request{NamespacedName: name})
				}
				return reqs
			})).
		// Watch for changes to owned resource PersistentVolume and enqueue the LocalVolumeSet
		// so that the controller can update the status and finalizer(TODO) based on the owned PVs
		Watches(&corev1.PersistentVolume{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {

				pv, ok := obj.(*corev1.PersistentVolume)
				if !ok {
					return []reconcile.Request{}
				}

				// get owner
				ownerName, found := pv.Labels[common.PVOwnerNameLabel]
				if !found {
					return []reconcile.Request{}
				}
				ownerNamespace, found := pv.Labels[common.PVOwnerNamespaceLabel]
				if !found {
					return []reconcile.Request{}
				}

				// skip LocalVolume owned PVs
				ownerKind, found := pv.Labels[common.PVOwnerKindLabel]
				if ownerKind != localv1alpha1.LocalVolumeSetKind || !found {
					return []reconcile.Request{}
				}
				req := reconcile.Request{NamespacedName: types.NamespacedName{Name: ownerName, Namespace: ownerNamespace}}

				return []reconcile.Request{req}
			})).
		Complete(r)
}

// removeStorageClass removes the storageClass associated with the LocalVolumeSet
func (r *LocalVolumeSetReconciler) removeStorageClass(ctx context.Context, lvs *localv1alpha1.LocalVolumeSet) error {
	klog.InfoS("removing storageClass", "storageClassName", lvs.Spec.StorageClassName)

	// Fetch the storageClass
	lvsStorageClass := &storagev1.StorageClass{}
	err := r.Client.Get(ctx, client.ObjectKey{Name: lvs.Spec.StorageClassName}, lvsStorageClass)
	if err != nil {
		if kerrors.IsNotFound(err) {
			klog.InfoS("storageClass not found, skipping deletion", "storageClassName", lvs.Spec.StorageClassName)
			return nil
		}
		return fmt.Errorf("failed to get storageClass %q for LocalVolumeSet %q: %w", lvs.Spec.StorageClassName, lvs.Name, err)
	}

	// Check whether storageClass is owned by the LocalVolumeSet
	if !isStorageClassOwnedByLocalVolumeSet(lvsStorageClass, lvs) {
		klog.InfoS("storageClass does not have matching owner labels, skipping deletion", "storageClassName", lvs.Spec.StorageClassName)
		return nil
	}

	// Delete the storageClass
	if err := r.Client.Delete(ctx, lvsStorageClass); err != nil && !kerrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete storageClass %q: %w", lvs.Spec.StorageClassName, err)
	}

	klog.InfoS("Successfully deleted storageClass", "storageClassName", lvs.Spec.StorageClassName)
	return nil
}

// isStorageClassOwnedByLocalVolumeSet checks whether the StorageClass has the owner labels
func isStorageClassOwnedByLocalVolumeSet(sc *storagev1.StorageClass, lvs *localv1alpha1.LocalVolumeSet) bool {
	if sc.Labels == nil {
		return false
	}

	expectedLabels := map[string]string{
		common.OwnerNameLabel:      lvs.GetName(),
		common.OwnerNamespaceLabel: lvs.GetNamespace(),
		common.OwnerKindLabel:      localv1alpha1.LocalVolumeSetKind,
	}

	for key, expectedValue := range expectedLabels {
		if actualValue, exists := sc.Labels[key]; !exists || actualValue != expectedValue {
			klog.V(4).InfoS("StorageClass label mismatch", "key", key, "expected", expectedValue, "found", actualValue)
			return false
		}
	}
	return true
}
