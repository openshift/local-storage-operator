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

func checkForSymlinks(t *testing.T, ctx *framework.TestCtx, nodeEnv []nodeDisks, path string) error {
	t.Logf("checking for leftover symlinks on host")
	f := framework.Global
	matcher := gomega.NewWithT(t)
	namespace, err := ctx.GetOperatorNamespace()
	if err != nil || namespace == "" {
		return fmt.Errorf("could not determine namespace: %w", err)
	}

	checkSymlinkJobs := make([]batchv1.Job, 0)
	started := metav1.NewTime(time.Now())

	for _, nd := range nodeEnv {
		node := nd.node
		t.Logf("creating check-symlink job on node: %q", node.GetName())
		matcher.Eventually(func() error {
			checkSymlinkJob, err := newNodeCheckSymlinkJob(node, namespace, path)
			if err != nil {
				return err
			}
			checkSymlinkJobs = append(checkSymlinkJobs, checkSymlinkJob)
			err = f.Client.Create(context.TODO(), &checkSymlinkJob, &framework.CleanupOptions{TestContext: ctx})
			if errors.IsAlreadyExists(err) {
				err = f.Client.Get(
					context.TODO(),
					types.NamespacedName{Name: checkSymlinkJob.GetName(), Namespace: checkSymlinkJob.GetNamespace()},
					&checkSymlinkJob,
				)
				if err != nil {
					return err
				}
				if checkSymlinkJob.CreationTimestamp.Before(&started) {
					checkSymlinkJob.TypeMeta.Kind = "Job"
					eventuallyDelete(t, false, &checkSymlinkJob)
					return err
				}
			}
			return err

		}, time.Minute*1, time.Second*5).ShouldNot(gomega.HaveOccurred(), "creating check-symlink job on node: %q", node.GetName())

	}

	// wait for jobs to have non-nil completetion time
	for _, checkSymlinkJob := range checkSymlinkJobs {
		// waot for job
		matcher.Eventually(func() int32 {
			job := &batchv1.Job{}
			matcher.Eventually(func() error {
				return f.Client.Get(
					context.TODO(),
					types.NamespacedName{
						Name:      checkSymlinkJob.GetName(),
						Namespace: checkSymlinkJob.GetNamespace()},
					job,
				)
			}).ShouldNot(gomega.HaveOccurred())
			completions := job.Status.Succeeded
			t.Logf("job completions: %d", completions)
			return completions
		}, time.Minute*2, time.Second*5).Should(gomega.BeNumerically(">=", 1), "waiting check-symlink job to complete: %q", checkSymlinkJob.GetName())
		checkSymlinkJob.TypeMeta.Kind = "Job"
		eventuallyDelete(t, false, &checkSymlinkJob)
	}

	return nil
}

func newNodeCheckSymlinkJob(node corev1.Node, namespace string, path string) (batchv1.Job, error) {

	// check each node for symlinks, fail the job if it exists
	const hostCheckSymlinkScript = `
set -eu
set -x
echo "checking $DISKPATH for leftover symlinks"
if [[ ! -d $DISKPATH ]]; then
	echo "not a directory: $DISKPATH"
else
	symlinks="$(find -L $DISKPATH -xtype l)"
	[[ -z $symlinks ]] || (echo "found symlinks: $symlinks" && exit 1)
fi
set +x
`

	nodeName, found := node.Labels[corev1.LabelHostname]
	if !found {
		return batchv1.Job{}, fmt.Errorf("could not get %q label for node: %q", corev1.LabelHostname, node.GetName())

	}
	hostContainerPropagation := corev1.MountPropagationHostToContainer
	directoryHostPath := corev1.HostPathDirectory
	backoffLimit := int32(1)
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
				hostCheckSymlinkScript,
			},
			Name: "local-diskmaker",
			SecurityContext: &corev1.SecurityContext{
				Privileged: &privileged,
			},
			Env: []corev1.EnvVar{
				{
					Name:  "DISKPATH",
					Value: path,
				},
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
			Name:      fmt.Sprintf("symlink-check-%s", nodeName),
			Namespace: namespace,

			Labels: map[string]string{
				corev1.LabelHostname: nodeName,
				"app":                "symlink-check",
			},
			Annotations: map[string]string{
				"description": "checks for leftover symlinks on the node following functional tests",
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					Containers:         containers,
					Volumes:            volumes,
					Affinity:           affinity,
					ServiceAccountName: "local-storage-admin",
				},
			},
		},
	}
	return job, nil
}
