package e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	localv1 "github.com/openshift/local-storage-operator/api/v1"
	framework "github.com/openshift/local-storage-operator/test/framework"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

func checkForSymlinks(namespace string, nodeEnv []nodeDisks, path string) error {
	f := framework.Global
	f.Logf("checking for leftover symlinks on host")

	checkSymlinkJobs := make([]*batchv1.Job, 0)

	for _, nd := range nodeEnv {
		node := nd.node
		f.Logf("creating check-symlink job on node: %q", node.GetName())
		checkSymlinkJob, err := newNodeCheckSymlinkJob(node, namespace, path)
		if err != nil {
			return err
		}
		checkSymlinkJobs = append(checkSymlinkJobs, checkSymlinkJob)
		createOrReplaceJob(namespace, checkSymlinkJob, fmt.Sprintf("creating check-symlink job on node: %q", node.GetName()))
	}

	for _, checkSymlinkJob := range checkSymlinkJobs {
		waitForJobCompletion(checkSymlinkJob, fmt.Sprintf("waiting for check-symlink job to complete: %q", checkSymlinkJob.GetName()))
		checkSymlinkJob.TypeMeta.Kind = "Job"
		eventuallyDelete(checkSymlinkJob)
	}

	return nil
}

func newNodeCheckSymlinkJob(node corev1.Node, namespace string, path string) (*batchv1.Job, error) {
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

func currentSymlinkForDisk(d disk) string {
	if d.id != "" {
		return filepath.Join("/dev/disk/by-id", d.id)
	}
	return filepath.Join("/dev", d.name)
}

func findCurrentSymlinkForPV(nodeEnv []nodeDisks, pv *corev1.PersistentVolume) string {
	nodeHostName := findNodeHostnameForPV(pv)
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
	Fail(fmt.Sprintf("failed to find current symlink for PV %q on node hostname %q", pv.Name, nodeHostName))
	return ""
}

// findNodeHostnameForLVDL returns the node hostname where the LVDL's PV is scheduled,
// by inspecting the PV's spec.nodeAffinity (Required, LabelHostname).
func findNodeHostnameForPV(pv *corev1.PersistentVolume) string {
	pvName := pv.Name
	if pv.Spec.NodeAffinity == nil || pv.Spec.NodeAffinity.Required == nil {
		Fail(fmt.Sprintf("PV %s has no NodeAffinity.Required", pvName))
	}
	for _, term := range pv.Spec.NodeAffinity.Required.NodeSelectorTerms {
		for _, expr := range term.MatchExpressions {
			if expr.Key == corev1.LabelHostname && len(expr.Values) > 0 {
				return expr.Values[0]
			}
		}
	}
	Fail(fmt.Sprintf("PV %s NodeAffinity has no %q expression", pvName, corev1.LabelHostname))
	return ""
}

func eventuallyGetLVDL(f *framework.Framework, namespace, name string) *localv1.LocalVolumeDeviceLink {
	lvdl := &localv1.LocalVolumeDeviceLink{}

	Eventually(func(ctx context.Context) error {
		return f.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, lvdl)
	}, time.Minute*5, time.Second*5).WithContext(context.Background()).ShouldNot(HaveOccurred(), "waiting for LocalVolumeDeviceLink %q", name)
	return lvdl
}

func waitForLVDLPreferredLinkTarget(f *framework.Framework, lvdl *localv1.LocalVolumeDeviceLink, expectedPreferredTarget string) *localv1.LocalVolumeDeviceLink {
	Expect(lvdl).NotTo(BeNil(), "LVDL pointer must be provided")
	name := lvdl.Name
	Expect(name).ToNot(BeEmpty(), "LVDL name must be set")
	namespace := lvdl.Namespace
	Expect(namespace).ToNot(BeEmpty(), "LVDL namespace must be set")
	Eventually(func() string {
		lvdl = eventuallyGetLVDL(f, namespace, name)
		return lvdl.Status.PreferredLinkTarget
	}, time.Minute*5, time.Second*5).Should(Equal(expectedPreferredTarget), "waiting for LVDL %q preferred link target to update", name)
	return lvdl
}

