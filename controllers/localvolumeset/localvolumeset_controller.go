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

	"github.com/go-logr/logr"
	"github.com/openshift/local-storage-operator/common"
	"github.com/openshift/local-storage-operator/controllers/nodedaemon"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
)

const (
	ComponentName                = "localvolumeset-controller"
	pvStorageClassField          = "spec.storageClassName"
	LocalVolumeSetNameLabel      = "local.storage.openshift.io/localvolumeset-owner-name"
	LocalVolumeSetNamespaceLabel = "local.storage.openshift.io/localvolumeset-owner-namespace"
)

// LocalVolumeSetReconciler reconciles a LocalVolumeSet object
type LocalVolumeSetReconciler struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	Client    client.Client
	Scheme    *runtime.Scheme
	ReqLogger logr.Logger
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
	r.ReqLogger.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	r.ReqLogger.Info("Reconciling LocalVolumeSet")
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
		r.ReqLogger.Error(err, "failed to get localvolumeset")
		return ctrl.Result{}, err
	}

	// store a one to many association from storageClass to LocalVolumeSet
	r.LvSetMap.RegisterStorageClassOwner(lvSet.Spec.StorageClassName, request.NamespacedName)

	// handle the LocalVolumeSet finalizer
	err = r.syncFinalizer(*lvSet)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to update localvolumeset finalizer: %w", err)
	}

	// The diskmaker daemonset, local-staic-provisioner daemonset and configmap are created in pkg/daemon
	// this way, there can be one daemonset for all LocalVolumeSets

	err = r.syncStorageClass(ctx, lvSet)
	if err != nil {
		r.ReqLogger.Error(err, "failed to sync storageclass")
		return ctrl.Result{}, err
	}
	r.ReqLogger.Info("updating status")

	err = r.updateDaemonSetsCondition(ctx, request)
	if err != nil {
		r.ReqLogger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	err = r.updateTotalProvisionedDeviceCountStatus(ctx, request)
	if err != nil {
		r.ReqLogger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *LocalVolumeSetReconciler) syncStorageClass(ctx context.Context, lvs *localv1alpha1.LocalVolumeSet) error {
	deleteReclaimPolicy := corev1.PersistentVolumeReclaimDelete
	firstConsumerBinding := storagev1.VolumeBindingWaitForFirstConsumer
	storageClass := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:      lvs.Spec.StorageClassName,
			Namespace: lvs.GetNamespace(),
			Labels: map[string]string{
				common.OwnerNameLabel:      lvs.GetName(),
				common.OwnerNamespaceLabel: lvs.GetNamespace(),
			},
		},
		Provisioner:       "kubernetes.io/no-provisioner",
		ReclaimPolicy:     &deleteReclaimPolicy,
		VolumeBindingMode: &firstConsumerBinding,
	}

	err := r.Client.Create(ctx, storageClass)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *LocalVolumeSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
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
		Watches(&source.Kind{Type: &appsv1.DaemonSet{}}, &handler.EnqueueRequestForOwner{OwnerType: &localv1alpha1.LocalVolumeSet{}}, builder.WithPredicates(common.EnqueueOnlyLabeledSubcomponents(nodedaemon.DiskMakerName, nodedaemon.ProvisionerName))).
		//  watch for storageclass, enqueue owner
		Watches(&source.Kind{Type: &corev1.PersistentVolume{}}, handler.EnqueueRequestsFromMapFunc(
			func(obj client.Object) []reconcile.Request {
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
		Watches(&source.Kind{Type: &corev1.PersistentVolume{}}, handler.EnqueueRequestsFromMapFunc(
			func(obj client.Object) []reconcile.Request {

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
