package e2e

import (
	"context"
	goctx "context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/onsi/gomega"
	operatorv1 "github.com/openshift/api/operator/v1"
	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	commontypes "github.com/openshift/local-storage-operator/pkg/common"
	"github.com/openshift/local-storage-operator/pkg/controller/nodedaemon"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	dynclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	awsEBSNitroRegex  = "^[cmr]5.*|t3|z1d"
	labelInstanceType = "beta.kubernetes.io/instance-type"
)

func LocalVolumeTest(ctx *framework.Context, cleanupFuncs *[]cleanupFn) func(*testing.T) {
	return func(t *testing.T) {
		f := framework.Global
		namespace, err := ctx.GetNamespace()
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
		err = createAndAttachAWSVolumes(t, ec2Client, ctx, namespace, nodeEnv)
		matcher.Expect(err).NotTo(gomega.HaveOccurred(), "createAndAttachAWSVolumes: %+v", nodeEnv)

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

	}

}

func cleanupLVResources(t *testing.T, f *framework.Framework, localVolume *localv1.LocalVolume) error {
	err := deleteResource(localVolume, localVolume.Name, localVolume.Namespace, f.Client)
	if err != nil {
		t.Fatalf("error deleting localvolume: %v", err)
	}
	sc := &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: localVolume.Spec.StorageClassDevices[0].StorageClassName}}
	eventuallyDelete(t, sc, localVolume.Spec.StorageClassDevices[0].StorageClassName)
	pvList := &corev1.PersistentVolumeList{}
	matcher := gomega.NewWithT(t)
	matcher.Eventually(func() error {
		err := f.Client.List(context.TODO(), pvList)
		if err != nil {
			return err
		}
		t.Logf("Deleting %d PVs", len(pvList.Items))
		for _, pv := range pvList.Items {
			eventuallyDelete(t, &pv, pv.GetName())
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
		daemonset, err := kubeclient.AppsV1().DaemonSets(namespace).Get(daemonSetName, metav1.GetOptions{})
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
		_, err = kubeclient.StorageV1().StorageClasses().Get(scName, metav1.GetOptions{})
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
	err := kubeClient.CoreV1().PersistentVolumes().DeleteCollection(nil, metav1.ListOptions{LabelSelector: commontypes.GetPVOwnerSelector(lv).String()})
	if err == nil {
		t.Log("PV deletion successful")
	}
	return err
}

func waitForCreatedPV(kubeClient kubernetes.Interface, lv *localv1.LocalVolume) error {
	waitErr := wait.PollImmediate(retryInterval, timeout, func() (bool, error) {
		pvs, err := kubeClient.CoreV1().PersistentVolumes().List(metav1.ListOptions{LabelSelector: commontypes.GetPVOwnerSelector(lv).String()})
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
	nodes, err := kubeClient.CoreV1().Nodes().List(metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/worker"})
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
		nodes, err = c.CoreV1().Nodes().List(metav1.ListOptions{FieldSelector: fields.Set{
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
		daemonset, err := kubeclient.AppsV1().DaemonSets(namespace).Get(name, metav1.GetOptions{})
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
		newNode, err = kubeclient.CoreV1().Nodes().Get(name, metav1.GetOptions{})
		newNode.Spec.Taints = node.Spec.Taints
		newNode, err = kubeclient.CoreV1().Nodes().Update(newNode)
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
			APIVersion: localv1.SchemeGroupVersion.String(),
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
					StorageClassName: "test-local-sc",
					DevicePaths:      []string{selectedDisk},
				},
			},
		},
	}

	return localVolume
}

func deleteResource(obj runtime.Object, namespace, name string, client framework.FrameworkClient) error {
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