func waitForLVDLLinkTargets(f *framework.Framework, lvdl *localv1.LocalVolumeDeviceLink, expectedCurrentTarget, expectedPreferredTarget string) *localv1.LocalVolumeDeviceLink {
	Expect(lvdl).NotTo(BeNil(), "LVDL pointer must be provided")
	name := lvdl.Name
	Expect(name).ToNot(BeEmpty(), "LVDL name must be set")
	namespace := lvdl.Namespace
	Expect(namespace).ToNot(BeEmpty(), "LVDL namespace must be set")
	Eventually(func() bool {
		lvdl = eventuallyGetLVDL(f, namespace, name)
		return lvdl.Status.CurrentLinkTarget == expectedCurrentTarget &&
			lvdl.Status.PreferredLinkTarget == expectedPreferredTarget
	}, time.Minute*5, time.Second*5).Should(BeTrue(), "waiting for LVDL %q link targets to converge", name)
	return lvdl
}

func updateLVDLPolicy(f *framework.Framework, lvdl *localv1.LocalVolumeDeviceLink, policy localv1.DeviceLinkPolicy) *localv1.LocalVolumeDeviceLink {
	Expect(lvdl).NotTo(BeNil(), "LVDL pointer must be provided")
	name := lvdl.Name
	Expect(name).ToNot(BeEmpty(), "LVDL name must be set")
	namespace := lvdl.Namespace
	Expect(namespace).ToNot(BeEmpty(), "LVDL namespace must be set")
	Eventually(func(ctx context.Context) error {
		if err := f.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, lvdl); err != nil {
			return err
		}
		lvdl.Spec.Policy = policy
		return f.Client.Update(ctx, lvdl)
	}, time.Minute, time.Second*5).WithContext(context.Background()).ShouldNot(HaveOccurred(), "updating LVDL %q policy", name)
	return eventuallyGetLVDL(f, namespace, name)
}

