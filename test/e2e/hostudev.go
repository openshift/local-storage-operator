package e2e

import (
	"fmt"
	"testing"

	"github.com/onsi/gomega"
	framework "github.com/openshift/local-storage-operator/test/framework"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

// addNewUdevSymlink adds a new udev symlink on the node. The link will point to the same device as the currentLink.
func addNewUdevSymlink(t *testing.T, ctx *framework.TestCtx, nodeHostname string, currentLink, newLink string) {
	t.Logf("adding new udev symlink %s -> %s on node %s", newLink, currentLink, nodeHostname)
	matcher := gomega.NewWithT(t)

	namespace, err := ctx.GetOperatorNamespace()
	matcher.Expect(err).NotTo(gomega.HaveOccurred(), "could not determine namespace")

	symlinkJob, err := newAddUdevSymlinkJob(nodeHostname, namespace, currentLink, newLink)
	matcher.Expect(err).NotTo(gomega.HaveOccurred(), "could not create symlink job")
	createOrReplaceJob(t, ctx, symlinkJob, fmt.Sprintf("creating symlink job on node: %q", nodeHostname))

	waitForJobCompletion(t, symlinkJob, fmt.Sprintf("waiting for check-symlink job to complete: %q", symlinkJob.GetName()))
	symlinkJob.TypeMeta.Kind = "Job"
	eventuallyDelete(t, false, symlinkJob)
	t.Logf("added udev symlink %s -> %s on node %s", newLink, currentLink, nodeHostname)
}

// removeUdevSymlink removes a udev symlink on the node. It literally calls `rm -f $linkPattern` (in the root directory),
// so it can be used to remove multiple symlinks at once.
func removeUdevSymlink(t *testing.T, ctx *framework.TestCtx, nodeHostname string, linkPattern string) {
	t.Logf("removing udev symlinks matching %s on node %s", linkPattern, nodeHostname)
	matcher := gomega.NewWithT(t)

	namespace, err := ctx.GetOperatorNamespace()
	matcher.Expect(err).NotTo(gomega.HaveOccurred(), "could not determine namespace")

	symlinkJob, err := newRemoveUdevSymlinksJob(nodeHostname, namespace, linkPattern)
	matcher.Expect(err).NotTo(gomega.HaveOccurred(), "could not create symlink job")
	createOrReplaceJob(t, ctx, symlinkJob, fmt.Sprintf("creating symlink job on node: %q", nodeHostname))

	waitForJobCompletion(t, symlinkJob, fmt.Sprintf("waiting for check-symlink job to complete: %q", symlinkJob.GetName()))
	symlinkJob.TypeMeta.Kind = "Job"
	eventuallyDelete(t, false, symlinkJob)
	t.Logf("removed udev symlinks matching %s on node %s", linkPattern, nodeHostname)
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
		fmt.Sprintf("add-udev-symlink-job-%s", nodeHostname),
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
		fmt.Sprintf("remove-udev-symlink-job-%s", nodeHostname),
		"removes udev symlinks matching the pattern",
		[]string{"/bin/bash", "-c", script},
		&NodeJobOptions{
			ContainerRestartPolicy: corev1.RestartPolicyNever,
			Env: []corev1.EnvVar{
				{Name: "PATTERN", Value: pattern},
			},
		})
}
