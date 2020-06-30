package localvolumediscovery

import (
	"context"

	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/controller/nodedaemon"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_localvolumediscovery")

const (
	udevPath           = "/run/udev"
	udevVolName        = "run-udev"
	DiskMakerDiscovery = "diskmaker-discovery"
)

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new LocalVolumeDiscovery Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileLocalVolumeDiscovery{client: mgr.GetClient(), scheme: mgr.GetScheme()}
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

	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	// Watch for changes to secondary resource Pods and requeue the owner LocalVolumeDiscovery
	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &localv1alpha1.LocalVolumeDiscovery{},
	})
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
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a LocalVolumeDiscovery object and makes changes based on the state read
// and what is in the LocalVolumeDiscovery.Spec
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileLocalVolumeDiscovery) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
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

	diskMakerDSMutateFn := getDiskMakerDiscoveryDSMutateFn(request, instance.Spec.Tolerations, getOwnerRefs(instance), instance.Spec.NodeSelector)
	ds, opResult, err := nodedaemon.CreateOrUpdateDaemonset(r.client, diskMakerDSMutateFn)
	if err != nil {
		instance.Status.Phase = localv1alpha1.DiscoveryFailed
		err = r.updateStatus(instance)
		if err != nil {
			reqLogger.Error(err, "failed to update status")
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, err
	} else if opResult == controllerutil.OperationResultUpdated || opResult == controllerutil.OperationResultCreated {
		reqLogger.Info("daemonset changed", "daemonset.Name", ds.GetName(), "op.Result", opResult)
	}

	instance.Status.Phase = localv1alpha1.Discovering
	err = r.updateStatus(instance)
	if err != nil {
		reqLogger.Error(err, "failed to update status")
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func getDiskMakerDiscoveryDSMutateFn(request reconcile.Request,
	tolerations []corev1.Toleration,
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
		discoveryVolumes, discoveryVolumeMounts := getDiscoveryVolumesAndMounts()
		ds.Spec.Template.Spec.Volumes = discoveryVolumes
		ds.Spec.Template.Spec.Containers[0].VolumeMounts = discoveryVolumeMounts
		ds.Spec.Template.Spec.Containers[0].Env = append(ds.Spec.Template.Spec.Containers[0].Env, getUIDEnvVar())
		ds.Spec.Template.Spec.Containers[0].Image = common.GetDiskMakerImage()
		ds.Spec.Template.Spec.Containers[0].Args = []string{"discover"}

		return nil
	}

}

func (r *ReconcileLocalVolumeDiscovery) updateStatus(lvd *localv1alpha1.LocalVolumeDiscovery) error {
	err := r.client.Status().Update(context.TODO(), lvd)
	if err != nil {
		return err
	}

	return nil
}

func getDiscoveryVolumesAndMounts() ([]corev1.Volume, []corev1.VolumeMount) {
	hostContainerPropagation := corev1.MountPropagationHostToContainer
	volumeMounts := []corev1.VolumeMount{
		{
			Name:             "local-disks",
			MountPath:        common.GetLocalDiskLocationPath(),
			MountPropagation: &hostContainerPropagation,
		},
		{
			Name:             "device-dir",
			MountPath:        "/dev",
			MountPropagation: &hostContainerPropagation,
		},
		{
			Name:             udevVolName,
			MountPath:        udevPath,
			MountPropagation: &hostContainerPropagation,
		},
	}
	directoryHostPath := corev1.HostPathDirectory
	volumes := []corev1.Volume{
		{
			Name: "local-disks",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: common.GetLocalDiskLocationPath(),
				},
			},
		},
		{
			Name: "device-dir",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: "/dev",
					Type: &directoryHostPath,
				},
			},
		},
		{
			Name: udevVolName,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{Path: udevPath},
			},
		},
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

func getUIDEnvVar() corev1.EnvVar {
	return corev1.EnvVar{
		Name: "UID",
		ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{
				FieldPath: "metadata.uid",
			},
		},
	}
}
