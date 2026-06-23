package e2e

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/local-storage-operator/api/v1alpha1"
	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	framework "github.com/openshift/local-storage-operator/test/framework"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	dynclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("LocalVolumeDiscovery", Label("LocalVolumeDiscovery"), Ordered, func() {
	var (
		f                    *framework.Framework
		namespace            string
		selectedNode         corev1.Node
		localVolumeDiscovery *localv1alpha1.LocalVolumeDiscovery
		discoveredDevices    []v1alpha1.DiscoveredDevice
	)

	BeforeAll(func() {
		f = framework.Global
		namespace = f.OperatorNamespace

		selectedNode = selectNode(f.KubeClient)
		originalNodeTaints := selectedNode.Spec.Taints
		selectedNode.Spec.Taints = []corev1.Taint{
			{
				Key:    "localstorage",
				Value:  "testvalue",
				Effect: "NoSchedule",
			},
		}
		var err error
		selectedNode, err = waitForNodeTaintUpdate(f.KubeClient, selectedNode, retryInterval, timeout)
		Expect(err).NotTo(HaveOccurred(), "tainting node")

		DeferCleanup(func() {
			selectedNode.Spec.Taints = originalNodeTaints
			_, err := waitForNodeTaintUpdate(f.KubeClient, selectedNode, retryInterval, timeout)
			if err != nil {
				f.Logf("error restoring original taints on node: %v", err)
			}
		})

		localVolumeDiscovery = getFakeLocalVolumeDiscovery(selectedNode, namespace)
		err = f.Client.Create(context.TODO(), localVolumeDiscovery, nil)
		Expect(err).NotTo(HaveOccurred(), "creating localvolumediscovery cr")

		DeferCleanup(func() {
			deleteResource(localVolumeDiscovery, localVolumeDiscovery.Namespace, localVolumeDiscovery.Name, f.Client)
		})
	})

	It("discovers local volumes on tainted node", func() {
		err := waitForDaemonSet(f.KubeClient, namespace, "diskmaker-discovery", retryInterval, timeout)
		Expect(err).NotTo(HaveOccurred(), "waiting for diskmaker-discovery daemonset")

		err = verifyLocalVolumeDiscovery(localVolumeDiscovery, f.Client)
		Expect(err).NotTo(HaveOccurred(), "verifying localvolumediscovery cr")

		localVolumeDiscoveryResult := &localv1alpha1.LocalVolumeDiscoveryResult{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("discovery-result-%s", selectedNode.Name),
				Namespace: namespace,
			},
		}

		var verifyErr error
		discoveredDevices, verifyErr = verifyLocalVolumeDiscoveryResult(localVolumeDiscoveryResult, selectedNode.Name, f.Client)
		Expect(verifyErr).NotTo(HaveOccurred(), "verifying localvolumediscoveryresult")
	})

	It("discovers new devices via continuous discovery", func() {
		f.Logf("getting AWS region info from node spec")
		_, region, _, err := getAWSNodeInfo(selectedNode)
		Expect(err).NotTo(HaveOccurred(), "getAWSNodeInfo")

		f.Logf("initializing ec2 creds")
		ec2Client, err := getEC2Client(region)
		Expect(err).NotTo(HaveOccurred(), "getEC2Client")

		DeferCleanup(func() error {
			return cleanupAWSDisks(ec2Client)
		})

		f.Logf("creating and attaching disks")
		nodeEnv := []nodeDisks{
			{
				disks: []disk{{size: 10}},
				node:  selectedNode,
			},
		}
		createAndAttachAWSVolumes(ec2Client, namespace, nodeEnv)

		localVolumeDiscoveryResult := &localv1alpha1.LocalVolumeDiscoveryResult{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("discovery-result-%s", selectedNode.Name),
				Namespace: namespace,
			},
		}

		err = verifyContinousDiscovery(f.Client, localVolumeDiscoveryResult, discoveredDevices, selectedNode.Name)
		Expect(err).NotTo(HaveOccurred(), "running continuous discovery")
	})

	It("reports correct discovery status", func() {
		err := checkLocalVolumeDiscoveryStatus(localVolumeDiscovery)
		Expect(err).NotTo(HaveOccurred(), "checking localvolumediscovery status")
	})
})

