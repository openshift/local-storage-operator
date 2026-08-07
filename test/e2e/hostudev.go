package e2e

import (
	"fmt"

	. "github.com/onsi/gomega"
	framework "github.com/openshift/local-storage-operator/test/framework"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

const sharedScsi8Link = "/dev/disk/by-id/scsi-8-local-storage-e2e-shared"

func addNewUdevSymlink(namespace, nodeHostname string, currentLink, newLink string) {
	f := framework.Global
	f.Logf("adding new udev symlink %s -> %s on node %s", currentLink, newLink, nodeHostname)

	symlinkJob, err := newAddUdevSymlinkJob(nodeHostname, namespace, currentLink, newLink)
	Expect(err).NotTo(HaveOccurred(), "could not create symlink job")
	createOrReplaceJob(namespace, symlinkJob, fmt.Sprintf("creating symlink job on node: %q", nodeHostname))

	waitForJobCompletion(symlinkJob, fmt.Sprintf("waiting for check-symlink job to complete: %q", symlinkJob.GetName()))
	symlinkJob.TypeMeta.Kind = "Job"
	eventuallyDelete(symlinkJob)
	f.Logf("added udev symlink %s -> %s on node %s", currentLink, newLink, nodeHostname)
}

func removeUdevSymlink(namespace, nodeHostname string, linkPattern string) {
	f := framework.Global
	f.Logf("removing udev symlinks matching %s on node %s", linkPattern, nodeHostname)

	symlinkJob, err := newRemoveUdevSymlinksJob(nodeHostname, namespace, linkPattern)
	Expect(err).NotTo(HaveOccurred(), "could not create symlink job")
	createOrReplaceJob(namespace, symlinkJob, fmt.Sprintf("creating symlink job on node: %q", nodeHostname))

	waitForJobCompletion(symlinkJob, fmt.Sprintf("waiting for check-symlink job to complete: %q", symlinkJob.GetName()))
	symlinkJob.TypeMeta.Kind = "Job"
	eventuallyDelete(symlinkJob)
	f.Logf("removed udev symlinks matching %s on node %s", linkPattern, nodeHostname)
}

func newAddUdevSymlinkJob(nodeHostname, namespace, currentUdevLink, newLink string) (*batchv1.Job, error) {
	script := `
set -eu
set -x

echo "adding new udev symlink $CURRENT_LINK -> $NEW_LINK"
if [[ ! -L "$CURRENT_LINK" ]]; then
	echo "not a symlink: $CURRENT_LINK"
	exit 1
fi

DEVICE=$(readlink -f "$CURRENT_LINK")
ln -sfv "$DEVICE" "$NEW_LINK"
`
	return newNodeJob(
		nodeHostname,
		namespace,
		fmt.Sprintf("add-symlink-job-%s", nodeHostname),
		"adds a new udev symlink",
		[]string{"/bin/bash", "-c", script},
		&NodeJobOptions{
			ContainerRestartPolicy: corev1.RestartPolicyNever,
			Env: []corev1.EnvVar{
				{Name: "CURRENT_LINK", Value: currentUdevLink},
				{Name: "NEW_LINK", Value: newLink},
			},
		})

}

func newRemoveUdevSymlinksJob(nodeHostname, namespace, pattern string) (*batchv1.Job, error) {
	script := `
set -eu
set -x

ls -la "$PATTERN" || echo "No matching files found"
rm -fv "$PATTERN"
`
	return newNodeJob(
		nodeHostname,
		namespace,
		fmt.Sprintf("remove-symlink-%s", nodeHostname),
		"removes udev symlinks matching the pattern",
		[]string{"/bin/bash", "-c", script},
		&NodeJobOptions{
			ContainerRestartPolicy: corev1.RestartPolicyNever,
			Env: []corev1.EnvVar{
				{Name: "PATTERN", Value: pattern},
			},
		})
}