func waitForRecreatedPVByName(f *framework.Framework, name string, previousUID types.UID) corev1.PersistentVolume {
	pv := corev1.PersistentVolume{}
	Eventually(func(ctx context.Context) bool {
		err := f.Client.Get(ctx, types.NamespacedName{Name: name}, &pv)
		if err != nil {
			f.Logf("waiting for recreated PV %q: %v", name, err)
			return false
		}
		if pv.UID == previousUID || pv.UID == "" {
			f.Logf("PV %q has not been recreated yet; current UID=%q previous UID=%q", name, pv.UID, previousUID)
			return false
		}
		return true
	}, time.Minute*8, time.Second*10).WithContext(context.Background()).Should(BeTrue(), "waiting for PV %q recreation", name)
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
	tc *testContext,
	pv corev1.PersistentVolume,
	currentSymlink string,
) corev1.PersistentVolume {
	nodeHostName := findNodeHostnameForPV(&pv)

	type relinkStep struct {
		name   string
		target string
	}
	// Steps must go from lower to higher priority to trigger relinking.
	// Priority (high to low): wwn > scsi-3 > scsi-2 > scsi-8 > scsi-S > scsi-1 > scsi-0 > scsi > nvme-eui > nvme
	// step through scsi-2 → scsi-3 → wwn (each higher priority than the last).
	steps := []relinkStep{
		{"scsi-2", "/dev/disk/by-id/scsi-2-local-storage-e2e-step1"},
		{"scsi-3", "/dev/disk/by-id/scsi-3-local-storage-e2e-step2"},
		{"wwn", "/dev/disk/by-id/wwn-local-storage-e2e-step3"},
	}

	selectedLVDL := eventuallyGetLVDL(tc.f, tc.namespace, pv.Name)

	currentSymlink = selectedLVDL.Status.CurrentLinkTarget

	prometheusClient := newPrometheusClient(tc.f)
	waitForMetric(prometheusClient, fmt.Sprintf("ALERTS{alertname=\"LSODeviceLinkMismatch\", alertstate=\"pending\", persistent_volume=\"%s\"}", selectedLVDL.Name), 0)

	// ---- Step 1: initial relink ----
	tc.f.Logf("multi-step relink step 1: adding %s symlink %s", steps[0].name, steps[0].target)
	tc.addHostSymlink(nodeHostName, currentSymlink, steps[0].target)

	tc.f.Logf("step 1: waiting for LVDL PreferredLinkTarget to update")
	lvdl := waitForLVDLPreferredLinkTarget(tc.f, selectedLVDL, steps[0].target)
	Expect(lvdl.Status.CurrentLinkTarget).To(Equal(currentSymlink),
		"step 1: current should still be original before policy change")

	waitForMetric(prometheusClient, fmt.Sprintf("lso_device_link_mismatch{name=\"%s\", policy=\"None\"}", lvdl.Name), 1)
	waitForMetric(prometheusClient, fmt.Sprintf("ALERTS{alertname=\"LSODeviceLinkMismatch\", alertstate=\"pending\", persistent_volume=\"%s\"}", lvdl.Name), 1)

	lvdl = updateLVDLPolicy(tc.f, lvdl, localv1.DeviceLinkPolicyCurrentLinkTarget)
	waitForMetric(prometheusClient, fmt.Sprintf("lso_device_link_mismatch{name=\"%s\", policy=\"None\"}", lvdl.Name), 0)
	waitForMetric(prometheusClient, fmt.Sprintf("lso_device_link_mismatch{name=\"%s\", policy=\"CurrentLinkTarget\"}", lvdl.Name), 1)

	tc.f.Logf("step 1: setting LVDL policy to PreferredLinkTarget")
	lvdl = updateLVDLPolicy(tc.f, lvdl, localv1.DeviceLinkPolicyPreferredLinkTarget)
	lvdl = waitForLVDLLinkTargets(tc.f, lvdl, steps[0].target, steps[0].target)
	verifyNodeSymlinkTarget(tc.namespace, nodeHostName, pv.Spec.Local.Path, steps[0].target)

	// ---- Steps 2 and 3: higher-priority links, auto-relink, PV recreation ----
	for i := 1; i < len(steps); i++ {
		step := steps[i]
		prevTarget := steps[i-1].target

		tc.f.Logf("multi-step relink step %d: adding %s symlink %s", i+1, step.name, step.target)
		tc.addHostSymlink(nodeHostName, prevTarget, step.target)

		tc.f.Logf("step %d: wait for lvdl preferredTarget update to %s", i+1, step.target)
		lvdl = waitForLVDLPreferredLinkTarget(tc.f, lvdl, step.target)

		tc.f.Logf("step %d: waiting for auto-relink to %s", i+1, step.target)
		lvdl = waitForLVDLLinkTargets(tc.f, lvdl, step.target, step.target)
		verifyNodeSymlinkTarget(tc.namespace, nodeHostName, pv.Spec.Local.Path, step.target)

		tc.f.Logf("step %d: deleting PV %q and verifying recreation", i+1, pv.Name)
		oldPVUID := pv.UID
		oldPVPath := pv.Spec.Local.Path
		eventuallyDeletePV(&pv)

		pv = waitForRecreatedPVByName(tc.f, pv.Name, oldPVUID)
		Expect(pv.Spec.Local.Path).To(Equal(oldPVPath))

		verifyNodeSymlinkTarget(tc.namespace, nodeHostName, pv.Spec.Local.Path, step.target)

		tc.f.Logf("step %d: verifying LVDL state after PV recreation", i+1)
		lvdls := eventuallyFindLVDLsForPVs(tc.f, tc.namespace, []string{pv.Name})
		Expect(lvdls).To(HaveLen(1),
			"step %d: expected exactly 1 LVDL for recreated PV", i+1)
		lvdl = &lvdls[0]
		Expect(lvdl.Status.CurrentLinkTarget).To(Equal(step.target),
			"step %d: LVDL CurrentLinkTarget after PV recreation", i+1)
		Expect(lvdl.Status.PreferredLinkTarget).To(Equal(step.target),
			"step %d: LVDL PreferredLinkTarget after PV recreation", i+1)
		Expect(lvdl.Spec.Policy).To(Equal(localv1.DeviceLinkPolicyPreferredLinkTarget),
			"step %d: LVDL policy must remain PreferredLinkTarget after PV recreation", i+1)
		Expect(lvdl.Status.PersistentVolumeSymlinkPath).To(Equal(pv.Spec.Local.Path),
			"step %d: LVDL SymlinkPath after PV recreation should match PV local path", i+1)
		waitForMetric(prometheusClient, fmt.Sprintf("lso_device_link_mismatch{name=\"%s\"}", lvdl.Name), 0)
	}
	waitForMetric(prometheusClient, fmt.Sprintf("ALERTS{alertname=\"LSODeviceLinkMismatch\", alertstate=\"pending\", persistent_volume=\"%s\"}", lvdl.Name), 0)

	return pv
}

