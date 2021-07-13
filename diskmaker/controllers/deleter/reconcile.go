package deleter

import (
	"context"
	"fmt"
	"time"

	"github.com/openshift/local-storage-operator/common"
	"github.com/prometheus/common/log"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/mount"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	provCache "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/cache"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
	staticProvisioner "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
	provDeleter "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/deleter"
	provUtil "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/util"
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

	reqLogger := logf.Log.WithName(ComponentName).WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Looking for released PVs to clean up")
	// enqueue if cache is not initialized
	// and if any pv has phase == Releaseds

	// get associated provisioner config
	cm := &corev1.ConfigMap{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: common.ProvisionerConfigMapName, Namespace: request.Namespace}, cm)
	if err != nil {
		reqLogger.Error(err, "could not get provisioner configmap")
		return ctrl.Result{}, err
	}

	// read provisioner config
	provisionerConfig := staticProvisioner.ProvisionerConfiguration{}
	staticProvisioner.ConfigMapDataToVolumeConfig(cm.Data, &provisionerConfig)

	r.runtimeConfig.DiscoveryMap = provisionerConfig.StorageClassConfig
	r.runtimeConfig.NodeLabelsForPV = provisionerConfig.NodeLabelsForPV
	r.runtimeConfig.Namespace = request.Namespace
	r.runtimeConfig.SetPVOwnerRef = provisionerConfig.SetPVOwnerRef

	// ignored by our implementation of static-provisioner,
	// but not by deleter (if applicable)
	r.runtimeConfig.UseNodeNameOnly = provisionerConfig.UseNodeNameOnly
	r.runtimeConfig.MinResyncPeriod = provisionerConfig.MinResyncPeriod
	r.runtimeConfig.UseAlphaAPI = provisionerConfig.UseAlphaAPI
	r.runtimeConfig.LabelsForPV = provisionerConfig.LabelsForPV

	// initialize the pv cache
	// initialize the deleter's pv cache on the first run
	if !r.firstRunOver {
		r.runtimeConfig.Node = &corev1.Node{}
		err = r.Client.Get(ctx, types.NamespacedName{Name: nodeName}, r.runtimeConfig.Node)
		if err != nil {
			return ctrl.Result{}, err
		}
		r.runtimeConfig.Name = common.GetProvisionedByValue(*r.runtimeConfig.Node)
		reqLogger.Info("first run", "provisionerName", r.runtimeConfig.Name)
		reqLogger.Info("initializing PV cache")
		pvList := &corev1.PersistentVolumeList{}
		err := r.Client.List(context.TODO(), pvList)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to initialize PV cache: %w", err)
		}
		for _, pv := range pvList.Items {
			// skip non-owned PVs
			name, found := pv.Annotations[provCommon.AnnProvisionedBy]
			if !found || name != r.runtimeConfig.Name {
				continue
			}
			addOrUpdatePV(r.runtimeConfig, pv)
		}

		r.firstRunOver = true
	}

	reqLogger.Info("Deleting Pvs through sig storage deleter")
	r.deleter.DeletePVs(common.GetCleanPVSymlinkFunc(r.runtimeConfig))
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

func (r *DeleteReconciler) SetupWithManager(mgr ctrl.Manager, cleanupTracker *provDeleter.CleanupStatusTracker, pvCache *provCache.VolumeCache) error {

	clientSet := provCommon.SetupClient()
	runtimeConfig := &provCommon.RuntimeConfig{
		UserConfig: &provCommon.UserConfig{
			Node: &corev1.Node{},
		},
		Cache:    pvCache,
		VolUtil:  provUtil.NewVolumeUtil(),
		APIUtil:  provUtil.NewAPIUtil(clientSet),
		Client:   clientSet,
		Recorder: mgr.GetEventRecorderFor(ComponentName),
		Mounter:  mount.New("" /* defaults to /bin/mount */),
		// InformerFactory: , // unused

	}

	r.runtimeConfig = runtimeConfig
	r.deleter = &provDeleter.Deleter{
		RuntimeConfig: runtimeConfig,
		CleanupStatus: cleanupTracker,
	}
	return ctrl.NewControllerManagedBy(mgr).
		// set to 1 explicitly, despite it being the default, as the reconciler is not thread-safe.
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		For(&corev1.PersistentVolume{}).
		// update owned-pv cache used by provisioner/deleter libs and enequeue owning lvset
		// only the cache is touched by
		Watches(&source.Kind{Type: &corev1.PersistentVolume{}}, handler.Funcs{
			GenericFunc: func(e event.GenericEvent, q workqueue.RateLimitingInterface) {
				pv, ok := e.Object.(*corev1.PersistentVolume)
				if ok {
					handlePVChange(runtimeConfig, pv, q, false)
				}
			},
			CreateFunc: func(e event.CreateEvent, q workqueue.RateLimitingInterface) {
				pv, ok := e.Object.(*corev1.PersistentVolume)
				if ok {
					handlePVChange(runtimeConfig, pv, q, false)
				}
			},
			UpdateFunc: func(e event.UpdateEvent, q workqueue.RateLimitingInterface) {
				pv, ok := e.ObjectNew.(*corev1.PersistentVolume)
				if ok {
					handlePVChange(runtimeConfig, pv, q, false)
				}
			},
			DeleteFunc: func(e event.DeleteEvent, q workqueue.RateLimitingInterface) {
				pv, ok := e.Object.(*corev1.PersistentVolume)
				if ok {
					handlePVChange(runtimeConfig, pv, q, true)
				}
			},
		}).
		Complete(r)
}

func handlePVChange(runtimeConfig *provCommon.RuntimeConfig, pv *corev1.PersistentVolume, q workqueue.RateLimitingInterface, isDelete bool) {

	// if provisioner name is not known, enqueue to initialize the cache and discover provisioner name
	if runtimeConfig.Name == "" {
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Namespace: watchNamespace}})
		return
	}
	// skip non-owned PVs
	annotations := pv.GetAnnotations()
	name, found := annotations[provCommon.AnnProvisionedBy]
	// enqueue only if the proviiosner name matches,
	// or the first run hasn't happened yet and e don't know the provisioner name to compare it to
	if !found || name != runtimeConfig.Name {
		return
	}

	// update cache
	if isDelete {
		removePV(runtimeConfig, *pv)
	} else {
		addOrUpdatePV(runtimeConfig, *pv)
	}
	if pv.Status.Phase == corev1.VolumeReleased {
		log.Info("found PV with state released", "pvName", pv.Name)
	}

	// enqueue owner
	_, found = pv.Labels[common.PVOwnerNameLabel]
	if !found {
		return
	}
	ownerNamespace, found := pv.Labels[common.PVOwnerNamespaceLabel]
	if !found {
		return
	}
	q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ownerNamespace}})
	if isDelete {
		time.Sleep(time.Second * 10)
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ownerNamespace}})
	}
}

func removePV(r *provCommon.RuntimeConfig, pv corev1.PersistentVolume) {
	_, exists := r.Cache.GetPV(pv.GetName())
	if exists {
		r.Cache.DeletePV(pv.Name)
	}
}
