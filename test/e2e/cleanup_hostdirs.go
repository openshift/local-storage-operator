package e2e

import (
	"fmt"
	"testing"

	framework "github.com/openshift/local-storage-operator/test/framework"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

func cleanupSymlinkDir(t *testing.T, ctx *framework.TestCtx, nodeEnv []nodeDisks) error {
	t.Logf("cleaning up hostdirs")
	namespace, err := ctx.GetOperatorNamespace()
	if err != nil || namespace == "" {
		return fmt.Errorf("could not determine namespace: %w", err)
	}

	cleanupJobs := make([]*batchv1.Job, 0)

	// create jobs
	for _, nd := range nodeEnv {
		node := nd.node
		t.Logf("creating cleanup job on node: %q", node.GetName())
		cleanupJob, err := newNodeCleanupJob(node, namespace)
		if err != nil {
			return err
		}
		cleanupJobs = append(cleanupJobs, cleanupJob)
		createOrReplaceJob(t, ctx, cleanupJob, fmt.Sprintf("creating cleanup job on node: %q", node.GetName()))

	}

	// wait for jobs to have non-nil completetion time
	for _, cleanupJob := range cleanupJobs {
		waitForJobCompletion(t, cleanupJob, fmt.Sprintf("waiting for cleanup job to complete: %q", cleanupJob.GetName()))
		cleanupJob.TypeMeta.Kind = "Job"
		eventuallyDelete(t, false, cleanupJob)
	}

	return nil
}

func newNodeCleanupJob(node corev1.Node, namespace string) (*batchv1.Job, error) {
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
