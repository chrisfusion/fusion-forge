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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
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

// ZombieCleanup handles POST /api/v1/builds/zombie-cleanup.
// It queries PENDING/BUILDING builds older than older_than whose CIBuild CR no longer
// exists in Kubernetes, then deletes them: index version (best-effort) then DB row.
// At most 1000 builds are inspected per call.
func (h *BuildsHandler) ZombieCleanup(c *gin.Context) {
	var req dto.ZombieCleanupRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.OlderThan.IsZero() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "older_than is required"})
		return
	}
	if req.BuildType != "" && !validBuildTypes[req.BuildType] {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": fmt.Sprintf("unknown build_type %q: accepted values are requirements, git, app", req.BuildType)})
		return
	}

	ctx := c.Request.Context()
	logger := middleware.LoggerFromCtx(c)

	builds, err := h.DB.ListInflightBuilds(ctx, req.OlderThan, req.BuildType)
	if err != nil {
		internalError(c, err)
		return
	}

	deleted := make([]int64, 0, len(builds))
	failed := make([]dto.BulkDeleteFailure, 0, len(builds))

	for _, b := range builds {
		var ciBuild buildv1alpha1.CIBuild
		err := h.K8sCRClient.Get(ctx, types.NamespacedName{
			Name:      *b.CIBuildName,
			Namespace: h.Cfg.K8sNamespace,
		}, &ciBuild)
		if err == nil {
			continue // CR still exists, not a zombie
		}
		if !apierrors.IsNotFound(err) {
			logger.Warn("zombie check: get CIBuild CR", "ci_build_name", *b.CIBuildName, "error", err)
			failed = append(failed, dto.BulkDeleteFailure{ID: b.ID, Error: "k8s: " + err.Error()})
			continue
		}
		if err := h.deleteZombie(ctx, logger, b); err != nil {
			logger.Error("delete zombie build", "build_id", b.ID, "error", err)
			failed = append(failed, dto.BulkDeleteFailure{ID: b.ID, Error: err.Error()})
		} else {
			deleted = append(deleted, b.ID)
		}
	}

	logger.Info("zombie cleanup",
		"inspected", len(builds),
		"deleted", len(deleted),
		"failed", len(failed),
		"older_than", req.OlderThan,
		"build_type", req.BuildType,
	)
	c.JSON(http.StatusOK, dto.BulkDeleteResponse{Deleted: deleted, Failed: failed})
}

// IndexDriftCleanup handles POST /api/v1/builds/index-drift-cleanup.
// It is the housekeeper for DANGLING-INDEX-ARTIFACT.md: it inspects SUCCESS builds older than
// older_than and, for each, verifies the recorded fusion-index artifact/version still exists.
// The normal desync-catching path lives in the GitWatcher reconciler itself (it self-heals
// opportunistically whenever it reconciles); this endpoint is the backstop for builds whose
// watcher never reconciles again because the watched repo's HEAD stopped moving, and for
// requirements-type builds which have no watcher at all. A dangling build's DB row is deleted
// (its index side is already confirmed gone, so there's nothing to clean up there) and any
// GitWatcher CR that was relying on it is nudged to rebuild on its next reconcile.
// At most 1000 builds are inspected per call.
func (h *BuildsHandler) IndexDriftCleanup(c *gin.Context) {
	var req dto.IndexDriftCleanupRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.OlderThan.IsZero() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "older_than is required"})
		return
	}
	if req.BuildType != "" && !validBuildTypes[req.BuildType] {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": fmt.Sprintf("unknown build_type %q: accepted values are requirements, git, app", req.BuildType)})
		return
	}

	ctx := c.Request.Context()
	logger := middleware.LoggerFromCtx(c)

	builds, err := h.DB.ListBuildsForDeletion(ctx, []string{"SUCCESS"}, req.OlderThan, req.BuildType)
	if err != nil {
		internalError(c, err)
		return
	}

	deleted := make([]int64, 0, len(builds))
	failed := make([]dto.BulkDeleteFailure, 0, len(builds))

	for _, b := range builds {
		if b.IndexArtifactID == nil || b.IndexArtifactVersion == nil {
			continue // nothing recorded to verify against
		}
		exists, err := h.IndexClient.VersionExists(ctx, *b.IndexArtifactID, *b.IndexArtifactVersion)
		if err != nil {
			logger.Warn("index-drift check: verify version", "build_id", b.ID, "artifact_id", *b.IndexArtifactID, "version", *b.IndexArtifactVersion, "error", err)
			failed = append(failed, dto.BulkDeleteFailure{ID: b.ID, Error: "index: " + err.Error()})
			continue
		}
		if exists {
			continue // not dangling
		}
		if err := h.DB.DeleteVenvBuild(ctx, b.ID); err != nil {
			logger.Error("delete dangling build row", "build_id", b.ID, "error", err)
			failed = append(failed, dto.BulkDeleteFailure{ID: b.ID, Error: err.Error()})
			continue
		}
		deleted = append(deleted, b.ID)
		h.nudgeMatchingWatchers(ctx, logger, b)
	}

	logger.Info("index-drift cleanup",
		"inspected", len(builds),
		"deleted", len(deleted),
		"failed", len(failed),
		"older_than", req.OlderThan,
		"build_type", req.BuildType,
	)
	c.JSON(http.StatusOK, dto.BulkDeleteResponse{Deleted: deleted, Failed: failed})
}

