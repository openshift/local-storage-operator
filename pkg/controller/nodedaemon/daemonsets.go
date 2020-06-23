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

// createOrUpdateDaemonset fetches and creates/updates ds according to desiredDs
// fields that are not allowed update at runtime are not copied unless the creationtimestamp is zero
func createOrUpdateDaemonset(c client.Client, desiredDs *appsv1.DaemonSet) (controllerutil.OperationResult, error) {
	ds := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: desiredDs.GetName(), Namespace: desiredDs.GetNamespace()}}
	return controllerutil.CreateOrUpdate(context.TODO(), c, ds, func() error {
		updateDaemonSet(*desiredDs, ds)
		return nil
	})
}

// updateDaemonSet copies the daemonset from in to out
// fields that are not allowed update at runtime are not copied unless the creationtimestamp is zero
func updateDaemonSet(in appsv1.DaemonSet, out *appsv1.DaemonSet) {

	if out.CreationTimestamp.IsZero() {
		in.DeepCopyInto(out)
		return
	}
	out.ObjectMeta.Labels = in.GetLabels()
	out.ObjectMeta.Annotations = in.ObjectMeta.Annotations
	out.ObjectMeta.OwnerReferences = in.ObjectMeta.OwnerReferences

	out.Spec.Template.ObjectMeta.Annotations = in.Spec.Template.ObjectMeta.Annotations
	out.Spec.Template.Spec = in.Spec.Template.Spec

	out.Spec.UpdateStrategy = in.Spec.UpdateStrategy
	out.Spec.MinReadySeconds = in.Spec.MinReadySeconds
	out.Spec.RevisionHistoryLimit = in.Spec.RevisionHistoryLimit
	return
}

func generateDiskMakerDaemonset(request reconcile.Request, tolerations []corev1.Toleration, ownerRefs []metav1.OwnerReference, nodeSelector corev1.NodeSelector) *appsv1.DaemonSet {

	labels := map[string]string{
		"app": DiskMakerName,
	}
	hostContainerPropagation := corev1.MountPropagationHostToContainer
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
	}
	privileged := true
	containers := []corev1.Container{
		{
			Image: common.GetDiskMakerImage(),
			Args:  []string{"lv-manager"},
			Name:  "local-diskmaker",
			SecurityContext: &corev1.SecurityContext{
				Privileged: &privileged,
			},
			VolumeMounts: []corev1.VolumeMount{
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
			},
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
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            DiskMakerName,
			Namespace:       request.Namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Volumes:            volumes,
					ServiceAccountName: common.ProvisionerServiceAccount,
					Tolerations:        tolerations,
					Containers:         containers,
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &nodeSelector,
						},
					},
				},
			},
		},
	}
	return ds
}

func generateLocalProvisionerDaemonset(request reconcile.Request, tolerations []corev1.Toleration, ownerRefs []metav1.OwnerReference, nodeSelector corev1.NodeSelector) *appsv1.DaemonSet {

	labels := map[string]string{
		"app": ProvisionerConfigMapName,
	}
	hostContainerPropagation := corev1.MountPropagationHostToContainer
	directoryHostPath := corev1.HostPathDirectory
	volumes := []corev1.Volume{
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
	}
	privileged := true
	containers := []corev1.Container{
		{
			Name:  "local-storage-provisioner",
			Image: common.GetLocalProvisionerImage(),
			SecurityContext: &corev1.SecurityContext{
				Privileged: &privileged,
			},
			Env: []corev1.EnvVar{
				{
					Name: "MY_NODE_NAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "spec.nodeName",
						},
					},
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "provisioner-config",
					ReadOnly:  true,
					MountPath: "/etc/provisioner/config",
				},
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
			},
		},
	}

	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            ProvisionerConfigMapName,
			Namespace:       request.Namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Volumes:            volumes,
					ServiceAccountName: common.ProvisionerServiceAccount,
					Tolerations:        tolerations,
					Containers:         containers,
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &nodeSelector,
						},
					},
				},
			},
		},
	}

	return ds
}
