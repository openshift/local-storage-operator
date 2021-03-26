package e2e

import (
	goctx "context"
	"fmt"
	"testing"
	"time"

	"github.com/onsi/gomega"
	"github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	dynclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func LocalVolumeDiscoveryTest(ctx *framework.Context, cleanupFuncs *[]cleanupFn) func(*testing.T) {
	return func(t *testing.T) {
		f := framework.Global
		namespace, err := ctx.GetNamespace()
		if err != nil {
			t.Fatalf("error fetching namespace : %v", err)
		}

		selectedNode := selectNode(t, f.KubeClient)
		originalNodeTaints := selectedNode.Spec.Taints
		selectedNode.Spec.Taints = []v1.Taint{
			{
				Key:    "localstorage",
				Value:  "testvalue",
				Effect: "NoSchedule",
			},
		}
		updatedNode, err := waitForNodeTaintUpdate(t, f.KubeClient, selectedNode, retryInterval, timeout)
		if err != nil {
			t.Fatalf("error tainting node : %v", err)
		}
		selectedNode = updatedNode

		defer func() {
			selectedNode.Spec.Taints = originalNodeTaints
			selectedNode, err = waitForNodeTaintUpdate(t, f.KubeClient, selectedNode, retryInterval, timeout)
			if err != nil {
				t.Fatalf("error restoring original taints on node: %v", err)
			}
		}()

		matcher := gomega.NewGomegaWithT(t)
		gomega.SetDefaultEventuallyTimeout(time.Minute * 10)
		gomega.SetDefaultEventuallyPollingInterval(time.Second * 2)

		localVolumeDiscovery := getFakeLocalVolumeDiscovery(selectedNode, namespace)

		err = f.Client.Create(goctx.TODO(), localVolumeDiscovery, nil)
		if err != nil {
			t.Fatalf("error creating localvolumediscovery cr : %v", err)
		}

		defer deleteResource(localVolumeDiscovery, localVolumeDiscovery.Name, localVolumeDiscovery.Namespace, f.Client)

		discoveryDSName := "diskmaker-discovery"
		err = waitForDaemonSet(t, f.KubeClient, namespace, discoveryDSName, retryInterval, timeout)
		if err != nil {
			t.Fatalf("error waiting for diskmaker daemonset : %v", err)
		}

		err = verifyLocalVolumeDiscovery(localVolumeDiscovery, f.Client)
		if err != nil {
			t.Fatalf("error verifying localvolumediscovery cr: %v", err)
		}

		localVolumeDiscoveryResult := &localv1alpha1.LocalVolumeDiscoveryResult{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("discovery-result-%s", selectedNode.Name),
				Namespace: namespace,
			},
		}

		discoveredDevices, err := verifyLocalVolumeDiscoveryResult(t, localVolumeDiscoveryResult, selectedNode.Name, f.Client)
		if err != nil {
			t.Fatalf("error verifying localvolumediscoveryresult. %v", err)
		}

		t.Log("getting AWS region info from node spec")
		_, region, _, err := getAWSNodeInfo(selectedNode)
		matcher.Expect(err).NotTo(gomega.HaveOccurred(), "getAWSNodeInfo")

		// initialize client
		t.Log("initialize ec2 creds")
		ec2Client, err := getEC2Client(region)
		matcher.Expect(err).NotTo(gomega.HaveOccurred(), "getEC2Client")

		// register disk cleanup
		addToCleanupFuncs(cleanupFuncs, "cleanupAWSDisks", func(t *testing.T) error {
			return cleanupAWSDisks(t, ec2Client)
		})

		// create and attach volumes
		t.Log("creating and attaching disks")

		// represents the disk layout to setup on the nodes.
		nodeEnv := []nodeDisks{
			{
				disks: []disk{
					{size: 10},
				},
				node: selectedNode,
			},
		}
		err = createAndAttachAWSVolumes(t, ec2Client, ctx, namespace, nodeEnv)
		matcher.Expect(err).NotTo(gomega.HaveOccurred(), "createAndAttachAWSVolumes: %+v", selectedNode)

		// verify that discovered device list got updated when a new volume is added
		err = verifyContinousDiscovery(t, f.Client, localVolumeDiscoveryResult, discoveredDevices, selectedNode.Name)
		if err != nil {
			t.Fatalf("error running continuous discovery. %v", err)
		}

		err = checkLocalVolumeDiscoveryStatus(localVolumeDiscovery)
		if err != nil {
			t.Fatalf("error checking localvolumediscovery status. %v", err)
		}

		err = deleteResource(localVolumeDiscovery, localVolumeDiscovery.Name, localVolumeDiscovery.Namespace, f.Client)
		if err != nil {
			t.Fatalf("error deleting localvolumediscovery: %v", err)
		}
	}
}

