package nodedaemon

import (
	"context"

	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/local-storage-operator/assets"
	"github.com/openshift/local-storage-operator/common"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// daemonsets are defined as: daemonSetMutateFn func(*appsv1.DaemonSet) error
// the function mutates whichever part of the daemonset it needs.
// if the daemonset does not exist, the mutate func will be run on an empty DaemonSet which will be created
// if it already exists, the mutate func will run on the existing daemonset and then updated
// if the mutate func does not result in any changes, no api call will be made

// createOrUpdateDaemonset fetches and creates/updates ds according to desiredDs
// fields that are not allowed update at runtime are not copied unless the creationtimestamp is zero
func CreateOrUpdateDaemonset(
	ctx context.Context,
	c client.Client,
	daemonSetMutateFn func(*appsv1.DaemonSet) error,
) (*appsv1.DaemonSet, controllerutil.OperationResult, error) {
	ds := &appsv1.DaemonSet{}
	err := daemonSetMutateFn(ds)
	if err != nil {
		return ds, controllerutil.OperationResultNone, err
	}
	mutateFn := func() error {
		return daemonSetMutateFn(ds)
	}
	opResult, err := controllerutil.CreateOrUpdate(context.TODO(), c, ds, mutateFn)
	return ds, opResult, err
}

// for a daemonset, only the following fields can be updated after creation:
// we do not simply copy over all mutable fields,
// so that we don't overwrite defaults with empty values which would result in a change each time

// ds.ObjectMeta.Labels
// ds.ObjectMeta.Annotations
// ds.ObjectMeta.OwnerReferences

// ds.Spec.Template.ObjectMeta.Annotations
// ds.Spec.Template.Spec

// ds.Spec.UpdateStrategy
// ds.Spec.MinReadySeconds
// ds.Spec.RevisionHistoryLimit

// Diskmaker Daemonset
// to be consumed by createOrUpdateDaemonset
func getDiskMakerDSMutateFn(
	request reconcile.Request,
	tolerations []corev1.Toleration,
	ownerRefs []metav1.OwnerReference,
	nodeSelector *corev1.NodeSelector,
	dataHash string,
) func(*appsv1.DaemonSet) error {

	return func(ds *appsv1.DaemonSet) error {
		// read template for default values
		dsBytes, err := assets.ReadFileAndReplace(
			common.DiskMakerManagerDaemonSetTemplate,
			[]string{
				"${OBJECT_NAMESPACE}", request.Namespace,
				"${CONTAINER_IMAGE}", common.GetDiskMakerImage(),
			},
		)
		if err != nil {
			return err
		}
		dsTemplate := resourceread.ReadDaemonSetV1OrDie(dsBytes)

		// common spec
		MutateAggregatedSpec(
			ds,
			tolerations,
			ownerRefs,
			nodeSelector,
			dsTemplate,
		)

		// add provisioner configmap hash
		initMapIfNil(&ds.ObjectMeta.Annotations)
		ds.ObjectMeta.Annotations[dataHashAnnotationKey] = dataHash

		//Add kube-rbac-proxy sidecar container to provide https proxy for http-based lso metrics.
		ds.Spec.Template.Spec.Containers = append(ds.Spec.Template.Spec.Containers, common.KubeProxySideCar())
		ds.Spec.Template.Spec.Volumes = append(ds.Spec.Template.Spec.Volumes, common.DiskmakerMetricsCertVolume)

		return nil
	}
}

// MutateAggregatedSpec returns a mutate function that applies the other arguments to the referenced daemonset
// its purpose is to be used in more specific mutate functions
// that can that be applied to an empty corev1.DaemonSet{} before a Create()
// or applied to an existing one before an Update()
func MutateAggregatedSpec(
	ds *appsv1.DaemonSet,
	tolerations []corev1.Toleration,
	ownerRefs []metav1.OwnerReference,
	nodeSelector *corev1.NodeSelector,
	dsTemplate *appsv1.DaemonSet,
) {
	// create only actions
	if ds.CreationTimestamp.IsZero() {
		// name, namespace, and labels
		ds.ObjectMeta = dsTemplate.ObjectMeta
		// daemonset selector
		ds.Spec.Selector = dsTemplate.Spec.Selector
	}

	// copy selector labels to daemonset spec
	initMapIfNil(&ds.ObjectMeta.Labels)
	for key, value := range dsTemplate.ObjectMeta.Labels {
		ds.ObjectMeta.Labels[key] = value
	}
	// copy selector labels to template spec
	initMapIfNil(&ds.Spec.Template.ObjectMeta.Labels)
	for key, value := range dsTemplate.Spec.Template.ObjectMeta.Labels {
		ds.Spec.Template.ObjectMeta.Labels[key] = value
	}
	// add management workload annotations
	initMapIfNil(&ds.Spec.Template.ObjectMeta.Annotations)
	for key, value := range dsTemplate.Spec.Template.ObjectMeta.Annotations {
		ds.Spec.Template.ObjectMeta.Annotations[key] = value
	}

	// ownerRefs
	ds.ObjectMeta.OwnerReferences = ownerRefs

	// service account
	ds.Spec.Template.Spec.ServiceAccountName = dsTemplate.Spec.Template.Spec.ServiceAccountName

	// priority class
	ds.Spec.Template.Spec.PriorityClassName = dsTemplate.Spec.Template.Spec.PriorityClassName

	// tolerations
	ds.Spec.Template.Spec.Tolerations = tolerations

	// nodeSelector if non-nil
	if nodeSelector != nil {
		ds.Spec.Template.Spec.Affinity = &corev1.Affinity{
			NodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: nodeSelector,
			},
		}
	} else {
		ds.Spec.Template.Spec.Affinity = nil
	}

	// set common volumes and mounts
	ds.Spec.Template.Spec.Volumes = dsTemplate.Spec.Template.Spec.Volumes

	// define containers
	ds.Spec.Template.Spec.Containers = dsTemplate.Spec.Template.Spec.Containers

	// setting maxUnavailable as a percentage
	ds.Spec.UpdateStrategy = dsTemplate.Spec.UpdateStrategy
	// to read /proc/1/mountinfo
	ds.Spec.Template.Spec.HostPID = dsTemplate.Spec.Template.Spec.HostPID
}

func initMapIfNil(m *map[string]string) {
	if len(*m) > 1 {
		return
	}
	*m = make(map[string]string)
	return
}
