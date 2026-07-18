package e2e

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	operatorv1 "github.com/openshift/api/operator/v1"
	localv1 "github.com/openshift/local-storage-operator/api/v1"
	"github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/controllers/nodedaemon"
	framework "github.com/openshift/local-storage-operator/test/framework"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
)

var (
	awsEBSNitroRegex   = "^[cmr]5.*|t3|z1d"
	labelInstanceType  = "beta.kubernetes.io/instance-type"
	lvStorageClassName = "test-local-sc"
)

// Disk indices on node 0. diskIndexSharedByID is used by the by-id reproducer on
// both nodes and ext4 fsType reuses node-0 diskIndexSharedByID after that phase
const (
	diskIndexDefaultFsType = 0
	diskIndexSharedByID    = 1
	diskIndexXfsFsType     = 2
	diskIndexBlockMode     = 3
	diskIndexLVDL          = 4
	diskIndexTolerations   = 0
)

type fsTypeTestCase struct {
	name             string
	fsType           string
	volumeMode       localv1.PersistentVolumeMode
	storageClassName string
	diskIndex        int
	withTolerations  bool
}

var _ = Describe("LocalVolume", Label("LocalVolume"), Ordered, func() {
	var (
		f            *framework.Framework
		namespace    string
		nodeEnv      []nodeDisks
		nodeList     *corev1.NodeList
		selectedDisk disk
	)

	BeforeAll(func() {
		f = framework.Global
		namespace = f.OperatorNamespace

		nodeList = &corev1.NodeList{}
		err := f.Client.List(context.TODO(), nodeList, client.HasLabels{labelNodeRoleWorker})
		Expect(err).NotTo(HaveOccurred(), "listing worker nodes")
		Expect(len(nodeList.Items)).To(BeNumerically(">=", 3), "expected at least 3 worker nodes")

		// Disk sizes must be unique per node for discovery matching.
		nodeEnv = []nodeDisks{
			{
				// diskIndexDefaultFsType, diskIndexSharedByID, diskIndexXfsFsType, diskIndexBlockMode, diskIndexLVDL
				disks: []disk{{size: 10}, {size: 20}, {size: 30}, {size: 40}, {size: 15}},
				node:  nodeList.Items[0],
			},
			{
				disks: []disk{{size: 10}, {size: 20}},
				node:  nodeList.Items[1],
			},
		}

		f.Logf("getting AWS region info from node spec")
		_, region, _, err := getAWSNodeInfo(nodeList.Items[0])
		Expect(err).NotTo(HaveOccurred(), "getAWSNodeInfo")

		f.Logf("initializing ec2 creds")
		ec2Client, err := getEC2Client(region)
		Expect(err).NotTo(HaveOccurred(), "getEC2Client")

		DeferCleanup(func() error { return cleanupSymlinkDir(namespace, nodeEnv) })
		DeferCleanup(func() error { return cleanupAWSDisks(ec2Client) })

		f.Logf("creating and attaching disks")
		createAndAttachAWSVolumes(ec2Client, namespace, nodeEnv)

		nodeEnv = populateDeviceInfo(namespace, nodeEnv)
	})

	Context("duplicate by-id cache reproducer", Ordered, func() {
		var tc *testContext

		BeforeAll(func() {
			tc = &testContext{
				f:         f,
				namespace: namespace,
				nodeEnv:   nodeEnv,
			}

			sharedSelectedDisk := nodeEnv[0].disks[diskIndexSharedByID]
			Expect(sharedSelectedDisk.path).ShouldNot(BeZero(), "shared selected device path should not be empty")
			sharedAliasDisk := nodeEnv[1].disks[diskIndexSharedByID]
			Expect(sharedAliasDisk.path).ShouldNot(BeZero(), "shared alias device path should not be empty")

			tc.addHostSymlink(nodeEnv[0].node.Labels[corev1.LabelHostname], currentSymlinkForDisk(sharedSelectedDisk), sharedScsi8Link)
			tc.addHostSymlink(nodeEnv[1].node.Labels[corev1.LabelHostname], currentSymlinkForDisk(sharedAliasDisk), sharedScsi8Link)

			selectedNode := nodeEnv[0].node
			tc.localVolume = getLocalVolume(selectedNode, sharedScsi8Link, namespace)
			tc.localVolume.Name = "test-local-disk-shared-byid"
			tc.localVolume.Spec.StorageClassDevices[0].StorageClassName = "test-local-sc-shared-byid"

			Eventually(func(ctx context.Context) error {
				f.Logf("creating shared localvolume reproducer")
				return f.Client.Create(ctx, tc.localVolume, nil)
			}, time.Minute, time.Second*2).WithContext(context.Background()).ShouldNot(HaveOccurred(), "creating shared localvolume reproducer")

			DeferCleanup(func() { tc.Cleanup() })
		})

		It("creates PVs on single node with shared scsi-8 alias", func() {
			err := waitForDaemonSet(f.KubeClient, namespace, nodedaemon.DiskMakerName, retryInterval, hourTimeout)
			Expect(err).NotTo(HaveOccurred(), "waiting for diskmaker daemonset for shared localvolume reproducer")

			err = verifyLocalVolume(tc.localVolume, f.Client)
			Expect(err).NotTo(HaveOccurred(), "verifying shared localvolume reproducer")

			err = checkLocalVolumeStatus(tc.localVolume)
			Expect(err).NotTo(HaveOccurred(), "checking shared localvolume reproducer condition")

			sharedPVs := eventuallyFindPVs(f, tc.localVolume.Spec.StorageClassDevices[0].StorageClassName, 1)
			Expect(filepath.Base(sharedPVs[0].Spec.Local.Path)).To(Equal(filepath.Base(sharedScsi8Link)))

			sharedPVNames := []string{sharedPVs[0].Name}
			sharedLVDLs := eventuallyFindLVDLsForPVs(f, namespace, sharedPVNames)
			assertLVDLsContainTargetAndNodes(sharedLVDLs, sharedScsi8Link, []string{nodeEnv[0].node.Name})
			assertLVDLSymlinkPathMatchesPVs(sharedLVDLs, sharedPVs)
		})

		It("creates PVs on second node after expanding node selector", func() {
			Eventually(func(ctx context.Context) error {
				f.Logf("expanding shared localvolume reproducer to a second node")
				key := types.NamespacedName{Name: tc.localVolume.Name, Namespace: tc.localVolume.Namespace}
				if err := f.Client.Get(ctx, key, tc.localVolume); err != nil {
					return err
				}
				matchFields := tc.localVolume.Spec.NodeSelector.NodeSelectorTerms[0].MatchFields
				if len(matchFields) == 0 {
					return fmt.Errorf("shared localvolume reproducer node selector is missing matchFields")
				}
				secondNodeTerm := tc.localVolume.Spec.NodeSelector.NodeSelectorTerms[0]
				secondNodeTerm.MatchFields = append([]corev1.NodeSelectorRequirement(nil), matchFields...)
				secondNodeTerm.MatchFields[0].Values = []string{nodeEnv[1].node.Name}
				tc.localVolume.Spec.NodeSelector.NodeSelectorTerms = append(
					tc.localVolume.Spec.NodeSelector.NodeSelectorTerms,
					secondNodeTerm,
				)
				return f.Client.Update(ctx, tc.localVolume)
			}, time.Minute, time.Second*2).WithContext(context.Background()).ShouldNot(HaveOccurred(), "updating shared localvolume reproducer")

			sharedPVs := eventuallyFindPVs(f, tc.localVolume.Spec.StorageClassDevices[0].StorageClassName, 2)
			sharedPVNames := make([]string, 0, len(sharedPVs))
			for _, pv := range sharedPVs {
				Expect(filepath.Base(pv.Spec.Local.Path)).To(Equal(filepath.Base(sharedScsi8Link)))
				sharedPVNames = append(sharedPVNames, pv.Name)
			}
			sharedLVDLs := eventuallyFindLVDLsForPVs(f, namespace, sharedPVNames)
			assertLVDLsContainTargetAndNodes(sharedLVDLs, sharedScsi8Link, []string{nodeEnv[0].node.Name, nodeEnv[1].node.Name})
			assertLVDLSymlinkPathMatchesPVs(sharedLVDLs, sharedPVs)
		})

		It("cleans up shared by-id reproducer", func() {
			f.Logf("cleaning up LocalVolume duplicate by-id reproducer before continuing with standard test flow")
			cleanupLVAndWaitForOwnedPVsToDisappear(f, tc.localVolume)
		})
	})

	Context("fsType and volumeMode", func() {
		var fsTypeTestCases = []fsTypeTestCase{
			{
				name:             "default-filesystem-with-tolerations",
				volumeMode:       localv1.PersistentVolumeFilesystem,
				storageClassName: lvStorageClassName,
				diskIndex:        diskIndexDefaultFsType,
				withTolerations:  true,
			},
			{
				name:             "ext4-filesystem",
				fsType:           "ext4",
				volumeMode:       localv1.PersistentVolumeFilesystem,
				storageClassName: "test-local-sc-ext4",
				diskIndex:        diskIndexSharedByID,
			},
			{
				name:             "xfs-filesystem",
				fsType:           "xfs",
				volumeMode:       localv1.PersistentVolumeFilesystem,
				storageClassName: "test-local-sc-xfs",
				diskIndex:        diskIndexXfsFsType,
			},
			{
				name:             "block-mode",
				volumeMode:       localv1.PersistentVolumeBlock,
				storageClassName: "test-local-sc-block",
				diskIndex:        diskIndexBlockMode,
			},
		}
		for _, tc := range fsTypeTestCases {
			tc := tc
			It(tc.name, func() {
				selectedNode := nodeEnv[0].node
				testDisk := nodeEnv[0].disks[tc.diskIndex]
				Expect(testDisk.path).ShouldNot(BeZero(), "device path should not be empty for %s", tc.name)

				var localVolume *localv1.LocalVolume
				if tc.withTolerations {
					localVolume = getLocalVolume(selectedNode, testDisk.path, namespace)
				} else {
					localVolume = getLocalVolumeWithFSType(selectedNode, testDisk.path, namespace, tc.storageClassName, tc.fsType, tc.volumeMode, "", nil)
				}

				DeferCleanup(func() { cleanupLVResources(f, localVolume) })

				Eventually(func(ctx context.Context) error {
					f.Logf("creating localvolume %s with fsType=%q, volumeMode=%s", tc.name, tc.fsType, tc.volumeMode)
					return f.Client.Create(ctx, localVolume, nil)
				}, time.Minute, time.Second*2).WithContext(context.Background()).ShouldNot(HaveOccurred(), "creating localvolume %s", tc.name)

				err := waitForDaemonSet(f.KubeClient, namespace, nodedaemon.DiskMakerName, retryInterval, hourTimeout)
				Expect(err).NotTo(HaveOccurred(), "waiting for diskmaker daemonset for %s", tc.name)

				err = verifyLocalVolume(localVolume, f.Client)
				Expect(err).NotTo(HaveOccurred(), "verifying localvolume cr for %s", tc.name)

				err = checkLocalVolumeStatus(localVolume)
				Expect(err).NotTo(HaveOccurred(), "checking localvolume condition for %s", tc.name)

				pvs := eventuallyFindPVs(f, tc.storageClassName, 1)
				Expect(pvs).NotTo(BeEmpty(), "no pvs returned by eventuallyFindPVs for %s", tc.name)

				// Verify the PV path matches the disk name or ID
				expectedPath := testDisk.name
				if testDisk.id != "" {
					expectedPath = testDisk.id
				}
				Expect(filepath.Base(pvs[0].Spec.Local.Path)).To(Equal(expectedPath))

				pv := pvs[0]
				f.Logf("Verifying PV %s for test case %s", pv.Name, tc.name)
				if tc.volumeMode == localv1.PersistentVolumeBlock {
					Expect(pv.Spec.VolumeMode).NotTo(BeNil())
					Expect(*pv.Spec.VolumeMode).To(Equal(corev1.PersistentVolumeBlock))
					f.Logf("verified PV %s has volumeMode=Block", pv.Name)
				} else if tc.fsType != "" {
					Expect(pv.Spec.Local.FSType).NotTo(BeNil())
					Expect(*pv.Spec.Local.FSType).To(Equal(tc.fsType))
					f.Logf("verified PV %s has fsType=%s", pv.Name, tc.fsType)
				}

				_, err = f.KubeClient.StorageV1().StorageClasses().Get(context.TODO(), tc.storageClassName, metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "getting storage class %s", tc.storageClassName)
				f.Logf("Verified storage class %s exists", tc.storageClassName)

				for i := range pvs {
					eventuallyDeletePV(&pvs[i])
				}
				pvs = eventuallyFindPVs(f, tc.storageClassName, 1)

				consumingObjectList := make([]client.Object, 0)
				for _, pv := range pvs {
					pvc, job, pod := consumePV(namespace, pv)
					consumingObjectList = append(consumingObjectList, job, pvc, pod)
				}
				eventuallyDelete(consumingObjectList...)
				eventuallyFindAvailablePVs(f, tc.storageClassName, pvs)
			})
		}
	})

	Context("localvolume with tolerations", func() {
		It("schedules and consumes PV on tainted node", func() {
			selectedNode := nodeEnv[1].node
			testDisk := nodeEnv[1].disks[diskIndexTolerations]
			Expect(testDisk.path).ShouldNot(BeZero(), "device path should not be empty for tolerations test")

			originalNodeTaints := append([]corev1.Taint(nil), selectedNode.Spec.Taints...)
			selectedNode.Spec.Taints = []corev1.Taint{localVolumeNodeTaint()}
			var err error
			selectedNode, err = waitForNodeTaintUpdate(f.KubeClient, selectedNode, retryInterval, hourTimeout)
			Expect(err).NotTo(HaveOccurred(), "tainting node for tolerations test")
			DeferCleanup(func() error {
				selectedNode.Spec.Taints = originalNodeTaints
				_, restoreErr := waitForNodeTaintUpdate(f.KubeClient, selectedNode, retryInterval, hourTimeout)
				if restoreErr != nil {
					f.Logf("error restoring original taints on node: %v", restoreErr)
				}
				return restoreErr
			})

			localVolume := getLocalVolumeWithFSType(
				selectedNode,
				testDisk.path,
				namespace,
				lvStorageClassName,
				"ext4",
				localv1.PersistentVolumeFilesystem,
				"test-local-disk-tolerations",
				localVolumeTolerations(),
			)
			DeferCleanup(func() { cleanupLVResources(f, localVolume) })

			Eventually(func(ctx context.Context) error {
				f.Logf("creating localvolume for tolerations test")
				return f.Client.Create(ctx, localVolume, nil)
			}, time.Minute, time.Second*2).WithContext(context.Background()).ShouldNot(HaveOccurred(), "creating localvolume for tolerations test")

			err = waitForDaemonSet(f.KubeClient, namespace, nodedaemon.DiskMakerName, retryInterval, hourTimeout)
			Expect(err).NotTo(HaveOccurred(), "waiting for diskmaker daemonset for tolerations test")

			err = verifyLocalVolume(localVolume, f.Client)
			Expect(err).NotTo(HaveOccurred(), "verifying localvolume cr for tolerations test")

			err = checkLocalVolumeStatus(localVolume)
			Expect(err).NotTo(HaveOccurred(), "checking localvolume condition for tolerations test")

			pvs := eventuallyFindPVs(f, lvStorageClassName, 1)
			expectedPath := testDisk.name
			if testDisk.id != "" {
				expectedPath = testDisk.id
			}
			Expect(filepath.Base(pvs[0].Spec.Local.Path)).To(Equal(expectedPath))

			f.Logf("consuming PV with tolerations")
			pvc, job, pod := consumePVWithTolerations(namespace, pvs[0], localVolumeTolerations())
			eventuallyDelete(job, pvc, pod)
		})
	})

	Context("standard LocalVolume lifecycle", Ordered, func() {
		var tc *testContext

		BeforeAll(func() {
			Expect(len(nodeEnv[0].disks)).To(BeNumerically(">", diskIndexLVDL), "expected at least %d disks on node 0", diskIndexLVDL+1)

			tc = &testContext{
				f:         f,
				namespace: namespace,
				nodeEnv:   nodeEnv,
			}

			selectedDisk = nodeEnv[0].disks[diskIndexLVDL]
			Expect(selectedDisk.path).ShouldNot(BeZero(), "device path should not be empty")

			selectedNode := nodeEnv[0].node
			tc.localVolume = getLocalVolume(selectedNode, selectedDisk.path, namespace)

			Eventually(func(ctx context.Context) error {
				f.Logf("creating localvolume")
				return f.Client.Create(ctx, tc.localVolume, nil)
			}, time.Minute, time.Second*2).WithContext(context.Background()).ShouldNot(HaveOccurred(), "creating localvolume")

			DeferCleanup(func() { tc.Cleanup() })
		})

		It("creates LocalVolume and provisions PVs", func() {
			err := waitForDaemonSet(f.KubeClient, namespace, nodedaemon.DiskMakerName, retryInterval, hourTimeout)
			Expect(err).NotTo(HaveOccurred(), "waiting for diskmaker daemonset")

			err = verifyLocalVolume(tc.localVolume, f.Client)
			Expect(err).NotTo(HaveOccurred(), "verifying localvolume cr")

			err = checkLocalVolumeStatus(tc.localVolume)
			Expect(err).NotTo(HaveOccurred(), "checking localvolume condition")

			tc.pvs = eventuallyFindPVs(f, tc.localVolume.Spec.StorageClassDevices[0].StorageClassName, 1)
			Expect(tc.pvs).NotTo(BeEmpty(), "no pvs returned by eventuallyFindPVs")

			var expectedPath string
			if selectedDisk.id != "" {
				expectedPath = selectedDisk.id
			} else {
				expectedPath = selectedDisk.name
			}
			Expect(filepath.Base(tc.pvs[0].Spec.Local.Path)).To(Equal(expectedPath))

			f.Logf("looking for %q annotation on pvs", provCommon.AnnProvisionedBy)
			verifyProvisionerAnnotation(tc.pvs, nodeList.Items)
		})

		It("verifies LVDL objects with correct link targets", func() {
			pvNames := make([]string, 0, len(tc.pvs))
			for _, pv := range tc.pvs {
				pvNames = append(pvNames, pv.Name)
			}
			f.Logf("verifying LocalVolumeDeviceLink objects were created for PVs")
			lvdls := eventuallyFindLVDLsForPVs(f, namespace, pvNames)
			tc.lvdlNames = make([]string, 0, len(lvdls))
			for _, lvdl := range lvdls {
				tc.lvdlNames = append(tc.lvdlNames, lvdl.Name)
				Expect(lvdl.Status.CurrentLinkTarget).ToNot(BeEmpty(), "expected CurrentLinkTarget for LVDL %q", lvdl.Name)
				Expect(lvdl.Status.PreferredLinkTarget).ToNot(BeEmpty(), "expected PreferredLinkTarget for LVDL %q", lvdl.Name)
				Expect(lvdl.Status.ValidLinkTargets).ToNot(BeEmpty(), "expected ValidLinkTargets for LVDL %q", lvdl.Name)
			}
			assertLVDLSymlinkPathMatchesPVs(lvdls, tc.pvs)

			tc.selectedPV = tc.pvs[0]
			tc.currentSymlink = currentSymlinkForDisk(selectedDisk)
			tc.pvs = []corev1.PersistentVolume{tc.selectedPV}
		})

		It("reconciles preferred link through multiple priority levels", func() {
			f.Logf("TEST: multi-step preferred link reconciliation (scsi-2 -> scsi-3 -> wwn)")
			tc.selectedPV = verifyMultiStepPreferredLinkReconciliation(tc, tc.selectedPV, tc.currentSymlink)
		})

		It("falls back when preferred link disappears", func() {
			f.Logf("TEST: symlink fallback on disappearing link for %s (wwn -> scsi-3)", tc.selectedPV.Name)
			tc.selectedPV = verifySymlinkFallbackOnDisappearingLink(
				tc, tc.selectedPV,
				"/dev/disk/by-id/wwn-local-storage-e2e-step3",
				"/dev/disk/by-id/scsi-3-local-storage-e2e-step2",
			)
			tc.pvs = []corev1.PersistentVolume{tc.selectedPV}
		})

		It("handles consuming and releasing PVs", func() {
			consumingObjectList := make([]client.Object, 0)
			filesystemConsumedPVNames := make([]string, 0)
			for _, pv := range tc.pvs {
				pvc, job, pod := consumePV(namespace, pv)
				consumingObjectList = append(consumingObjectList, job, pvc, pod)
				if pv.Spec.VolumeMode == nil || *pv.Spec.VolumeMode == corev1.PersistentVolumeFilesystem {
					filesystemConsumedPVNames = append(filesystemConsumedPVNames, pv.Name)
				}
			}
			if len(filesystemConsumedPVNames) > 0 {
				f.Logf("verifying filesystemUUID is populated on LVDL for consumed filesystem PVs")
				verifyLVDLFilesystemUUIDForPVs(f, namespace, filesystemConsumedPVNames)
			}
			eventuallyDelete(consumingObjectList...)

			eventuallyFindAvailablePVs(f, tc.localVolume.Spec.StorageClassDevices[0].StorageClassName, tc.pvs)
		})

		It("blocks deletion while PVs are bound", func() {
			consumingObjectList := make([]client.Object, 0)
			for _, pv := range tc.pvs[:1] {
				pvc, job, pod := consumePV(namespace, pv)
				consumingObjectList = append(consumingObjectList, job, pvc, pod)
			}

			Eventually(func(ctx context.Context) error {
				f.Logf("deleting LocalVolume %q", tc.localVolume.Name)
				return f.Client.Delete(ctx, tc.localVolume, client.PropagationPolicy(metav1.DeletePropagationBackground))
			}, time.Minute*5, time.Second*5).WithContext(context.Background()).ShouldNot(HaveOccurred(), "deleting LocalVolume %q", tc.localVolume.Name)

			Consistently(func() bool {
				f.Logf("verifying finalizer still exists")
				err := f.Client.Get(context.TODO(), types.NamespacedName{Name: tc.localVolume.Name, Namespace: f.OperatorNamespace}, tc.localVolume)
				if err != nil && (apierrors.IsGone(err) || apierrors.IsNotFound(err)) {
					Fail("LocalVolume deleted with bound PVs")
					return false
				} else if err != nil {
					f.Logf("error getting LocalVolume: %+v", err)
					return false
				}
				return len(tc.localVolume.ObjectMeta.Finalizers) > 0
			}, time.Second*15, time.Second*3).Should(BeTrue(), "checking finalizer exists with bound PVs")

			f.Logf("releasing pvs")
			eventuallyDelete(consumingObjectList...)

			Eventually(func(ctx context.Context) bool {
				f.Logf("verifying LocalVolume deletion")
				err := f.Client.Get(ctx, types.NamespacedName{Name: tc.localVolume.Name, Namespace: f.OperatorNamespace}, tc.localVolume)
				if err != nil && (apierrors.IsGone(err) || apierrors.IsNotFound(err)) {
					f.Logf("LocalVolume deleted: %+v", err)
					return true
				} else if err != nil {
					f.Logf("error getting LocalVolume: %+v", err)
					return false
				}
				f.Logf("LocalVolume found: %q with finalizers: %+v", tc.localVolume.Name, tc.localVolume.ObjectMeta.Finalizers)
				return false
			}).WithContext(context.Background()).Should(BeTrue(), "verifying LocalVolume has been deleted")
		})

		It("verifies cleanup after deletion", func() {
			f.Logf("verifying LocalVolumeDeviceLink objects are deleted after LocalVolume deletion")
			verifyLVDLsDeleted(f, namespace, tc.lvdlNames)

			symLinkPath := path.Join(common.GetLocalDiskLocationPath(), lvStorageClassName)
			checkForSymlinks(namespace, nodeEnv, symLinkPath)
		})
	})

	Context("symlink restoration after wipe", Ordered, func() {
		var tc *testContext

		BeforeAll(func() {
			tc = &testContext{
				f:         f,
				namespace: namespace,
				nodeEnv:   nodeEnv,
			}

			selectedNode := nodeEnv[0].node
			tc.localVolume = getLocalVolume(selectedNode, selectedDisk.path, namespace)
			tc.localVolume.Name = "test-local-disk-wipe"
			tc.localVolume.Spec.StorageClassDevices[0].StorageClassName = "test-local-sc-wipe"
			tc.localVolume.Spec.StorageClassDevices[0].VolumeMode = localv1.PersistentVolumeFilesystem

			Eventually(func(ctx context.Context) error {
				f.Logf("creating localvolume for wipe test")
				return f.Client.Create(ctx, tc.localVolume, nil)
			}, time.Minute, time.Second*2).WithContext(context.Background()).ShouldNot(HaveOccurred(), "creating localvolume for wipe test")

			DeferCleanup(func() { tc.Cleanup() })
		})

		It("restores symlink when device has no filesystem signature", func() {
			err := waitForDaemonSet(f.KubeClient, namespace, nodedaemon.DiskMakerName, retryInterval, hourTimeout)
			Expect(err).NotTo(HaveOccurred(), "waiting for diskmaker daemonset")

			tc.pvs = eventuallyFindPVs(f, tc.localVolume.Spec.StorageClassDevices[0].StorageClassName, 1)
			Expect(tc.pvs).To(HaveLen(1))

			lvdl := eventuallyGetLVDL(f, namespace, tc.pvs[0].Name)
			lvdl = updateLVDLPolicy(f, lvdl, localv1.DeviceLinkPolicyPreferredLinkTarget)
			waitForLVDLLinkTargets(f, lvdl, lvdl.Status.PreferredLinkTarget, lvdl.Status.PreferredLinkTarget)

			f.Logf("TEST: symlink restoration after wipe with no filesystem signature")
			tc.pvs[0] = verifySymlinkRestorationAfterWipeNoFSSignature(tc, tc.pvs[0])
		})

		It("restores symlink when device has a filesystem signature", func() {
			Expect(tc.pvs).To(HaveLen(1))
			f.Logf("TEST: symlink restoration after wipe with filesystem signature (rejected-device path)")
			tc.pvs[0] = verifySymlinkRestorationAfterWipeWithFSSignature(tc, tc.pvs[0])
		})
	})
})

