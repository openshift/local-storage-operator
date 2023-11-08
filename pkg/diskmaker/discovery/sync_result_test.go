package discovery

import (
	"fmt"
	"testing"

	"github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/diskmaker"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEnsureDiscoveryResult(t *testing.T) {
	dd := getFakeDeviceDiscovery()
	setEnv()
	defer unsetEnv()
	err := dd.ensureDiscoveryResultCR()
	assert.NoError(t, err)
}

func TestEnsureDiscoveryResultNoEnv(t *testing.T) {
	// failed to ensure discovery result due to missing env variables.
	dd := getFakeDeviceDiscovery()
	err := dd.ensureDiscoveryResultCR()
	assert.Error(t, err)
}

func TestEnsureDiscoveryResultFail(t *testing.T) {
	// failed to get existing discovery result object
	mockClient := &diskmaker.MockAPIUpdater{
		MockGetDiscoveryResult: func(name, namespace string) (*v1alpha1.LocalVolumeDiscoveryResult, error) {
			return nil, fmt.Errorf("failed to get result object")
		},
	}

	dd := getFakeDeviceDiscovery()
	dd.apiClient = mockClient
	setEnv()
	defer unsetEnv()
	err := dd.ensureDiscoveryResultCR()
	assert.Error(t, err)
	assert.EqualError(t, err, "failed to get result object")
}

func TestUpdateStatus(t *testing.T) {
	dd := getFakeDeviceDiscovery()
	setEnv()
	defer unsetEnv()
	err := dd.updateStatus()
	assert.NoError(t, err)
}

func TestUpdateStatusFail(t *testing.T) {
	// failed to get discovery result
	mockClient := &diskmaker.MockAPIUpdater{
		MockGetDiscoveryResult: func(name, namespace string) (*v1alpha1.LocalVolumeDiscoveryResult, error) {
			return nil, fmt.Errorf("failed to get result object")
		},
	}
	dd := getFakeDeviceDiscovery()
	dd.apiClient = mockClient
	setEnv()
	defer unsetEnv()
	err := dd.updateStatus()
	assert.Error(t, err)

	// failed to update discovery result status
	mockClient = &diskmaker.MockAPIUpdater{
		MockUpdateDiscoveryResultStatus: func(lvdr *v1alpha1.LocalVolumeDiscoveryResult) error {
			return fmt.Errorf("failed to update status")
		},
	}
	dd = getFakeDeviceDiscovery()
	dd.apiClient = mockClient
	setEnv()
	defer unsetEnv()
	err = dd.updateStatus()
	assert.Error(t, err)
}

func TestNewDiscoveryResultInstance(t *testing.T) {
	testCases := []struct {
		label            string
		nodeName         string
		namespace        string
		parentObjectName string
		parentObjectUID  string
		expected         v1alpha1.LocalVolumeDiscoveryResult
	}{
		{
			label:            "Case 1: node name less than 253 characters",
			nodeName:         "node1",
			namespace:        "local-storage",
			parentObjectName: "diskmaker-discvoery-123",
			parentObjectUID:  "f288b336-434e-4939-b742-9d8fd232a56c",
			expected: v1alpha1.LocalVolumeDiscoveryResult{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "discovery-result-node1",
					Namespace: "local-storage",
					Labels:    map[string]string{"discovery-result-node": "node1"},
					OwnerReferences: []metav1.OwnerReference{
						{
							Name: "diskmaker-discvoery-123",
							UID:  "f288b336-434e-4939-b742-9d8fd232a56c",
						},
					},
				},
				Spec: v1alpha1.LocalVolumeDiscoveryResultSpec{
					NodeName: "node1",
				},
			},
		},
		{
			label:            "Case 2: node name greater than 253 characters",
			nodeName:         "192.168.1.27.ec2.internal.node-name-greater-than-253-characters-1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890",
			namespace:        "default",
			parentObjectName: "diskmaker-discvoery-456",
			parentObjectUID:  "f288b336-434e-4939-b742-9d8fd232a56c",
			expected: v1alpha1.LocalVolumeDiscoveryResult{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "discovery-result-d57ec549800941f89ed17bbfcd013459",
					Namespace: "default",
					Labels:    map[string]string{"discovery-result-node": "192.168.1.27.ec2.internal.node-name-greater-than-253-characters-1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890"},
					OwnerReferences: []metav1.OwnerReference{
						{
							Name: "diskmaker-discvoery-456",
							UID:  "f288b336-434e-4939-b742-9d8fd232a56c",
						},
					},
				},
				Spec: v1alpha1.LocalVolumeDiscoveryResultSpec{
					NodeName: "192.168.1.27.ec2.internal.node-name-greater-than-253-characters-1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890",
				},
			},
		},
	}

	for _, tc := range testCases {
		actual := newDiscoveryResultInstance(tc.nodeName, tc.namespace, tc.parentObjectName, tc.parentObjectUID)
		assert.Equalf(t, tc.expected.Name, *&actual.Name, "[%q] LocalVolumeDiscoveryResult name not set correctly", tc.label)
		assert.Equalf(t, tc.expected.Namespace, *&actual.Namespace, "[%q] LocalVolumeDiscoveryResult namespace not set correctly", tc.label)
		assert.Equalf(t, tc.expected.Labels, *&actual.Labels, "[%q] LocalVolumeDiscoveryResult labels not set correctly", tc.label)
		assert.Equalf(t, tc.expected.Spec.NodeName, *&actual.Spec.NodeName, "[%q] LocalVolumeDiscoveryResult NodeName spec not set correctly", tc.label)
		assert.Equalf(t, tc.expected.ObjectMeta.OwnerReferences[0].Name, *&actual.ObjectMeta.OwnerReferences[0].Name, "[%q] LocalVolumeDiscoveryResult ownerReference name not set correctly", tc.label)
		assert.Equalf(t, tc.expected.ObjectMeta.OwnerReferences[0].UID, *&actual.ObjectMeta.OwnerReferences[0].UID, "[%q] LocalVolumeDiscoveryResult ownerReference UID not set correctly", tc.label)
	}
}

func TestTruncateNodeName(t *testing.T) {
	testcases := []struct {
		label    string
		input    string
		expected string
	}{
		{
			label:    "Case 1: node name is equal to 68 chars",
			input:    "k8s-worker-1234567890.this.is.a.very.very.long.node.name.example.com",
			expected: "discovery-result-k8s-worker-1234567890.this.is.a.very.very.long.node.name.example.com",
		},
		{
			label:    "Case 2: node name is equal to 5 chars",
			input:    "k8s01",
			expected: "discovery-result-k8s01",
		},
		{
			label:    "Case 3: node name is equal to 47 chars",
			input:    "k8s-worker-500.this.is.a.not.so.long.name",
			expected: "discovery-result-k8s-worker-500.this.is.a.not.so.long.name",
		},
		{
			label:    "Case 4: node name is equal to 256 chars",
			input:    "k8s-worker-1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.1234567890.very.very.long.node.name.example.com",
			expected: "discovery-result-5705c7b58bd04799d9ab6aadbde0db3e",
		},
	}

	for _, tc := range testcases {
		actual := truncateNodeName("discovery-result-%s", tc.input)
		assert.Equalf(t, tc.expected, actual, "[%s]: failed to truncate node name", tc.label)
	}
}