// verifySymlinkFallbackOnDisappearingLink tests that when the current preferred
// by-id symlink disappears from /dev/disk/by-id (e.g. udev removes a wwn- link),
// LSO automatically detects the dangling symlink in /mnt/local-storage and
// relinks it to the next-best available by-id symlink.
//
// Precondition: the PV's on-disk symlink currently points to currentPreferred
// (e.g. wwn-*), and expectedFallback (e.g. scsi-3-*) also exists on the node
// for the same underlying device. LVDL policy must already be PreferredLinkTarget.
func verifySymlinkFallbackOnDisappearingLink(
	tc *testContext,
	pv corev1.PersistentVolume,
	currentPreferred string,
	expectedFallback string,
) corev1.PersistentVolume {
	nodeHostName := findNodeHostnameForPV(&pv)

	lvdl := eventuallyGetLVDL(tc.f, tc.namespace, pv.Name)
	Expect(lvdl.Status.CurrentLinkTarget).To(Equal(currentPreferred),
		"precondition: LVDL CurrentLinkTarget should be the link we are about to remove")
	Expect(lvdl.Spec.Policy).To(Equal(localv1.DeviceLinkPolicyPreferredLinkTarget),
		"precondition: LVDL policy must be PreferredLinkTarget")

	tc.f.Logf("fallback test: removing current preferred link %s from node %s", currentPreferred, nodeHostName)
	removeUdevSymlink(tc.namespace, nodeHostName, currentPreferred)

	tc.f.Logf("fallback test: waiting for auto-relink from %s to %s", currentPreferred, expectedFallback)
	lvdl = waitForLVDLLinkTargets(tc.f, lvdl, expectedFallback, expectedFallback)

	verifyNodeSymlinkTarget(tc.namespace, nodeHostName, pv.Spec.Local.Path, expectedFallback)

	tc.f.Logf("fallback test: deleting PV %q and verifying recreation with fallback target", pv.Name)
	oldPVUID := pv.UID
	oldPVPath := pv.Spec.Local.Path
	eventuallyDeletePV(&pv)

	pv = waitForRecreatedPVByName(tc.f, pv.Name, oldPVUID)
	Expect(pv.Spec.Local.Path).To(Equal(oldPVPath),
		"recreated PV should keep the same local path")
	verifyNodeSymlinkTarget(tc.namespace, nodeHostName, pv.Spec.Local.Path, expectedFallback)

	lvdls := eventuallyFindLVDLsForPVs(tc.f, tc.namespace, []string{pv.Name})
	Expect(lvdls).To(HaveLen(1), "expected exactly 1 LVDL for recreated PV")
	Expect(lvdls[0].Status.CurrentLinkTarget).To(Equal(expectedFallback),
		"LVDL CurrentLinkTarget after fallback and PV recreation")
	Expect(lvdls[0].Status.PreferredLinkTarget).To(Equal(expectedFallback),
		"LVDL PreferredLinkTarget after fallback and PV recreation")
	Expect(lvdls[0].Spec.Policy).To(Equal(localv1.DeviceLinkPolicyPreferredLinkTarget),
		"LVDL policy must remain PreferredLinkTarget after fallback")
	Expect(lvdls[0].Status.PersistentVolumeSymlinkPath).To(Equal(pv.Spec.Local.Path),
		"LVDL SymlinkPath after fallback and PV recreation should match PV local path")

	return pv
}