// --- Helper functions ---

func verifyLVDLFilesystemUUIDForPVs(f *framework.Framework, namespace string, pvNames []string) {
	pvNameSet := map[string]struct{}{}
	for _, pvName := range pvNames {
		pvNameSet[pvName] = struct{}{}
	}
	Eventually(func(ctx context.Context) bool {
		lvdlList := &localv1.LocalVolumeDeviceLinkList{}
		err := f.Client.List(ctx, lvdlList, client.InNamespace(namespace))
		if err != nil {
			f.Logf("error listing LocalVolumeDeviceLink objects while verifying filesystemUUID: %v", err)
			return false
		}
		uuidFoundCount := 0
		for _, lvdl := range lvdlList.Items {
			if _, ok := pvNameSet[lvdl.Spec.PersistentVolumeName]; !ok {
				continue
			}
			if lvdl.Status.FilesystemUUID == "" {
				f.Logf("filesystemUUID not populated yet for LVDL %q / PV %q", lvdl.Name, lvdl.Spec.PersistentVolumeName)
				return false
			}
			if lvdl.Status.PersistentVolumeSymlinkPath == "" {
				f.Logf("SymlinkPath not populated yet for LVDL %q / PV %q", lvdl.Name, lvdl.Spec.PersistentVolumeName)
				return false
			}
			uuidFoundCount++
		}
		return uuidFoundCount == len(pvNameSet)
	}, time.Minute*5, time.Second*5).WithContext(context.Background()).Should(BeTrue(), "waiting for filesystemUUID on all consumed filesystem PV LVDLs")
}

