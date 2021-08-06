package nodedaemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	v1 "github.com/openshift/local-storage-operator/api/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/common"
	"github.com/openshift/local-storage-operator/localmetrics"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	oldProvisionerName     = "localvolumeset-local-provisioner"
	oldLVDiskMakerPrefix   = "local-volume-diskmaker-"
	oldLVProvisionerPrefix = "local-volume-provisioner-"
	appLabelKey            = "app"
	// ProvisionerName is the name of the local-static-provisioner daemonset
	ProvisionerName = "local-provisioner"
	// DiskMakerName is the name of the diskmaker-manager daemonset
	DiskMakerName = "diskmaker-manager"

	controllerName = "localvolumesetdaemon-controller"

	dataHashAnnotationKey = "local.storage.openshift.io/configMapDataHash"
)

var log = logf.Log.WithName(controllerName)

// blank assignment to verify that DaemonReconciler implements reconcile.Reconciler
//var _ reconcile.Reconciler = &DaemonReconciler{}

// DaemonReconciler reconciles all LocalVolumeSet objects at once
type DaemonReconciler struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	Client                   client.Client
	Scheme                   *runtime.Scheme
	Log                      logr.Logger
	deletedStaticProvisioner bool
}

// Reconcile reads that state of the cluster for a LocalVolumeSet object and makes changes based on the state read
// and what is in the LocalVolumeSet.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *DaemonReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	r.Log = r.Log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	// do a one-time delete of the old static-provisioner daemonset
	err := r.cleanupOldDaemonsets(ctx, request.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	lvSets, lvs, tolerations, ownerRefs, nodeSelector, err := r.aggregateDeamonInfo(ctx, request)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(lvSets.Items) < 1 && len(lvs.Items) < 1 {
		return ctrl.Result{}, nil
	}

	configMap, opResult, err := r.reconcileProvisionerConfigMap(ctx, request, lvSets.Items, lvs.Items, ownerRefs)
	if err != nil {
		return ctrl.Result{}, err
	} else if opResult == controllerutil.OperationResultUpdated || opResult == controllerutil.OperationResultCreated {
		r.Log.Info("provisioner configmap changed")
	}

	// enable service and servicemonitor for diskmaker daemonset
	serviceLabels := map[string]string{"app": DiskMakerName}
	metricsExportor := localmetrics.NewExporter(ctx, r.Client, common.DiskMakerServiceName, request.Namespace, common.DiskMakerMetricsServingCert,
		ownerRefs, serviceLabels)
	if err := metricsExportor.EnableMetricsExporter(); err != nil {
		r.Log.Error(err, "failed to create service and servicemonitors for diskmaker daemonset")
		return ctrl.Result{}, err
	}

	configMapDataHash := dataHash(configMap.Data)

	diskMakerDSMutateFn := getDiskMakerDSMutateFn(request, tolerations, ownerRefs, nodeSelector, configMapDataHash)
	ds, opResult, err := CreateOrUpdateDaemonset(ctx, r.Client, diskMakerDSMutateFn)
	if err != nil {
		return ctrl.Result{}, err
	} else if opResult == controllerutil.OperationResultUpdated || opResult == controllerutil.OperationResultCreated {
		r.Log.Info("daemonset changed", "daemonset.Name", ds.GetName(), "op.Result", opResult)
	}

	return ctrl.Result{}, err
}

// do a one-time delete of the old static-provisioner daemonset
func (r *DaemonReconciler) cleanupOldDaemonsets(ctx context.Context, namespace string) error {
	if r.deletedStaticProvisioner {
		return nil
	}

	// search for old localvolume daemons
	dsList := &appsv1.DaemonSetList{}
	err := r.Client.List(ctx, dsList, client.InNamespace(namespace))
	if err != nil {
		r.Log.Error(err, "could not list daemonsets")
		return err
	}
	appNameList := make([]string, 0)
	for _, ds := range dsList.Items {
		appLabel, found := ds.ObjectMeta.Labels[appLabelKey]
		if !found {
			continue
		} else if strings.HasPrefix(appLabel, oldLVDiskMakerPrefix) || strings.HasPrefix(appLabel, oldLVProvisionerPrefix) {
			// remember name to watch for pods to delete
			appNameList = append(appNameList, appLabel)
			// delete daemonset
			err = r.Client.Delete(ctx, &ds)
			if err != nil && !(errors.IsNotFound(err) || errors.IsGone(err)) {
				r.Log.Error(err, "could not delete daemonset: %q", ds.Name)
				return err
			}
		}
	}

	// search for old localvolumeset daemons
	provisioner := &appsv1.DaemonSet{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: oldProvisionerName, Namespace: namespace}, provisioner)
	if err == nil { // provisioner daemonset found
		r.Log.Info(fmt.Sprintf("old daemonset %q found, cleaning up", oldProvisionerName))
		err = r.Client.Delete(ctx, provisioner)
		if err != nil && !(errors.IsNotFound(err) || errors.IsGone(err)) {
			r.Log.Error(err, fmt.Sprintf("could not delete daemonset %q", oldProvisionerName))
			return err
		}
	} else if !(errors.IsNotFound(err) || errors.IsGone(err)) { // unknown error
		r.Log.Error(err, fmt.Sprintf("could not fetch daemonset %q to clean it up", oldProvisionerName))
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
		// search for any pods with label 'app' in appNameList
		appNameList = append(appNameList, oldProvisionerName)
		requirement, err := labels.NewRequirement(appLabelKey, selection.In, appNameList)
		if err != nil {
			r.Log.Error(err, "failed to compose labelselector requirement %q in (%v)", appLabelKey, appNameList)
			return false, err
		}
		selector := labels.NewSelector().Add(*requirement)
		err = r.Client.List(context.TODO(), podList, client.MatchingLabelsSelector{Selector: selector})
		if err != nil && !errors.IsNotFound(err) {
			return false, err
		} else if len(podList.Items) == 0 {
			allGone = true
		}
		r.Log.Info(fmt.Sprintf("waiting for 0 pods with label app : %q", oldProvisionerName), "numberFound", len(podList.Items))
		return allGone, nil
	})
	if err != nil {
		r.Log.Error(err, "could not determine that old provisioner pods were deleted")
		return err
	}
	r.deletedStaticProvisioner = true
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DaemonReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// The controller will ignore the name part of the enqueued request as
	// every reconcile gathers multiple resources an acts on a few one-per-namespace objects.
	enqueueOnlyNamespace := handler.EnqueueRequestsFromMapFunc(
		func(obj client.Object) []reconcile.Request {
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: obj.GetNamespace()},
			}
			return []reconcile.Request{req}
		})

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.LocalVolume{}).
		Watches(&source.Kind{Type: &localv1alpha1.LocalVolumeSet{}}, enqueueOnlyNamespace).
		// watch provisioner, diskmaker-manager daemonsets
		Watches(&source.Kind{Type: &appsv1.DaemonSet{}}, enqueueOnlyNamespace, builder.WithPredicates(common.EnqueueOnlyLabeledSubcomponents(DiskMakerName, ProvisionerName))).
		// watch provisioner configmap
		Watches(&source.Kind{Type: &corev1.ConfigMap{}}, enqueueOnlyNamespace, builder.WithPredicates(common.EnqueueOnlyLabeledSubcomponents(common.ProvisionerConfigMapName))).
		Watches(&source.Kind{Type: &v1.LocalVolume{}}, enqueueOnlyNamespace).
		Complete(r)
}
