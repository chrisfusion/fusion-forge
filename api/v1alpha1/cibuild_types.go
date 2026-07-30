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

// AppSourceSpec specifies the git repository and app-specific fields for an app build.
type AppSourceSpec struct {
	// URL is the HTTPS git clone URL of the repository.
	URL string `json:"url"`

	// Ref is the branch or tag to check out. Defaults to "main".
	// +optional
	Ref string `json:"ref,omitempty"`

	// ProjectDir is an optional relative path to the app project within the repository.
	// +optional
	ProjectDir string `json:"projectDir,omitempty"`

	// BaseDependencies is the optional URL of a base venvpack to layer project requirements on top of.
	// When empty the venv is built from scratch using the project's requirements.txt.
	// +optional
	BaseDependencies string `json:"baseDependencies,omitempty"`

	// FileUploadMode controls which loose Python files are uploaded to fusion-index
	// alongside the venv archive. "legacy" (default) requires and uploads exactly
	// main.py. "auto" uploads every top-level *.py file found in the project. "list"
	// uploads exactly the files named in Files. Derived from metadata.yaml's `files`
	// key — see gitutil.AppMetadata.FileUploadMode.
	// +kubebuilder:validation:Enum=legacy;auto;list
	// +optional
	FileUploadMode string `json:"fileUploadMode,omitempty"`

	// Files is the explicit whitelist of filenames to upload. Only used when
	// FileUploadMode is "list".
	// +optional
	Files []string `json:"files,omitempty"`

	// TokenSecretRef, when set, references a Secret+key used to authenticate the
	// builder Job's git clone of a private repository. Never inlined as a
	// literal value on this spec — resolved by the kubelet via the builder
	// container's env valueFrom.
	// +optional
	TokenSecretRef *SecretKeyRef `json:"tokenSecretRef,omitempty"`
}

// GitSourceSpec specifies the git repository to clone for a git build.
type GitSourceSpec struct {
	// URL is the https git clone URL of the repository.
	URL string `json:"url"`

	// Ref is the branch or tag to check out. Defaults to "main".
	// +optional
	Ref string `json:"ref,omitempty"`

	// EntrypointFile is the name of the optional Python file at the project root that
	// acts as the runnable entry point. When set, the file is uploaded to fusion-index
	// as a second artefact alongside the venv archive.
	// +optional
	EntrypointFile string `json:"entrypointFile,omitempty"`

	// ProjectDir is an optional relative path to the Python project within the repository.
	// Use this for monorepos where multiple projects live in separate subdirectories.
	// When set, pyproject.toml, src/, and the entrypoint file are resolved relative to
	// this directory instead of the repository root.
	// +optional
	ProjectDir string `json:"projectDir,omitempty"`

	// TokenSecretRef, when set, references a Secret+key used to authenticate the
	// builder Job's git clone of a private repository. Never inlined as a
	// literal value on this spec — resolved by the kubelet via the builder
	// container's env valueFrom.
	// +optional
	TokenSecretRef *SecretKeyRef `json:"tokenSecretRef,omitempty"`
}

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

	// BuildType identifies the build mode.
	// "requirements" installs from a requirements.txt supplied via ConfigData.
	// "git" clones a repository and builds from pyproject.toml.
	// "app" clones a repository with metadata.yaml + requirements.txt, plus main.py
	// (legacy mode) or one/many loose Python files selected via metadata.yaml's `files` key.
	// +kubebuilder:validation:Enum=requirements;git;app
	// +optional
	BuildType string `json:"buildType,omitempty"`

	// GitSource specifies the repository to clone. Required when BuildType is "git".
	// +optional
	GitSource *GitSourceSpec `json:"gitSource,omitempty"`

	// AppSource specifies the repository and app metadata. Required when BuildType is "app".
	// +optional
	AppSource *AppSourceSpec `json:"appSource,omitempty"`

	// ConfigData holds filename→content pairs mounted as a ConfigMap volume at /workspace.
	// For requirements builds this contains "requirements.txt"; empty for git builds.
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
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.buildType"
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