func verifyLVDLsDeleted(f *framework.Framework, namespace string, lvdlNames []string) {
	targetNames := map[string]struct{}{}
	for _, name := range lvdlNames {
		targetNames[name] = struct{}{}
	}
	Eventually(func(ctx context.Context) bool {
		lvdlList := &localv1.LocalVolumeDeviceLinkList{}
		err := f.Client.List(ctx, lvdlList, client.InNamespace(namespace))
		if err != nil {
			f.Logf("error listing LocalVolumeDeviceLink objects during deletion check: %v", err)
			return false
		}
		for _, lvdl := range lvdlList.Items {
			if _, ok := targetNames[lvdl.Name]; ok {
				f.Logf("LocalVolumeDeviceLink %q still exists", lvdl.Name)
				return false
			}
		}
		return true
	}, time.Minute*5, time.Second*5).WithContext(context.Background()).Should(BeTrue(), "waiting for LVDL deletion after LocalVolume deletion")
}

func verifyProvisionerAnnotation(pvs []corev1.PersistentVolume, nodeList []corev1.Node) {
	for _, pv := range pvs {
		hostFound := true
		hostname, found := pv.Labels[corev1.LabelHostname]
		if !found {
			Fail(fmt.Sprintf("expected to find %q label on the pv %+v", corev1.LabelHostname, pv))
		}
		for _, node := range nodeList {
			nodeHostName, found := node.Labels[corev1.LabelHostname]
			if !found {
				Fail(fmt.Sprintf("expected to find %q label on the node %+v", corev1.LabelHostname, node))
			}
			if hostname == nodeHostName {
				expectedAnnotation := common.GetProvisionedByValue(node)
				actualAnnotation, found := pv.Annotations[provCommon.AnnProvisionedBy]
				Expect(found).To(BeTrue(), "expected to find annotation %q on pv", provCommon.AnnProvisionedBy)
				Expect(actualAnnotation).To(Equal(expectedAnnotation), "expected to find correct annotation value for %q", provCommon.AnnProvisionedBy)
				hostFound = true
				break
			}
		}
		if !hostFound {
			Fail(fmt.Sprintf("did not find a node entry matching this pv: %+v nodeList: %+v", pv, nodeList))
		}
	}
}

