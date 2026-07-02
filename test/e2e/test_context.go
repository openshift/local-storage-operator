package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	. "github.com/onsi/gomega"
	localv1 "github.com/openshift/local-storage-operator/api/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
	framework "github.com/openshift/local-storage-operator/test/framework"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	waitPeriodForPVDeletion = 5 * time.Minute
	waitPeriodForSCDeletion = 2 * time.Minute
)

// hostSymlink records a udev symlink created on a node's /dev/disk/by-id that
// must be removed during cleanup.
type hostSymlink struct {
	nodeHostname string
	linkPath     string
}

// testContext bundles state shared across ordered It blocks within a Context.
// It owns host-side symlinks created during reconciliation/fallback tests and
// optional LV/LVSet CRs. Cleanup runs once at Context exit via DeferCleanup.
//
// The cleanup ordering is critical: LV/LVSet CRs and owned PVs are deleted
// FIRST, and host by-id symlinks are removed LAST. This is because diskmaker's
// quick_reset.sh resolves /mnt/local-storage/<sc>/<name> -> /dev/disk/by-id/<id>
// -> /dev/<dev> and validates the block device with [ -b "$BLKDEVICE" ]. If
// the by-id symlink is removed before the PV is wiped, the wipe stalls, the PV
// is never deleted, and the local-volume-protection finalizer never clears.
type testContext struct {
	f         *framework.Framework
	namespace string
	nodeEnv   []nodeDisks

	// LV-specific (nil for LVSet-only contexts)
	localVolume *localv1.LocalVolume

	// LVSet-specific (nil for LV-only contexts)
	lvset  *localv1alpha1.LocalVolumeSet
	lvSets []*localv1alpha1.LocalVolumeSet

	// Shared accumulated state across It blocks
	pvs            []corev1.PersistentVolume
	selectedPV     corev1.PersistentVolume
	currentSymlink string
	lvdlNames      []string

	// Host /dev/disk/by-id symlinks created during tests.
	// Removed in Cleanup AFTER CR/PV deletion (diskmaker wipe depends on them).
	hostSymlinks []hostSymlink
}

// addHostSymlink creates a udev symlink on the host and records it for cleanup.
func (tc *testContext) addHostSymlink(nodeHostname, currentLink, newLink string) {
	addNewUdevSymlink(tc.namespace, nodeHostname, currentLink, newLink)
	tc.hostSymlinks = append(tc.hostSymlinks, hostSymlink{nodeHostname, newLink})
}

// Cleanup deletes LV/LVSet CRs and owned PVs first, then removes host symlinks.
// Safe to call even if some fields are nil (best-effort, idempotent).
func (tc *testContext) Cleanup() {
	// 1. Delete LV/LVSet CRs + owned PVs + StorageClasses FIRST.
	//    diskmaker's quick_reset.sh resolves the symlink chain and validates
	//    the block device; by-id symlinks must be present or the wipe stalls.
	if tc.localVolume != nil {
		cleanupLVResources(tc.f, tc.localVolume)
	}
	for _, lvset := range tc.lvSets {
		cleanupSingleLVSet(tc.f, lvset)
	}

	// 2. Remove host by-id symlinks AFTER CR/PV deletion is complete.
	for i := len(tc.hostSymlinks) - 1; i >= 0; i-- {
		s := tc.hostSymlinks[i]
		removeUdevSymlink(tc.namespace, s.nodeHostname, s.linkPath)
	}
}

// cleanupLVResources deletes a LocalVolume, its owned PVs, and its StorageClass.
func cleanupLVResources(f *framework.Framework, localVolume *localv1.LocalVolume) {
	eventuallyDelete(localVolume)
	checkForPersistentVolumes(f, localVolume)
	checkForStorageClass(f, localVolume.Spec.StorageClassDevices[0].StorageClassName)
}

// cleanupSingleLVSet deletes a single LocalVolumeSet, its owned PVs, and its
// StorageClass. Extracted from cleanupLVSetResources for reuse by testContext.
func cleanupSingleLVSet(f *framework.Framework, lvset *localv1alpha1.LocalVolumeSet) {
	f.Logf("cleaning up pvs and storageclasses: %q", lvset.GetName())

	eventuallyDelete(lvset)
	checkForPersistentVolumes(f, lvset)
	checkForStorageClass(f, lvset.Spec.StorageClassName)
}

// checkForPersistentVolumes verifies that no PVs exist for the given LocalVolume or LocalVolumeSet.
func checkForPersistentVolumes(f *framework.Framework, obj client.Object) {
	name := obj.GetName()
	namespace := obj.GetNamespace()

	var ownerKind string
	switch obj.(type) {
	case *localv1.LocalVolume:
		ownerKind = localv1.LocalVolumeKind
	case *localv1alpha1.LocalVolumeSet:
		ownerKind = localv1.LocalVolumeSetKind
	default:
		Fail(fmt.Sprintf("checkForPersistentVolumes: unsupported type %T", obj))
	}

	Eventually(func(ctx context.Context) error {
		pvList := &corev1.PersistentVolumeList{}

		err := f.Client.List(ctx, pvList, client.MatchingLabels{
			common.PVOwnerKindLabel:      ownerKind,
			common.PVOwnerNamespaceLabel: namespace,
			common.PVOwnerNameLabel:      name,
		})
		if err != nil {
			return fmt.Errorf("checkForPersistentVolumes: cannot list PVs for %s %s: %v", ownerKind, name, err)
		}
		if len(pvList.Items) != 0 {
			return fmt.Errorf("checkForPersistentVolumes: %d PVs still exist for %s %s", len(pvList.Items), ownerKind, name)
		}
		f.Logf("checkForPersistentVolumes: no PVs found for %s %s", ownerKind, name)
		return nil
	}, waitPeriodForPVDeletion, time.Second*2).WithContext(context.Background()).ShouldNot(HaveOccurred(), "check for %s %s", ownerKind, name)
}

func checkForStorageClass(f *framework.Framework, scName string) {
	Eventually(func(ctx context.Context) error {
		_, err := f.KubeClient.StorageV1().StorageClasses().Get(ctx, scName, metav1.GetOptions{})
		if err == nil {
			return fmt.Errorf("checkForStorageClass: StorageClass %s still exists", scName)
		} else if !apierrors.IsNotFound(err) {
			return fmt.Errorf("checkForStorageClass: failed to Get() StorageClass %s: %v", scName, err)
		}
		f.Logf("checkForStorageClass: StorageClass %s not found -- OK", scName)
		return nil
	}, waitPeriodForSCDeletion, time.Second*2).WithContext(context.Background()).ShouldNot(gomega.HaveOccurred(), "check for StorageClass %s", scName)

}
