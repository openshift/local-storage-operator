package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onsi/gomega"
	framework "github.com/openshift/local-storage-operator/test/framework"
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
	namespace, err := ctx.GetOperatorNamespace()
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
	nodeName, _ := node.Labels[corev1.LabelHostname]
	return newNodeJob(
		node,
		namespace,
		fmt.Sprintf("cleanup-%s", nodeName),
		"cleans up the hostdir artificats on the node following functional tests",
		[]string{"/bin/bash", "-c", hostDirCleanupScript},
		&NodeJobOptions{ContainerRestartPolicy: corev1.RestartPolicyOnFailure})
}