func waitForLocalVolumeAndOwnedPVsToDisappear(f *framework.Framework, localVolume *localv1.LocalVolume) {
	Eventually(func(ctx context.Context) error {
		key := types.NamespacedName{Name: localVolume.Name, Namespace: localVolume.Namespace}
		currentLocalVolume := &localv1.LocalVolume{}
		err := f.Client.Get(ctx, key, currentLocalVolume)
		if err != nil {
			if !apierrors.IsNotFound(err) && !apierrors.IsGone(err) {
				return err
			}
		} else {
			return fmt.Errorf("localvolume %q still exists with finalizers: %+v", currentLocalVolume.Name, currentLocalVolume.Finalizers)
		}

		pvList := &corev1.PersistentVolumeList{}
		err = f.Client.List(ctx, pvList, client.MatchingLabels{
			common.PVOwnerKindLabel:      localv1.LocalVolumeKind,
			common.PVOwnerNamespaceLabel: localVolume.Namespace,
			common.PVOwnerNameLabel:      localVolume.Name,
		})
		if err != nil {
			return err
		}
		if len(pvList.Items) > 0 {
			pvNames := make([]string, 0, len(pvList.Items))
			for _, pv := range pvList.Items {
				pvNames = append(pvNames, pv.Name)
			}
			return fmt.Errorf("waiting for owned PVs to disappear for localvolume %q: %+v", localVolume.Name, pvNames)
		}

		return nil
	}, time.Minute*8, time.Second*5).WithContext(context.Background()).ShouldNot(HaveOccurred(), "waiting for localvolume %q and owned PVs to be deleted", localVolume.Name)
}

