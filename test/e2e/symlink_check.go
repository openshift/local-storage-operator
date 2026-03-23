package e2e

import (
	goctx "context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/onsi/gomega"
	localv1 "github.com/openshift/local-storage-operator/api/v1"
	framework "github.com/openshift/local-storage-operator/test/framework"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
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
		nodeName,
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

func verifyPreferredLinkReconciliationForPV(t *testing.T, ctx *framework.TestCtx, f *framework.Framework, namespace string, pv corev1.PersistentVolume, currentSymlink, newPreferredTarget string) (corev1.PersistentVolume, cleanupFn) {

	matcher := gomega.NewWithT(t)
	// LVDL name is same as PV name
	selectedLVDL := eventuallyGetLVDL(t, f, namespace, pv.Name)
	nodeHostName := findNodeHostnameForPV(t, &pv)

	addNewUdevSymlink(t, ctx, nodeHostName, currentSymlink, newPreferredTarget)
	cleanupToRun := cleanupFn{
		name: fmt.Sprintf("removeUdevSymlink-%s", filepath.Base(newPreferredTarget)),
		fn: func(t *testing.T) error {
			removeUdevSymlink(t, ctx, nodeHostName, newPreferredTarget)
			return nil
		},
	}

	t.Logf("verifying LVDL %s automatically updates PreferredLinkTarget when a new by-id link appears", selectedLVDL.Name)
	selectedLVDL = waitForLVDLPreferredLinkTarget(t, f, selectedLVDL, newPreferredTarget)
	matcher.Expect(selectedLVDL.Status.CurrentLinkTarget).To(gomega.Equal(currentSymlink))

	t.Logf("setting LVDL policy to PreferredLinkTarget and waiting for automatic symlink reconciliation on %s", selectedLVDL.Name)
	selectedLVDL = updateLVDLPolicy(t, f, selectedLVDL, localv1.DeviceLinkPolicyPreferredLinkTarget)
	selectedLVDL = waitForLVDLLinkTargets(t, f, selectedLVDL, newPreferredTarget, newPreferredTarget)
	verifyNodeSymlinkTarget(t, ctx, nodeHostName, pv.Spec.Local.Path, newPreferredTarget)

	t.Log("verifying recreated PV uses the updated preferred link after policy-driven reconciliation")
	oldPVUID := pv.UID
	oldPVPath := pv.Spec.Local.Path
	eventuallyDelete(t, false, &pv)
	recreatedPV := waitForRecreatedPVByName(t, f, pv.Name, oldPVUID)
	matcher.Expect(recreatedPV.Spec.Local.Path).To(gomega.Equal(oldPVPath))
	verifyNodeSymlinkTarget(t, ctx, nodeHostName, recreatedPV.Spec.Local.Path, newPreferredTarget)
	return recreatedPV, cleanupToRun
}

func currentSymlinkForDisk(d disk) string {
	if d.id != "" {
		return filepath.Join("/dev/disk/by-id", d.id)
	}
	return filepath.Join("/dev", d.name)
}

func findCurrentSymlinkForPV(t *testing.T, nodeEnv []nodeDisks, pv *corev1.PersistentVolume) string {

	nodeHostName := findNodeHostnameForPV(t, pv)
	pvBase := filepath.Base(pv.Spec.Local.Path)
	for _, nodeEntry := range nodeEnv {
		if nodeEntry.node.Labels[corev1.LabelHostname] != nodeHostName {
			continue
		}
		for _, diskEntry := range nodeEntry.disks {
			if diskEntry.id == pvBase || diskEntry.name == pvBase {
				return currentSymlinkForDisk(diskEntry)
			}
		}
	}
	t.Fatalf("failed to find current symlink for PV %q on node hostname %q", pv.Name, nodeHostName)
	return ""
}

// findNodeHostnameForLVDL returns the node hostname where the LVDL's PV is scheduled,
// by inspecting the PV's spec.nodeAffinity (Required, LabelHostname).
func findNodeHostnameForPV(t *testing.T, pv *v1.PersistentVolume) string {
	pvName := pv.Name
	if pv.Spec.NodeAffinity == nil || pv.Spec.NodeAffinity.Required == nil {
		t.Fatalf("PV %s has no NodeAffinity.Required", pvName)
	}
	for _, term := range pv.Spec.NodeAffinity.Required.NodeSelectorTerms {
		for _, expr := range term.MatchExpressions {
			if expr.Key == corev1.LabelHostname && len(expr.Values) > 0 {
				return expr.Values[0]
			}
		}
	}
	t.Fatalf("PV %s NodeAffinity has no %q expression", pvName, corev1.LabelHostname)
	return ""
}

func eventuallyGetLVDL(t *testing.T, f *framework.Framework, namespace, name string) *localv1.LocalVolumeDeviceLink {
	matcher := gomega.NewWithT(t)
	lvdl := &localv1.LocalVolumeDeviceLink{}

	matcher.Eventually(func() error {
		return f.Client.Get(goctx.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, lvdl)
	}, time.Minute*5, time.Second*5).ShouldNot(gomega.HaveOccurred(), "waiting for LocalVolumeDeviceLink %q", name)
	return lvdl
}

func waitForLVDLPreferredLinkTarget(t *testing.T, f *framework.Framework, lvdl *localv1.LocalVolumeDeviceLink, expectedPreferredTarget string) *localv1.LocalVolumeDeviceLink {
	matcher := gomega.NewWithT(t)
	matcher.Expect(lvdl).NotTo(gomega.BeNil(), "LVDL pointer must be provided")
	name := lvdl.Name
	matcher.Expect(name).ToNot(gomega.BeEmpty(), "LVDL name must be set")
	namespace := lvdl.Namespace
	matcher.Expect(namespace).ToNot(gomega.BeEmpty(), "LVDL namespace must be set")
	matcher.Eventually(func() string {
		lvdl = eventuallyGetLVDL(t, f, namespace, name)
		return lvdl.Status.PreferredLinkTarget
	}, time.Minute*5, time.Second*5).Should(gomega.Equal(expectedPreferredTarget), "waiting for LVDL %q preferred link target to update", name)
	return lvdl
}

func waitForLVDLLinkTargets(t *testing.T, f *framework.Framework, lvdl *localv1.LocalVolumeDeviceLink, expectedCurrentTarget, expectedPreferredTarget string) *localv1.LocalVolumeDeviceLink {
	matcher := gomega.NewWithT(t)
	matcher.Expect(lvdl).NotTo(gomega.BeNil(), "LVDL pointer must be provided")
	name := lvdl.Name
	matcher.Expect(name).ToNot(gomega.BeEmpty(), "LVDL name must be set")
	namespace := lvdl.Namespace
	matcher.Expect(namespace).ToNot(gomega.BeEmpty(), "LVDL namespace must be set")
	matcher.Eventually(func() bool {
		lvdl = eventuallyGetLVDL(t, f, namespace, name)
		return lvdl.Status.CurrentLinkTarget == expectedCurrentTarget &&
			lvdl.Status.PreferredLinkTarget == expectedPreferredTarget
	}, time.Minute*5, time.Second*5).Should(gomega.BeTrue(), "waiting for LVDL %q link targets to converge", name)
	return lvdl
}

func updateLVDLPolicy(t *testing.T, f *framework.Framework, lvdl *localv1.LocalVolumeDeviceLink, policy localv1.DeviceLinkPolicy) *localv1.LocalVolumeDeviceLink {
	matcher := gomega.NewWithT(t)
	matcher.Expect(lvdl).NotTo(gomega.BeNil(), "LVDL pointer must be provided")
	name := lvdl.Name
	matcher.Expect(name).ToNot(gomega.BeEmpty(), "LVDL name must be set")
	namespace := lvdl.Namespace
	matcher.Expect(namespace).ToNot(gomega.BeEmpty(), "LVDL namespace must be set")
	matcher.Eventually(func() error {
		if err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, lvdl); err != nil {
			return err
		}
		lvdl.Spec.Policy = policy
		return f.Client.Update(goctx.TODO(), lvdl)
	}, time.Minute, time.Second*5).ShouldNot(gomega.HaveOccurred(), "updating LVDL %q policy", name)
	return eventuallyGetLVDL(t, f, namespace, name)
}

