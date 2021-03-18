package deleter

import (
	"time"

	"github.com/openshift/local-storage-operator/pkg/common"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/kubernetes/pkg/util/mount"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	// sig-local-static-provisioner libs
	provCache "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/cache"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
	provDeleter "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/deleter"
	provUtil "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/util"
)

const ComponentName = "deleter"

var log = logf.Log.WithName(ComponentName)

var watchNamespace string
var nodeName string

func init() {
	nodeName = common.GetNodeNameEnvVar()
	watchNamespace = common.GetWatchNameSpaceEnfVar()
}

type ReconcileDeleter struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client         client.Client
	scheme         *runtime.Scheme
	cleanupTracker *provDeleter.CleanupStatusTracker
	runtimeConfig  *provCommon.RuntimeConfig
	deleter        *provDeleter.Deleter
	firstRunOver   bool
}

func Add(mgr manager.Manager, cleanupTracker *provDeleter.CleanupStatusTracker, pvCache *provCache.VolumeCache) error {
	// populate the pv cache
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

	r := &ReconcileDeleter{
		client:        mgr.GetClient(),
		scheme:        mgr.GetScheme(),
		runtimeConfig: runtimeConfig,
		deleter: &provDeleter.Deleter{
			RuntimeConfig: runtimeConfig,
			CleanupStatus: cleanupTracker,
		},
	}

	// Create a new controller
	c, err := controller.New(ComponentName, mgr, controller.Options{
		Reconciler: r,
		// set to 1 explicitly, despite it being the default, as the reconciler is not thread-safe.
		MaxConcurrentReconciles: 1,
	})
	if err != nil {
		return err
	}

	// update owned-pv cache used by provisioner/deleter libs and enequeue owning lvset
	// only the cache is touched by
	err = c.Watch(&source.Kind{Type: &corev1.PersistentVolume{}}, &handler.Funcs{
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
	})
	if err != nil {
		return err
	}

	return nil

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

func addOrUpdatePV(r *provCommon.RuntimeConfig, pv corev1.PersistentVolume) {
	_, exists := r.Cache.GetPV(pv.GetName())
	if exists {
		r.Cache.UpdatePV(&pv)
	} else {
		r.Cache.AddPV(&pv)
	}
}

func removePV(r *provCommon.RuntimeConfig, pv corev1.PersistentVolume) {
	_, exists := r.Cache.GetPV(pv.GetName())
	if exists {
		r.Cache.DeletePV(pv.Name)
	}
}