func cleanupLVAndWaitForOwnedPVsToDisappear(f *framework.Framework, localVolume *localv1.LocalVolume) {
	eventuallyDelete(localVolume)
	waitForLocalVolumeAndOwnedPVsToDisappear(f, localVolume)
}

func verifyLocalVolume(lv *localv1.LocalVolume, cl framework.FrameworkClient) error {
	return wait.PollImmediate(retryInterval, hourTimeout, func() (bool, error) {
		objectKey := types.NamespacedName{Namespace: lv.Namespace, Name: lv.Name}
		err := cl.Get(context.TODO(), objectKey, lv)
		if err != nil {
			return false, err
		}
		if len(lv.GetFinalizers()) == 0 {
			return false, nil
		}
		framework.Global.Logf("Local volume verification successful")
		return true, nil
	})
}

func verifyDaemonSetTolerations(kubeclient kubernetes.Interface, daemonSetName, namespace string, tolerations []corev1.Toleration) error {
	dsTolerations := []corev1.Toleration{}
	err := wait.Poll(retryInterval, hourTimeout, func() (done bool, err error) {
		daemonset, err := kubeclient.AppsV1().DaemonSets(namespace).Get(context.TODO(), daemonSetName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		dsTolerations = daemonset.Spec.Template.Spec.Tolerations
		return true, err
	})
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(dsTolerations, tolerations) {
		return fmt.Errorf("toleration mismatch between daemonset and localvolume: %v, %v", dsTolerations, tolerations)
	}
	return nil
}

func verifyStorageClassDeletion(scName string, kubeclient kubernetes.Interface) error {
	return wait.Poll(retryInterval, hourTimeout, func() (done bool, err error) {
		_, err = kubeclient.StorageV1().StorageClasses().Get(context.TODO(), scName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	})
}

func checkLocalVolumeStatus(lv *localv1.LocalVolume) error {
	localVolumeConditions := lv.Status.Conditions
	if len(localVolumeConditions) == 0 {
		return fmt.Errorf("expected local volume to have conditions")
	}

	c := localVolumeConditions[0]
	if c.Type != operatorv1.OperatorStatusTypeAvailable || c.Status != operatorv1.ConditionTrue {
		return fmt.Errorf("expected available operator condition got %v", localVolumeConditions)
	}

	if c.LastTransitionTime.IsZero() {
		return fmt.Errorf("expect last transition time to be set")
	}
	framework.Global.Logf("LocalVolume status verification successful")
	return nil
}

func deleteCreatedPV(kubeClient kubernetes.Interface, lv *localv1.LocalVolume) error {
	err := kubeClient.CoreV1().PersistentVolumes().DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: common.GetPVOwnerSelector(lv).String()})
	if err == nil {
		framework.Global.Logf("PV deletion successful")
	}
	return err
}

