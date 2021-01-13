package v1alpha1

import (
	operatorv1 "github.com/openshift/api/operator/v1"
	localv1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeviceMechanicalProperty holds the device's mechanical spec. It can be rotational or nonRotational
type DeviceMechanicalProperty string

// The mechanical properties of the devices
const (
	// Rotational refers to magnetic disks
	Rotational DeviceMechanicalProperty = "Rotational"
	// NonRotational refers to ssds
	NonRotational DeviceMechanicalProperty = "NonRotational"
)

// DeviceType is the types that will be supported by the LSO.
type DeviceType string

const (
	// RawDisk represents a device-type of block disk
	RawDisk DeviceType = "disk"
	// Partition represents a device-type of partition
	Partition DeviceType = "part"
	// Loop type device
	Loop DeviceType = "loop"
)

// DeviceInclusionSpec holds the inclusion filter spec
type DeviceInclusionSpec struct {
	// Devices is the list of devices that should be used for automatic detection.
	// This would be one of the types supported by the local-storage operator. Currently,
	// the supported types are: disk, part. If the list is empty only `disk` types will be selected
	// +optional
	DeviceTypes []DeviceType `json:"deviceTypes,omitempty"`
	// DeviceMechanicalProperty denotes whether Rotational or NonRotational disks should be used.
	// by default, it selects both
	// +optional
	DeviceMechanicalProperties []DeviceMechanicalProperty `json:"deviceMechanicalProperties,omitempty"`
	// MinSize is the minimum size of the device which needs to be included. Defaults to `1Gi` if empty
	// +optional
	MinSize *resource.Quantity `json:"minSize,omitempty"`
	// MaxSize is the maximum size of the device which needs to be included
	// +optional
	MaxSize *resource.Quantity `json:"maxSize,omitempty"`
	// Models is a list of device models. If not empty, the device's model as outputted by lsblk needs
	// to contain at least one of these strings.
	// +optional
	Models []string `json:"models,omitempty"`
	// Vendors is a list of device vendors. If not empty, the device's model as outputted by lsblk needs
	// to contain at least one of these strings.
	// +optional
	Vendors []string `json:"vendors,omitempty"`
}

// LocalVolumeSetSpec defines the desired state of LocalVolumeSet
type LocalVolumeSetSpec struct {
	// Nodes on which the automatic detection policies must run.
	// +optional
	NodeSelector *corev1.NodeSelector `json:"nodeSelector,omitempty"`
	// StorageClassName to use for set of matched devices
	StorageClassName string `json:"storageClassName"`
	// MaxDeviceCount is the maximum number of Devices that needs to be detected per node.
	// If it is not specified, there will be no limit to the number of provisioned devices.
	// +optional
	MaxDeviceCount *int32 `json:"maxDeviceCount,omitempty"`
	// VolumeMode determines whether the PV created is Block or Filesystem.
	// It will default to Filesystem.
	// +optional
	VolumeMode localv1.PersistentVolumeMode `json:"volumeMode,omitempty"`
	// FSType type to create when volumeMode is Filesystem
	// +optional
	FSType string `json:"fsType,omitempty"`
	// If specified, a list of tolerations to pass to the discovery daemons.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
	// DeviceInclusionSpec is the filtration rule for including a device in the device discovery
	// +optional
	DeviceInclusionSpec *DeviceInclusionSpec `json:"deviceInclusionSpec,omitempty"`
}

// LocalVolumeSetStatus defines the observed state of LocalVolumeSet
type LocalVolumeSetStatus struct {
	// Conditions is a list of conditions and their status.
	Conditions []operatorv1.OperatorCondition `json:"conditions,omitempty"`
	// TotalProvisionedDeviceCount is the count of the total devices over which the PVs has been provisioned
	TotalProvisionedDeviceCount *int32 `json:"totalProvisionedDeviceCount,omitempty"`
	// observedGeneration is the last generation change the operator has dealt with
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LocalVolumeSet is the Schema for the localvolumesets API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=localvolumesets,scope=Namespaced
type LocalVolumeSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LocalVolumeSetSpec   `json:"spec,omitempty"`
	Status LocalVolumeSetStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LocalVolumeSetList contains a list of LocalVolumeSet
type LocalVolumeSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LocalVolumeSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LocalVolumeSet{}, &LocalVolumeSetList{})
}
