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
	nodeName, _ := node.Labels[corev1.LabelHostname]
	backoffLimit := int32(1)
	return newNodeJob(
		node,
		namespace,
		fmt.Sprintf("symlink-check-%s", nodeName),
		"checks for leftover symlinks on the node following functional tests",
		[]string{"/bin/bash", "-c", hostCheckSymlinkScript},
		&NodeJobOptions{
			Env:                    []corev1.EnvVar{{Name: "DISKPATH", Value: path}},
			JobBackoffLimit:        &backoffLimit,
			ContainerRestartPolicy: corev1.RestartPolicyNever,
		})
}
