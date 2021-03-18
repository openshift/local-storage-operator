package lv

import (
	"time"

	"github.com/openshift/local-storage-operator/pkg/apis"
	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
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

	//event "github.com/openshift/local-storage-operator/pkg/diskmaker"

	// sig-local-static-provisioner libs
	provCache "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/cache"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"

	provDeleter "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/deleter"
	provUtil "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/util"
)

const (
	// ComponentName for lv symlinker
	ComponentName = "localvolume-symlink-controller"
)

var log = logf.Log.WithName(ComponentName)

// Add adds a new nodeside lv controller to mgr
func Add(mgr manager.Manager, cleanupTracker *provDeleter.CleanupStatusTracker, pvCache *provCache.VolumeCache) error {
	apis.AddToScheme(mgr.GetScheme())
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

	r := &ReconcileLocalVolume{
		client:          mgr.GetClient(),
		scheme:          mgr.GetScheme(),
		eventSync:       newEventReporter(mgr.GetEventRecorderFor(ComponentName)),
		symlinkLocation: common.GetLocalDiskLocationPath(),
		cleanupTracker:  cleanupTracker,
		runtimeConfig:   runtimeConfig,
		deleter:         provDeleter.NewDeleter(runtimeConfig, cleanupTracker),
	}
	// Create a new controller
	//	apis.AddToScheme(r.scheme)
	c, err := controller.New(ComponentName, mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	err = c.Watch(&source.Kind{Type: &corev1.ConfigMap{}}, &handler.EnqueueRequestForOwner{
		OwnerType: &localv1.LocalVolume{},
	})

	err = c.Watch(&source.Kind{Type: &localv1.LocalVolume{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// TODO enqueue for the PV based on labels

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
	// skip non-owned PVs
	name, found := pv.Annotations[provCommon.AnnProvisionedBy]
	if !found || name != runtimeConfig.Name {
		return
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
	ownerKind, found := pv.Labels[common.PVOwnerKindLabel]
	if ownerKind != localv1.LocalVolumeKind || !found {
		return
	}

	if isDelete {
		// delayed reconcile so that the cleanup tracker has time to mark the PV cleaned up
		time.Sleep(time.Second * 10)
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: ownerName, Namespace: ownerNamespace}})
	} else {
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: ownerName, Namespace: ownerNamespace}})
	}

}

type ReconcileLocalVolume struct {
	client          client.Client
	scheme          *runtime.Scheme
	symlinkLocation string
	localVolume     *localv1.LocalVolume
	eventSync       *eventReporter

	// static-provisioner stuff
	cleanupTracker *provDeleter.CleanupStatusTracker
	runtimeConfig  *provCommon.RuntimeConfig
	deleter        *provDeleter.Deleter
	firstRunOver   bool
}

var _ reconcile.Reconciler = &ReconcileLocalVolume{}
