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

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LocalVolumeDiscovery is the Schema for the localvolumediscoveries API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=localvolumediscoveries,scope=Namespaced
type LocalVolumeDiscovery struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LocalVolumeDiscoverySpec   `json:"spec,omitempty"`
	Status LocalVolumeDiscoveryStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LocalVolumeDiscoveryList contains a list of LocalVolumeDiscovery
type LocalVolumeDiscoveryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LocalVolumeDiscovery `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LocalVolumeDiscovery{}, &LocalVolumeDiscoveryList{})
}
