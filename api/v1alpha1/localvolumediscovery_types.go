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

package v1alpha1

import (
	operatorv1 "github.com/openshift/api/operator/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DiscoveryPhase defines the observed phase of the discovery process
type DiscoveryPhase string

// Different phases of the discovery process
const (
	// Discovering represents that the continuous discovery of devices is in progress
	Discovering DiscoveryPhase = "Discovering"
	// DiscoveryFailed represents that the discovery process has failed
	DiscoveryFailed DiscoveryPhase = "DiscoveryFailed"
)

// DiscoveredDeviceType is the types that will be discovered by the LSO.
type DiscoveredDeviceType string

const (
	// DiskType represents a device-type of block disk
	DiskType DiscoveredDeviceType = "disk"
	// PartType represents a device-type of partition
	PartType DiscoveredDeviceType = "part"
	// LVMType is an LVM type
	LVMType DiscoveredDeviceType = "lvm"
	// MultiPathType is a multipath type
	MultiPathType DiscoveredDeviceType = "mpath"
)

// LocalVolumeDiscoverySpec defines the desired state of LocalVolumeDiscovery
type LocalVolumeDiscoverySpec struct {
	// Nodes on which the automatic detection policies must run.
	// +optional
	NodeSelector *corev1.NodeSelector `json:"nodeSelector,omitempty"`
	// If specified tolerations is the list of toleration that is passed to the
	// LocalVolumeDiscovery Daemon
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
}

// LocalVolumeDiscoveryStatus defines the observed state of LocalVolumeDiscovery
type LocalVolumeDiscoveryStatus struct {
	// Phase represents the current phase of discovery process
	// This is used by the OLM UI to provide status information
	// to the user
	Phase DiscoveryPhase `json:"phase,omitempty"`
	// Conditions are the list of conditions and their status.
	Conditions []operatorv1.OperatorCondition `json:"conditions,omitempty"`
	// observedGeneration is the last generation change the operator has dealt with
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=localvolumediscoveries,scope=Namespaced
// LocalVolumeDiscovery is the Schema for the localvolumediscoveries API
type LocalVolumeDiscovery struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LocalVolumeDiscoverySpec   `json:"spec,omitempty"`
	Status LocalVolumeDiscoveryStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// LocalVolumeDiscoveryList contains a list of LocalVolumeDiscovery
type LocalVolumeDiscoveryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LocalVolumeDiscovery `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LocalVolumeDiscovery{}, &LocalVolumeDiscoveryList{})
}