// verifySymlinkRestorationAfterWipe deletes all symlinks under /mnt/local-storage
// on the PV's node, then verifies that LSO restores the symlink when the LVDL
// has PreferredLinkTarget policy.
//
// Precondition: the PV's LVDL must have policy=PreferredLinkTarget.
//
// A DeferCleanup restores the PV symlink if the test fails or times out after
// the wipe; otherwise diskmaker's PV deleter cannot resolve the device and
// LocalVolume(Set) cleanup hangs on the protection finalizer.
func verifySymlinkRestorationAfterWipe(
	tc *testContext,
	pv corev1.PersistentVolume,
) corev1.PersistentVolume {
	nodeHostname := findNodeHostnameForPV(&pv)

	lvdl := eventuallyGetLVDL(tc.f, tc.namespace, pv.Name)
	Expect(lvdl.Spec.Policy).To(Equal(localv1.DeviceLinkPolicyPreferredLinkTarget),
		"precondition: LVDL policy must be PreferredLinkTarget")
	preferredTarget := lvdl.Status.PreferredLinkTarget
	Expect(preferredTarget).ToNot(BeEmpty(),
		"precondition: LVDL PreferredLinkTarget must be set before wipe")

	symlinkPath := pv.Spec.Local.Path
	DeferCleanup(func() {
		tc.f.Logf("cleanup: ensuring PV symlink %s -> %s exists on node %s after wipe test",
			symlinkPath, preferredTarget, nodeHostname)
		restoreLocalStorageSymlink(tc.namespace, nodeHostname, symlinkPath, preferredTarget)
	})

	tc.f.Logf("wipe test: wiping /mnt/local-storage on node %s", nodeHostname)
	wipeLocalStorageSymlinks(tc.namespace, nodeHostname, symlinkPath)

	tc.f.Logf("wipe test: waiting for symlink restoration %s -> %s", symlinkPath, preferredTarget)
	eventuallyVerifyNodeSymlinkTarget(tc.namespace, nodeHostname, symlinkPath, preferredTarget)

	tc.f.Logf("wipe test: verifying LVDL state after symlink restoration")
	lvdl = waitForLVDLLinkTargets(tc.f, lvdl, preferredTarget, preferredTarget)
	Expect(lvdl.Spec.Policy).To(Equal(localv1.DeviceLinkPolicyPreferredLinkTarget),
		"LVDL policy must remain PreferredLinkTarget after restoration")

	return pv
}

// verifySymlinkRestorationAfterWipeNoFSSignature covers the valid-device reconcile
// path: the PV has never been consumed, so LVDL.Status.FilesystemUUID is empty.
func verifySymlinkRestorationAfterWipeNoFSSignature(
	tc *testContext,
	pv corev1.PersistentVolume,
) corev1.PersistentVolume {
	lvdl := eventuallyGetLVDL(tc.f, tc.namespace, pv.Name)
	Expect(lvdl.Status.FilesystemUUID).To(BeEmpty(),
		"precondition: LVDL FilesystemUUID must be empty when device has no filesystem signature")
	return verifySymlinkRestorationAfterWipe(tc, pv)
}

