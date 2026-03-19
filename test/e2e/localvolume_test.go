package e2e

import (
	"context"
	goctx "context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/onsi/gomega"
	operatorv1 "github.com/openshift/api/operator/v1"
	localv1 "github.com/openshift/local-storage-operator/api/v1"
	"github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/controllers/nodedaemon"
	framework "github.com/openshift/local-storage-operator/test/framework"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	dynclient "sigs.k8s.io/controller-runtime/pkg/client"
	provCommon "sigs.k8s.io/sig-storage-local-static-provisioner/pkg/common"
)

var (
	awsEBSNitroRegex   = "^[cmr]5.*|t3|z1d"
	labelInstanceType  = "beta.kubernetes.io/instance-type"
	lvStorageClassName = "test-local-sc"
)

func LocalVolumeTest(ctx *framework.Context, cleanupFuncs *[]cleanupFn) func(*testing.T) {
	return func(t *testing.T) {
		f := framework.Global
		namespace, err := ctx.GetOperatorNamespace()
		if err != nil {
			t.Fatalf("error fetching namespace : %v", err)
		}

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
				},
				node: nodeList.Items[0],
			},
			{
				disks: []disk{
					{size: 10},
					{size: 20},
				},
				node: nodeList.Items[1],
			},
		}
		selectedNode := nodeEnv[0].node

		matcher := gomega.NewGomegaWithT(t)
		gomega.SetDefaultEventuallyTimeout(time.Minute * 10)
		gomega.SetDefaultEventuallyPollingInterval(time.Second * 2)

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
		createAndAttachAWSVolumes(t, ec2Client, ctx, namespace, nodeEnv)

		// get the device paths and IDs
		nodeEnv = populateDeviceInfo(t, ctx, nodeEnv)

		selectedDisk := nodeEnv[0].disks[0]
		matcher.Expect(selectedDisk.path).ShouldNot(gomega.BeZero(), "device path should not be empty")

		localVolume := getFakeLocalVolume(selectedNode, selectedDisk.path, namespace)

		matcher.Eventually(func() error {
			t.Log("creating localvolume")
			return f.Client.Create(goctx.TODO(), localVolume, &framework.CleanupOptions{TestContext: ctx})
		}, time.Minute, time.Second*2).ShouldNot(gomega.HaveOccurred(), "creating localvolume")

		// add pv and storageclass cleanup
		addToCleanupFuncs(
			cleanupFuncs,
			"cleanupLVResources",
			func(t *testing.T) error {
				return cleanupLVResources(t, f, localVolume)
			},
		)
		err = waitForDaemonSet(t, f.KubeClient, namespace, nodedaemon.DiskMakerName, retryInterval, timeout)
		if err != nil {
			t.Fatalf("error waiting for diskmaker daemonset : %v", err)
		}

		err = verifyLocalVolume(t, localVolume, f.Client)
		if err != nil {
			t.Fatalf("error verifying localvolume cr: %v", err)
		}

		//	time.Sleep(10 * time.Minute)
		err = checkLocalVolumeStatus(t, localVolume)
		if err != nil {
			t.Fatalf("error checking localvolume condition: %v", err)
		}

		pvs := eventuallyFindPVs(t, f, localVolume.Spec.StorageClassDevices[0].StorageClassName, 1)
		var expectedPath string
		if len(pvs) > 0 {
			if selectedDisk.id != "" {
				expectedPath = selectedDisk.id
			} else {
				expectedPath = selectedDisk.name
			}
		} else {
			t.Fatalf("no pvs returned by eventuallyFindPVs: %+v", pvs)
		}
		matcher.Expect(filepath.Base(pvs[0].Spec.Local.Path)).To(gomega.Equal(expectedPath))

		// verify pv annotation
		t.Logf("looking for %q annotation on pvs", provCommon.AnnProvisionedBy)
		verifyProvisionerAnnotation(t, pvs, nodeList.Items)
		pvNames := make([]string, 0, len(pvs))
		for _, pv := range pvs {
			pvNames = append(pvNames, pv.Name)
		}

		t.Log("verifying LocalVolumeDeviceLink objects were created for PVs")
		lvdls := eventuallyFindLVDLsForPVs(t, f, namespace, pvNames)
		lvdlNames := make([]string, 0, len(lvdls))
		for _, lvdl := range lvdls {
			lvdlNames = append(lvdlNames, lvdl.Name)
			matcher.Expect(lvdl.Status.CurrentLinkTarget).ToNot(gomega.BeEmpty(), "expected CurrentLinkTarget for LVDL %q", lvdl.Name)
			matcher.Expect(lvdl.Status.PreferredLinkTarget).ToNot(gomega.BeEmpty(), "expected PreferredLinkTarget for LVDL %q", lvdl.Name)
			matcher.Expect(lvdl.Status.ValidLinkTargets).ToNot(gomega.BeEmpty(), "expected ValidLinkTargets for LVDL %q", lvdl.Name)
		}

		// Use just the first LVDL for the test. Figure its node by inspecting the corresponding PV spec.nodeAffinity.
		lvdl := lvdls[0]
		nodeHostname := findNodeHostnameForLVDL(t, f, lvdl)
		t.Logf("using node %q for the test of LVDL %s", nodeHostname, lvdl.Name)
		// On AWS, the preferred link is usually "/dev/disk/by-id/nvme-Amazon_Elastic_Block_Store_*"
		// scsi-1 has a higher priority in dikutil.go and should become the new preferred target
		newPreferredTarget := "/dev/disk/by-id/scsi-1-local-storage-e2e-test"
		oldPreferredTarget := lvdl.Status.PreferredLinkTarget
		addNewUdevSymlink(t, ctx, nodeHostname, lvdl.Status.PreferredLinkTarget, newPreferredTarget)
		waitForLVDLContent(t, f, namespace, lvdl.Name, "waiting for LVDL to get new preferredLinkTarget", func(lvdl *localv1.LocalVolumeDeviceLink) error {
			if lvdl.Status.PreferredLinkTarget != newPreferredTarget {
				return fmt.Errorf("expected PreferredLinkTarget for LVDL %q to be updated, got %q", lvdl.Name, lvdl.Status.PreferredLinkTarget)
			}
			t.Logf("PreferredLinkTarget for LVDL %q is updated, got %q", lvdl.Name, lvdl.Status.PreferredLinkTarget)
			return nil
		})

		removeUdevSymlink(t, ctx, nodeHostname, "/dev/disk/by-id/scsi-1-local-storage-e2e-test")

		waitForLVDLContent(t, f, namespace, lvdl.Name, "waiting for LVDL to restore old preferredLinkTarget", func(lvdl *localv1.LocalVolumeDeviceLink) error {
			if lvdl.Status.PreferredLinkTarget != oldPreferredTarget {
				return fmt.Errorf("expected PreferredLinkTarget for LVDL %q to be restored, got %q", lvdl.Name, lvdl.Status.PreferredLinkTarget)
			}
			t.Logf("PreferredLinkTarget for LVDL %q is restored, got %q", lvdl.Name, lvdl.Status.PreferredLinkTarget)
			return nil
		})

		// verify deletion
		for _, pv := range pvs {
			eventuallyDelete(t, false, &pv)
		}
		// verify that PVs come back after deletion
		pvs = eventuallyFindPVs(t, f, localVolume.Spec.StorageClassDevices[0].StorageClassName, 1)

		// consume pvs
		consumingObjectList := make([]client.Object, 0)
		filesystemConsumedPVNames := make([]string, 0)
		for _, pv := range pvs {
			pvc, job, pod := consumePV(t, ctx, pv)
			consumingObjectList = append(consumingObjectList, job, pvc, pod)
			if pv.Spec.VolumeMode == nil || *pv.Spec.VolumeMode == corev1.PersistentVolumeFilesystem {
				filesystemConsumedPVNames = append(filesystemConsumedPVNames, pv.Name)
			}
		}
		if len(filesystemConsumedPVNames) > 0 {
			t.Log("verifying filesystemUUID is populated on LVDL for consumed filesystem PVs")
			verifyLVDLFilesystemUUIDForPVs(t, f, namespace, filesystemConsumedPVNames)
		}
		// release pvs
		eventuallyDelete(t, false, consumingObjectList...)

		// verify that PVs eventually come back
		eventuallyFindAvailablePVs(t, f, localVolume.Spec.StorageClassDevices[0].StorageClassName, pvs)

		// consume one PV
		consumingObjectList = make([]client.Object, 0)

		addToCleanupFuncs(cleanupFuncs, "pv-consumer", func(t *testing.T) error {
			eventuallyDelete(t, false, consumingObjectList...)
			return nil
		})
		for _, pv := range pvs[:1] {
			pvc, job, pod := consumePV(t, ctx, pv)
			consumingObjectList = append(consumingObjectList, job, pvc, pod)
		}
		// attempt localVolume deletion
		matcher.Eventually(func() error {
			t.Logf("deleting LocalVolume %q", localVolume.Name)
			return f.Client.Delete(context.TODO(), localVolume, client.PropagationPolicy(metav1.DeletePropagationBackground))
		}, time.Minute*5, time.Second*5).ShouldNot(gomega.HaveOccurred(), "deleting LocalVolume %q", localVolume.Name)

		// verify finalizer not removed with while bound pvs exists
		matcher.Consistently(func() bool {
			t.Logf("verifying finalizer still exists")
			err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: localVolume.Name, Namespace: f.OperatorNamespace}, localVolume)
			if err != nil && (errors.IsGone(err) || errors.IsNotFound(err)) {
				t.Fatalf("LocalVolume deleted with bound PVs")
				return false
			} else if err != nil {
				t.Logf("error getting LocalVolume: %+v", err)
				return false
			}
			return len(localVolume.ObjectMeta.Finalizers) > 0
		}, time.Second*15, time.Second*3).Should(gomega.BeTrue(), "checking finalizer exists with bound PVs")
		// release PV
		t.Logf("releasing pvs")
		eventuallyDelete(t, false, consumingObjectList...)
		// verify localVolume deletion
		matcher.Eventually(func() bool {
			t.Log("verifying LocalVolume deletion")
			err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: localVolume.Name, Namespace: f.OperatorNamespace}, localVolume)
			if err != nil && (errors.IsGone(err) || errors.IsNotFound(err)) {
				t.Logf("LocalVolume deleted: %+v", err)
				return true
			} else if err != nil {
				t.Logf("error getting LocalVolume: %+v", err)
				return false
			}
			t.Logf("LocalVolume found: %q with finalizers: %+v", localVolume.Name, localVolume.ObjectMeta.Finalizers)
			return false
		}).Should(gomega.BeTrue(), "verifying LocalVolume has been deleted", localVolume.Name)

		t.Log("verifying LocalVolumeDeviceLink objects are deleted after LocalVolume deletion")
		verifyLVDLsDeleted(t, f, namespace, lvdlNames)

		// check for leftover symlinks before cleanup
		symLinkPath := path.Join(common.GetLocalDiskLocationPath(), lvStorageClassName)
		checkForSymlinks(t, ctx, nodeEnv, symLinkPath)
	}

}

