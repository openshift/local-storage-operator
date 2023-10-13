/*
Copyright 2021 The Local Storage Operator Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	operatorv1 "github.com/openshift/api/operator/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LocalVolumeSpec defines the desired state of LocalVolume
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
	// This option will destroy all leftover data on the devices before they're used as PersistentVolumes. Use with care.
	// +optional
	ForceWipeDevicesAndDestroyAllData bool `json:"forceWipeDevicesAndDestroyAllData,omitempty"`
}

// LocalVolumeStatus defines the observed state of LocalVolume
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

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=localvolumes,scope=Namespaced
// LocalVolume is the Schema for the localvolumes API
type LocalVolume struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LocalVolumeSpec   `json:"spec,omitempty"`
	Status LocalVolumeStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// LocalVolumeList contains a list of LocalVolume
type LocalVolumeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LocalVolume `json:"items"`
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

func init() {
	SchemeBuilder.Register(&LocalVolume{}, &LocalVolumeList{})
}
