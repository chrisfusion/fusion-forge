// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package dto

import (
	"time"

	"fusion-platform.io/fusion-forge/internal/db"
	"fusion-platform.io/fusion-forge/internal/validation"
)

// VenvBuildResponse is the JSON representation of a VenvBuild.
type VenvBuildResponse struct {
	ID                   int64      `json:"id"`
	Name                 string     `json:"name"`
	Version              string     `json:"version"`
	Description          *string    `json:"description"`
	Status               string     `json:"status"`
	CreatorID            *string    `json:"creatorId"`
	CreatorEmail         *string    `json:"creatorEmail"`
	IndexArtifactID      *int64     `json:"indexArtifactId"`
	IndexArtifactVersion *string    `json:"indexArtifactVersion"`
	CIBuildName          *string    `json:"ciBuildName"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
}

// ToResponse maps a db.VenvBuild row to a VenvBuildResponse.
func ToResponse(b db.VenvBuild) VenvBuildResponse {
	return VenvBuildResponse{
		ID:                   b.ID,
		Name:                 b.Name,
		Version:              b.Version,
		Description:          b.Description,
		Status:               b.Status,
		CreatorID:            b.CreatorID,
		CreatorEmail:         b.CreatorEmail,
		IndexArtifactID:      b.IndexArtifactID,
		IndexArtifactVersion: b.IndexArtifactVersion,
		CIBuildName:          b.CIBuildName,
		CreatedAt:            b.CreatedAt,
		UpdatedAt:            b.UpdatedAt,
	}
}

// PageResponse is the paginated list response for GET /api/v1/venvs.
type PageResponse struct {
	Items    []VenvBuildResponse `json:"items"`
	Total    int64               `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"pageSize"`
}

// ValidationResponse is the body for POST /api/v1/venvs/validate.
type ValidationResponse struct {
	Valid      bool                   `json:"valid"`
	Violations []validation.Violation `json:"violations"`
}

// FromValidationResult converts a validation.Result to a ValidationResponse.
func FromValidationResult(r validation.Result) ValidationResponse {
	vv := r.Violations
	if vv == nil {
		vv = []validation.Violation{}
	}
	return ValidationResponse{Valid: r.Valid, Violations: vv}
}