func getFakeLocalVolumeDiscovery(selectedNode v1.Node, namespace string) *localv1alpha1.LocalVolumeDiscovery {
	localVolumeDiscovery := &localv1alpha1.LocalVolumeDiscovery{
		TypeMeta: metav1.TypeMeta{
			Kind:       "LocalVolumeDiscovery",
			APIVersion: localv1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "auto-discover-devices",
			Namespace: namespace,
		},
		Spec: localv1alpha1.LocalVolumeDiscoverySpec{
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
		},
	}

	return localVolumeDiscovery
}

func checkLocalVolumeDiscoveryStatus(lvd *localv1alpha1.LocalVolumeDiscovery) error {
	localVolumeDiscoveryPhase := lvd.Status.Phase
	if localVolumeDiscoveryPhase != localv1alpha1.Discovering {
		return fmt.Errorf("expected local volume discovery to be in discovering phase")
	}

	return nil
}

func verifyLocalVolumeDiscovery(lvd *localv1alpha1.LocalVolumeDiscovery, client framework.FrameworkClient) error {
	waitErr := wait.PollImmediate(retryInterval, timeout, func() (bool, error) {
		objectKey := dynclient.ObjectKey{
			Namespace: lvd.Namespace,
			Name:      lvd.Name,
		}
		err := client.Get(goctx.TODO(), objectKey, lvd)
		if err != nil {
			return false, err
		}
		return true, nil
	})

	return waitErr
}

func verifyContinousDiscovery(t *testing.T, client framework.FrameworkClient, lvdr *localv1alpha1.LocalVolumeDiscoveryResult,
	oldDevices []v1alpha1.DiscoveredDevice, nodeName string) error {
	waitErr := wait.Poll(retryInterval, timeout, func() (bool, error) {
		updatedDiscoveredDevices, err := verifyLocalVolumeDiscoveryResult(t, lvdr, nodeName, client)
		if err != nil {
			return false, fmt.Errorf("error fetching discovered devices from localvolumediscoveryresult. %v", err)
		}
		if len(updatedDiscoveredDevices) > len(oldDevices) {
			t.Log("discovered device list updated with newly added device")
			return true, nil
		}
		t.Log("waiting for continous discovery to discover newly added device")
		return false, nil
	})

	return waitErr
}

func verifyLocalVolumeDiscoveryResult(t *testing.T, lvdr *localv1alpha1.LocalVolumeDiscoveryResult, nodeName string,
	client framework.FrameworkClient) ([]v1alpha1.DiscoveredDevice, error) {
	discoveredDevices := []v1alpha1.DiscoveredDevice{}
	waitErr := wait.PollImmediate(retryInterval, timeout, func() (bool, error) {
		objectKey := dynclient.ObjectKey{
			Namespace: lvdr.Namespace,
			Name:      lvdr.Name,
		}

		err := client.Get(goctx.TODO(), objectKey, lvdr)
		if err != nil {
			if apierrors.IsNotFound(err) {
				t.Logf("LocalVolumeDiscoveryResult %q not found.", lvdr.Name)
				return false, nil
			}
			return false, err
		}

		if lvdr.Spec.NodeName != nodeName {
			return false, fmt.Errorf("invalid node name in spec. expected: %q. actual: %q", nodeName, lvdr.Spec.NodeName)
		}

		if lvdr.Labels["discovery-result-node"] != nodeName {
			return false, fmt.Errorf("failed to apply correct node name label")
		}

		if len(lvdr.Status.DiscoveredDevices) == 0 {
			t.Errorf("waiting for discovered devices to be populated")
			return false, nil
		}

		discoveredDevices = lvdr.Status.DiscoveredDevices
		return true, nil
	})

	return discoveredDevices, waitErr
}