// findNodeHostnameForLVDL returns the node hostname where the LVDL's PV is scheduled,
// by inspecting the PV's spec.nodeAffinity (Required, LabelHostname).
func findNodeHostnameForLVDL(t *testing.T, f *framework.Framework, lvdl localv1.LocalVolumeDeviceLink) string {
	t.Helper()
	pvName := lvdl.Spec.PersistentVolumeName
	if pvName == "" {
		t.Fatalf("LVDL %q has no PersistentVolumeName", lvdl.Name)
	}
	pv := &corev1.PersistentVolume{}
	err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: pvName}, pv)
	if err != nil {
		t.Fatalf("expected to find PV %s for LVDL %q: %v", pvName, lvdl.Name, err)
	}
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

func eventuallyFindLVDLsForPVs(t *testing.T, f *framework.Framework, namespace string, pvNames []string) []localv1.LocalVolumeDeviceLink {
	matcher := gomega.NewWithT(t)
	pvNameSet := sets.New(pvNames...)
	foundLVDLs := make([]localv1.LocalVolumeDeviceLink, 0)
	matcher.Eventually(func() bool {
		lvdlList := &localv1.LocalVolumeDeviceLinkList{}
		err := f.Client.List(goctx.TODO(), lvdlList, client.InNamespace(namespace))
		if err != nil {
			t.Logf("error listing LocalVolumeDeviceLink objects: %v", err)
			return false
		}
		foundLVDLs = foundLVDLs[:0]
		for _, lvdl := range lvdlList.Items {
			if ok := pvNameSet.Has(lvdl.Spec.PersistentVolumeName); ok {
				foundLVDLs = append(foundLVDLs, lvdl)
			}
		}
		return len(foundLVDLs) == len(pvNameSet)
	}, time.Minute*5, time.Second*5).Should(gomega.BeTrue(), "waiting for LVDL objects for all PVs")
	return foundLVDLs
}

