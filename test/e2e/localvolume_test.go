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
		// Extended to support testing different FSTypes: default, ext4, xfs, block
		nodeEnv := []nodeDisks{
			{
				disks: []disk{
					{size: 10}, // default test disk
					{size: 20}, // ext4 test disk
					{size: 30}, // xfs test disk
					{size: 40}, // block test disk
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

		// Verify we have enough disks for all test cases
		if len(nodeEnv[0].disks) < 4 {
			t.Fatalf("expected at least 4 disks, got %d", len(nodeEnv[0].disks))
		}

		// Test cases for different FSType configurations and scenarios
		testCases := []struct {
			name             string
			fsType           string
			volumeMode       localv1.PersistentVolumeMode
			storageClassName string
			diskIndex        int
			testFinalizer    bool // whether to test finalizer behavior
			testAnnotations  bool // whether to test provisioner annotations
			testSymlinks     bool // whether to test symlink cleanup
		}{
			{
				name:             "default-filesystem-with-tolerations",
				fsType:           "",
				volumeMode:       localv1.PersistentVolumeFilesystem,
				storageClassName: lvStorageClassName,
				diskIndex:        0,
				testFinalizer:    true,
				testAnnotations:  true,
				testSymlinks:     true,
			},
			{
				name:             "ext4-filesystem",
				fsType:           "ext4",
				volumeMode:       localv1.PersistentVolumeFilesystem,
				storageClassName: "test-local-sc-ext4",
				diskIndex:        1,
				testFinalizer:    false,
				testAnnotations:  false,
				testSymlinks:     false,
			},
			{
				name:             "xfs-filesystem",
				fsType:           "xfs",
				volumeMode:       localv1.PersistentVolumeFilesystem,
				storageClassName: "test-local-sc-xfs",
				diskIndex:        2,
				testFinalizer:    false,
				testAnnotations:  false,
				testSymlinks:     false,
			},
			{
				name:             "block-mode",
				fsType:           "",
				volumeMode:       localv1.PersistentVolumeBlock,
				storageClassName: "test-local-sc-block",
				diskIndex:        3,
				testFinalizer:    false,
				testAnnotations:  false,
				testSymlinks:     false,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				selectedDisk := nodeEnv[0].disks[tc.diskIndex]
				matcher.Expect(selectedDisk.path).ShouldNot(gomega.BeZero(), "device path should not be empty for "+tc.name)

				var localVolume *localv1.LocalVolume
				if tc.name == "default-filesystem-with-tolerations" {
					// Use the original getFakeLocalVolume which includes tolerations
					localVolume = getFakeLocalVolume(selectedNode, selectedDisk.path, namespace)
				} else {
					// Use getFakeLocalVolumeWithFSType for other cases
					localVolume = getFakeLocalVolumeWithFSType(
						selectedNode,
						selectedDisk.path,
						namespace,
						tc.storageClassName,
						tc.fsType,
						tc.volumeMode,
						"",  // auto-generate name
						nil, // no tolerations
					)
				}

				matcher.Eventually(func() error {
					t.Logf("creating localvolume %s with fsType=%q, volumeMode=%s", tc.name, tc.fsType, tc.volumeMode)
					return f.Client.Create(goctx.TODO(), localVolume, &framework.CleanupOptions{TestContext: ctx})
				}, time.Minute, time.Second*2).ShouldNot(gomega.HaveOccurred(), "creating localvolume "+tc.name)

				// add pv and storageclass cleanup
				addToCleanupFuncs(
					cleanupFuncs,
					fmt.Sprintf("cleanupLVResources-%s", tc.name),
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

				err = checkLocalVolumeStatus(t, localVolume)
				if err != nil {
					t.Fatalf("error checking localvolume condition: %v", err)
				}

				// Find PVs
				pvs := eventuallyFindPVs(t, f, tc.storageClassName, 1)
				if len(pvs) == 0 {
					t.Fatalf("no pvs returned by eventuallyFindPVs for %s", tc.name)
				}

				// Verify PV path
				var expectedPath string
				if selectedDisk.id != "" {
					expectedPath = selectedDisk.id
				} else {
					expectedPath = selectedDisk.name
				}
				matcher.Expect(filepath.Base(pvs[0].Spec.Local.Path)).To(gomega.Equal(expectedPath))

				// Verify PV spec matches expected FSType and VolumeMode
				pv := pvs[0]
				t.Logf("verifying PV %s for test case %s", pv.Name, tc.name)

				if tc.volumeMode == localv1.PersistentVolumeBlock {
					// For block mode, verify volumeMode is Block
					if pv.Spec.VolumeMode == nil || *pv.Spec.VolumeMode != corev1.PersistentVolumeBlock {
						t.Fatalf("expected PV volumeMode to be Block, got %v", pv.Spec.VolumeMode)
					}
					t.Logf("verified PV %s has volumeMode=Block", pv.Name)
				} else {
					// For filesystem mode, verify fsType if specified
					if tc.fsType != "" {
						if pv.Spec.Local.FSType == nil || *pv.Spec.Local.FSType != tc.fsType {
							t.Fatalf("expected PV fsType to be %s, got %v", tc.fsType, pv.Spec.Local.FSType)
						}
						t.Logf("verified PV %s has fsType=%s", pv.Name, tc.fsType)
					}
				}

				// Verify StorageClass exists (Local Storage Operator uses kubernetes.io/no-provisioner,
				// so FSType is set on PV spec directly, not in StorageClass parameters)
				_, err = f.KubeClient.StorageV1().StorageClasses().Get(goctx.TODO(), tc.storageClassName, metav1.GetOptions{})
				if err != nil {
					t.Fatalf("error getting storage class %s: %v", tc.storageClassName, err)
				}
				t.Logf("verified StorageClass %s exists", tc.storageClassName)

				// Test provisioner annotations (only for default test)
				if tc.testAnnotations {
					t.Logf("looking for %q annotation on pvs", provCommon.AnnProvisionedBy)
					verifyProvisionerAnnotation(t, pvs, nodeList.Items)
				}

				// Test PV deletion and recreation
				for _, pv := range pvs {
					eventuallyDelete(t, false, &pv)
				}
				// verify that PVs come back after deletion
				pvs = eventuallyFindPVs(t, f, tc.storageClassName, 1)

				// consume pvs
				consumingObjectList := make([]client.Object, 0)
				for _, pv := range pvs {
					pvc, job, pod := consumePV(t, ctx, pv)
					consumingObjectList = append(consumingObjectList, job, pvc, pod)
				}
				// release pvs
				eventuallyDelete(t, false, consumingObjectList...)

				// verify that PVs eventually come back
				eventuallyFindAvailablePVs(t, f, tc.storageClassName, pvs)

				// Test finalizer behavior (only for default test)
				if tc.testFinalizer {
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

					// verify finalizer not removed while bound pvs exist
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
				} else {
					// For non-finalizer tests, just delete the LocalVolume
					matcher.Eventually(func() error {
						t.Logf("deleting LocalVolume %q", localVolume.Name)
						return f.Client.Delete(context.TODO(), localVolume, client.PropagationPolicy(metav1.DeletePropagationBackground))
					}, time.Minute*5, time.Second*5).ShouldNot(gomega.HaveOccurred(), "deleting LocalVolume %q", localVolume.Name)
				}

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
				}, time.Minute*5, time.Second*5).Should(gomega.BeTrue(), "verifying LocalVolume %s has been deleted", tc.name)

				// Check for leftover symlinks (only for default test)
				if tc.testSymlinks {
					symLinkPath := path.Join(common.GetLocalDiskLocationPath(), tc.storageClassName)
					checkForSymlinks(t, ctx, nodeEnv, symLinkPath)
				}
			})
		}
	}

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
	// Use the flexible helper with default values for backward compatibility
	return getFakeLocalVolumeWithFSType(
		selectedNode,
		selectedDisk,
		namespace,
		lvStorageClassName,
		"",                                 // default: no fsType specified
		localv1.PersistentVolumeFilesystem, // default: Filesystem mode
		"test-local-disk",                  // default name
		[]v1.Toleration{ // default tolerations
			{
				Key:      "localstorage",
				Value:    "testvalue",
				Operator: "Equal",
			},
		},
	)
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

