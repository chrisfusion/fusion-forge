// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CIBuildPhase is the lifecycle phase of a CIBuild.
// +kubebuilder:validation:Enum=Pending;Building;Succeeded;Failed
type CIBuildPhase string

const (
	CIBuildPhasePending   CIBuildPhase = "Pending"
	CIBuildPhaseBuilding  CIBuildPhase = "Building"
	CIBuildPhaseSucceeded CIBuildPhase = "Succeeded"
	CIBuildPhaseFailed    CIBuildPhase = "Failed"
)

// CIBuildSpec defines the desired state of a CIBuild.
// ConfigData holds arbitrary filename→content pairs that are mounted as a ConfigMap
// volume at /workspace inside the builder pod. This keeps the spec generic so future
// build types can supply different sets of input files.
type CIBuildSpec struct {
	// BuilderImage is the container image used to execute the build.
	BuilderImage string `json:"builderImage"`

	// IndexBackendURL is the base URL of the fusion-index artifact registry.
	IndexBackendURL string `json:"indexBackendURL"`

	// ArtifactName is the logical name of the artifact being built (for display).
	ArtifactName string `json:"artifactName"`

	// ArtifactVersion is the semver version string of the artifact being built (for display).
	ArtifactVersion string `json:"artifactVersion"`

	// Description is a human-readable description of the artifact.
	// +optional
	Description string `json:"description,omitempty"`

	// ConfigData holds filename→content pairs mounted as a ConfigMap volume at /workspace.
	// For venv builds this contains "requirements.txt".
	ConfigData map[string]string `json:"configData"`

	// Env contains additional environment variables injected into the builder pod.
	// The operator always injects INDEX_BACKEND_URL from spec; Env carries build-specific
	// vars such as ARTIFACT_ID and ARTIFACT_VERSION.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`
}

// CIBuildStatus reflects the live state of a CIBuild.
type CIBuildStatus struct {
	// Phase is the current lifecycle phase.
	// +optional
	Phase CIBuildPhase `json:"phase,omitempty"`

	// JobName is the name of the batch/v1 Job created for this build.
	// +optional
	JobName string `json:"jobName,omitempty"`

	// ConfigMapName is the name of the ConfigMap holding the build inputs.
	// Cleared after the build reaches a terminal phase.
	// +optional
	ConfigMapName string `json:"configMapName,omitempty"`

	// Message holds a human-readable status detail or failure reason.
	// +optional
	Message string `json:"message,omitempty"`

	// StartedAt is when the build Job was first submitted.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// CompletedAt is when the build reached a terminal phase.
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=cib
// +kubebuilder:printcolumn:name="Artifact",type=string,JSONPath=".spec.artifactName"
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=".spec.artifactVersion"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// CIBuild represents an asynchronous artifact build executed as a Kubernetes Job.
type CIBuild struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CIBuildSpec   `json:"spec,omitempty"`
	Status CIBuildStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CIBuildList contains a list of CIBuild.
type CIBuildList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CIBuild `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CIBuild{}, &CIBuildList{})
}