// verifySymlinkRestorationAfterWipeWithFSSignature covers the rejected-device
// reconcile path. It consumes the filesystem PV (so blkid reports an FS UUID
// and the device is in-use), wipes /mnt/local-storage while the volume is still
// Bound, verifies symlink restoration, then deletes the consumers.
//
// Do not release the PVC before wipe: Delete reclaim clears the filesystem
// signature and would send the device back through the valid-device path.
func verifySymlinkRestorationAfterWipeWithFSSignature(
	tc *testContext,
	pv corev1.PersistentVolume,
) corev1.PersistentVolume {
	tc.f.Logf("wipe+fs test: consuming PV %q to establish filesystem signature", pv.Name)
	pvc, job, pod := consumePV(tc.namespace, pv)
	verifyLVDLFilesystemUUIDForPVs(tc.f, tc.namespace, []string{pv.Name})

	lvdl := eventuallyGetLVDL(tc.f, tc.namespace, pv.Name)
	Expect(lvdl.Status.FilesystemUUID).ToNot(BeEmpty(),
		"precondition: LVDL FilesystemUUID must be set when device has a filesystem signature")

	tc.f.Logf("wipe+fs test: wiping while PV %q is still Bound", pv.Name)
	pv = verifySymlinkRestorationAfterWipe(tc, pv)

	lvdl = eventuallyGetLVDL(tc.f, tc.namespace, pv.Name)
	Expect(lvdl.Status.FilesystemUUID).ToNot(BeEmpty(),
		"FilesystemUUID must remain set after wipe restoration while Bound")

	tc.f.Logf("wipe+fs test: deleting consumers for PV %q", pv.Name)
	eventuallyDelete(job, pvc, pod)
	return pv
}

func wipeLocalStorageSymlinks(namespace, nodeHostname, symlinkPath string) {
	f := framework.Global
	f.Logf("wiping /mnt/local-storage symlinks on node %s (expecting %s present)", nodeHostname, symlinkPath)

	job, err := newWipeLocalStorageJob(nodeHostname, namespace, symlinkPath)
	Expect(err).NotTo(HaveOccurred(), "could not create wipe job")

	createOrReplaceJob(namespace, job, fmt.Sprintf("creating wipe job on node: %q", nodeHostname))
	waitForJobCompletion(job, fmt.Sprintf("waiting for wipe job to complete: %q", job.GetName()))
	job.TypeMeta.Kind = "Job"
	eventuallyDelete(job)
}

// restoreLocalStorageSymlink recreates a /mnt/local-storage PV symlink on the
// node. Used by DeferCleanup after wipe tests so PV deletion can still resolve
// the device path if LSO did not restore the link in time.
func restoreLocalStorageSymlink(namespace, nodeHostname, symlinkPath, target string) {
	f := framework.Global
	f.Logf("restoring PV symlink %s -> %s on node %s", symlinkPath, target, nodeHostname)

	job, err := newRestoreLocalStorageSymlinkJob(nodeHostname, namespace, symlinkPath, target)
	Expect(err).NotTo(HaveOccurred(), "could not create restore symlink job")

	createOrReplaceJob(namespace, job, fmt.Sprintf("creating restore symlink job on node: %q", nodeHostname))
	waitForJobCompletion(job, fmt.Sprintf("waiting for restore symlink job to complete: %q", job.GetName()))
	job.TypeMeta.Kind = "Job"
	eventuallyDelete(job)
}

func newWipeLocalStorageJob(nodeHostname, namespace, symlinkPath string) (*batchv1.Job, error) {
	script := `
set -eu
set -x
echo "verifying PV symlink exists before wipe: $SYMLINK_PATH"
test -L "$SYMLINK_PATH"
echo "wiping symlinks under /mnt/local-storage"
find /mnt/local-storage -type l -delete -print
echo "wipe complete"
`
	return newNodeJob(
		nodeHostname,
		namespace,
		fmt.Sprintf("wipe-symlinks-%s", nodeHostname),
		"deletes all symlinks under /mnt/local-storage",
		[]string{"/bin/bash", "-c", script},
		&NodeJobOptions{
			ContainerRestartPolicy: corev1.RestartPolicyNever,
			Env: []corev1.EnvVar{
				{Name: "SYMLINK_PATH", Value: symlinkPath},
			},
		})
}