func verifyLVDLFilesystemUUIDForPVs(t *testing.T, f *framework.Framework, namespace string, pvNames []string) {
	matcher := gomega.NewWithT(t)
	pvNameSet := map[string]struct{}{}
	for _, pvName := range pvNames {
		pvNameSet[pvName] = struct{}{}
	}
	matcher.Eventually(func() bool {
		lvdlList := &localv1.LocalVolumeDeviceLinkList{}
		err := f.Client.List(goctx.TODO(), lvdlList, client.InNamespace(namespace))
		if err != nil {
			t.Logf("error listing LocalVolumeDeviceLink objects while verifying filesystemUUID: %v", err)
			return false
		}
		uuidFoundCount := 0
		for _, lvdl := range lvdlList.Items {
			if _, ok := pvNameSet[lvdl.Spec.PersistentVolumeName]; !ok {
				continue
			}
			if lvdl.Status.FilesystemUUID == "" {
				t.Logf("filesystemUUID not populated yet for LVDL %q / PV %q", lvdl.Name, lvdl.Spec.PersistentVolumeName)
				return false
			}
			uuidFoundCount++
		}
		return uuidFoundCount == len(pvNameSet)
	}, time.Minute*5, time.Second*5).Should(gomega.BeTrue(), "waiting for filesystemUUID on all consumed filesystem PV LVDLs")
}

