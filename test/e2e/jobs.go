package e2e

import (
	"fmt"

	"github.com/openshift/local-storage-operator/pkg/common"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NodeJobOptions customizes a node Job (env vars, backoff, restart policy).
type NodeJobOptions struct {
	// Environment variables to set in the container.
	Env []corev1.EnvVar
	// Backoff limit for the job, i.e. how many times to retry the job if it fails.
	JobBackoffLimit *int32
	// Restart policy for the container. Should be RestartPolicyNever for the JobBackoffLimit to work correctly.
	ContainerRestartPolicy corev1.RestartPolicy
}

// newNodeJob returns a Job that runs the given command on the specified node.
// The job uses the diskmaker image, mounts local-disks and /dev, and is pinned to the node via affinity.
func newNodeJob(node corev1.Node, namespace, jobName, description string, command []string, opts *NodeJobOptions) (batchv1.Job, error) {
	nodeName, found := node.Labels[corev1.LabelHostname]
	if !found {
		return batchv1.Job{}, fmt.Errorf("could not get %q label for node: %q", corev1.LabelHostname, node.GetName())
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
	container := corev1.Container{
		Image:   common.GetDiskMakerImage(),
		Command: command,
		Name:    "local-diskmaker",
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
	}

	restartPolicy := corev1.RestartPolicyOnFailure
	if opts != nil && opts.ContainerRestartPolicy != "" {
		restartPolicy = opts.ContainerRestartPolicy
	}
	if opts != nil && len(opts.Env) > 0 {
		container.Env = opts.Env
	}

	affinity := &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{
					{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{
								Key:      corev1.LabelHostname,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{nodeName},
							},
						},
					},
				},
			},
		},
	}

	jobSpec := batchv1.JobSpec{
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				RestartPolicy:      restartPolicy,
				Containers:         []corev1.Container{container},
				Volumes:            volumes,
				Affinity:           affinity,
				ServiceAccountName: "local-storage-admin",
			},
		},
	}
	if opts != nil && opts.JobBackoffLimit != nil {
		jobSpec.BackoffLimit = opts.JobBackoffLimit
	}

	job := batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: namespace,
			Labels: map[string]string{
				corev1.LabelHostname: nodeName,
				"app":                jobName,
			},
			Annotations: map[string]string{
				"description": description,
			},
		},
		Spec: jobSpec,
	}
	return job, nil
}