func waitForCreatedPV(kubeClient kubernetes.Interface, lv *localv1.LocalVolume) error {
	return wait.PollImmediate(retryInterval, hourTimeout, func() (bool, error) {
		pvs, err := kubeClient.CoreV1().PersistentVolumes().List(context.TODO(), metav1.ListOptions{LabelSelector: common.GetPVOwnerSelector(lv).String()})
		if err != nil {
			if isRetryableAPIError(err) {
				return false, nil
			}
			return false, err
		}
		if len(pvs.Items) > 0 {
			return true, nil
		}
		return false, nil
	})
}

func selectNode(kubeClient kubernetes.Interface) corev1.Node {
	nodes, err := kubeClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/worker"})
	if err != nil {
		Fail(fmt.Sprintf("error finding worker node with %v", err))
	}
	if len(nodes.Items) != 0 {
		return nodes.Items[0]
	}
	nodeList, err := waitListSchedulableNodes(kubeClient)
	if err != nil {
		Fail(fmt.Sprintf("error listing schedulable nodes: %v", err))
	}
	if len(nodeList.Items) != 0 {
		return nodeList.Items[0]
	}
	Fail("found no schedulable node")
	return corev1.Node{}
}

func selectDisk(kubeClient kubernetes.Interface, node corev1.Node) (string, error) {
	var nodeInstanceType string
	for k, v := range node.ObjectMeta.Labels {
		if k == labelInstanceType {
			nodeInstanceType = v
		}
	}
	if ok, _ := regexp.MatchString(awsEBSNitroRegex, nodeInstanceType); ok {
		return getNitroDisk(kubeClient, node)
	}
	localDisk := os.Getenv("TEST_LOCAL_DISK")
	if localDisk != "" {
		return localDisk, nil
	}
	return "", fmt.Errorf("can not find a suitable disk")
}