func waitForRecreatedPVByName(t *testing.T, f *framework.Framework, name string, previousUID types.UID) corev1.PersistentVolume {
	matcher := gomega.NewWithT(t)
	pv := corev1.PersistentVolume{}
	matcher.Eventually(func() bool {
		err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: name}, &pv)
		if err != nil {
			t.Logf("waiting for recreated PV %q: %v", name, err)
			return false
		}
		if pv.UID == previousUID || pv.UID == "" {
			t.Logf("PV %q has not been recreated yet; current UID=%q previous UID=%q", name, pv.UID, previousUID)
			return false
		}
		return true
	}, time.Minute*8, time.Second*10).Should(gomega.BeTrue(), "waiting for PV %q recreation", name)
	return pv
}

// verifyMultiStepPreferredLinkReconciliation tests that the preferred symlink
// can be changed in multiple steps, each time to a higher-priority by-id link.
// At each step it verifies:
//   - The LVDL reflects the updated current and preferred link targets
//   - The on-disk symlink points to the expected target
//   - After PV deletion, the PV is recreated with the correct symlink target
//
// The three steps use increasingly preferred by-id patterns:
func verifyMultiStepPreferredLinkReconciliation(
	t *testing.T,
	ctx *framework.TestCtx,
	f *framework.Framework,
	namespace string,
	storageClassName string,
	pv corev1.PersistentVolume,
	currentSymlink string,
) (corev1.PersistentVolume, []cleanupFn) {
	matcher := gomega.NewWithT(t)
	nodeHostName := findNodeHostnameForPV(t, &pv)
	cleanups := make([]cleanupFn, 0, 3)

	type relinkStep struct {
		name   string
		target string
	}
	// Steps must go from lower to higher priority to trigger relinking.
	// Priority (high to low): wwn > scsi-3 > scsi-2 > scsi-8 > scsi-S > scsi-1 > scsi-0 > scsi > nvme-eui > nvme
	// The currentSymlink is already scsi-1 (from the single-step test), so we
	// step through scsi-2 → scsi-3 → wwn (each higher priority than the last).
	steps := []relinkStep{
		{"scsi-2", "/dev/disk/by-id/scsi-2-local-storage-e2e-step1"},
		{"scsi-3", "/dev/disk/by-id/scsi-3-local-storage-e2e-step2"},
		{"wwn", "/dev/disk/by-id/wwn-local-storage-e2e-step3"},
	}

	// pv name is same as LVDL name
	selectedLVDL := eventuallyGetLVDL(t, f, namespace, pv.Name)

	// ---- Step 1: initial relink ----
	t.Logf("multi-step relink step 1: adding %s symlink %s", steps[0].name, steps[0].target)
	addNewUdevSymlink(t, ctx, nodeHostName, currentSymlink, steps[0].target)
	cleanups = append(cleanups, cleanupFn{
		name: fmt.Sprintf("removeUdevSymlink-%s", filepath.Base(steps[0].target)),
		fn: func(t *testing.T) error {
			removeUdevSymlink(t, ctx, nodeHostName, steps[0].target)
			return nil
		},
	})

	t.Log("step 1: waiting for LVDL PreferredLinkTarget to update")
	lvdl := waitForLVDLPreferredLinkTarget(t, f, selectedLVDL, steps[0].target)
	matcher.Expect(lvdl.Status.CurrentLinkTarget).To(gomega.Equal(currentSymlink),
		"step 1: current should still be original before policy change")

	t.Log("step 1: setting LVDL policy to PreferredLinkTarget")
	lvdl = updateLVDLPolicy(t, f, lvdl, localv1.DeviceLinkPolicyPreferredLinkTarget)
	lvdl = waitForLVDLLinkTargets(t, f, lvdl, steps[0].target, steps[0].target)
	verifyNodeSymlinkTarget(t, ctx, nodeHostName, pv.Spec.Local.Path, steps[0].target)
	currentSymlink = lvdl.Status.CurrentLinkTarget

	// ---- Steps 2 and 3: higher-priority links, auto-relink, PV recreation ----
	for i := 1; i < len(steps); i++ {
		step := steps[i]
		prevTarget := steps[i-1].target

		removalTarget := step.target

		t.Logf("multi-step relink step %d: adding %s symlink %s", i+1, step.name, step.target)
		addNewUdevSymlink(t, ctx, nodeHostName, prevTarget, step.target)
		cleanups = append(cleanups, cleanupFn{
			name: fmt.Sprintf("removeUdevSymlink-%s", filepath.Base(removalTarget)),
			fn: func(t *testing.T) error {
				removeUdevSymlink(t, ctx, nodeHostName, removalTarget)
				return nil
			},
		})
		t.Logf("step %d: wait for lvdl preferredTarget update to %s", i+1, step.target)
		lvdl = waitForLVDLPreferredLinkTarget(t, f, lvdl, step.target)

		// Policy is already PreferredLinkTarget, so relink should happen automatically.
		t.Logf("step %d: waiting for auto-relink to %s", i+1, step.target)
		lvdl = waitForLVDLLinkTargets(t, f, lvdl, step.target, step.target)
		verifyNodeSymlinkTarget(t, ctx, nodeHostName, pv.Spec.Local.Path, step.target)
		currentSymlink = lvdl.Status.CurrentLinkTarget

		// Verify PV comes back correctly after deletion.
		t.Logf("step %d: deleting PV %q and verifying recreation", i+1, pv.Name)
		oldPVUID := pv.UID
		oldPVPath := pv.Spec.Local.Path
		eventuallyDelete(t, false, &pv)

		// After PV deletion + symlink cleanup + recreation, the symlink filename
		// may change (the recreated symlink uses the current preferred by-id name).
		// Use eventuallyFindPVs to find the PV regardless of name.
		pv = waitForRecreatedPVByName(t, f, pv.Name, oldPVUID)
		matcher.Expect(pv.Spec.Local.Path).To(gomega.Equal(oldPVPath))

		// Verify the recreated PV's symlink points to the correct target.
		verifyNodeSymlinkTarget(t, ctx, nodeHostName, pv.Spec.Local.Path, step.target)

		// Verify LVDL reflects the correct state after PV recreation.
		t.Logf("step %d: verifying LVDL state after PV recreation", i+1)
		lvdls := eventuallyFindLVDLsForPVs(t, f, namespace, []string{pv.Name})
		matcher.Expect(lvdls).To(gomega.HaveLen(1),
			"step %d: expected exactly 1 LVDL for recreated PV", i+1)
		lvdl = &lvdls[0]
		matcher.Expect(lvdl.Status.CurrentLinkTarget).To(gomega.Equal(step.target),
			"step %d: LVDL CurrentLinkTarget after PV recreation", i+1)
		matcher.Expect(lvdl.Status.PreferredLinkTarget).To(gomega.Equal(step.target),
			"step %d: LVDL PreferredLinkTarget after PV recreation", i+1)
		matcher.Expect(lvdl.Spec.Policy).To(gomega.Equal(localv1.DeviceLinkPolicyPreferredLinkTarget),
			"step %d: LVDL policy must remain PreferredLinkTarget after PV recreation", i+1)
	}

	return pv, cleanups
}

