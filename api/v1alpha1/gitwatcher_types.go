// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// GitWatcherPhase is the lifecycle phase of a GitWatcher.
// +kubebuilder:validation:Enum=Active;Disabled
type GitWatcherPhase string

const (
	GitWatcherPhaseActive   GitWatcherPhase = "Active"
	GitWatcherPhaseDisabled GitWatcherPhase = "Disabled"
)

// SecretKeyRef selects a key from a Kubernetes Secret.
type SecretKeyRef struct {
	// Name is the name of the Secret.
	Name string `json:"name"`
	// Key is the key within the Secret whose value is the token.
	Key string `json:"key"`
}

// GitWatcherSpec defines the desired state of a GitWatcher.
type GitWatcherSpec struct {
	// RepoURL is the HTTPS git clone URL of the repository to watch.
	RepoURL string `json:"repoURL"`

	// RepoRef is the single branch to watch for new commits. Defaults to "main".
	// +optional
	RepoRef string `json:"repoRef,omitempty"`

	// BuildType selects the build pipeline. "git" builds a Python venv from
	// pyproject.toml; "app" builds an application from metadata.yaml.
	// +kubebuilder:validation:Enum=git;app
	BuildType string `json:"buildType"`

	// Enabled controls whether polling is active. Set to false to pause the
	// watcher without deleting it. Defaults to true when omitted.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// TokenSecretRef optionally references a K8s Secret holding a personal
	// access token for private repository access. The token is sent as HTTP
	// Basic Auth with username "oauth2".
	// +optional
	TokenSecretRef *SecretKeyRef `json:"tokenSecretRef,omitempty"`

	// --- git-build fields (ignored for app builds) ---

	// Name is the artifact name. Required when MetadataSource is "manual" or "version".
	// +optional
	Name string `json:"name,omitempty"`

	// MetadataSource controls how name and version are resolved.
	// "manual" — both from this spec; "version" — name from spec, version from
	// pyproject.toml; "full" — both from pyproject.toml.
	// +kubebuilder:validation:Enum=manual;version;full
	// +optional
	MetadataSource string `json:"metadataSource,omitempty"`

	// Version is the artifact version. Only used when MetadataSource is "manual".
	// +optional
	Version string `json:"version,omitempty"`

	// PythonVersion selects the builder image key. Accepted: "3.10", "3.12".
	// Defaults to "3.12". Ignored for app builds.
	// +optional
	PythonVersion string `json:"pythonVersion,omitempty"`

	// EntrypointFile is an optional Python file uploaded alongside the venv archive.
	// Ignored for app builds.
	// +optional
	EntrypointFile string `json:"entrypointFile,omitempty"`

	// ProjectDir is an optional relative path to the project within the repository.
	// Used for monorepos. Applies to both git and app builds.
	// +optional
	ProjectDir string `json:"projectDir,omitempty"`

	// Description is a human-readable description of the artifact.
	// +optional
	Description string `json:"description,omitempty"`
}

// GitWatcherStatus reflects the observed state of a GitWatcher.
type GitWatcherStatus struct {
	// Phase is the current lifecycle phase: Active or Disabled.
	// +optional
	Phase GitWatcherPhase `json:"phase,omitempty"`

	// LastSeenCommit is the HEAD commit SHA the last time the remote was successfully polled.
	// +optional
	LastSeenCommit string `json:"lastSeenCommit,omitempty"`

	// LastBuiltVersion is the artifact version of the most recently successful build.
	// +optional
	LastBuiltVersion string `json:"lastBuiltVersion,omitempty"`

	// LastBuildName is the CIBuild CR name of the currently in-flight build.
	// Empty when no build is pending.
	// +optional
	LastBuildName string `json:"lastBuildName,omitempty"`

	// LastBuildVersion is the artifact version being built by the in-flight build.
	// Set together with LastBuildName and cleared when the build completes.
	// +optional
	LastBuildVersion string `json:"lastBuildVersion,omitempty"`

	// ConsecutiveFailures counts consecutive build failures since the last success.
	// Reset to zero on success; when it reaches the configured max, the watcher is disabled.
	// +optional
	ConsecutiveFailures int `json:"consecutiveFailures,omitempty"`

	// LastCheckedAt is when the remote was last polled.
	// +optional
	LastCheckedAt *metav1.Time `json:"lastCheckedAt,omitempty"`

	// LastError is the most recent error or failure message.
	// +optional
	LastError string `json:"lastError,omitempty"`

	// Message is a human-readable status summary.
	// +optional
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=gw
// +kubebuilder:printcolumn:name="Repo",type=string,JSONPath=".spec.repoURL"
// +kubebuilder:printcolumn:name="Ref",type=string,JSONPath=".spec.repoRef"
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.buildType"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="LastBuilt",type=string,JSONPath=".status.lastBuiltVersion"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// GitWatcher polls a git repository branch and triggers a build whenever a new
// artifact version is detected in pyproject.toml (git builds) or metadata.yaml
// (app builds).
type GitWatcher struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GitWatcherSpec   `json:"spec,omitempty"`
	Status GitWatcherStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GitWatcherList contains a list of GitWatcher.
type GitWatcherList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GitWatcher `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GitWatcher{}, &GitWatcherList{})
}
