package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onsi/gomega"
	"github.com/openshift/local-storage-operator/common"

	framework "github.com/openshift/local-storage-operator/test-framework"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func cleanupSymlinkDir(t *testing.T, ctx *framework.TestCtx, nodeEnv []nodeDisks) error {
	t.Logf("cleaning up hostdirs")
	f := framework.Global
	matcher := gomega.NewWithT(t)
	namespace, err := ctx.GetNamespace()
	if err != nil || namespace == "" {
		return fmt.Errorf("could not determine namespace: %w", err)
	}

	cleanupJobs := make([]batchv1.Job, 0)

	started := metav1.NewTime(time.Now())

	// create jobs
	for _, nd := range nodeEnv {
		node := nd.node
		t.Logf("creating cleanup job on node: %q", node.GetName())
		matcher.Eventually(func() error {
			cleanupJob, err := newNodeCleanupJob(node, namespace)
			if err != nil {
				return err
			}
			cleanupJobs = append(cleanupJobs, cleanupJob)
			err = f.Client.Create(context.TODO(), &cleanupJob, &framework.CleanupOptions{TestContext: ctx})
			if errors.IsAlreadyExists(err) {
				err = f.Client.Get(
					context.TODO(),
					types.NamespacedName{Name: cleanupJob.GetName(), Namespace: cleanupJob.GetNamespace()},
					&cleanupJob,
				)
				if err != nil {
					return err
				}
				if cleanupJob.CreationTimestamp.Before(&started) {
					cleanupJob.TypeMeta.Kind = "Job"
					eventuallyDelete(t, false, &cleanupJob)
					return err
				}
			}
			return err

		}, time.Minute*1, time.Second*5).ShouldNot(gomega.HaveOccurred(), "creating cleanup job on node: %q", node.GetName())

	}

	// wait for jobs to have non-nil completetion time
	for _, cleanupJob := range cleanupJobs {
		// waot for job
		matcher.Eventually(func() int32 {
			job := &batchv1.Job{}
			matcher.Eventually(func() error {
				return f.Client.Get(
					context.TODO(),
					types.NamespacedName{
						Name:      cleanupJob.GetName(),
						Namespace: cleanupJob.GetNamespace()},
					job,
				)
			}).ShouldNot(gomega.HaveOccurred())
			completions := job.Status.Succeeded
			t.Logf("job completions: %d", completions)
			return completions
		}, time.Minute*2, time.Second*5).Should(gomega.BeNumerically(">=", 1), "waiting cleanup job to complete: %q", cleanupJob.GetName())
		cleanupJob.TypeMeta.Kind = "Job"
		eventuallyDelete(t, false, &cleanupJob)
	}

	return nil
}

func newNodeCleanupJob(node corev1.Node, namespace string) (batchv1.Job, error) {

	const hostDirCleanupScript = `
set -eu
set -x
for path in local local-storage
do
	fullpath="/mnt/${path}"
	echo cleaning $fullpath
	[[ -d $fullpath ]] && rm -rfv ${fullpath}/*
	[[ ! -d $fullpath ]] && echo  "not a directory ${fullpath}"
done
set +x
`

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
	containers := []corev1.Container{
		{
			Image: common.GetDiskMakerImage(),
			Command: []string{
				"/bin/bash",
				"-c",
				hostDirCleanupScript,
			},
			Name: "local-diskmaker",
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
		},
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
	job := batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("cleanup-%s", nodeName),
			Namespace: namespace,

			Labels: map[string]string{
				corev1.LabelHostname: nodeName,
				"app":                "cleanup",
			},
			Annotations: map[string]string{
				"description": "cleans up the hostdir artificats on the node following functional tests",
			},
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyOnFailure,
					Containers:    containers,
					Volumes:       volumes,
					Affinity:      affinity,
				},
			},
		},
	}
	return job, nil
}
