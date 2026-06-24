package e2e

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	localv1 "github.com/openshift/local-storage-operator/api/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
	framework "github.com/openshift/local-storage-operator/test/framework"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
)

const (
	labelNodeRoleWorker = "node-role.kubernetes.io/worker"
)

var _ = Describe("LocalVolumeSet", Label("LocalVolumeSet"), Ordered, func() {
	var (
		f         *framework.Framework
		namespace string
		nodeEnv   []nodeDisks
		nodeList  *corev1.NodeList
		lvSets    []*localv1alpha1.LocalVolumeSet
	)

	BeforeAll(func() {
		f = framework.Global
		namespace = f.OperatorNamespace

		nodeList = &corev1.NodeList{}
		err := f.Client.List(context.TODO(), nodeList, client.HasLabels{labelNodeRoleWorker})
		Expect(err).NotTo(HaveOccurred(), "listing worker nodes")
		Expect(len(nodeList.Items)).To(BeNumerically(">=", 3), "expected at least 3 worker nodes")

		nodeEnv = []nodeDisks{
			{
				disks: []disk{{size: 10}, {size: 20}, {size: 30}, {size: 40}, {size: 70}},
				node:  nodeList.Items[0],
			},
			{
				disks: []disk{{size: 10}, {size: 20}, {size: 30}, {size: 40}, {size: 70}},
				node:  nodeList.Items[1],
			},
			{
				disks: []disk{{size: 10}, {size: 20}, {size: 30}, {size: 40}, {size: 70}},
				node:  nodeList.Items[2],
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

		lvSets = []*localv1alpha1.LocalVolumeSet{}
		DeferCleanup(func() error {
			return cleanupLVSetResources(&lvSets)
		})

		hundredTi := resource.MustParse("100Ti")
		noOpLVSet := &localv1alpha1.LocalVolumeSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "noop",
				Namespace: namespace,
			},
			Spec: localv1alpha1.LocalVolumeSetSpec{
				StorageClassName: "noop",
				DeviceInclusionSpec: &localv1alpha1.DeviceInclusionSpec{
					DeviceTypes: []localv1alpha1.DeviceType{localv1alpha1.RawDisk},
					MinSize:     &hundredTi,
				},
			},
		}
		lvSets = append(lvSets, noOpLVSet)
		f.Logf("creating noop localvolumeset %q", noOpLVSet.GetName())
		err = f.Client.Create(context.TODO(), noOpLVSet, nil)
		Expect(err).NotTo(HaveOccurred(), "create noop localvolumeset")
		DeferCleanup(func() {
			eventuallyDelete(noOpLVSet)
		})

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

			tc.addHostSymlink(nodeEnv[0].node.Labels[corev1.LabelHostname], currentSymlinkForDisk(nodeEnv[0].disks[0]), sharedScsi8Link)
			tc.addHostSymlink(nodeEnv[1].node.Labels[corev1.LabelHostname], currentSymlinkForDisk(nodeEnv[1].disks[0]), sharedScsi8Link)

			twentyGi := resource.MustParse("20G")
			sharedLVSet := &localv1alpha1.LocalVolumeSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "shared-byid-10g",
					Namespace: namespace,
				},
				Spec: localv1alpha1.LocalVolumeSetSpec{
					StorageClassName: "shared-byid-10g",
					VolumeMode:       localv1.PersistentVolumeBlock,
					NodeSelector: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      corev1.LabelHostname,
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{nodeEnv[0].node.ObjectMeta.Labels[corev1.LabelHostname]},
								},
							},
						},
					}},
					DeviceInclusionSpec: &localv1alpha1.DeviceInclusionSpec{
						DeviceTypes: []localv1alpha1.DeviceType{localv1alpha1.RawDisk},
						MaxSize:     &twentyGi,
					},
				},
			}
			tc.lvSets = append(tc.lvSets, sharedLVSet)

			f.Logf("creating localvolumeset %q", sharedLVSet.GetName())
			err := f.Client.Create(context.TODO(), sharedLVSet, nil)
			Expect(err).NotTo(HaveOccurred(), "create shared localvolumeset reproducer")

			DeferCleanup(func() { tc.Cleanup() })
		})

		It("creates PVs on single node with shared scsi-8 alias", func() {
			sharedPVs := eventuallyFindPVs(f, tc.lvSets[0].Spec.StorageClassName, 1)
			Expect(filepath.Base(sharedPVs[0].Spec.Local.Path)).To(Equal(filepath.Base(sharedScsi8Link)))

			sharedPVNames := []string{sharedPVs[0].Name}
			sharedLVDLs := eventuallyFindLVDLsForPVs(f, namespace, sharedPVNames)
			assertLVDLsContainTargetAndNodes(sharedLVDLs, sharedScsi8Link, []string{nodeEnv[0].node.Name})
			assertLVDLSymlinkPathMatchesPVs(sharedLVDLs, sharedPVs)
		})

		It("creates PVs on second node after expanding node selector", func() {
			sharedLVSet := tc.lvSets[0]
			Eventually(func() error {
				f.Logf("expanding shared localvolumeset reproducer to a second node")
				key := types.NamespacedName{Name: sharedLVSet.GetName(), Namespace: sharedLVSet.GetNamespace()}
				if err := f.Client.Get(context.TODO(), key, sharedLVSet); err != nil {
					return err
				}
				sharedLVSet.Spec.NodeSelector.NodeSelectorTerms[0].MatchExpressions[0].Values = append(
					sharedLVSet.Spec.NodeSelector.NodeSelectorTerms[0].MatchExpressions[0].Values,
					nodeEnv[1].node.ObjectMeta.Labels[corev1.LabelHostname],
				)
				return f.Client.Update(context.TODO(), sharedLVSet)
			}, time.Minute, time.Second*2).ShouldNot(HaveOccurred(), "updating shared localvolumeset reproducer")

			sharedPVs := eventuallyFindPVs(f, sharedLVSet.Spec.StorageClassName, 2)
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
			f.Logf("cleaning up LocalVolumeSet duplicate by-id reproducer before continuing with standard test flow")
			sharedLVSet := tc.lvSets[0]
			eventuallyDelete(sharedLVSet)
			waitForLVSetAndOwnedPVsToDisappear(sharedLVSet)
		})
	})

	Context("block volume sets with device size filtering", Ordered, func() {
		var tc *testContext

		BeforeAll(func() {
			tc = &testContext{
				f:         f,
				namespace: namespace,
				nodeEnv:   nodeEnv,
			}
			DeferCleanup(func() { tc.Cleanup() })
		})

		It("creates block PVs with size range 20-50G", func() {
			twentyGi := resource.MustParse("20G")
			fiftyGi := resource.MustParse("50G")
			two := int32(2)

			tc.lvset = &localv1alpha1.LocalVolumeSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "twentytofifty-1",
					Namespace: namespace,
				},
				Spec: localv1alpha1.LocalVolumeSetSpec{
					StorageClassName: "twentytofifty-1",
					MaxDeviceCount:   &two,
					VolumeMode:       localv1.PersistentVolumeBlock,
					NodeSelector: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      corev1.LabelHostname,
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{nodeEnv[0].node.ObjectMeta.Labels[corev1.LabelHostname]},
								},
							},
						},
					}},
					DeviceInclusionSpec: &localv1alpha1.DeviceInclusionSpec{
						DeviceTypes: []localv1alpha1.DeviceType{localv1alpha1.RawDisk},
						MinSize:     &twentyGi,
						MaxSize:     &fiftyGi,
					},
				},
			}
			tc.lvSets = append(tc.lvSets, tc.lvset)

			f.Logf("creating localvolumeset %q", tc.lvset.GetName())
			err := f.Client.Create(context.TODO(), tc.lvset, nil)
			Expect(err).NotTo(HaveOccurred(), "create localvolumeset")

			eventuallyFindPVs(f, tc.lvset.Spec.StorageClassName, 2)
		})

		It("increases device count and verifies PV recreation after deletion", func() {
			three := int32(3)
			Eventually(func() error {
				f.Logf("updating lvset")
				key := types.NamespacedName{Name: tc.lvset.GetName(), Namespace: tc.lvset.GetNamespace()}
				err := f.Client.Get(context.TODO(), key, tc.lvset)
				if err != nil {
					f.Logf("error getting lvset %q: %+v", key, err)
					return err
				}
				tc.lvset.Spec.MaxDeviceCount = &three
				err = f.Client.Update(context.TODO(), tc.lvset)
				if err != nil {
					f.Logf("error updating lvset %q: %+v", key, err)
					return err
				}
				return nil
			}, time.Minute, time.Second*2).ShouldNot(HaveOccurred(), "updating lvset")

			tc.pvs = eventuallyFindPVs(f, tc.lvset.Spec.StorageClassName, 3)
			for _, pv := range tc.pvs {
				eventuallyDeletePV(&pv)
			}
			eventuallyFindPVs(f, tc.lvset.Spec.StorageClassName, 3)
		})

		It("creates overlapping lvset and expands to second node", func() {
			tenGi := resource.MustParse("10G")
			thirtyGi := resource.MustParse("30G")

			overlappingLVSet := &localv1alpha1.LocalVolumeSet{}
			tc.lvset.DeepCopyInto(overlappingLVSet)
			overlappingLVSet.ObjectMeta = metav1.ObjectMeta{
				Name:      fmt.Sprintf("tentothirty-overlapping-%s-2", tc.lvset.GetName()),
				Namespace: tc.lvset.GetNamespace(),
			}
			overlappingLVSet.Spec.StorageClassName = overlappingLVSet.GetName()
			overlappingLVSet.Spec.DeviceInclusionSpec.MinSize = &tenGi
			overlappingLVSet.Spec.DeviceInclusionSpec.MaxSize = &thirtyGi
			tc.lvSets = append(tc.lvSets, overlappingLVSet)

			f.Logf("creating localvolumeset %q", overlappingLVSet.GetName())
			err := f.Client.Create(context.TODO(), overlappingLVSet, nil)
			Expect(err).NotTo(HaveOccurred(), "create localvolumeset")

			eventuallyFindPVs(f, overlappingLVSet.Spec.StorageClassName, 1)

			Eventually(func() error {
				f.Logf("updating lvset")
				key := types.NamespacedName{Name: overlappingLVSet.GetName(), Namespace: overlappingLVSet.GetNamespace()}
				err := f.Client.Get(context.TODO(), key, overlappingLVSet)
				if err != nil {
					f.Logf("error getting lvset %q: %+v", key, err)
					return err
				}
				overlappingLVSet.Spec.NodeSelector.NodeSelectorTerms[0].MatchExpressions[0].Values = append(
					overlappingLVSet.Spec.NodeSelector.NodeSelectorTerms[0].MatchExpressions[0].Values,
					nodeEnv[1].node.ObjectMeta.Labels[corev1.LabelHostname],
				)
				err = f.Client.Update(context.TODO(), overlappingLVSet)
				if err != nil {
					f.Logf("error updating lvset %q: %+v", key, err)
					return err
				}
				return nil
			}, time.Minute, time.Second*2).ShouldNot(HaveOccurred(), "updating lvset")

			eventuallyFindPVs(f, overlappingLVSet.Spec.StorageClassName, 3)
		})

		It("verifies consumed block PVs are recreated upon release", func() {
			f.Logf("TEST: consumed block PVs are deleted and recreated upon release")
			consumingObjectList := make([]client.Object, 0)
			for _, pv := range tc.pvs[:1] {
				pvc, job, pod := consumePV(namespace, pv)
				consumingObjectList = append(consumingObjectList, job, pvc, pod)
			}
			eventuallyDelete(consumingObjectList...)
			tc.pvs = eventuallyFindAvailablePVs(f, tc.lvset.Spec.StorageClassName, tc.pvs)
		})

		It("blocks deletion while PVs are bound", func() {
			f.Logf("TEST: localvolumeset deletion does not occur with bound PVs")
			consumingObjectList := make([]client.Object, 0)
			for _, pv := range tc.pvs[:1] {
				pvc, job, pod := consumePV(namespace, pv)
				consumingObjectList = append(consumingObjectList, job, pvc, pod)
			}

			Eventually(func() error {
				f.Logf("deleting LocalVolumeSet %q", tc.lvset.Name)
				return f.Client.Delete(context.TODO(), tc.lvset)
			}, time.Minute*5, time.Second*5).ShouldNot(HaveOccurred(), "deleting LocalVolumeSet %q", tc.lvset.Name)

			var finalizers []string
			Consistently(func() bool {
				f.Logf("verifying finalizer still exists because of bound PVs")
				err := f.Client.Get(context.TODO(), types.NamespacedName{Name: tc.lvset.Name, Namespace: f.OperatorNamespace}, tc.lvset)
				if err != nil && (apierrors.IsGone(err) || apierrors.IsNotFound(err)) {
					Fail(fmt.Sprintf("LocalVolumeSet deleted with bound PVs: %+v", err))
					return false
				} else if err != nil {
					f.Logf("error getting LocalVolumeSet: %+v", err)
					return false
				}
				finalizers = tc.lvset.GetFinalizers()
				return len(finalizers) > 0
			}, time.Second*15, time.Second*3).Should(BeTrue(), "checking finalizer exists with bound PVs")

			f.Logf("finalizers not removed with bound PVs: %q", finalizers)
			f.Logf("releasing pvs")
			eventuallyDelete(consumingObjectList...)

			lvdlNames := make([]string, 0, len(tc.pvs))
			for _, pv := range tc.pvs {
				lvdlNames = append(lvdlNames, pv.Name)
			}

			Eventually(func() bool {
				f.Logf("verifying LocalVolumeSet deletion")
				err := f.Client.Get(context.TODO(), types.NamespacedName{Name: tc.lvset.Name, Namespace: f.OperatorNamespace}, tc.lvset)
				if err != nil && (apierrors.IsGone(err) || apierrors.IsNotFound(err)) {
					f.Logf("LocalVolumeSet deleted: %+v", err)
					return true
				} else if err != nil {
					f.Logf("error getting LocalVolumeSet: %+v", err)
					return false
				}
				f.Logf("LocalVolumeSet found: %s with finalizers: %+v", tc.lvset.Name, tc.lvset.ObjectMeta.Finalizers)
				return false
			}, time.Minute*5, time.Second*5).Should(BeTrue(), "verifying LocalVolumeSet has been deleted")

			f.Logf("verifying LocalVolumeDeviceLink objects are deleted for removed LocalVolumeSet")
			verifyLVDLsDeleted(f, namespace, lvdlNames)

			symLinkPath := path.Join(common.GetLocalDiskLocationPath(), tc.lvset.Spec.StorageClassName)
			checkForSymlinks(namespace, nodeEnv, symLinkPath)
		})
	})

	Context("filesystem volume sets", Ordered, func() {
		var tc *testContext

		BeforeAll(func() {
			tc = &testContext{
				f:         f,
				namespace: namespace,
				nodeEnv:   nodeEnv,
			}
			DeferCleanup(func() { tc.Cleanup() })
		})

		It("creates filesystem PVs and expands to all nodes", func() {
			twentyGi := resource.MustParse("20G")
			fiftyGi := resource.MustParse("50G")
			two := int32(2)

			tc.lvset = &localv1alpha1.LocalVolumeSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "twentytofifty-fs-3",
					Namespace: namespace,
				},
				Spec: localv1alpha1.LocalVolumeSetSpec{
					StorageClassName: "twentytofifty-fs-3",
					VolumeMode:       localv1.PersistentVolumeFilesystem,
					MaxDeviceCount:   &two,
					NodeSelector: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      corev1.LabelHostname,
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{nodeEnv[2].node.ObjectMeta.Labels[corev1.LabelHostname]},
								},
							},
						},
					}},
					DeviceInclusionSpec: &localv1alpha1.DeviceInclusionSpec{
						DeviceTypes: []localv1alpha1.DeviceType{localv1alpha1.RawDisk},
						MinSize:     &twentyGi,
						MaxSize:     &fiftyGi,
					},
				},
			}
			tc.lvSets = append(tc.lvSets, tc.lvset)

			f.Logf("creating localvolumeset %q", tc.lvset.GetName())
			err := f.Client.Create(context.TODO(), tc.lvset, nil)
			Expect(err).NotTo(HaveOccurred(), "create localvolumeset")

			eventuallyFindPVs(f, tc.lvset.Spec.StorageClassName, 2)

			Eventually(func() error {
				f.Logf("updating lvset")
				key := types.NamespacedName{Name: tc.lvset.GetName(), Namespace: tc.lvset.GetNamespace()}
				err := f.Client.Get(context.TODO(), key, tc.lvset)
				if err != nil {
					f.Logf("error getting lvset %q: %+v", key, err)
					return err
				}
				tc.lvset.Spec.NodeSelector = nil
				tc.lvset.Spec.MaxDeviceCount = nil
				tc.lvset.Spec.DeviceInclusionSpec = nil
				err = f.Client.Update(context.TODO(), tc.lvset)
				if err != nil {
					f.Logf("error updating lvset %q: %+v", key, err)
					return err
				}
				return nil
			}, time.Minute, time.Second*2).ShouldNot(HaveOccurred(), "updating lvset")

			// this should give us all 15 PVs now
			tc.pvs = eventuallyFindPVs(f, tc.lvset.Spec.StorageClassName, 15)

			f.Logf("looking for %q annotation on pvs", provCommon.AnnProvisionedBy)
			verifyProvisionerAnnotation(tc.pvs, nodeList.Items)

			fsPVNames := make([]string, 0, len(tc.pvs))
			for _, pv := range tc.pvs {
				fsPVNames = append(fsPVNames, pv.Name)
			}
			f.Logf("verifying LocalVolumeDeviceLink objects were created for filesystem PVs")
			fsLVDLs := eventuallyFindLVDLsForPVs(f, namespace, fsPVNames)
			for _, lvdl := range fsLVDLs {
				Expect(lvdl.Status.CurrentLinkTarget).ToNot(BeEmpty(), "expected CurrentLinkTarget for LVDL %q", lvdl.Name)
				Expect(lvdl.Status.PreferredLinkTarget).ToNot(BeEmpty(), "expected PreferredLinkTarget for LVDL %q", lvdl.Name)
				Expect(lvdl.Status.ValidLinkTargets).ToNot(BeEmpty(), "expected ValidLinkTargets for LVDL %q", lvdl.Name)
			}
			assertLVDLSymlinkPathMatchesPVs(fsLVDLs, tc.pvs)
		})

		It("reconciles preferred link through multiple priority levels", func() {
			tc.selectedPV = tc.pvs[0]
			tc.currentSymlink = findCurrentSymlinkForPV(nodeEnv, &tc.selectedPV)

			f.Logf("TEST: multi-step preferred link reconciliation for lvset (scsi-2 -> scsi-3 -> wwn) for %s", tc.selectedPV.Name)
			tc.selectedPV = verifyMultiStepPreferredLinkReconciliation(tc, tc.selectedPV, tc.currentSymlink)
			tc.pvs[0] = tc.selectedPV
		})

		It("falls back when preferred link disappears", func() {
			tc.selectedPV = tc.pvs[0]
			f.Logf("TEST: symlink fallback on disappearing link for %s (wwn -> scsi-3)", tc.selectedPV.Name)
			tc.selectedPV = verifySymlinkFallbackOnDisappearingLink(
				tc, tc.selectedPV,
				"/dev/disk/by-id/wwn-local-storage-e2e-step3",
				"/dev/disk/by-id/scsi-3-local-storage-e2e-step2",
			)
			tc.pvs[0] = tc.selectedPV
		})

		It("verifies PV recreation after deletion", func() {
			f.Logf("verify that filesystem PVs come back after deletion")
			for _, pv := range tc.pvs {
				eventuallyDeletePV(&pv)
			}
			tc.pvs = eventuallyFindPVs(f, tc.lvset.Spec.StorageClassName, 15)
		})

		It("verifies consumed filesystem PVs are recreated upon release", func() {
			f.Logf("TEST: that consumed filesystem PVs are deleted and recreated upon release")
			consumingObjectList := make([]client.Object, 0)
			for _, pv := range tc.pvs[:1] {
				pvc, job, pod := consumePV(namespace, pv)
				consumingObjectList = append(consumingObjectList, job, pvc, pod)
			}
			consumedFSPVNames := make([]string, 0, len(tc.pvs[:1]))
			for _, pv := range tc.pvs[:1] {
				consumedFSPVNames = append(consumedFSPVNames, pv.Name)
			}
			f.Logf("verifying filesystemUUID is populated on LVDL for consumed filesystem PVs")
			verifyLVDLFilesystemUUIDForPVs(f, namespace, consumedFSPVNames)

			eventuallyDelete(consumingObjectList...)
			eventuallyFindAvailablePVs(f, tc.lvset.Spec.StorageClassName, tc.pvs)
		})
	})
})