func getNitroDisk(kubeClient kubernetes.Interface, node corev1.Node) (string, error) {
	return "", fmt.Errorf("unimplemented")
}

func isRetryableAPIError(err error) bool {
	if apierrors.IsInternalError(err) || apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) ||
		apierrors.IsTooManyRequests(err) || utilnet.IsProbableEOF(err) || utilnet.IsConnectionReset(err) {
		return true
	}
	if _, shouldRetry := apierrors.SuggestsClientDelay(err); shouldRetry {
		return true
	}
	return false
}

func waitListSchedulableNodes(c kubernetes.Interface) (*corev1.NodeList, error) {
	var nodes *corev1.NodeList
	var err error
	if wait.PollImmediate(retryInterval, hourTimeout, func() (bool, error) {
		nodes, err = c.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{FieldSelector: fields.Set{
			"spec.unschedulable": "false",
		}.AsSelector().String()})
		if err != nil {
			if isRetryableAPIError(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}) != nil {
		return nodes, err
	}
	return nodes, nil
}

func waitForDaemonSet(kubeclient kubernetes.Interface, namespace, name string, retryInterval, timeout time.Duration) error {
	nodeCount := 1
	err := wait.Poll(retryInterval, timeout, func() (done bool, err error) {
		daemonset, err := kubeclient.AppsV1().DaemonSets(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				framework.Global.Logf("Waiting for availability of %s daemonset", name)
				return false, nil
			}
			return false, err
		}
		if int(daemonset.Status.NumberReady) == nodeCount {
			return true, nil
		}
		framework.Global.Logf("Waiting for full availability of %s daemonset (%d/%d)", name, int(daemonset.Status.NumberReady), nodeCount)
		return false, nil
	})
	if err != nil {
		return err
	}
	framework.Global.Logf("Daemonset available (%d/%d)", nodeCount, nodeCount)
	return nil
}

