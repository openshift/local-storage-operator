package e2e

import (
	"fmt"
	"testing"

	framework "github.com/openshift/local-storage-operator/test/framework"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

func checkForSymlinks(t *testing.T, ctx *framework.TestCtx, nodeEnv []nodeDisks, path string) error {
	t.Logf("checking for leftover symlinks on host")
	namespace, err := ctx.GetOperatorNamespace()
	if err != nil || namespace == "" {
		return fmt.Errorf("could not determine namespace: %w", err)
	}

	checkSymlinkJobs := make([]*batchv1.Job, 0)

	for _, nd := range nodeEnv {
		node := nd.node
		t.Logf("creating check-symlink job on node: %q", node.GetName())
		checkSymlinkJob, err := newNodeCheckSymlinkJob(node, namespace, path)
		if err != nil {
			return err
		}
		checkSymlinkJobs = append(checkSymlinkJobs, checkSymlinkJob)
		createOrReplaceJob(t, ctx, checkSymlinkJob, fmt.Sprintf("creating check-symlink job on node: %q", node.GetName()))
	}

	// wait for jobs to have non-nil completetion time
	for _, checkSymlinkJob := range checkSymlinkJobs {
		waitForJobCompletion(t, checkSymlinkJob, fmt.Sprintf("waiting for check-symlink job to complete: %q", checkSymlinkJob.GetName()))
		checkSymlinkJob.TypeMeta.Kind = "Job"
		eventuallyDelete(t, false, checkSymlinkJob)
	}

	return nil
}

func newNodeCheckSymlinkJob(node corev1.Node, namespace string, path string) (*batchv1.Job, error) {
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
