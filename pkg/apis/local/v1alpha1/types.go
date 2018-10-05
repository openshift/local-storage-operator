package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// LocalStorageProviderList returns list of local storage configurations
type LocalStorageProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []LocalStorageProvider `json:"items"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// LocalStorageProvider is a local storage configuration used by the operator
type LocalStorageProvider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Spec              LocalStorageProviderSpec   `json:"spec"`
	Status            LocalStorageProviderStatus `json:"status,omitempty"`
}

// LocalStorageProviderSpec returns spec of configuration
type LocalStorageProviderSpec struct {
	// Nodes on which the provisoner must run
	NodeSelector *corev1.NodeSelector `json:"nodeSelector,omitempty"`
	// List of storage class and devices they can match
	StorageClassDevices []StorageClassDevice `json:"storageClassDevice,omitempty"`
	// Version of external local provisioner to use
	LocalProvisionerImageVersion
}

// PersistentVolumeMode describes how a volume is intended to be consumed, either Block or Filesystem.
type PersistentVolumeMode string

const (
	// PersistentVolumeBlock means the volume will not be formatted with a filesystem and will remain a raw block device.
	PersistentVolumeBlock PersistentVolumeMode = "Block"
	// PersistentVolumeFilesystem means the volume will be or is formatted with a filesystem.
	PersistentVolumeFilesystem PersistentVolumeMode = "Filesystem"
)

// StorageClassDevice returns device configuration
type StorageClassDevice struct {
	// StorageClass name to use for set of matches devices
	StorageClassName string `json:"storageClassName"`
	// Volume mode. Raw or with file system
	VolumeMode PersistentVolumeMode `json:"volumeMode"`
	// File system type
	FSType string `json:"fsType"`
	// A list of devices which would be chosen for local storage.
	// For example - ["/dev/sda", "/dev/sdb"]
	// Alternately deviceWhitelistPattern can be also used to selecting
	// devices which should be considered for local provisioning.
	DeviceNames []string `json:"deviceNames,omitempty"`
	// A list of patterns that can match one or more devices
	// which can be selected for local storage provisioning.
	// For example - ["/dev/nvme*1", "/dev/xvdb*"]
	DeviceWhitelistPattern []string `json:"deviceWhitelistPattern,omitempty"`
}

type LocalProvisionerImageVersion struct {
	ProvisionerImage string `json:"provisionerImage,omitempty"`
}

type LocalStorageProviderStatus struct {
	ProvisionerRolloutStatuses []ProvisionerRolloutStatus `json:"provisionerRolloutStatuses,omitempty"`
}

type RolloutStatus string

const (
	Completed  RolloutStatus = "Completed"
	Failed     RolloutStatus = "Failed"
	InProgress RolloutStatus = "InProgress"
)

type ProvisionerRolloutStatus struct {
	// StorageClass name to use for set of matches devices
	StorageClassName string        `json:"storageClassName"`
	Status           RolloutStatus `json:"status"`
	Message          string        `json:"message"`
}
