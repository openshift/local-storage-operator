package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeviceState defines the observed state of the disk
type DeviceState string

const (
	// Available means that the device is available to use and a new persistent volume can be provisioned on it
	Available DeviceState = "Available"
	// NotAvailable means that the device is already used by some other process and shouldn't be used to provision a Persistent Volume
	NotAvailable DeviceState = "NotAvailable"
	// Unknown means that the state of the device can't be determined
	Unknown DeviceState = "Unknown"
)

// DeviceStatus defines the observed state of the discovered devices
type DeviceStatus struct {
	// State shows the availability of the device
	State DeviceState `json:"state"`
}

// DiscoveredDevice shows the list of discovered devices with their properties
type DiscoveredDevice struct {
	// DeviceID represents the persistent name of the device. For eg, /dev/disk/by-id/...
	DeviceID string `json:"deviceID"`
	// Path represents the device path. For eg, /dev/sdb
	Path string `json:"path"`
	// Model of the discovered device
	Model string `json:"model"`
	// Type of the discovered device
	Type DeviceType `json:"type"`
	// Vendor of the discovered device
	Vendor string `json:"vendor"`
	// Serial number of the disk
	Serial string `json:"serial"`
	// Size of the discovered device
	Size int64 `json:"size"`
	// Property represents whether the device type is rotational or not
	Property DeviceMechanicalProperty `json:"property"`
	// FSType represents the filesystem available on the device
	FSType string `json:"fstype"`
	// Status defines whether the device is available for use or not
	Status DeviceStatus `json:"status"`
}

// LocalVolumeDiscoveryResultSpec defines the desired state of LocalVolumeDiscoveryResult
type LocalVolumeDiscoveryResultSpec struct {
	// Node on which the devices are discovered
	NodeName string `json:"nodeName"`
}

// LocalVolumeDiscoveryResultStatus defines the observed state of LocalVolumeDiscoveryResult
type LocalVolumeDiscoveryResultStatus struct {
	// DiscoveredTimeStamp is the last timestamp when the list of discovered devices was updated
	DiscoveredTimeStamp string `json:"discoveredTimeStamp,omitempty"`
	// DiscoveredDevices contains the list of devices on which LSO
	// is capable of creating LocalPVs
	// The devices in this list qualify these following conditions.
	// - it should be a non-removable device.
	// - it should not be a read-only device.
	// - it should not be mounted anywhere
	// - it should not be a boot device
	// - it should not have child partitions
	// +optional
	DiscoveredDevices []DiscoveredDevice `json:"discoveredDevices"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LocalVolumeDiscoveryResult is the Schema for the localvolumediscoveryresults API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=localvolumediscoveryresults,scope=Namespaced
type LocalVolumeDiscoveryResult struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LocalVolumeDiscoveryResultSpec   `json:"spec,omitempty"`
	Status LocalVolumeDiscoveryResultStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LocalVolumeDiscoveryResultList contains a list of LocalVolumeDiscoveryResult
type LocalVolumeDiscoveryResultList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LocalVolumeDiscoveryResult `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LocalVolumeDiscoveryResult{}, &LocalVolumeDiscoveryResultList{})
}