func getFakeLocalVolumeDiscovery(selectedNode corev1.Node, namespace string) *localv1alpha1.LocalVolumeDiscovery {
	return &localv1alpha1.LocalVolumeDiscovery{
		TypeMeta: metav1.TypeMeta{
			Kind:       "LocalVolumeDiscovery",
			APIVersion: localv1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "auto-discover-devices",
			Namespace: namespace,
		},
		Spec: localv1alpha1.LocalVolumeDiscoverySpec{
			NodeSelector: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{
					{
						MatchFields: []corev1.NodeSelectorRequirement{
							{Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{selectedNode.Name}},
						},
					},
				},
			},
			Tolerations: []corev1.Toleration{
				{
					Key:      "localstorage",
					Value:    "testvalue",
					Operator: "Equal",
				},
			},
		},
	}
}

func checkLocalVolumeDiscoveryStatus(lvd *localv1alpha1.LocalVolumeDiscovery) error {
	localVolumeDiscoveryPhase := lvd.Status.Phase
	if localVolumeDiscoveryPhase != localv1alpha1.Discovering {
		return fmt.Errorf("expected local volume discovery to be in discovering phase")
	}
	return nil
}

func verifyLocalVolumeDiscovery(lvd *localv1alpha1.LocalVolumeDiscovery, client framework.FrameworkClient) error {
	return wait.PollImmediate(retryInterval, timeout, func() (bool, error) {
		objectKey := dynclient.ObjectKey{
			Namespace: lvd.Namespace,
			Name:      lvd.Name,
		}
		err := client.Get(context.TODO(), objectKey, lvd)
		if err != nil {
			return false, err
		}
		return true, nil
	})
}

func verifyContinousDiscovery(client framework.FrameworkClient, lvdr *localv1alpha1.LocalVolumeDiscoveryResult,
	oldDevices []v1alpha1.DiscoveredDevice, nodeName string) error {
	f := framework.Global
	return wait.Poll(retryInterval, timeout, func() (bool, error) {
		updatedDiscoveredDevices, err := verifyLocalVolumeDiscoveryResult(lvdr, nodeName, client)
		if err != nil {
			return false, fmt.Errorf("error fetching discovered devices from localvolumediscoveryresult: %v", err)
		}
		if len(updatedDiscoveredDevices) > len(oldDevices) {
			f.Logf("discovered device list updated with newly added device")
			return true, nil
		}
		f.Logf("waiting for continuous discovery to discover newly added device")
		return false, nil
	})
}

func verifyLocalVolumeDiscoveryResult(lvdr *localv1alpha1.LocalVolumeDiscoveryResult, nodeName string,
	client framework.FrameworkClient) ([]v1alpha1.DiscoveredDevice, error) {
	f := framework.Global
	discoveredDevices := []v1alpha1.DiscoveredDevice{}
	waitErr := wait.PollImmediate(retryInterval, timeout, func() (bool, error) {
		objectKey := dynclient.ObjectKey{
			Namespace: lvdr.Namespace,
			Name:      lvdr.Name,
		}

		err := client.Get(context.TODO(), objectKey, lvdr)
		if err != nil {
			if apierrors.IsNotFound(err) {
				f.Logf("LocalVolumeDiscoveryResult %q not found", lvdr.Name)
				return false, nil
			}
			return false, err
		}

		if lvdr.Spec.NodeName != nodeName {
			return false, fmt.Errorf("invalid node name in spec. expected: %q actual: %q", nodeName, lvdr.Spec.NodeName)
		}

		if lvdr.Labels["discovery-result-node"] != nodeName {
			return false, fmt.Errorf("failed to apply correct node name label")
		}

		if len(lvdr.Status.DiscoveredDevices) == 0 {
			f.Logf("waiting for discovered devices to be populated")
			return false, nil
		}

		discoveredDevices = lvdr.Status.DiscoveredDevices
		return true, nil
	})

	return discoveredDevices, waitErr
}
