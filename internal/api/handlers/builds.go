// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	buildv1alpha1 "fusion-platform.io/fusion-forge/api/v1alpha1"
	"fusion-platform.io/fusion-forge/internal/api/dto"
	"fusion-platform.io/fusion-forge/internal/api/middleware"
	"fusion-platform.io/fusion-forge/internal/config"
	"fusion-platform.io/fusion-forge/internal/db"
	"fusion-platform.io/fusion-forge/internal/indexclient"
)

var validBuildTypes = map[string]bool{"requirements": true, "git": true, "app": true}

// BuildsHandler handles cross-cutting build endpoints.
type BuildsHandler struct {
	DB          *db.Queries
	K8sCRClient client.Client
	IndexClient *indexclient.Client
	Cfg         *config.Config
}

// BulkDelete handles DELETE /api/v1/builds.
// Deletes builds matching the requested statuses that were created before older_than.
// PENDING and BUILDING statuses are rejected. At most 1000 rows are deleted per call.
func (h *BuildsHandler) BulkDelete(c *gin.Context) {
	var req dto.BulkDeleteBuildsRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.OlderThan.IsZero() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "older_than is required"})
		return
	}

	for i, s := range req.Statuses {
		s = strings.ToUpper(s)
		req.Statuses[i] = s
		switch s {
		case "PENDING", "BUILDING":
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": fmt.Sprintf("status %q is not eligible for deletion: only FAILED and SUCCESS builds may be bulk-deleted", s)})
			return
		case "FAILED", "SUCCESS":
			// valid
		default:
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": fmt.Sprintf("unknown status %q: accepted values are FAILED, SUCCESS", s)})
			return
		}
	}

	if req.BuildType != "" && !validBuildTypes[req.BuildType] {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": fmt.Sprintf("unknown build_type %q: accepted values are requirements, git, app", req.BuildType)})
		return
	}

	ctx := c.Request.Context()
	logger := middleware.LoggerFromCtx(c)

	builds, err := h.DB.ListBuildsForDeletion(ctx, req.Statuses, req.OlderThan, req.BuildType)
	if err != nil {
		internalError(c, err)
		return
	}

	deleted := make([]int64, 0, len(builds))
	failed := make([]dto.BulkDeleteFailure, 0, len(builds))

	for _, b := range builds {
		if err := h.deleteBuild(ctx, logger, b); err != nil {
			logger.Error("delete build row", "build_id", b.ID, "error", err)
			failed = append(failed, dto.BulkDeleteFailure{ID: b.ID, Error: err.Error()})
		} else {
			deleted = append(deleted, b.ID)
		}
	}

	logger.Info("bulk delete builds",
		"deleted", len(deleted),
		"failed", len(failed),
		"statuses", req.Statuses,
		"older_than", req.OlderThan,
		"build_type", req.BuildType,
	)
	c.JSON(http.StatusOK, dto.BulkDeleteResponse{Deleted: deleted, Failed: failed})
}

// deleteBuild performs the cleanup sequence for a single build row:
// index version deletion (FAILED only, best-effort), CIBuild CR deletion (best-effort),
// then DB row deletion (definitive — error is returned to the caller).
func (h *BuildsHandler) deleteBuild(ctx context.Context, logger *slog.Logger, b db.VenvBuild) error {
	if b.Status == "FAILED" && b.IndexArtifactID != nil && b.IndexArtifactVersion != nil {
		if err := h.IndexClient.DeleteVersion(ctx, *b.IndexArtifactID, *b.IndexArtifactVersion); err != nil {
			logger.Warn("delete version from index", "artifact_id", *b.IndexArtifactID, "version", *b.IndexArtifactVersion, "error", err)
		}
	}

	if b.CIBuildName != nil {
		ciBuild := buildv1alpha1.CIBuild{
			ObjectMeta: metav1.ObjectMeta{
				Name:      *b.CIBuildName,
				Namespace: h.Cfg.K8sNamespace,
			},
		}
		if err := client.IgnoreNotFound(h.K8sCRClient.Delete(ctx, &ciBuild)); err != nil {
			logger.Warn("delete CIBuild CR", "name", *b.CIBuildName, "error", err)
		}
	}

	return h.DB.DeleteVenvBuild(ctx, b.ID)
}
