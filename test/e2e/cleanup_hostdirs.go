package e2e

import (
	"fmt"

	framework "github.com/openshift/local-storage-operator/test/framework"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

func cleanupSymlinkDir(namespace string, nodeEnv []nodeDisks) error {
	f := framework.Global
	f.Logf("cleaning up hostdirs")

	cleanupJobs := make([]*batchv1.Job, 0)

	// create jobs
	for _, nd := range nodeEnv {
		node := nd.node
		f.Logf("creating cleanup job on node: %q", node.GetName())
		cleanupJob, err := newNodeCleanupJob(node, namespace)
		if err != nil {
			return err
		}
		cleanupJobs = append(cleanupJobs, cleanupJob)
		createOrReplaceJob(namespace, cleanupJob, fmt.Sprintf("creating cleanup job on node: %q", node.GetName()))

	}

	// wait for jobs to have non-nil completetion time
	for _, cleanupJob := range cleanupJobs {
		waitForJobCompletion(cleanupJob, fmt.Sprintf("waiting for cleanup job to complete: %q", cleanupJob.GetName()))
		cleanupJob.TypeMeta.Kind = "Job"
		eventuallyDelete(cleanupJob)
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
		nodeName,
		namespace,
		fmt.Sprintf("cleanup-%s", nodeName),
		"cleans up the hostdir artificats on the node following functional tests",
		[]string{"/bin/bash", "-c", hostDirCleanupScript},
		&NodeJobOptions{ContainerRestartPolicy: corev1.RestartPolicyOnFailure})
}