func newRestoreLocalStorageSymlinkJob(nodeHostname, namespace, symlinkPath, target string) (*batchv1.Job, error) {
	script := `
set -eu
set -x
echo "restoring PV symlink $SYMLINK_PATH -> $TARGET"
mkdir -p "$(dirname "$SYMLINK_PATH")"
ln -sfn "$TARGET" "$SYMLINK_PATH"
actual_target="$(readlink "$SYMLINK_PATH")"
[[ "$actual_target" == "$TARGET" ]]
`
	return newNodeJob(
		nodeHostname,
		namespace,
		fmt.Sprintf("restore-symlink-%s", nodeHostname),
		"restores a PV symlink under /mnt/local-storage after wipe tests",
		[]string{"/bin/bash", "-c", script},
		&NodeJobOptions{
			ContainerRestartPolicy: corev1.RestartPolicyNever,
			Env: []corev1.EnvVar{
				{Name: "SYMLINK_PATH", Value: symlinkPath},
				{Name: "TARGET", Value: target},
			},
		})
}

func newCheckSymlinkTargetJob(nodeHostname, namespace, symlinkPath, expectedTarget string) (*batchv1.Job, error) {
	script := `
set -eu
set -x

actual_target="$(readlink "$SYMLINK_PATH")"
[[ "$actual_target" == "$EXPECTED_TARGET" ]]
`
	backoffLimit := int32(0)
	return newNodeJob(
		nodeHostname,
		namespace,
		fmt.Sprintf("check-symlink-%s", nodeHostname),
		"checks that a host symlink points to the expected target",
		[]string{"/bin/bash", "-c", script},
		&NodeJobOptions{
			JobBackoffLimit:        &backoffLimit,
			ContainerRestartPolicy: corev1.RestartPolicyNever,
			Env: []corev1.EnvVar{
				{Name: "SYMLINK_PATH", Value: symlinkPath},
				{Name: "EXPECTED_TARGET", Value: expectedTarget},
			},
		})
}

// checkNodeSymlinkTarget runs a one-shot job that verifies the host symlink
// points at expectedTarget. Returns an error if the check job fails (e.g. the
// symlink is missing or points elsewhere), so callers can poll with Eventually.
func checkNodeSymlinkTarget(namespace, nodeHostname, symlinkPath, expectedTarget string) error {
	f := framework.Global
	job, err := newCheckSymlinkTargetJob(nodeHostname, namespace, symlinkPath, expectedTarget)
	if err != nil {
		return err
	}
	createOrReplaceJob(namespace, job, fmt.Sprintf("checking symlink target on node %s", nodeHostname))

	j := &batchv1.Job{}
	Eventually(func(ctx context.Context) int32 {
		if err := f.Client.Get(ctx, types.NamespacedName{Name: job.Name, Namespace: namespace}, j); err != nil {
			return 0
		}
		return j.Status.Succeeded + j.Status.Failed
	}, time.Minute, time.Second*5).WithContext(context.Background()).
		Should(BeNumerically(">=", 1),
			"check symlink job %q on node %q for path %q did not complete", job.Name, nodeHostname, symlinkPath)

	job.TypeMeta.Kind = "Job"
	eventuallyDelete(job)

	if j.Status.Succeeded == 0 {
		return fmt.Errorf("symlink %s does not point to %s on node %s", symlinkPath, expectedTarget, nodeHostname)
	}
	return nil
}

func verifyNodeSymlinkTarget(namespace, nodeHostname, symlinkPath, expectedTarget string) {
	Expect(checkNodeSymlinkTarget(namespace, nodeHostname, symlinkPath, expectedTarget)).To(Succeed(),
		"symlink %s on node %s should point to %s", symlinkPath, nodeHostname, expectedTarget)
}

func eventuallyVerifyNodeSymlinkTarget(namespace, nodeHostname, symlinkPath, expectedTarget string) {
	Eventually(func() error {
		return checkNodeSymlinkTarget(namespace, nodeHostname, symlinkPath, expectedTarget)
	}, time.Minute*8, time.Second*15).Should(Succeed(),
		"waiting for symlink %s on node %s to point to %s", symlinkPath, nodeHostname, expectedTarget)
}
