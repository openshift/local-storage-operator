package v1alpha1

import (
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
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
	StorageClassDevices []StorageClassDevice `json:"storageClassDevices,omitempty"`
	// Version of external local provisioner to use
	LocalProvisionerImageVersion
	// DiskMakerImage version
	DiskMakerImageVersion
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
	// StorageClass name to use for set of matched devices
	StorageClassName string `json:"storageClassName"`
	// Volume mode. Raw or with file system
	VolumeMode PersistentVolumeMode `json:"volumeMode"`
	// File system type
	FSType string `json:"fsType"`
	// A list of devices which would be chosen for local storage.
	// For example - ["/dev/sda", "/dev/sdb"]
	// Alternately deviceIDs can be also used to selecting
	// devices which should be considered for local provisioning.
	DeviceNames []string `json:"deviceNames,omitempty"`
	// A list of unique device names taken from /dev/disk/by-uuid/*
	// For example - ["/dev/disk/by-uuid/12a022e6-13f5-4510-95b5-7bea2a312d3f"]
	// Either DeviceNames or DeviceUUIDs must be specified while defining
	// StorageClassDevice but not both.
	DeviceUUIDs []string `json:"deviceUUIDs,omitempty"`
}

type LocalProvisionerImageVersion struct {
	ProvisionerImage string `json:"provisionerImage,omitempty"`
}

type DiskMakerImageVersion struct {
	DiskMakerImage string `json:"diskMakerImage,omitempty"`
}
type LocalStorageProviderStatus struct {
	// ObservedGeneration is the last generation of this object that
	// the operator has acted on.
	ObservedGeneration *int64 `json:"observedGeneration,omitempty"`

	// Generation of API objects that the operator has created / updated.
	// For internal operator bookkeeping purposes.
	Children []operatorv1alpha1.GenerationHistory `json:"children,omitempty"`

	// state indicates what the operator has observed to be its current operational status.
	State operatorv1alpha1.ManagementState `json:"state,omitempty"`

	// Conditions is a list of conditions and their status.
	Conditions []operatorv1alpha1.OperatorCondition
}
