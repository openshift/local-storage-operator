package nodedaemon

import (
	"context"

	"github.com/openshift/local-storage-operator/pkg/common"
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
func createOrUpdateDaemonset(
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
		name := DiskMakerName

		// common spec
		mutateAggregatedSpec(
			ds,
			request,
			tolerations,
			ownerRefs,
			nodeSelector,
			dataHash,
			name,
		)
		ds.Spec.Template.Spec.Containers[0].Image = common.GetDiskMakerImage()
		ds.Spec.Template.Spec.Containers[0].Args = []string{"lv-manager"}

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
		mutateAggregatedSpec(
			ds,
			request,
			tolerations,
			ownerRefs,
			nodeSelector,
			dataHash,
			name,
		)
		ds.Spec.Template.Spec.Containers[0].Image = common.GetLocalProvisionerImage()

		return nil
	}
}

func getProvisionerVolumesAndMounts() ([]corev1.Volume, []corev1.VolumeMount) {
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
			Name:      "provisioner-config",
			ReadOnly:  true,
			MountPath: "/etc/provisioner/config",
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
			Name: "provisioner-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: ProvisionerConfigMapName,
					},
				},
			},
		},
	}
	return volumes, volumeMounts
}
func mutateAggregatedSpec(
	ds *appsv1.DaemonSet,
	request reconcile.Request,
	tolerations []corev1.Toleration,
	ownerRefs []metav1.OwnerReference,
	nodeSelector *corev1.NodeSelector,
	dataHash string,
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

	// add provisioner configmap hash
	initMapIfNil(&ds.Spec.Template.ObjectMeta.Annotations)
	ds.Spec.Template.ObjectMeta.Annotations[dataHashAnnotationKey] = dataHash

	// ownerRefs
	ds.ObjectMeta.OwnerReferences = ownerRefs

	// service account
	ds.Spec.Template.Spec.ServiceAccountName = common.ProvisionerServiceAccount

	// tolerations
	ds.Spec.Template.Spec.Tolerations = tolerations

	// nodeSelector if non-nil
	if nodeSelector != nil {
		ds.Spec.Template.Spec.Affinity = &corev1.Affinity{
			NodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: nodeSelector,
			},
		}
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
			VolumeMounts: volumeMounts,
			Env: []corev1.EnvVar{
				{
					Name: "MY_NODE_NAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "spec.nodeName",
						},
					},
				},
				{
					Name: "WATCH_NAMESPACE",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "metadata.namespace",
						},
					},
				},
				{
					Name: "POD_NAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "metadata.name",
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
