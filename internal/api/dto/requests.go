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