// getFakeLocalVolumeWithFSType creates a LocalVolume CR with specific FSType and VolumeMode
// Parameters:
//   - selectedNode: the node to deploy on
//   - selectedDisk: the disk device path
//   - namespace: the namespace for the LocalVolume
//   - storageClassName: the storage class name
//   - fsType: filesystem type (ext4, xfs, or empty string for block mode)
//   - volumeMode: PersistentVolumeFilesystem or PersistentVolumeBlock
//   - name: the name of the LocalVolume CR (if empty, generates from storageClassName)
//   - tolerations: tolerations to apply (can be nil)
func getFakeLocalVolumeWithFSType(selectedNode v1.Node, selectedDisk, namespace, storageClassName, fsType string, volumeMode localv1.PersistentVolumeMode, name string, tolerations []v1.Toleration) *localv1.LocalVolume {
	// Generate name if not provided
	if name == "" {
		name = fmt.Sprintf("test-local-disk-%s", storageClassName)
	}

	// Build StorageClassDevice
	scd := localv1.StorageClassDevice{
		StorageClassName: storageClassName,
		DevicePaths:      []string{selectedDisk},
		VolumeMode:       volumeMode,
	}

	// Only set FSType for filesystem mode, not for block mode
	if volumeMode != localv1.PersistentVolumeBlock && fsType != "" {
		scd.FSType = fsType
	}

	localVolume := &localv1.LocalVolume{
		TypeMeta: metav1.TypeMeta{
			Kind:       "LocalVolume",
			APIVersion: localv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
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
			Tolerations:         tolerations,
			StorageClassDevices: []localv1.StorageClassDevice{scd},
		},
	}

	return localVolume
}