func waitForNodeTaintUpdate(kubeclient kubernetes.Interface, node corev1.Node, retryInterval, timeout time.Duration) (corev1.Node, error) {
	var newNode *corev1.Node
	name := node.Name
	err := wait.PollUntilContextTimeout(context.TODO(), retryInterval, timeout, true, func(ctx context.Context) (done bool, err error) {
		newNode, err = kubeclient.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			framework.Global.Logf("error getting node %v: %v", name, err)
			return false, nil
		}
		newNode.Spec.Taints = node.Spec.Taints
		newNode, err = kubeclient.CoreV1().Nodes().Update(ctx, newNode, metav1.UpdateOptions{})
		if err != nil {
			framework.Global.Logf("Failed to update node %v successfully: %v", name, err)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return *newNode, err
	}
	framework.Global.Logf("Node %v updated successfully", name)
	return *newNode, nil
}

func getLocalVolume(selectedNode corev1.Node, selectedDisk, namespace string) *localv1.LocalVolume {
	return getLocalVolumeWithFSType(
		selectedNode,
		selectedDisk,
		namespace,
		lvStorageClassName,
		"",
		localv1.PersistentVolumeFilesystem,
		"test-local-disk",
		localVolumeTolerations(),
	)
}

func localVolumeTolerations() []corev1.Toleration {
	return []corev1.Toleration{
		{
			Key:      "localstorage",
			Value:    "testvalue",
			Operator: corev1.TolerationOpEqual,
		},
	}
}

func localVolumeNodeTaint() corev1.Taint {
	return corev1.Taint{
		Key:    "localstorage",
		Value:  "testvalue",
		Effect: corev1.TaintEffectNoSchedule,
	}
}

// getLocalVolumeWithFSType creates a LocalVolume CR with specific FSType and VolumeMode
// Parameters:
//   - selectedNode: the node to deploy on
//   - selectedDisk: the disk device path
//   - namespace: the namespace for the LocalVolume
//   - storageClassName: the storage class name
//   - fsType: filesystem type (ext4, xfs, or empty string for block mode)
//   - volumeMode: PersistentVolumeFilesystem or PersistentVolumeBlock
//   - name: the name of the LocalVolume CR (if empty, generates from storageClassName)
//   - tolerations: tolerations to apply (can be nil)
func getLocalVolumeWithFSType(selectedNode corev1.Node, selectedDisk, namespace, storageClassName, fsType string, volumeMode localv1.PersistentVolumeMode, name string, tolerations []corev1.Toleration) *localv1.LocalVolume {
	if name == "" {
		name = fmt.Sprintf("test-local-disk-%s", storageClassName)
	}

	scd := localv1.StorageClassDevice{
		StorageClassName: storageClassName,
		DevicePaths:      []string{selectedDisk},
		VolumeMode:       volumeMode,
	}
	if volumeMode != localv1.PersistentVolumeBlock && fsType != "" {
		scd.FSType = fsType
	}

	return &localv1.LocalVolume{
		TypeMeta: metav1.TypeMeta{
			Kind:       "LocalVolume",
			APIVersion: localv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: localv1.LocalVolumeSpec{
			NodeSelector: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{
					{
						MatchFields: []corev1.NodeSelectorRequirement{
							{Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{selectedNode.Name}},
						},
					},
				},
			},
			Tolerations:         tolerations,
			StorageClassDevices: []localv1.StorageClassDevice{scd},
		},
	}
}
