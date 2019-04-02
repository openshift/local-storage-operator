package v1alpha1

import (
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	defaultDiskMakerImageVersion = "registry.svc.ci.openshift.org/openshift/origin-v4.0:local-storage-diskmaker"
	defaultProvisionImage        = "quay.io/external_storage/local-volume-provisioner:v2.3.0"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
//  LocalVolumeList returns list of local storage configurations
type LocalVolumeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []LocalVolume `json:"items"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// LocalVolume is a local storage configuration used by the operator
type LocalVolume struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Spec              LocalVolumeSpec   `json:"spec"`
	Status            LocalVolumeStatus `json:"status,omitempty"`
}

// LocalVolumeSpec returns spec of configuration
type LocalVolumeSpec struct {
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
	// A list of unique device names taken from /dev/disk/by-id/*
	// For example - ["/dev/disk/by-id/ata-SanDisk_SD7SB7S512G1001_163057401172"]
	// Either DeviceNames or DevicIDs must be specified while defining
	// StorageClassDevice but not both.
	DeviceIDs []string `json:"deviceIDs,omitempty"`
}

type LocalProvisionerImageVersion struct {
	ProvisionerImage string `json:"provisionerImage,omitempty"`
}

type DiskMakerImageVersion struct {
	DiskMakerImage string `json:"diskMakerImage,omitempty"`
}
type LocalVolumeStatus struct {
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

// SetDefaults sets image defaults
func (local *LocalVolume) SetDefaults() {
	if len(local.Spec.DiskMakerImageVersion.DiskMakerImage) == 0 {
		local.Spec.DiskMakerImageVersion = DiskMakerImageVersion{defaultDiskMakerImageVersion}
	}

	if len(local.Spec.LocalProvisionerImageVersion.ProvisionerImage) == 0 {
		local.Spec.LocalProvisionerImageVersion = LocalProvisionerImageVersion{defaultProvisionImage}
	}
}