func verifyLVDLsDeleted(t *testing.T, f *framework.Framework, namespace string, lvdlNames []string) {
	matcher := gomega.NewWithT(t)
	targetNames := map[string]struct{}{}
	for _, name := range lvdlNames {
		targetNames[name] = struct{}{}
	}

	matcher.Eventually(func() bool {
		lvdlList := &localv1.LocalVolumeDeviceLinkList{}
		err := f.Client.List(goctx.TODO(), lvdlList, client.InNamespace(namespace))
		if err != nil {
			t.Logf("error listing LocalVolumeDeviceLink objects during deletion check: %v", err)
			return false
		}
		for _, lvdl := range lvdlList.Items {
			if _, ok := targetNames[lvdl.Name]; ok {
				t.Logf("LocalVolumeDeviceLink %q still exists", lvdl.Name)
				return false
			}
		}
		return true
	}, time.Minute*5, time.Second*5).Should(gomega.BeTrue(), "waiting for LVDL deletion after LocalVolume deletion")
}

func verifyProvisionerAnnotation(t *testing.T, pvs []corev1.PersistentVolume, nodeList []corev1.Node) {
	matcher := gomega.NewWithT(t)
	for _, pv := range pvs {
		hostFound := true
		hostname, found := pv.ObjectMeta.Labels[corev1.LabelHostname]
		if !found {
			t.Fatalf("expected to find %q label on the pv %+v", corev1.LabelHostname, pv)
		}
		for _, node := range nodeList {
			nodeHostName, found := node.ObjectMeta.Labels[corev1.LabelHostname]
			if !found {
				t.Fatalf("expected to find %q label on the node %+v", corev1.LabelHostname, node)
			}
			if hostname == nodeHostName {
				expectedAnnotation := common.GetProvisionedByValue(node)
				actualAnnotation, found := pv.ObjectMeta.Annotations[provCommon.AnnProvisionedBy]
				matcher.Expect(found).To(gomega.BeTrue(), "expected to find annotation %q on pv", provCommon.AnnProvisionedBy)
				matcher.Expect(actualAnnotation).To(gomega.Equal(expectedAnnotation), "expected to find correct annotation value for %q", provCommon.AnnProvisionedBy)
				hostFound = true
				break
			}
		}
		if !hostFound {
			t.Fatalf("did not find a node entry matching this pv: %+v nodeList: %+v", pv, nodeList)
		}
	}

}

