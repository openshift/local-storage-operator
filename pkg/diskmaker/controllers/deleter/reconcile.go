package deleter

import (
	"context"
	"fmt"
	"time"

	"github.com/openshift/local-storage-operator/pkg/common"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
	provDeleter "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/deleter"
)

const ComponentName = "deleter"

var watchNamespace string
var nodeName string

func init() {
	nodeName = common.GetNodeNameEnvVar()
	watchNamespace, _ = common.GetWatchNamespace()
}

// Reconcile reads that state of the cluster for a LocalVolumeSet object and makes changes based on the state read
// and what is in the LocalVolumeSet.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *DeleteReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	klog.InfoS("Looking for released PVs to cleanup", "namespace", request.Namespace, "name", request.Name)

	err := common.ReloadRuntimeConfig(ctx, r.Client, request, nodeName, r.runtimeConfig)
	if err != nil {
		return ctrl.Result{}, err
	}

	// initialize the pv cache
	// initialize the deleter's pv cache on the first run
	if !r.firstRunOver {
		klog.InfoS("first run, initializing PV cache", "provisionerName", r.runtimeConfig.Name)
		pvList := &corev1.PersistentVolumeList{}
		err = r.Client.List(context.TODO(), pvList)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to initialize PV cache: %w", err)
		}

		for _, pv := range pvList.Items {
			// skip non-owned PVs
			if !common.PVMatchesProvisioner(pv, r.runtimeConfig.Name) {
				continue
			}
			addOrUpdatePV(r.runtimeConfig, pv)
		}

		r.firstRunOver = true
	}

	r.deleter.DeletePVs()
	return ctrl.Result{RequeueAfter: time.Second * 30}, nil
}

func addOrUpdatePV(r *provCommon.RuntimeConfig, pv corev1.PersistentVolume) {
	_, exists := r.Cache.GetPV(pv.GetName())
	if exists {
		r.Cache.UpdatePV(&pv)
	} else {
		r.Cache.AddPV(&pv)
	}
}

type DeleteReconciler struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	Client         client.Client
	Scheme         *runtime.Scheme
	cleanupTracker *provDeleter.CleanupStatusTracker
	runtimeConfig  *provCommon.RuntimeConfig
	deleter        *provDeleter.Deleter
	firstRunOver   bool
}

func NewDeleteReconciler(client client.Client, cleanupTracker *provDeleter.CleanupStatusTracker, rc *provCommon.RuntimeConfig) *DeleteReconciler {
	deleter := provDeleter.NewDeleter(rc, cleanupTracker)

	deleteReconciler := &DeleteReconciler{
		Client:        client,
		runtimeConfig: rc,
		deleter:       deleter,
	}

	return deleteReconciler
}

func (r *DeleteReconciler) WithManager(mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		// set to 1 explicitly, despite it being the default, as the reconciler is not thread-safe.
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		// Watch config maps with the deleter config
		For(&corev1.ConfigMap{}, builder.WithPredicates(common.EnqueueOnlyLabeledSubcomponents(common.ProvisionerConfigMapName))).
		// update owned-pv cache used by provisioner/deleter libs and enequeue owning lvset
		// only the cache is touched by
		Watches(&corev1.PersistentVolume{}, handler.Funcs{
			GenericFunc: func(ctx context.Context, e event.GenericEvent, q workqueue.RateLimitingInterface) {
				pv, ok := e.Object.(*corev1.PersistentVolume)
				if ok {
					handlePVChange(r.runtimeConfig, pv, q, false)
				}
			},
			CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.RateLimitingInterface) {
				pv, ok := e.Object.(*corev1.PersistentVolume)
				if ok {
					handlePVChange(r.runtimeConfig, pv, q, false)
				}
			},
			UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.RateLimitingInterface) {
				pv, ok := e.ObjectNew.(*corev1.PersistentVolume)
				if ok {
					handlePVChange(r.runtimeConfig, pv, q, false)
				}
			},
			DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.RateLimitingInterface) {
				pv, ok := e.Object.(*corev1.PersistentVolume)
				if ok {
					handlePVChange(r.runtimeConfig, pv, q, true)
				}
			},
		}).
		Complete(r)

	return err

}

func handlePVChange(runtimeConfig *provCommon.RuntimeConfig, pv *corev1.PersistentVolume, q workqueue.RateLimitingInterface, isDelete bool) {

	// if provisioner name is not known, enqueue to initialize the cache and discover provisioner name
	if runtimeConfig.Name == "" {
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Namespace: watchNamespace}})
		return
	}
	// skip non-owned PVs
	if !common.PVMatchesProvisioner(*pv, runtimeConfig.Name) {
		return
	}
	// enqueue only if the provisioner name matches,
	// or the first run hasn't happened yet, and we don't know the provisioner name to compare it to

	// update cache
	if isDelete {
		removePV(runtimeConfig, *pv)
	} else {
		addOrUpdatePV(runtimeConfig, *pv)
	}
	if pv.Status.Phase == corev1.VolumeReleased {
		klog.InfoS("found PV with state released", "pvName", pv.Name)
	}

	// enqueue owner
	_, found := pv.Labels[common.PVOwnerNameLabel]
	if !found {
		return
	}
	ownerNamespace, found := pv.Labels[common.PVOwnerNamespaceLabel]
	if !found {
		return
	}
	q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ownerNamespace}})
	if isDelete {
		// Don't block the informer goroutine.
		go func() {
			time.Sleep(time.Second * 10)
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ownerNamespace}})
		}()
	}
}

func removePV(r *provCommon.RuntimeConfig, pv corev1.PersistentVolume) {
	_, exists := r.Cache.GetPV(pv.GetName())
	if exists {
		r.Cache.DeletePV(pv.Name)
	}
}
