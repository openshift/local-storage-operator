package nodedaemon

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// ProvisionerName is the name of the local-static-provisioner daemonset
	ProvisionerName = "local-provisioner"
	// DiskMakerName is the name of the diskmaker-manager daemonset
	DiskMakerName = "diskmaker-manager"

	dataHashAnnotationKey = "local.storage.openshift.io/configMapDataHash"
)

var log = logf.Log.WithName(controllerName)

// blank assignment to verify that DaemonReconciler implements reconcile.Reconciler
var _ reconcile.Reconciler = &DaemonReconciler{}

// DaemonReconciler reconciles all LocalVolumeSet objects at once
type DaemonReconciler struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client                   client.Client
	scheme                   *runtime.Scheme
	reqLogger                logr.Logger
	deletedStaticProvisioner bool
}

// Reconcile reads that state of the cluster for a LocalVolumeSet object and makes changes based on the state read
// and what is in the LocalVolumeSet.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *DaemonReconciler) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	r.reqLogger = logf.Log.WithName(controllerName).WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)

	// do a one-time delete of the old static-provisioner daemonset
	err := r.cleanupOldDaemonsets(request.Namespace)
	if err != nil {
		return reconcile.Result{}, err
	}

	lvSets, lvs, tolerations, ownerRefs, nodeSelector, err := r.aggregateDeamonInfo(request)
	if err != nil {
		return reconcile.Result{}, err
	}
	if len(lvSets.Items) < 1 && len(lvs.Items) < 1 {
		return reconcile.Result{}, nil
	}

	configMap, opResult, err := r.reconcileProvisionerConfigMap(request, lvSets.Items, lvs.Items, ownerRefs)
	if err != nil {
		return reconcile.Result{}, err
	} else if opResult == controllerutil.OperationResultUpdated || opResult == controllerutil.OperationResultCreated {
		r.reqLogger.Info("provisioner configmap changed")
	}

	configMapDataHash := dataHash(configMap.Data)

	diskMakerDSMutateFn := getDiskMakerDSMutateFn(request, tolerations, ownerRefs, nodeSelector, configMapDataHash)
	ds, opResult, err := CreateOrUpdateDaemonset(r.client, diskMakerDSMutateFn)
	if err != nil {
		return reconcile.Result{}, err
	} else if opResult == controllerutil.OperationResultUpdated || opResult == controllerutil.OperationResultCreated {
		r.reqLogger.Info("daemonset changed", "daemonset.Name", ds.GetName(), "op.Result", opResult)
	}

	localProvisionerDSMutateFn := getLocalProvisionerDSMutateFn(request, tolerations, ownerRefs, nodeSelector, configMapDataHash)
	ds, opResult, err = CreateOrUpdateDaemonset(r.client, localProvisionerDSMutateFn)
	if err != nil {
		return reconcile.Result{}, err
	} else if opResult == controllerutil.OperationResultUpdated || opResult == controllerutil.OperationResultCreated {
		r.reqLogger.Info("daemonset changed", "daemonset.Name", ds.GetName(), "op.Result", opResult)
	}

	return reconcile.Result{}, err
}

// do a one-time delete of the old static-provisioner daemonset
func (r *DaemonReconciler) cleanupOldDaemonsets(namespace string) error {
	if r.deletedStaticProvisioner {
		return nil
	}
	provisioner := &appsv1.DaemonSet{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: ProvisionerName, Namespace: namespace}, provisioner)
	if err == nil { // provisioner daemonset found
		r.reqLogger.Info(fmt.Sprintf("old daemonset %q found, cleaning up", ProvisionerName))
		err = r.client.Delete(context.TODO(), provisioner)
		if err != nil && !errors.IsGone(err) {
			r.reqLogger.Error(err, fmt.Sprintf("could not delete daemonset %q", ProvisionerName))
			return err
		}
	} else if !errors.IsNotFound(err) { // unknown error
		r.reqLogger.Error(err, fmt.Sprintf("could not fetch daemonset %q to clean it up", ProvisionerName))
		return err
	}

	// wait for pods to die
	err = wait.ExponentialBackoff(wait.Backoff{
		Cap:      time.Minute * 2,
		Duration: time.Second,
		Factor:   1.7,
		Jitter:   1,
		Steps:    20,
	}, func() (done bool, err error) {
		podList := &corev1.PodList{}
		allGone := false
		err = r.client.List(context.TODO(), podList, client.MatchingLabels{"app": ProvisionerName})
		if err != nil && !errors.IsNotFound(err) {
			return false, err
		} else if len(podList.Items) == 0 {
			allGone = true
		}
		r.reqLogger.Info(fmt.Sprintf("waiting for 0 pods with label app : %q", ProvisionerName), "numberFound", len(podList.Items))
		return allGone, nil
	})
	if err != nil {
		r.reqLogger.Error(err, "could not determine that old provisioner pods were deleted")
		return err
	}
	r.deletedStaticProvisioner = true
	return nil
}