func cleanupLVResources(t *testing.T, f *framework.Framework, localVolume *localv1.LocalVolume) error {
	// cleanup lv force-removing the finalizer if necessary
	eventuallyDelete(t, true, localVolume)
	sc := &storagev1.StorageClass{
		TypeMeta:   metav1.TypeMeta{Kind: localv1.LocalVolumeKind},
		ObjectMeta: metav1.ObjectMeta{Name: localVolume.Spec.StorageClassDevices[0].StorageClassName},
	}
	eventuallyDelete(t, false, sc)
	pvList := &corev1.PersistentVolumeList{}
	matcher := gomega.NewWithT(t)
	matcher.Eventually(func() error {
		err := f.Client.List(context.TODO(), pvList)
		if err != nil {
			return err
		}
		t.Logf("Deleting %d PVs", len(pvList.Items))
		for _, pv := range pvList.Items {
			// pv.TypeMeta.Kind = kind
			eventuallyDelete(t, false, &pv)
		}
		return nil
	}, time.Minute*3, time.Second*2).ShouldNot(gomega.HaveOccurred(), "cleaning up pvs for lv: %q", localVolume.GetName())

	return nil

}
func verifyLocalVolume(t *testing.T, lv *localv1.LocalVolume, client framework.FrameworkClient) error {
	waitErr := wait.PollImmediate(retryInterval, timeout, func() (bool, error) {
		objectKey := dynclient.ObjectKey{
			Namespace: lv.Namespace,
			Name:      lv.Name,
		}
		err := client.Get(goctx.TODO(), objectKey, lv)
		if err != nil {
			return false, err
		}
		finaliers := lv.GetFinalizers()
		if len(finaliers) == 0 {
			return false, nil
		}
		t.Log("Local volume verification successful")
		return true, nil
	})
	return waitErr
}

