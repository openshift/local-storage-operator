/*
Copyright 2026 The Local Storage Operator Authors.

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LocalVolumeDeviceLink establishes a link between block devices on the node
// and PersistentVolumes created by Local Storage Operator. It stores discovered
// symlinks for the device and influences LSO's symlink selection process.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=localvolumedevicelinks,scope=Namespaced
type LocalVolumeDeviceLink struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is the standard object's metadata and has
	// ownerRef set to the LocalVolume or LocalVolumeSet object.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`
	// spec holds user settable values for the device link
	// +required
	Spec LocalVolumeDeviceLinkSpec `json:"spec,omitzero"`
	// status holds observed values for the device link
	// +optional
	Status LocalVolumeDeviceLinkStatus `json:"status,omitzero"`
}

// +kubebuilder:validation:Enum=None;CurrentLinkTarget;PreferredLinkTarget
type LocalVolumeDeviceLinkPolicy string

const (
	// LocalVolumeDeviceLinkPolicyNone means no policy has been selected for
	// the device and LSO generates an alert if there is a mismatch between
	// .status.currentLinkTarget and .status.preferredLinkTarget
	LocalVolumeDeviceLinkPolicyNone = "None" // default
	// LocalVolumeDeviceLinkPolicyCurrentLinkTarget silences the alert and
	// keeps the existing symlink pointing to .status.currentLinkTarget
	LocalVolumeDeviceLinkPolicyCurrentLinkTarget = "CurrentLinkTarget"
	// LocalVolumeDeviceLinkPolicyPreferredLinkTarget silences the alert and
	// changes the symlink to point to .status.preferredLinkTarget
	LocalVolumeDeviceLinkPolicyPreferredLinkTarget = "PreferredLinkTarget"
)

// LocalVolumeDeviceLinkSpec defines the desired state of the device link
// +kubebuilder:validation:MinProperties=1
type LocalVolumeDeviceLinkSpec struct {
	// persistentVolumeName is the name of the persistent volume linked to the device
	// +required
	PersistentVolumeName string `json:"persistentVolumeName"`
	// policy expresses how to manage symlinks for the device.
	// "None" means no policy has been chosen, and will generate an alert if
	// there is a mismatch between .status.currentLinkTarget and
	// .status.preferredLinkTarget.
	// "CurrentLinkTarget" silences the alert and keeps the existing symlink
	// pointing to .status.currentLinkTarget.
	// "PreferredLinkTarget" silences the alert and changes the symlink to point
	// to .status.preferredLinkTarget.
	// The default value is "None".
	// +default="None"
	// +optional
	Policy LocalVolumeDeviceLinkPolicy `json:"policy,omitempty"`
}

// LocalVolumeDeviceLinkStatus stores the observed state of the device link
// +kubebuilder:validation:MinProperties=1
type LocalVolumeDeviceLinkStatus struct {
	// currentLinkTarget is the current by-id symlink used for the device
	// +required
	CurrentLinkTarget string `json:"currentLinkTarget"`
	// preferredLinkTarget is the preferred by-id symlink for the device
	// +required
	PreferredLinkTarget string `json:"preferredLinkTarget"`
	// validLinkTargets is the list of valid by-id symlinks for the device
	// +required
	ValidLinkTargets []string `json:"validLinkTargets"`
	// filesystemUUID is the UUID of the filesystem found on the device (when available)
	// +optional
	FilesystemUUID string `json:"filesystemUUID,omitempty"`
	// conditions is a list of operator conditions
	// +optional
	Conditions []operatorv1.OperatorCondition `json:"conditions,omitempty"`
}

// LocalVolumeDeviceLinkList contains a list of device links
// +kubebuilder:object:root=true
type LocalVolumeDeviceLinkList struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is the standard list's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata
	metav1.ListMeta `json:"metadata"`

	Items []LocalVolumeDeviceLink `json:"items"`
}
