package localvolumeset

import (
	"context"

	"github.com/go-logr/logr"
	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	LocalVolumeSetNameLabel      = "local.storage.openshift.io/localvolumeset-owner-name"
	LocalVolumeSetNamespaceLabel = "local.storage.openshift.io/localvolumeset-owner-namespace"
	lvsMetrics                   = "local-volume-set-metrics"
)

// blank assignment to verify that ReconcileLocalVolumeSet implements reconcile.Reconciler
var _ reconcile.Reconciler = &LocalVolumeSetReconciler{}

// LocalVolumeSetReconciler reconciles a LocalVolumeSet object
type LocalVolumeSetReconciler struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client    client.Client
	scheme    *runtime.Scheme
	reqLogger logr.Logger
	lvSetMap  *lvSetMapStore
}

// Reconcile reads that state of the cluster for a LocalVolumeSet object and makes changes based on the state read
// and what is in the LocalVolumeSet.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *LocalVolumeSetReconciler) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	result, err := r.reconcile(request)
	// sets conditions based on the exit status of reconcile
	return r.addAvailabilityConditions(request, result, err)
}
func (r *LocalVolumeSetReconciler) reconcile(request reconcile.Request) (reconcile.Result, error) {
	r.reqLogger = logf.Log.WithName(ComponentName).WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	r.reqLogger.Info("Reconciling LocalVolumeSet")
	// Fetch the LocalVolumeSet instance
	lvSet := &localv1alpha1.LocalVolumeSet{}
	err := r.client.Get(context.TODO(), request.NamespacedName, lvSet)
	if err != nil {
		if kerrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			r.lvSetMap.deregisterLocalVolumeSet(lvSet.Spec.StorageClassName, request.NamespacedName)
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		r.reqLogger.Error(err, "failed to get localvolumeset")
		return reconcile.Result{}, err
	}

	// store a one to many association from storageClass to LocalVolumeSet
	r.lvSetMap.registerLocalVolumeSet(lvSet.Spec.StorageClassName, request.NamespacedName)

	// The diskmaker daemonset, local-staic-provisioner daemonset and configmap are created in pkg/daemon
	// this way, there can be one daemonset for all LocalVolumeSets

	err = r.syncStorageClass(lvSet)
	if err != nil {
		r.reqLogger.Error(err, "failed to sync storageclass")
		return reconcile.Result{}, err
	}
	r.reqLogger.Info("updating status")

	err = r.updateDaemonSetsCondition(request)
	if err != nil {
		r.reqLogger.Error(err, "failed to update status")
		return reconcile.Result{}, err
	}

	err = r.updateTotalProvisionedDeviceCountStatus(request)
	if err != nil {
		r.reqLogger.Error(err, "failed to update status")
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *LocalVolumeSetReconciler) syncStorageClass(lvs *localv1alpha1.LocalVolumeSet) error {
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

	err := r.client.Create(context.TODO(), storageClass)
	if err != nil && !kerrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}
