package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onsi/gomega"
	"github.com/openshift/local-storage-operator/pkg/common"
	framework "github.com/openshift/local-storage-operator/test/framework"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
func newNodeJob(nodeHostname, namespace, jobName, description string, command []string, opts *NodeJobOptions) (*batchv1.Job, error) {
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
								Values:   []string{nodeHostname},
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
				corev1.LabelHostname: nodeHostname,
				"app":                jobName,
			},
			Annotations: map[string]string{
				"description": description,
			},
		},
		Spec: jobSpec,
	}
	return &job, nil
}

// createOrReplaceJob creates a job. It deletes an old job with the same name, if it already exists and creates a new one.
func createOrReplaceJob(t *testing.T, ctx *framework.TestCtx, job *batchv1.Job, message string) {
	f := framework.Global
	matcher := gomega.NewWithT(t)
	started := metav1.NewTime(time.Now())
	matcher.Eventually(func() error {
		err := f.Client.Create(context.TODO(), job, &framework.CleanupOptions{TestContext: ctx})
		j := &batchv1.Job{}
		if errors.IsAlreadyExists(err) {
			err = f.Client.Get(
				context.TODO(),
				types.NamespacedName{Name: job.GetName(), Namespace: job.GetNamespace()},
				j,
			)
			if err != nil {
				return err
			}
			if j.CreationTimestamp.Before(&started) {
				j.TypeMeta.Kind = "Job"
				eventuallyDelete(t, false, j)
				return fmt.Errorf("deleted stale job %s/%s, retrying creation", j.GetNamespace(), j.GetName())
			}
		}
		return err

	}, time.Minute*1, time.Second*5).ShouldNot(gomega.HaveOccurred(), message)
}

func waitForJobCompletion(t *testing.T, job *batchv1.Job, message string) {
	f := framework.Global
	matcher := gomega.NewWithT(t)
	matcher.Eventually(func() int32 {
		j := &batchv1.Job{}
		matcher.Eventually(func() error {
			return f.Client.Get(
				context.TODO(),
				types.NamespacedName{
					Name:      job.GetName(),
					Namespace: job.GetNamespace()},
				j,
			)
		}).ShouldNot(gomega.HaveOccurred())
		completions := j.Status.Succeeded
		t.Logf("job completions: %d", completions)
		return completions
	}, time.Minute*2, time.Second*5).Should(gomega.BeNumerically(">=", 1), message)
}
