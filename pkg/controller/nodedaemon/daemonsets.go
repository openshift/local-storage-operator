package nodedaemon

import (
	"context"
	"fmt"

	"github.com/openshift/local-storage-operator/pkg/common"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
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
	maxUnavailable := intstr.FromString("10%")

	return func(ds *appsv1.DaemonSet) error {
		name := DiskMakerName

		// common spec
		MutateAggregatedSpec(
			ds,
			request,
			tolerations,
			ownerRefs,
			nodeSelector,
			name,
		)

		// bind mount the host's "/run/udev" for `lsblk -o FSTYPE` value to be accurate
		ds.Spec.Template.Spec.Volumes = append(ds.Spec.Template.Spec.Volumes, common.UDevHostDirVolume)
		if len(ds.Spec.Template.Spec.Containers) < 1 {
			return fmt.Errorf("can't add volumeMount to container, the daemonset has not specified any containers: %+v", ds)
		}
		ds.Spec.Template.Spec.Containers[0].VolumeMounts = append(ds.Spec.Template.Spec.Containers[0].VolumeMounts, common.UDevMount)
		// add provisioner configmap hash
		initMapIfNil(&ds.ObjectMeta.Annotations)
		ds.ObjectMeta.Annotations[dataHashAnnotationKey] = dataHash
		ds.Spec.Template.Spec.Containers[0].Image = common.GetDiskMakerImage()
		ds.Spec.Template.Spec.Containers[0].ImagePullPolicy = corev1.PullIfNotPresent
		ds.Spec.Template.Spec.Containers[0].Args = []string{"lv-manager"}

		// setting maxUnavailable as a percentage
		ds.Spec.UpdateStrategy = appsv1.DaemonSetUpdateStrategy{
			Type: appsv1.RollingUpdateDaemonSetStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDaemonSet{
				MaxUnavailable: &maxUnavailable,
			},
		}
		// to read /proc/1/mountinfo
		ds.Spec.Template.Spec.HostPID = true

		return nil
	}
}

// Local Provisioner Daemonset
// to be consumed by createOrUpdateDaemonset
func getLocalProvisionerDSMutateFn(
	request reconcile.Request,
	tolerations []corev1.Toleration,
	ownerRefs []metav1.OwnerReference,
	nodeSelector *corev1.NodeSelector,
	dataHash string,
) func(*appsv1.DaemonSet) error {

	return func(ds *appsv1.DaemonSet) error {
		name := ProvisionerName

		// common spec
		MutateAggregatedSpec(
			ds,
			request,
			tolerations,
			ownerRefs,
			nodeSelector,
			name,
		)
		// add provisioner configmap hash
		initMapIfNil(&ds.ObjectMeta.Annotations)
		ds.ObjectMeta.Annotations[dataHashAnnotationKey] = dataHash
		ds.Spec.Template.Spec.Containers[0].Image = common.GetLocalProvisionerImage()
		ds.Spec.Template.Spec.Containers[0].ImagePullPolicy = corev1.PullIfNotPresent

		return nil
	}
}

// getProvisionerVolumesAndMounts defines the common set of volumes and mounts for localvolumeset daemonsets
func getProvisionerVolumesAndMounts() ([]corev1.Volume, []corev1.VolumeMount) {
	volumes := []corev1.Volume{
		common.SymlinkHostDirVolume,
		common.DevHostDirVolume,
		common.ProvisionerConfigHostDirVolume,
	}
	volumeMounts := []corev1.VolumeMount{
		common.SymlinkMount,
		common.DevMount,
		common.ProvisionerConfigMount,
	}

	return volumes, volumeMounts
}

// MutateAggregatedSpec returns a mutate function that applies the other arguments to the referenced daemonset
// its purpose is to be used in more specific mutate functions
// that can that be applied to an empty corev1.DaemonSet{} before a Create()
// or applied to an existing one before an Update()
func MutateAggregatedSpec(
	ds *appsv1.DaemonSet,
	request reconcile.Request,
	tolerations []corev1.Toleration,
	ownerRefs []metav1.OwnerReference,
	nodeSelector *corev1.NodeSelector,
	name string,
) {

	selectorLabels := map[string]string{"app": name}
	// create only actions
	if ds.CreationTimestamp.IsZero() {
		// name and namespace
		ds.ObjectMeta = metav1.ObjectMeta{
			Name:      name,
			Namespace: request.Namespace,
		}
		// daemonset selector
		ds.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: selectorLabels,
		}
	}

	// copy selector labels to daemonset and template
	initMapIfNil(&ds.ObjectMeta.Labels)
	initMapIfNil(&ds.Spec.Template.ObjectMeta.Labels)
	for key, value := range selectorLabels {
		ds.ObjectMeta.Labels[key] = value
		ds.Spec.Template.ObjectMeta.Labels[key] = value
	}

	// add management workload annotations
	initMapIfNil(&ds.Spec.Template.ObjectMeta.Annotations)
	ds.Spec.Template.ObjectMeta.Annotations["target.workload.openshift.io/management"] = `{"effect": "PreferredDuringScheduling"}`

	// ownerRefs
	ds.ObjectMeta.OwnerReferences = ownerRefs

	// service account
	ds.Spec.Template.Spec.ServiceAccountName = common.ProvisionerServiceAccount

	// priority class
	ds.Spec.Template.Spec.PriorityClassName = common.PriorityClassName

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

	// fetch common volumes and mounts
	volumes, volumeMounts := getProvisionerVolumesAndMounts()
	ds.Spec.Template.Spec.Volumes = volumes

	// define containers
	privileged := true
	ds.Spec.Template.Spec.Containers = []corev1.Container{
		{
			Image: common.GetDiskMakerImage(),
			Args:  []string{"lv-manager"},
			Name:  name,
			SecurityContext: &corev1.SecurityContext{
				Privileged: &privileged,
			},
			VolumeMounts:             volumeMounts,
			TerminationMessagePath:   "/dev/termination-log",
			TerminationMessagePolicy: "File",
			Env: []corev1.EnvVar{
				{
					Name: "MY_NODE_NAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							APIVersion: "v1",
							FieldPath:  "spec.nodeName",
						},
					},
				},
				{
					Name: "WATCH_NAMESPACE",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							APIVersion: "v1",
							FieldPath:  "metadata.namespace",
						},
					},
				},
				{
					Name: "POD_NAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							APIVersion: "v1",
							FieldPath:  "metadata.name",
						},
					},
				},
			},
		},
	}

}

func initMapIfNil(m *map[string]string) {
	if len(*m) > 1 {
		return
	}
	*m = make(map[string]string)
	return
}