// note these jobs must not exceed more than 63 characters
func newCheckSymlinkTargetJob(nodeHostname, namespace, symlinkPath, expectedTarget string) (*batchv1.Job, error) {
	script := `
set -eu
set -x

actual_target="$(readlink "$SYMLINK_PATH")"
[[ "$actual_target" == "$EXPECTED_TARGET" ]]
`
	return newNodeJob(
		nodeHostname,
		namespace,
		fmt.Sprintf("check-symlink-%s", nodeHostname),
		"checks that a host symlink points to the expected target",
		[]string{"/bin/bash", "-c", script},
		&NodeJobOptions{
			ContainerRestartPolicy: corev1.RestartPolicyNever,
			Env: []corev1.EnvVar{
				{Name: "SYMLINK_PATH", Value: symlinkPath},
				{Name: "EXPECTED_TARGET", Value: expectedTarget},
			},
		})
}

func verifyNodeSymlinkTarget(t *testing.T, ctx *framework.TestCtx, nodeHostname, symlinkPath, expectedTarget string) {
	matcher := gomega.NewWithT(t)

	namespace, err := ctx.GetOperatorNamespace()
	matcher.Expect(err).NotTo(gomega.HaveOccurred(), "could not determine namespace")

	job, err := newCheckSymlinkTargetJob(nodeHostname, namespace, symlinkPath, expectedTarget)
	matcher.Expect(err).NotTo(gomega.HaveOccurred(), "could not create symlink target check job")

	createOrReplaceJob(t, ctx, job, fmt.Sprintf("creating symlink target check job on node: %q", nodeHostname))
	waitForJobCompletion(t, job, fmt.Sprintf("waiting for symlink target check job to complete: %q", job.GetName()))
	job.TypeMeta.Kind = "Job"
	eventuallyDelete(t, false, job)
}