// nudgeMatchingWatchers clears LastSeenCommit/LastBuiltVersion on any GitWatcher CR whose
// (repoURL, repoRef, lastBuiltVersion) matches the dangling build just removed, so the
// watcher's own reconcile loop re-evaluates and rebuilds it on its next tick instead of
// sitting on a stale LastBuiltVersion indefinitely. Best-effort: a watcher isn't guaranteed
// to exist for every build (e.g. one-off /gitbuilds calls), and failures here don't affect
// the already-committed row deletion, so they're only logged.
func (h *BuildsHandler) nudgeMatchingWatchers(ctx context.Context, logger *slog.Logger, b db.VenvBuild) {
	if b.RepoURL == nil {
		return
	}
	repoRef := ""
	if b.RepoRef != nil {
		repoRef = *b.RepoRef
	}

	var list buildv1alpha1.GitWatcherList
	if err := h.K8sCRClient.List(ctx, &list, client.InNamespace(h.Cfg.K8sNamespace)); err != nil {
		logger.Warn("index-drift: list gitwatchers", "error", err)
		return
	}

	for i := range list.Items {
		w := &list.Items[i]
		wantRef := w.Spec.RepoRef
		if wantRef == "" {
			wantRef = "main"
		}
		if w.Spec.RepoURL != *b.RepoURL || wantRef != repoRef || w.Status.LastBuiltVersion != b.Version {
			continue
		}
		base := client.MergeFrom(w.DeepCopy())
		w.Status.LastSeenCommit = ""
		w.Status.LastBuiltVersion = ""
		w.Status.Message = fmt.Sprintf("index artifact for version %s vanished — will rebuild on next reconcile", b.Version)
		if err := h.K8sCRClient.Status().Patch(ctx, w, base); err != nil {
			logger.Warn("index-drift: reset watcher status", "watcher", w.Name, "error", err)
			continue
		}
		logger.Info("index-drift: reset watcher for rebuild", "watcher", w.Name, "version", b.Version)
	}
}

// deleteZombie removes a zombie build: deletes the index version (best-effort) then the DB row.
func (h *BuildsHandler) deleteZombie(ctx context.Context, logger *slog.Logger, b db.VenvBuild) error {
	if b.IndexArtifactID != nil && b.IndexArtifactVersion != nil {
		if err := h.IndexClient.DeleteVersion(ctx, *b.IndexArtifactID, *b.IndexArtifactVersion); err != nil {
			logger.Warn("delete zombie version from index", "artifact_id", *b.IndexArtifactID, "version", *b.IndexArtifactVersion, "error", err)
		}
	}
	return h.DB.DeleteVenvBuild(ctx, b.ID)
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
