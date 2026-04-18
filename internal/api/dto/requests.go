// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package dto

import "mime/multipart"

// CreateVenvRequest is the multipart/form-data body for POST /api/v1/venvs.
type CreateVenvRequest struct {
	// Name is the package name. Pattern: [a-zA-Z0-9_-]+, max 255 chars.
	Name string `form:"name" binding:"required,max=255"`

	// Version is the semver version string (X.Y.Z). Max 50 chars.
	Version string `form:"version" binding:"required,max=50"`

	// Description is an optional human-readable description. Max 2000 chars.
	Description string `form:"description" binding:"max=2000"`

	// Requirements is the uploaded requirements.txt file (1 byte – 100 KB).
	Requirements *multipart.FileHeader `form:"requirements" binding:"required"`
}

// CreateGitBuildRequest is the JSON body for POST /api/v1/gitbuilds.
//
// MetadataSource controls where name and version come from:
//   - "manual" (default): caller provides both name and version.
//   - "version": caller provides name; version is read from pyproject.toml.
//   - "full": both name and version are read from pyproject.toml.
//
// Conditional required rules (enforced in the handler, not by binding tags):
//   - manual:  name required, version required.
//   - version: name required, version omitted.
//   - full:    name and version both omitted.
type CreateGitBuildRequest struct {
	// Name is the package name. Max 255 chars.
	// Required for metadata_source "manual" and "version"; optional for "full".
	Name string `json:"name" form:"name" binding:"max=255"`

	// Version is the semver version string (X.Y.Z). Max 50 chars.
	// Required for metadata_source "manual"; optional for "version" and "full".
	Version string `json:"version" form:"version" binding:"max=50"`

	// Description is an optional human-readable description. Max 2000 chars.
	Description string `json:"description" form:"description" binding:"max=2000"`

	// RepoURL is the HTTPS URL of the git repository to clone.
	RepoURL string `json:"repo_url" form:"repo_url" binding:"required,max=2048,url"`

	// RepoRef is the branch or tag to check out. Defaults to "main" when empty.
	RepoRef string `json:"repo_ref" form:"repo_ref" binding:"max=255"`

	// EntrypointFile is the name of an optional Python file at the repository root
	// that acts as the runnable entry point. Max 500 chars.
	EntrypointFile string `json:"entrypoint_file" form:"entrypoint_file" binding:"max=500"`

	// MetadataSource controls where name and version originate.
	// Accepted values: "manual" (default), "version", "full".
	MetadataSource string `json:"metadata_source" form:"metadata_source" binding:"max=32"`

	// ProjectDir is an optional relative path to the Python project within the repository.
	// Use this when the project lives in a subdirectory of a monorepo (e.g. "services/myapp").
	// Must be relative and must not contain ".." components. Max 500 chars.
	ProjectDir string `json:"project_dir" form:"project_dir" binding:"max=500"`
}
