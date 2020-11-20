package nodedaemon

import (
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// ProvisionerName is the name of the local-static-provisioner daemonset
	ProvisionerName = "localvolumeset-local-provisioner"
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
	client    client.Client
	scheme    *runtime.Scheme
	reqLogger logr.Logger
}

// Reconcile reads that state of the cluster for a LocalVolumeSet object and makes changes based on the state read
// and what is in the LocalVolumeSet.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *DaemonReconciler) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	r.reqLogger = logf.Log.WithName(controllerName).WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)

	lvSets, tolerations, ownerRefs, nodeSelector, err := r.aggregateDeamonInfo(request)
	if err != nil {
		return reconcile.Result{}, err
	}
	if len(lvSets.Items) < 1 {
		return reconcile.Result{}, nil
	}

	configMap, opResult, err := r.reconcileProvisionerConfigMap(request, lvSets.Items, ownerRefs)
	if err != nil {
		return reconcile.Result{}, err
	} else if opResult == controllerutil.OperationResultUpdated || opResult == controllerutil.OperationResultCreated {
		r.reqLogger.Info("provisioner configmap changed")
	}

	// hash the configMap data to include in daemonsets and ensure they update when data changes
	configMapDataHash := dataHash(configMap.Data)

	diskMakerDSMutateFn := getDiskMakerDSMutateFn(request, tolerations, ownerRefs, nodeSelector, configMapDataHash)
	ds, opResult, err := CreateOrUpdateDaemonset(r.client, diskMakerDSMutateFn)
	if err != nil {
		return reconcile.Result{}, err
	} else if opResult == controllerutil.OperationResultUpdated || opResult == controllerutil.OperationResultCreated {
		r.reqLogger.Info("daemonset changed", "daemonset.Name", ds.GetName(), "op.Result", opResult)
	}

	return reconcile.Result{}, nil
}
