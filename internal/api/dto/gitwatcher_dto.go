// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package dto

import (
	"time"

	buildv1alpha1 "fusion-platform.io/fusion-forge/api/v1alpha1"
)

// SecretKeyRefRequest selects a key from a Kubernetes Secret.
type SecretKeyRefRequest struct {
	Name string `json:"name" binding:"required"`
	Key  string `json:"key" binding:"required"`
}

// CreateGitWatcherRequest is the JSON body for POST /api/v1/gitwatchers.
type CreateGitWatcherRequest struct {
	Name           string               `json:"name" binding:"required,max=253"`
	RepoURL        string               `json:"repo_url" binding:"required,max=2048,url"`
	RepoRef        string               `json:"repo_ref" binding:"max=255"`
	BuildType      string               `json:"build_type" binding:"required,max=32"`
	Enabled        *bool                `json:"enabled"`
	TokenSecretRef *SecretKeyRefRequest `json:"token_secret_ref"`
	ArtifactName   string               `json:"artifact_name" binding:"max=255"`
	MetadataSource string               `json:"metadata_source" binding:"max=32"`
	Version        string               `json:"version" binding:"max=50"`
	PythonVersion  string               `json:"python_version" binding:"max=10"`
	EntrypointFile string               `json:"entrypoint_file" binding:"max=500"`
	ProjectDir     string               `json:"project_dir" binding:"max=500"`
	Description    string               `json:"description" binding:"max=2000"`
}

// UpdateGitWatcherRequest is the JSON body for PUT /api/v1/gitwatchers/:name.
// All spec fields are replaced; name comes from the URL path.
type UpdateGitWatcherRequest struct {
	RepoURL        string               `json:"repo_url" binding:"required,max=2048,url"`
	RepoRef        string               `json:"repo_ref" binding:"max=255"`
	BuildType      string               `json:"build_type" binding:"required,max=32"`
	Enabled        *bool                `json:"enabled"`
	TokenSecretRef *SecretKeyRefRequest `json:"token_secret_ref"`
	ArtifactName   string               `json:"artifact_name" binding:"max=255"`
	MetadataSource string               `json:"metadata_source" binding:"max=32"`
	Version        string               `json:"version" binding:"max=50"`
	PythonVersion  string               `json:"python_version" binding:"max=10"`
	EntrypointFile string               `json:"entrypoint_file" binding:"max=500"`
	ProjectDir     string               `json:"project_dir" binding:"max=500"`
	Description    string               `json:"description" binding:"max=2000"`
}

// SecretKeyRefResponse is the response representation of a SecretKeyRef.
type SecretKeyRefResponse struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// GitWatcherSpecResponse is the spec portion of a GitWatcherResponse.
type GitWatcherSpecResponse struct {
	RepoURL        string                `json:"repoURL"`
	RepoRef        string                `json:"repoRef,omitempty"`
	BuildType      string                `json:"buildType"`
	Enabled        *bool                 `json:"enabled"`
	TokenSecretRef *SecretKeyRefResponse `json:"tokenSecretRef,omitempty"`
	Name           string                `json:"name,omitempty"`
	MetadataSource string                `json:"metadataSource,omitempty"`
	Version        string                `json:"version,omitempty"`
	PythonVersion  string                `json:"pythonVersion,omitempty"`
	EntrypointFile string                `json:"entrypointFile,omitempty"`
	ProjectDir     string                `json:"projectDir,omitempty"`
	Description    string                `json:"description,omitempty"`
}

// GitWatcherStatusResponse is the status portion of a GitWatcherResponse.
type GitWatcherStatusResponse struct {
	Phase               string     `json:"phase,omitempty"`
	LastSeenCommit      string     `json:"lastSeenCommit,omitempty"`
	LastBuiltVersion    string     `json:"lastBuiltVersion,omitempty"`
	LastBuildName       string     `json:"lastBuildName,omitempty"`
	LastBuildVersion    string     `json:"lastBuildVersion,omitempty"`
	ConsecutiveFailures int        `json:"consecutiveFailures"`
	LastCheckedAt       *time.Time `json:"lastCheckedAt,omitempty"`
	LastError           string     `json:"lastError,omitempty"`
	Message             string     `json:"message,omitempty"`
}

// GitWatcherResponse is the JSON representation of a GitWatcher CR.
type GitWatcherResponse struct {
	Name      string                   `json:"name"`
	Namespace string                   `json:"namespace"`
	CreatedAt time.Time                `json:"createdAt"`
	Spec      GitWatcherSpecResponse   `json:"spec"`
	Status    GitWatcherStatusResponse `json:"status"`
}

// GitWatcherPageResponse is the paginated list response for GET /api/v1/gitwatchers.
type GitWatcherPageResponse struct {
	Items    []GitWatcherResponse `json:"items"`
	Total    int64                `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"pageSize"`
}

// ToGitWatcherResponse maps a GitWatcher CR to a GitWatcherResponse.
func ToGitWatcherResponse(gw buildv1alpha1.GitWatcher) GitWatcherResponse {
	spec := GitWatcherSpecResponse{
		RepoURL:        gw.Spec.RepoURL,
		RepoRef:        gw.Spec.RepoRef,
		BuildType:      gw.Spec.BuildType,
		Enabled:        gw.Spec.Enabled,
		Name:           gw.Spec.Name,
		MetadataSource: gw.Spec.MetadataSource,
		Version:        gw.Spec.Version,
		PythonVersion:  gw.Spec.PythonVersion,
		EntrypointFile: gw.Spec.EntrypointFile,
		ProjectDir:     gw.Spec.ProjectDir,
		Description:    gw.Spec.Description,
	}
	if gw.Spec.TokenSecretRef != nil {
		spec.TokenSecretRef = &SecretKeyRefResponse{
			Name: gw.Spec.TokenSecretRef.Name,
			Key:  gw.Spec.TokenSecretRef.Key,
		}
	}

	status := GitWatcherStatusResponse{
		Phase:               string(gw.Status.Phase),
		LastSeenCommit:      gw.Status.LastSeenCommit,
		LastBuiltVersion:    gw.Status.LastBuiltVersion,
		LastBuildName:       gw.Status.LastBuildName,
		LastBuildVersion:    gw.Status.LastBuildVersion,
		ConsecutiveFailures: gw.Status.ConsecutiveFailures,
		LastError:           gw.Status.LastError,
		Message:             gw.Status.Message,
	}
	if gw.Status.LastCheckedAt != nil {
		t := gw.Status.LastCheckedAt.Time
		status.LastCheckedAt = &t
	}

	return GitWatcherResponse{
		Name:      gw.Name,
		Namespace: gw.Namespace,
		CreatedAt: gw.CreationTimestamp.Time,
		Spec:      spec,
		Status:    status,
	}
}
