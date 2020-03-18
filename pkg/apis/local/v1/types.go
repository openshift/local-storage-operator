package v1

import (
	operatorv1 "github.com/openshift/api/operator/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LocalVolumeList returns list of local storage configurations
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type LocalVolumeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []LocalVolume `json:"items"`
}

// LocalVolume is a local storage configuration used by the operator
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type LocalVolume struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Spec              LocalVolumeSpec   `json:"spec"`
	Status            LocalVolumeStatus `json:"status,omitempty"`
}

// LocalVolumeSpec returns spec of configuration
type LocalVolumeSpec struct {
	// managementState indicates whether and how the operator should manage the component
	// +optional
	ManagementState operatorv1.ManagementState `json:"managementState,omitempty"`
	// logLevel is an intent based logging for an overall component.  It does not give fine grained control, but it is a
	// simple way to manage coarse grained logging choices that operators have to interpret for their operands.
	// +optional
	LogLevel operatorv1.LogLevel `json:"logLevel,omitempty"`
	// Nodes on which the provisoner must run
	// +optional
	NodeSelector *corev1.NodeSelector `json:"nodeSelector,omitempty"`
	// List of storage class and devices they can match
	StorageClassDevices []StorageClassDevice `json:"storageClassDevices,omitempty"`
	// If specified, a list of tolerations to pass to the diskmaker and provisioner DaemonSets.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
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
	// + optional
	VolumeMode PersistentVolumeMode `json:"volumeMode,omitempty"`
	// File system type
	// +optional
	FSType string `json:"fsType,omitempty"`
	// A list of device paths which would be chosen for local storage.
	// For example - ["/dev/sda", "/dev/sdb", "/dev/disk/by-id/ata-crucial"]
	DevicePaths []string `json:"devicePaths,omitempty"`
}

// LocalVolumeStatus holds the status of the LocalVolume
type LocalVolumeStatus struct {
	// ObservedGeneration is the last generation of this object that
	// the operator has acted on.
	ObservedGeneration *int64 `json:"observedGeneration,omitempty"`

	// state indicates what the operator has observed to be its current operational status.
	State operatorv1.ManagementState `json:"managementState,omitempty"`

	// Conditions is a list of conditions and their status.
	Conditions []operatorv1.OperatorCondition `json:"conditions,omitempty"`

	// readyReplicas indicates how many replicas are ready and at the desired state
	ReadyReplicas int32 `json:"readyReplicas"`

	// generations are used to determine when an item needs to be reconciled or has changed in a way that needs a reaction.
	// +optional
	Generations []operatorv1.GenerationStatus `json:"generations,omitempty"`
}

// SetDefaults sets values of log level and manage levels
func (local *LocalVolume) SetDefaults() {
	if len(local.Spec.LogLevel) == 0 {
		local.Spec.LogLevel = operatorv1.Normal
	}

	if len(local.Spec.ManagementState) == 0 {
		local.Spec.ManagementState = operatorv1.Managed
	}
}

// LocalVolumeGroupList returns list of
type LocalVolumeGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []LocalVolumeGroup `json:"items"`
}

// LocalVolumeGroup holds the auto provisioning configuration for LSO
type LocalVolumeGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Spec              LocalVolumeGroupSpec   `json:"spec"`
	Status            LocalVolumeGroupStatus `json:"status,omitempty"`
}

// LocalVolumeGroupSpec holds the spec of LocalVolumeGroup CR
type LocalVolumeGroupSpec struct {
	// Nodes on which the automatic detection policies must run.
	// +optional
	NodeSelector *corev1.NodeSelector `json:"nodeSelector,omitempty"`
	// StorageClassName to use for set of matched devices
	StorageClassName string `json:"storageClassName"`
	// MinDeviceCount is the minumum number of devices that needs to be detected per node.
	// If the match devices are less than the minCount specified then no PVs will be created.
	// +optional
	MinDeviceCount *int32 `json:"minDeviceCount"`
	// Maximum number of Devices that needs to be detected per node.
	// +optional
	MaxDeviceCount *int32 `json:"maxDeviceCount"`
	// VolumeMode determines whether the PV created is Block or Filesystem. By default it will
	// be block
	// + optional
	VolumeMode PersistentVolumeMode `json:"volumeMode,omitempty"`
	// FSType type to create when volumeMode is Filesystem
	// +optional
	FSType string `json:"fsType,omitempty"`
	// If specified, a list of tolerations to pass to the discovery daemons.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
	// DeviceInclusionSpec is the filtration rule for including a device in the device discovery
	// +optional
	DeviceInclusionSpec *DeviceInclusionSpec `json:"deviceInclusionSpec"`
}

// DeviceMechanicalProperty holds the device's mechanical spec. It can be rotational or nonRotational
type DeviceMechanicalProperty string

// The mechanical properties of the devices
const (
	// Rotational refers to magnetic disks
	Rotational DeviceMechanicalProperty = "Rotational"
	// NonRotational refers to ssds
	NonRotational DeviceMechanicalProperty = "NonRotational"
)

// DeviceType represents the disk type
type DeviceType string

// The DeviceTypes that will be supported by the LSO.
const (
	// Disk represents a device-type of disk
	Disk DeviceType = "Disk"
	// Part represents a device-type of partion
	Partition DeviceType = "Partition"
)

// DeviceInclusionSpec holds filters of devices that can be included during detection
type DeviceInclusionSpec struct {
	// Devices is the list of devices that should be used for automatic detection.
	// This would be one of the types supported by the local-storage operator. Currently,
	// the supported types are: disk, part. If the list is empty no devices will be selected.
	Devices []DeviceType `json:"devices"`

	// DeviceMechanicalProperty denotes whether Rotational or NonRotational disks should be used.
	// by default, it selects both
	// +optional
	DeviceMechanicalProperties []DeviceMechanicalProperty `json:"deviceMechanicalProperties"`

	// MinSize is the minimum size of the device which needs to be included
	// +optional
	MinSize *resource.Quantity `json:"minSize"`

	// MaxSize is the maximum size of the device which needs to be included
	// +optional
	MaxSize *resource.Quantity `json:"maxSize"`

	// Models is a list of device models. If not empty, the device's model as outputted by lsblk needs
	// to contain at least one of these strings.
	// +optional
	Models []string `json:"models"`

	// Vendors is a list of device vendors. If not empty, the device's model as outputted by lsblk needs
	// to contain at least one of these strings.
	// +optional
	Vendors []string `json:"vendors"`
}

// LocalVolumeGroupStatus holds the status of LocalVolumeGroup
type LocalVolumeGroupStatus struct {
	// Conditions is a list of conditions and their status.
	Conditions []operatorv1.OperatorCondition `json:"conditions,omitempty"`

	// TotalProvisionedDeviceCount is the count of the total devices over which the PVs has been provisioned
	TotalProvisionedDeviceCount *int32 `json:"totalProvisionedDeviceCount,omitempty"`

	// observedGeneration is the last generation change the operator has dealt with
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}