func waitForLVSetAndOwnedPVsToDisappear(lvset *localv1alpha1.LocalVolumeSet) {
	f := framework.Global

	Eventually(func() error {
		key := types.NamespacedName{Name: lvset.Name, Namespace: lvset.Namespace}
		currentLVSet := &localv1alpha1.LocalVolumeSet{}
		err := f.Client.Get(context.TODO(), key, currentLVSet)
		if err != nil {
			if !apierrors.IsNotFound(err) && !apierrors.IsGone(err) {
				return err
			}
		} else {
			return fmt.Errorf("localvolumeset %q still exists with finalizers: %+v", currentLVSet.Name, currentLVSet.Finalizers)
		}

		pvList := &corev1.PersistentVolumeList{}
		err = f.Client.List(context.TODO(), pvList, client.MatchingLabels{
			common.PVOwnerKindLabel:      localv1.LocalVolumeSetKind,
			common.PVOwnerNamespaceLabel: lvset.Namespace,
			common.PVOwnerNameLabel:      lvset.Name,
		})
		if err != nil {
			return err
		}
		if len(pvList.Items) > 0 {
			pvNames := make([]string, 0, len(pvList.Items))
			for _, pv := range pvList.Items {
				pvNames = append(pvNames, pv.Name)
			}
			return fmt.Errorf("waiting for owned PVs to disappear for localvolumeset %q: %+v", lvset.Name, pvNames)
		}

		return nil
	}, time.Minute*8, time.Second*5).ShouldNot(HaveOccurred(), "waiting for localvolumeset %q and owned PVs to be deleted", lvset.Name)
}

func cleanupLVSetResources(lvsets *[]*localv1alpha1.LocalVolumeSet) error {
	f := framework.Global
	for _, lvset := range *lvsets {
		cleanupSingleLVSet(f, lvset)
	}

	return nil
}
