package lvset

import (
	"fmt"
	"time"

	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
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
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	// sig-local-static-provisioner libs
	provCache "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/cache"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
	provDeleter "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/deleter"
	provUtil "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/util"
)

const (
	// ComponentName for lvset symlinker
	ComponentName = "localvolumeset-symlink-controller"

	pvOwnerKey = "pvOwner"
)

var log = logf.Log.WithName(ComponentName)

// Add adds a new nodeside lvset controller to mgr
func Add(mgr manager.Manager) error {

	clientSet := provCommon.SetupClient()

	runtimeConfig := &provCommon.RuntimeConfig{
		UserConfig: &provCommon.UserConfig{
			Node: &corev1.Node{},
		},
		Cache:    provCache.NewVolumeCache(),
		VolUtil:  provUtil.NewVolumeUtil(),
		APIUtil:  provUtil.NewAPIUtil(clientSet),
		Client:   clientSet,
		Recorder: mgr.GetEventRecorderFor(ComponentName),
		Mounter:  mount.New("" /* defaults to /bin/mount */),
		// InformerFactory: , // unused

	}
	cleanupTracker := &provDeleter.CleanupStatusTracker{ProcTable: provDeleter.NewProcTable()}
	clock := &wallTime{}
	crClient := mgr.GetClient()
	r := &ReconcileLocalVolumeSet{
		client:         crClient,
		scheme:         mgr.GetScheme(),
		nodeName:       getNodeNameEnvVar(),
		eventReporter:  newEventReporter(mgr.GetEventRecorderFor(ComponentName)),
		deviceAgeMap:   newAgeMap(clock),
		cleanupTracker: cleanupTracker,
		runtimeConfig:  runtimeConfig,
		deleter:        provDeleter.NewDeleter(runtimeConfig, cleanupTracker),
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

	err = c.Watch(&source.Kind{Type: &localv1alpha1.LocalVolumeSet{}}, &handler.EnqueueRequestForObject{}, predicate.GenerationChangedPredicate{})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource Pods and requeue the owner LocalVolumeSet
	err = c.Watch(&source.Kind{Type: &corev1.ConfigMap{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &localv1alpha1.LocalVolumeSet{},
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

	return err
}

func handlePVChange(runtimeConfig *provCommon.RuntimeConfig, pv *corev1.PersistentVolume, q workqueue.RateLimitingInterface, isDelete bool) {
	// skip non-owned PVs
	name, found := pv.Annotations[provCommon.AnnProvisionedBy]
	if !found || name != runtimeConfig.Name {
		return
	}

	// update cache
	if isDelete {
		removePV(runtimeConfig, *pv)
	} else {
		addOrUpdatePV(runtimeConfig, *pv)
	}

	// enqueue owner
	ownerName, found := pv.Labels[common.PVOwnerNameLabel]
	if !found {
		return
	}
	ownerNamespace, found := pv.Labels[common.PVOwnerNameLabel]
	if !found {
		return
	}
	q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: ownerName, Namespace: ownerNamespace}})
	if isDelete {
		time.Sleep(time.Second * 10)
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: ownerName, Namespace: ownerNamespace}})
	}
}

// blank assignment to verify that ReconcileLocalVolumeSet implements reconcile.Reconciler

// ReconcileLocalVolumeSet reconciles a LocalVolumeSet object
type ReconcileLocalVolumeSet struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client        client.Client
	scheme        *runtime.Scheme
	eventReporter *eventReporter
	nodeName      string
	// map from KNAME of device to time when the device was first observed since the process started
	deviceAgeMap *ageMap

	// static-provisioner stuff
	cleanupTracker *provDeleter.CleanupStatusTracker
	runtimeConfig  *provCommon.RuntimeConfig
	deleter        *provDeleter.Deleter
	firstRunOver   bool
}

var _ reconcile.Reconciler = &ReconcileLocalVolumeSet{}

func getProvisionedByValue(node corev1.Node) string {
	return fmt.Sprintf("local-volume-provisioner-%v-%v", node.Name, node.UID)
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
