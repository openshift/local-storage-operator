package localvolumediscovery

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	operatorv1 "github.com/openshift/api/operator/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/controller/nodedaemon"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	corev1helper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var (
	log                             = logf.Log.WithName("controller_localvolumediscovery")
	waitForRequeueIfDaemonsNotReady = reconcile.Result{Requeue: true, RequeueAfter: 10 * time.Second}
)

const (
	DiskMakerDiscovery = "diskmaker-discovery"
)

// Add creates a new LocalVolumeDiscovery Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileLocalVolumeDiscovery{
		client:    mgr.GetClient(),
		scheme:    mgr.GetScheme(),
		reqLogger: log,
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("localvolumediscovery-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource LocalVolumeDiscovery
	err = c.Watch(&source.Kind{Type: &localv1alpha1.LocalVolumeDiscovery{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for the child resources
	err = c.Watch(&source.Kind{Type: &appsv1.DaemonSet{}}, &handler.EnqueueRequestForOwner{OwnerType: &localv1alpha1.LocalVolumeDiscovery{}})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileLocalVolumeDiscovery implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileLocalVolumeDiscovery{}

// ReconcileLocalVolumeDiscovery reconciles a LocalVolumeDiscovery object
type ReconcileLocalVolumeDiscovery struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client    client.Client
	scheme    *runtime.Scheme
	reqLogger logr.Logger
}

// Reconcile reads that state of the cluster for a LocalVolumeDiscovery object and makes changes based on the state read
// and what is in the LocalVolumeDiscovery.Spec
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileLocalVolumeDiscovery) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := r.reqLogger.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling LocalVolumeDiscovery")

	// Fetch the LocalVolumeDiscovery instance
	instance := &localv1alpha1.LocalVolumeDiscovery{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	diskMakerDSMutateFn := getDiskMakerDiscoveryDSMutateFn(request, instance.Spec.Tolerations,
		getEnvVars(instance.Name, string(instance.UID)),
		getOwnerRefs(instance),
		instance.Spec.NodeSelector)
	ds, opResult, err := nodedaemon.CreateOrUpdateDaemonset(r.client, diskMakerDSMutateFn)
	if err != nil {
		message := fmt.Sprintf("failed to create discovery daemonset. Error %+v", err)
		err := r.updateDiscoveryStatus(instance, operatorv1.OperatorStatusTypeDegraded, message,
			operatorv1.ConditionFalse, localv1alpha1.DiscoveryFailed)
		if err != nil {
			return reconcile.Result{}, err
		}
	} else if opResult == controllerutil.OperationResultUpdated || opResult == controllerutil.OperationResultCreated {
		reqLogger.Info("daemonset changed", "daemonset.Name", ds.GetName(), "op.Result", opResult)
	}

	desiredDaemons, readyDaemons, err := r.getDaemonSetStatus(instance.Namespace)
	if err != nil {
		reqLogger.Error(err, "failed to get discovery daemonset")
		return reconcile.Result{}, err
	}

	if desiredDaemons == 0 {
		message := "no discovery daemons are scheduled for running"
		err := r.updateDiscoveryStatus(instance, operatorv1.OperatorStatusTypeDegraded, message,
			operatorv1.ConditionFalse, localv1alpha1.DiscoveryFailed)
		if err != nil {
			return reconcile.Result{}, err
		}
		return waitForRequeueIfDaemonsNotReady, fmt.Errorf(message)

	} else if !(desiredDaemons == readyDaemons) {
		message := fmt.Sprintf("running %d out of %d discovery daemons", readyDaemons, desiredDaemons)
		err := r.updateDiscoveryStatus(instance, operatorv1.OperatorStatusTypeProgressing, message,
			operatorv1.ConditionFalse, localv1alpha1.Discovering)
		if err != nil {
			return reconcile.Result{}, err
		}
		return waitForRequeueIfDaemonsNotReady, fmt.Errorf(message)
	}

	message := fmt.Sprintf("successfully running %d out of %d discovery daemons", desiredDaemons, readyDaemons)
	err = r.updateDiscoveryStatus(instance, operatorv1.OperatorStatusTypeAvailable, message,
		operatorv1.ConditionTrue, localv1alpha1.Discovering)
	if err != nil {
		return reconcile.Result{}, err
	}

	reqLogger.Info("deleting orphan discovery result instances")
	err = r.deleteOrphanDiscoveryResults(instance)
	if err != nil {
		reqLogger.Error(err, "failed to delete orphan discovery results")
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func getDiskMakerDiscoveryDSMutateFn(request reconcile.Request,
	tolerations []corev1.Toleration,
	envVars []corev1.EnvVar,
	ownerRefs []metav1.OwnerReference,
	nodeSelector *corev1.NodeSelector) func(*appsv1.DaemonSet) error {

	return func(ds *appsv1.DaemonSet) error {
		name := DiskMakerDiscovery

		nodedaemon.MutateAggregatedSpec(
			ds,
			request,
			tolerations,
			ownerRefs,
			nodeSelector,
			name,
		)
		discoveryVolumes, discoveryVolumeMounts := getVolumesAndMounts()
		ds.Spec.Template.Spec.Volumes = discoveryVolumes
		ds.Spec.Template.Spec.Containers[0].VolumeMounts = discoveryVolumeMounts
		ds.Spec.Template.Spec.Containers[0].Env = append(ds.Spec.Template.Spec.Containers[0].Env, envVars...)
		ds.Spec.Template.Spec.Containers[0].Image = common.GetDiskMakerImage()
		ds.Spec.Template.Spec.Containers[0].Args = []string{"discover"}
		ds.Spec.Template.Spec.HostPID = true

		return nil
	}
}

// updateDiscoveryStatus updates the discovery state with conditions and phase
func (r *ReconcileLocalVolumeDiscovery) updateDiscoveryStatus(instance *localv1alpha1.LocalVolumeDiscovery,
	conditionType, message string,
	status operatorv1.ConditionStatus,
	phase localv1alpha1.DiscoveryPhase) error {
	// avoid frequently updating the same status in the CR
	if len(instance.Status.Conditions) < 1 || instance.Status.Conditions[0].Message != message {
		condition := operatorv1.OperatorCondition{
			Type:               conditionType,
			Status:             status,
			Message:            message,
			LastTransitionTime: metav1.Now(),
		}
		newConditions := []operatorv1.OperatorCondition{condition}
		instance.Status.Conditions = newConditions
		instance.Status.Phase = phase
		instance.Status.ObservedGeneration = instance.Generation
		err := r.updateStatus(instance)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *ReconcileLocalVolumeDiscovery) deleteOrphanDiscoveryResults(instance *localv1alpha1.LocalVolumeDiscovery) error {
	if instance.Spec.NodeSelector == nil || len(instance.Spec.NodeSelector.NodeSelectorTerms) == 0 {
		r.reqLogger.Info("skip deleting orphan discovery results as no NodeSelectors are provided")
		return nil
	}

	discoveryResultList := &localv1alpha1.LocalVolumeDiscoveryResultList{}
	err := r.client.List(context.TODO(), discoveryResultList, client.InNamespace(instance.Namespace))
	if err != nil {
		return fmt.Errorf("failed to list LocalVolumeDiscoveryResult instances in namespace %q", instance.Namespace)
	}

	for _, discoveryResult := range discoveryResultList.Items {
		nodeName := discoveryResult.Spec.NodeName
		node := &corev1.Node{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: nodeName}, node)
		if err != nil {
			return fmt.Errorf("failed to get instance of node %q", nodeName)
		}

		matches := corev1helper.MatchNodeSelectorTerms(instance.Spec.NodeSelector.NodeSelectorTerms,
			node.Labels, fields.Set{
				"metadata.name": node.Name,
			})
		if !matches {
			err := r.client.Delete(context.TODO(), discoveryResult.DeepCopyObject())
			if err != nil {
				return fmt.Errorf("failed to delete orphan discovery result %q in node %q", discoveryResult.Name, nodeName)
			}
		}
	}

	return nil
}

func (r *ReconcileLocalVolumeDiscovery) updateStatus(lvd *localv1alpha1.LocalVolumeDiscovery) error {
	err := r.client.Status().Update(context.TODO(), lvd)
	if err != nil {
		return err
	}

	return nil
}

func (r *ReconcileLocalVolumeDiscovery) getDaemonSetStatus(namespace string) (int32, int32, error) {
	existingDS := &appsv1.DaemonSet{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: DiskMakerDiscovery, Namespace: namespace}, existingDS)
	if err != nil {
		return 0, 0, err
	}

	return existingDS.Status.DesiredNumberScheduled, existingDS.Status.NumberReady, nil
}

func getVolumesAndMounts() ([]corev1.Volume, []corev1.VolumeMount) {
	volumes := []corev1.Volume{
		common.SymlinkHostDirVolume,
		common.DevHostDirVolume,
		common.UDevHostDirVolume,
	}
	volumeMounts := []corev1.VolumeMount{
		common.SymlinkMount,
		common.DevMount,
		common.UDevMount,
	}
	return volumes, volumeMounts
}

func getOwnerRefs(cr *localv1alpha1.LocalVolumeDiscovery) []metav1.OwnerReference {
	trueVal := true
	return []metav1.OwnerReference{
		{
			APIVersion: localv1alpha1.SchemeGroupVersion.String(),
			Kind:       "LocalVolumeDiscovery",
			Name:       cr.Name,
			UID:        cr.UID,
			Controller: &trueVal,
		},
	}
}

func getEnvVars(objName, uid string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{
			Name:  "DISCOVERY_OBJECT_UID",
			Value: uid,
		},
		{
			Name:  "DISCOVERY_OBJECT_NAME",
			Value: objName,
		},
	}
}