func verifyDaemonSetTolerations(kubeclient kubernetes.Interface, daemonSetName, namespace string, tolerations []v1.Toleration) error {
	dsTolerations := []v1.Toleration{}
	err := wait.Poll(retryInterval, timeout, func() (done bool, err error) {
		daemonset, err := kubeclient.AppsV1().DaemonSets(namespace).Get(goctx.TODO(), daemonSetName, metav1.GetOptions{})
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
	waitError := wait.Poll(retryInterval, timeout, func() (done bool, err error) {
		_, err = kubeclient.StorageV1().StorageClasses().Get(goctx.TODO(), scName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	})
	return waitError
}

func checkLocalVolumeStatus(t *testing.T, lv *localv1.LocalVolume) error {
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
	t.Log("LocalVolume status verification successful")
	return nil
}

func deleteCreatedPV(t *testing.T, kubeClient kubernetes.Interface, lv *localv1.LocalVolume) error {
	err := kubeClient.CoreV1().PersistentVolumes().DeleteCollection(goctx.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: common.GetPVOwnerSelector(lv).String()})
	if err == nil {
		t.Log("PV deletion successful")
	}
	return err
}

func waitForCreatedPV(kubeClient kubernetes.Interface, lv *localv1.LocalVolume) error {
	waitErr := wait.PollImmediate(retryInterval, timeout, func() (bool, error) {
		pvs, err := kubeClient.CoreV1().PersistentVolumes().List(goctx.TODO(), metav1.ListOptions{LabelSelector: common.GetPVOwnerSelector(lv).String()})
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
	return waitErr
}

func selectNode(t *testing.T, kubeClient kubernetes.Interface) v1.Node {
	nodes, err := kubeClient.CoreV1().Nodes().List(goctx.TODO(), metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/worker"})
	var dummyNode v1.Node
	if err != nil {
		t.Fatalf("error finding worker node with %v", err)
	}

	if len(nodes.Items) != 0 {
		return nodes.Items[0]
	}
	nodeList, err := waitListSchedulableNodes(kubeClient)
	if err != nil {
		t.Fatalf("error listing schedulable nodes : %v", err)
	}
	if len(nodeList.Items) != 0 {
		return nodeList.Items[0]
	}
	t.Fatalf("found no schedulable node")
	return dummyNode
}

func selectDisk(kubeClient kubernetes.Interface, node v1.Node) (string, error) {
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

func getNitroDisk(kubeClient kubernetes.Interface, node v1.Node) (string, error) {
	return "", fmt.Errorf("unimplemented")
}

func isRetryableAPIError(err error) bool {
	// These errors may indicate a transient error that we can retry in tests.
	if apierrors.IsInternalError(err) || apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) ||
		apierrors.IsTooManyRequests(err) || utilnet.IsProbableEOF(err) || utilnet.IsConnectionReset(err) {
		return true
	}
	// If the error sends the Retry-After header, we respect it as an explicit confirmation we should retry.
	if _, shouldRetry := apierrors.SuggestsClientDelay(err); shouldRetry {
		return true
	}
	return false
}

// waitListSchedulableNodes is a wrapper around listing nodes supporting retries.
func waitListSchedulableNodes(c kubernetes.Interface) (*v1.NodeList, error) {
	var nodes *v1.NodeList
	var err error
	if wait.PollImmediate(retryInterval, timeout, func() (bool, error) {
		nodes, err = c.CoreV1().Nodes().List(goctx.TODO(), metav1.ListOptions{FieldSelector: fields.Set{
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

func waitForDaemonSet(t *testing.T, kubeclient kubernetes.Interface, namespace, name string, retryInterval, timeout time.Duration) error {
	nodeCount := 1
	var err error
	err = wait.Poll(retryInterval, timeout, func() (done bool, err error) {
		daemonset, err := kubeclient.AppsV1().DaemonSets(namespace).Get(goctx.TODO(), name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				t.Logf("Waiting for availability of %s daemonset\n", name)
				return false, nil
			}
			return false, err
		}
		if int(daemonset.Status.NumberReady) == nodeCount {
			return true, nil
		}
		t.Logf("Waiting for full availability of %s daemonset (%d/%d)\n", name, int(daemonset.Status.NumberReady), nodeCount)
		return false, nil
	})
	if err != nil {
		return err
	}
	t.Logf("Daemonset available (%d/%d)\n", nodeCount, nodeCount)
	return nil
}

func waitForNodeTaintUpdate(t *testing.T, kubeclient kubernetes.Interface, node v1.Node, retryInterval, timeout time.Duration) (v1.Node, error) {
	var err error
	var newNode *v1.Node
	name := node.Name
	err = wait.Poll(retryInterval, timeout, func() (done bool, err error) {
		newNode, err = kubeclient.CoreV1().Nodes().Get(goctx.TODO(), name, metav1.GetOptions{})
		newNode.Spec.Taints = node.Spec.Taints
		newNode, err = kubeclient.CoreV1().Nodes().Update(goctx.TODO(), newNode, metav1.UpdateOptions{})
		if err != nil {
			t.Logf("Failed to update node %v successfully : %v", name, err)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return *newNode, err
	}
	t.Logf("Node %v updated successfully\n", name)
	return *newNode, nil
}

func getFakeLocalVolume(selectedNode v1.Node, selectedDisk, namespace string) *localv1.LocalVolume {
	localVolume := &localv1.LocalVolume{
		TypeMeta: metav1.TypeMeta{
			Kind:       "LocalVolume",
			APIVersion: localv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-local-disk",
			Namespace: namespace,
		},
		Spec: localv1.LocalVolumeSpec{
			NodeSelector: &v1.NodeSelector{
				NodeSelectorTerms: []v1.NodeSelectorTerm{
					{
						MatchFields: []v1.NodeSelectorRequirement{
							{Key: "metadata.name", Operator: v1.NodeSelectorOpIn, Values: []string{selectedNode.Name}},
						},
					},
				},
			},
			Tolerations: []v1.Toleration{
				{
					Key:      "localstorage",
					Value:    "testvalue",
					Operator: "Equal",
				},
			},
			StorageClassDevices: []localv1.StorageClassDevice{
				{
					StorageClassName: lvStorageClassName,
					DevicePaths:      []string{selectedDisk},
				},
			},
		},
	}

	return localVolume
}

func deleteResource(obj client.Object, namespace, name string, client framework.FrameworkClient) error {
	err := client.Delete(goctx.TODO(), obj)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	waitErr := wait.PollImmediate(retryInterval, timeout, func() (bool, error) {
		objectKey := dynclient.ObjectKey{
			Namespace: namespace,
			Name:      name,
		}
		err := client.Get(goctx.TODO(), objectKey, obj)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	})
	return waitErr
}
