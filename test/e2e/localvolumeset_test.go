package e2e

import (
	"context"
	goctx "context"
	"fmt"
	"testing"
	"time"

	"github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	localv1 "github.com/openshift/local-storage-operator/api/v1"
	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	framework "github.com/openshift/local-storage-operator/test-framework"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
)

const (
	labelNodeRoleWorker = "node-role.kubernetes.io/worker"
)

func LocalVolumeSetTest(ctx *framework.TestCtx, cleanupFuncs *[]cleanupFn) func(*testing.T) {
	return func(t *testing.T) {

		f := framework.Global
		namespace, err := ctx.GetNamespace()
		if err != nil {
			t.Fatalf("error fetching namespace : %v", err)
		}

		matcher := gomega.NewGomegaWithT(t)
		gomega.SetDefaultEventuallyTimeout(time.Minute * 10)
		gomega.SetDefaultEventuallyPollingInterval(time.Second * 2)

		// get nodes
		nodeList := &corev1.NodeList{}
		err = f.Client.List(context.TODO(), nodeList, client.HasLabels{labelNodeRoleWorker})
		if err != nil {
			t.Fatalf("failed to list nodes: %+v", err)
		}

		minNodes := 3
		if len(nodeList.Items) < minNodes {
			t.Fatalf("expected to have at least %d nodes", minNodes)
		}

		// represents the disk layout to setup on the nodes.
		nodeEnv := []nodeDisks{
			{
				disks: []disk{
					{size: 10},
					{size: 20},
					{size: 30},
					{size: 40},
					{size: 70},
				},
				node: nodeList.Items[0],
			},
			{
				disks: []disk{
					{size: 10},
					{size: 20},
					{size: 30},
					{size: 40},
					{size: 70},
				},
				node: nodeList.Items[1],
			},
			{
				disks: []disk{
					{size: 10},
					{size: 20},
					{size: 30},
					{size: 40},
					{size: 70},
				},
				node: nodeList.Items[2],
			},
		}

		t.Log("getting AWS region info from node spec")
		_, region, _, err := getAWSNodeInfo(nodeList.Items[0])
		matcher.Expect(err).NotTo(gomega.HaveOccurred(), "getAWSNodeInfo")

		// initialize client
		t.Log("initialize ec2 creds")
		ec2Client, err := getEC2Client(region)
		matcher.Expect(err).NotTo(gomega.HaveOccurred(), "getEC2Client")

		// cleanup host dirs
		addToCleanupFuncs(cleanupFuncs, "cleanupSymlinkDir", func(t *testing.T) error {
			return cleanupSymlinkDir(t, ctx, nodeEnv)
		})
		// register disk cleanup
		addToCleanupFuncs(cleanupFuncs, "cleanupAWSDisks", func(t *testing.T) error {
			return cleanupAWSDisks(t, ec2Client)
		})

		// create and attach volumes
		t.Log("creating and attaching disks")
		//	for _, nodeDisks := range nodeEnv {
		err = createAndAttachAWSVolumes(t, ec2Client, ctx, namespace, nodeEnv)
		matcher.Expect(err).NotTo(gomega.HaveOccurred(), "createAndAttachAWSVolumes: %+v", nodeEnv)
		//	}
		tenGi := resource.MustParse("10G")
		twentyGi := resource.MustParse("20G")
		thirtyGi := resource.MustParse("30G")
		fiftyGi := resource.MustParse("50G")
		two := int32(2)
		three := int32(3)

		lvSets := []*localv1alpha1.LocalVolumeSet{}

		// start the lvset with a size range of twenty to fifty on the first node
		// should match amd claim 2 of node0: 20, 30, 40
		// total: 2
		// disks  left within range:
		// node0: 2
		// node1: 3
		// node2: 3
		twentyToFifty := &localv1alpha1.LocalVolumeSet{
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
				},
				},
				DeviceInclusionSpec: &localv1alpha1.DeviceInclusionSpec{
					DeviceTypes: []localv1alpha1.DeviceType{localv1alpha1.RawDisk},
					MinSize:     &twentyGi,
					MaxSize:     &fiftyGi,
				},
			},
		}
		lvSets = append(lvSets, twentyToFifty)

		// create an identical lvset with an overlap
		// should match amd claim 1 of node0: 10
		// total: 1
		// disks  left within range left:
		// node0: 1
		// node1: 4
		// node2: 4
		tenToThirty := &localv1alpha1.LocalVolumeSet{}
		twentyToFifty.DeepCopyInto(tenToThirty)
		tenToThirty.ObjectMeta.Name = fmt.Sprintf("tentothirty-overlapping-%s-2", twentyToFifty.GetName())
		tenToThirty.Spec.StorageClassName = tenToThirty.GetName()

		// introduce differences
		tenToThirty.Spec.DeviceInclusionSpec.MinSize = &tenGi
		tenToThirty.Spec.DeviceInclusionSpec.MaxSize = &thirtyGi

		// filesystem lvset that only matches the 3rd node
		twentyToFiftyFilesystem := &localv1alpha1.LocalVolumeSet{}
		twentyToFifty.DeepCopyInto(twentyToFiftyFilesystem)
		twentyToFiftyFilesystem.ObjectMeta.Name = "twentytofifty-fs-3"
		twentyToFiftyFilesystem.Spec.StorageClassName = twentyToFiftyFilesystem.GetName()
		twentyToFiftyFilesystem.Spec.NodeSelector.NodeSelectorTerms[0].MatchExpressions[0].Values[0] = nodeEnv[2].node.ObjectMeta.Labels[corev1.LabelHostname]

		twentyToFiftyFilesystem.Spec.VolumeMode = localv1.PersistentVolumeFilesystem

		lvSets = []*localv1alpha1.LocalVolumeSet{
			twentyToFifty,
			tenToThirty,
			twentyToFiftyFilesystem,
		}

		// add pv and storageclass cleanup
		addToCleanupFuncs(
			cleanupFuncs,
			"cleanupLVSetResources",
			func(t *testing.T) error {
				return cleanupLVSetResources(t, lvSets)
			},
		)

		t.Logf("creating localvolumeset %q", twentyToFifty.GetName())
		err = f.Client.Create(context.TODO(), twentyToFifty, &framework.CleanupOptions{TestContext: ctx})
		matcher.Expect(err).NotTo(gomega.HaveOccurred(), "create localvolumeset")

		// look for 2 PVs
		eventuallyFindPVs(t, f, twentyToFifty.Spec.StorageClassName, 2)

		// update lvset
		// already claimed: 2
		// should match amd claim 1 of node0: 20,30,40
		// total: 3
		// disks  left within range:
		// node0: 0
		// node1: 3
		// node2: 3
		matcher.Eventually(func() error {
			t.Log("updating lvset")
			key := types.NamespacedName{Name: twentyToFifty.GetName(), Namespace: twentyToFifty.GetNamespace()}
			err := f.Client.Get(context.TODO(), key, twentyToFifty)
			if err != nil {
				t.Logf("error getting lvset %q: %+v", key, err)
				return err
			}

			twentyToFifty.Spec.MaxDeviceCount = &three
			err = f.Client.Update(context.TODO(), twentyToFifty)
			if err != nil {
				t.Logf("error getting lvset %q: %+v", key, err)
				return err
			}
			return nil
		}, time.Minute, time.Second*2).ShouldNot(gomega.HaveOccurred(), "updating lvset")

		// look for 3 PVs
		twentyToFiftyBlockPVs := eventuallyFindPVs(t, f, twentyToFifty.Spec.StorageClassName, 3)
		// verify deletion
		for _, pv := range twentyToFiftyBlockPVs {
			eventuallyDelete(t, false, &pv)
		}
		// verify that block PVs come back after deletion
		eventuallyFindPVs(t, f, twentyToFifty.Spec.StorageClassName, 3)

		t.Logf("creating localvolumeset %q", tenToThirty.GetName())
		err = f.Client.Create(context.TODO(), tenToThirty, &framework.CleanupOptions{TestContext: ctx})
		matcher.Expect(err).NotTo(gomega.HaveOccurred(), "create localvolumeset")

		// look for 1 PV
		eventuallyFindPVs(t, f, tenToThirty.Spec.StorageClassName, 1)

		// expand overlappingLVSet to node1
		// already claimed: 1
		// should match amd claim 2 of node1: 10, 20, 30
		// total: 3
		// disks  left within range:
		// node0: 0
		// node1: 2
		// node2: 3
		matcher.Eventually(func() error {
			t.Log("updating lvset")
			key := types.NamespacedName{Name: tenToThirty.GetName(), Namespace: tenToThirty.GetNamespace()}
			err := f.Client.Get(context.TODO(), key, tenToThirty)
			if err != nil {
				t.Logf("error getting lvset %q: %+v", key, err)
				return err
			}

			// update node selector
			tenToThirty.Spec.NodeSelector.NodeSelectorTerms[0].MatchExpressions[0].Values = append(
				tenToThirty.Spec.NodeSelector.NodeSelectorTerms[0].MatchExpressions[0].Values,
				nodeEnv[1].node.ObjectMeta.Labels[corev1.LabelHostname],
			)
			err = f.Client.Update(context.TODO(), tenToThirty)
			if err != nil {
				t.Logf("error getting lvset %q: %+v", key, err)
				return err
			}
			return nil
		}, time.Minute, time.Second*2).ShouldNot(gomega.HaveOccurred(), "updating lvset")

		// look for 3 PVs
		eventuallyFindPVs(t, f, tenToThirty.Spec.StorageClassName, 3)

		// create twentyToFiftyFilesystem
		// should match amd claim 2 of node2: 20, 30, 40
		// total: 2
		// disks  left within range:
		// node0: 0
		// node1: 1
		// node2: 1
		t.Logf("creating localvolumeset %q", twentyToFiftyFilesystem.GetName())
		err = f.Client.Create(context.TODO(), twentyToFiftyFilesystem, &framework.CleanupOptions{TestContext: ctx})
		matcher.Expect(err).NotTo(gomega.HaveOccurred(), "create localvolumeset")

		eventuallyFindPVs(t, f, twentyToFiftyFilesystem.Spec.StorageClassName, 2)

		// expand twentyToFiftyFilesystem to all remaining devices by setting nil nodeSelector, maxDevices, and deviceInclusionSpec
		// devices total: 15
		// devices claimed so far by other lvsets: 6
		// devices claimed so far: 2
		// new devices to be claimed: (15-(6+2))= 7
		// total: 7+2 = 9

		matcher.Eventually(func() error {
			t.Log("updating lvset")
			key := types.NamespacedName{Name: twentyToFiftyFilesystem.GetName(), Namespace: twentyToFiftyFilesystem.GetNamespace()}
			err := f.Client.Get(context.TODO(), key, twentyToFiftyFilesystem)
			if err != nil {
				t.Logf("error getting lvset %q, %+v", key, err)
				return err
			}

			// update node selector
			twentyToFiftyFilesystem.Spec.NodeSelector = nil

			// update maxDeviceCount
			twentyToFiftyFilesystem.Spec.MaxDeviceCount = nil

			// update deviceInclusionSpec
			twentyToFiftyFilesystem.Spec.DeviceInclusionSpec = nil

			err = f.Client.Update(context.TODO(), twentyToFiftyFilesystem)
			if err != nil {
				t.Logf("error updating lvset %q: %+v", key, err)
				return err
			}
			return nil
		}, time.Minute, time.Second*2).ShouldNot(gomega.HaveOccurred(), "updating lvset")
		fsPVs := eventuallyFindPVs(t, f, twentyToFiftyFilesystem.Spec.StorageClassName, 9)

		// verify pv annotation
		t.Logf("looking for %q annotation on pvs", provCommon.AnnProvisionedBy)
		verifyProvisionerAnnotation(t, fsPVs, nodeList.Items)

		// verify deletion
		t.Log("verify that filesystem PVs come back after deletion")
		for _, pv := range fsPVs {
			eventuallyDelete(t, false, &pv)
		}
		fsPVs = eventuallyFindPVs(t, f, twentyToFiftyFilesystem.Spec.StorageClassName, 9)

		// verify consumed PVs come back after release
		// filesystem PVs
		t.Log("TEST: that consumed filesystem PVs are deleted and recreated upon release")
		// record consuming objects for cleanup
		consumingObjectList := make([]client.Object, 0)
		addToCleanupFuncs(cleanupFuncs, "pv-consumer", func(t *testing.T) error {
			eventuallyDelete(t, false, consumingObjectList...)
			return nil
		})
		for _, pv := range fsPVs {
			pvc, job, pod := consumePV(t, ctx, pv)
			consumingObjectList = append(consumingObjectList, job, pvc, pod)
		}
		// release pvs
		eventuallyDelete(t, false, consumingObjectList...)
		// verify that PVs eventually come back
		eventuallyFindAvailablePVs(t, f, twentyToFiftyFilesystem.Spec.StorageClassName, fsPVs)

		// block PVs from lvset twentyToFifty
		t.Log("TEST: consumed block PVs are deleted and recreated upon release")
		// record consuming objects for cleanup
		consumingObjectList = make([]client.Object, 0)
		for _, pv := range twentyToFiftyBlockPVs {
			pvc, job, pod := consumePV(t, ctx, pv)
			consumingObjectList = append(consumingObjectList, job, pvc, pod)
		}
		// release pvs
		eventuallyDelete(t, false, consumingObjectList...)
		// verify that PVs eventually come back
		twentyToFiftyBlockPVs = eventuallyFindAvailablePVs(t, f, twentyToFifty.Spec.StorageClassName, twentyToFiftyBlockPVs)

		// verify localVolumeset deletion
		t.Log("TEST: localvolumeset deletion does not occur with bound PVs")

		// consume one PV
		consumingObjectList = make([]client.Object, 0)
		for _, pv := range twentyToFiftyBlockPVs[:1] {
			pvc, job, pod := consumePV(t, ctx, pv)
			consumingObjectList = append(consumingObjectList, job, pvc, pod)
		}

		matcher.Eventually(func() error {
			t.Logf("deleting LocalVolumeSet %q", twentyToFifty.Name)
			return f.Client.Delete(context.TODO(), twentyToFifty)
		}, time.Minute*5, time.Second*5).ShouldNot(gomega.HaveOccurred(), "deleting LocalVolumeSet %q", twentyToFifty.Name)

		var finalizers []string
		// verify finalizer not removed with while bound pvs exists
		matcher.Consistently(func() bool {
			t.Logf("verifying finalizer still exists because of bound PVs")
			err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: twentyToFifty.Name, Namespace: f.OperatorNamespace}, twentyToFifty)
			if err != nil && (errors.IsGone(err) || errors.IsNotFound(err)) {
				t.Fatalf("FAIL: LocalVolumeSet deleted with bound PVs: %+v", err)
				return false
			} else if err != nil {
				t.Logf("error getting LocalVolumeSet: %+v", err)
				return false
			}
			finalizers = twentyToFifty.GetFinalizers()
			return len(finalizers) > 0
		}, time.Second*30, time.Second*5).Should(gomega.BeTrue(), "checking finalizer exists with bound PVs")
		t.Logf("finalizers not removed with bound PVs: %q", finalizers)
		// release PV
		t.Logf("releasing pvs")
		eventuallyDelete(t, false, consumingObjectList...)
		// verify LocalVolumeSet deletion
		matcher.Eventually(func() bool {
			t.Log("verifying LocalVolumeSet deletion")
			err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: twentyToFifty.Name, Namespace: f.OperatorNamespace}, twentyToFifty)
			if err != nil && (errors.IsGone(err) || errors.IsNotFound(err)) {
				t.Logf("LocalVolumeSet deleted: %+v", err)
				return true
			} else if err != nil {
				t.Logf("error getting LocalVolumeSet: %+v", err)
				return false
			}
			t.Logf("LocalVolumeSet found: %q with finalizers: %+v", twentyToFifty.Name, twentyToFifty.ObjectMeta.Finalizers)
			return false
		}).Should(gomega.BeTrue(), "verifying LocalVolumeSet has been deleted", twentyToFifty.Name)

	}

}

func cleanupLVSetResources(t *testing.T, lvsets []*localv1alpha1.LocalVolumeSet) error {
	for _, lvset := range lvsets {

		t.Logf("cleaning up pvs and storageclasses: %q", lvset.GetName())
		f := framework.Global
		matcher := gomega.NewWithT(t)
		sc := &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: lvset.Spec.StorageClassName}}

		eventuallyDelete(t, true, lvset)
		eventuallyDelete(t, false, sc)
		pvList := &corev1.PersistentVolumeList{}
		t.Logf("listing pvs for lvset: %q", lvset.GetName())
		matcher.Eventually(func() error {
			err := f.Client.List(context.TODO(), pvList)
			if err != nil {
				return err
			}
			t.Logf("Deleting %d PVs", len(pvList.Items))
			for _, pv := range pvList.Items {
				if pv.Spec.StorageClassName == lvset.Spec.StorageClassName {
					eventuallyDelete(t, false, &pv)
				}
			}
			return nil
		}, time.Minute*3, time.Second*2).ShouldNot(gomega.HaveOccurred(), "cleaning up pvs for lvset: %q", lvset.GetName())
	}

	return nil
}
