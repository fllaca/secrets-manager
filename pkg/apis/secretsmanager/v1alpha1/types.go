package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SecretDefinition defines how to generate a secret in K8s from remote secrets backends
type SecretDefinition struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SecretDefinitionSpec   `json:"spec"`
	Status SecretDefinitionStatus `json:"status"`
}

// SecretDefinitionSpec is the spec for a SecretDefinition resource
type SecretDefinitionSpec struct {
	// Name for the secret in K8s
	Name string `yaml:"name"`
	// Namespaces is the list of namespaces where the secret is going to be created
	Namespaces []string `yaml:"namespaces"`
	// Type is the type of K8s Secret ("Opaque", "kubernetes.io/tls", ...)
	Type string `yaml:"type"`
	// Data is a dictionary which keys are the name of each entry in the K8s Secret data and the value is
	// the Datasource (from backend) for that entry
	Data map[string]DatasourceSpec `yaml:"data"` //optional?
}

// DatasourceSpec represents a reference to a secret in a backend (source of truth)
type DatasourceSpec struct {
	// Path to a secret in a secret backend
	Path string `yaml:"path"`
	// Key in the secret in the backend
	Key string `yaml:"key"`
	// Encoding type for the secret. Only base64 supported. Optional
	Encoding string `yaml:"encoding,omitempty"`
}

// SecretDefinitionStatus is the status for a SecretDefinition resource
type SecretDefinitionStatus struct {
	Synced bool `json:"synced"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SecretDefinitionList is a list of SecretDefinition resources
type SecretDefinitionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []SecretDefinition `json:"items"`
}
