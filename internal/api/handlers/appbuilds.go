// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	buildv1alpha1 "fusion-platform.io/fusion-forge/api/v1alpha1"
	"fusion-platform.io/fusion-forge/internal/api/dto"
	"fusion-platform.io/fusion-forge/internal/api/middleware"
	"fusion-platform.io/fusion-forge/internal/buildtrigger"
	"fusion-platform.io/fusion-forge/internal/config"
	"fusion-platform.io/fusion-forge/internal/db"
	"fusion-platform.io/fusion-forge/internal/gitutil"
	"fusion-platform.io/fusion-forge/internal/indexclient"
	"fusion-platform.io/fusion-forge/internal/validation"
)

// AppBuildHandler handles all /api/v1/appbuilds endpoints.
type AppBuildHandler struct {
	DB          *db.Queries
	K8sCRClient client.Client
	KubeClient  kubernetes.Interface
	IndexClient *indexclient.Client
	Cfg         *config.Config
}

// List handles GET /api/v1/appbuilds.
func (h *AppBuildHandler) List(c *gin.Context) {
	page := parseIntDefault(c.Query("page"), 0)
	pageSize := parseIntDefault(c.Query("pageSize"), 20)
	if pageSize > 100 {
		pageSize = 100
	}

	params := db.ListParams{
		Page:      page,
		PageSize:  pageSize,
		BuildType: "app",
		Status:    c.Query("status"),
		Name:      c.Query("name"),
		CreatorID: c.Query("creatorId"),
		SortBy:    c.DefaultQuery("sortBy", "createdAt"),
		SortDir:   c.DefaultQuery("sortDir", "desc"),
	}

	total, err := h.DB.CountVenvBuilds(c.Request.Context(), params)
	if err != nil {
		internalError(c, err)
		return
	}
	builds, err := h.DB.ListVenvBuilds(c.Request.Context(), params)
	if err != nil {
		internalError(c, err)
		return
	}

	items := make([]dto.VenvBuildResponse, len(builds))
	for i, b := range builds {
		items[i] = dto.ToResponse(b)
	}
	c.JSON(http.StatusOK, dto.PageResponse{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

// Create handles POST /api/v1/appbuilds.
func (h *AppBuildHandler) Create(c *gin.Context) {
	var req dto.CreateAppBuildRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.RepoRef == "" {
		req.RepoRef = "main"
	}
	if err := validateProjectDir(req.ProjectDir); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	log := middleware.LoggerFromCtx(c)

	// Resolve all metadata from the repository's metadata.yaml.
	meta, err := gitutil.FetchAppMetadata(ctx, req.RepoURL, req.RepoRef, req.ProjectDir, "")
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "failed to read metadata.yaml: " + err.Error()})
		return
	}

	// Look up the builder image for the key declared in metadata.yaml.
	builderImage, err := h.Cfg.BuilderImageFor(meta.BuilderImage)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if _, err := h.DB.GetVenvBuildByNameAndVersion(ctx, meta.Name, meta.Version); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("app build '%s:%s' already exists", meta.Name, meta.Version)})
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		internalError(c, err)
		return
	}

	buildID, _, err := buildtrigger.TriggerAppBuild(ctx, buildtrigger.Deps{
		DB:          h.DB,
		K8sCRClient: h.K8sCRClient,
		IndexClient: h.IndexClient,
		Cfg:         h.Cfg,
	}, buildtrigger.AppBuildInput{
		RepoURL:      req.RepoURL,
		RepoRef:      req.RepoRef,
		ProjectDir:   req.ProjectDir,
		CreatorID:    callerUsername(c),
		BuilderImage: builderImage,
		Meta:         meta,
	})
	if err != nil {
		if errors.Is(err, buildtrigger.ErrConflict) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		} else {
			log.Error("trigger app build", "name", meta.Name, "version", meta.Version, "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	build, err := h.DB.GetVenvBuild(ctx, buildID)
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, dto.ToResponse(build))
}

// Validate handles POST /api/v1/appbuilds/validate.
// Fetches metadata.yaml to validate the request and checks for conflicts.
func (h *AppBuildHandler) Validate(c *gin.Context) {
	var req dto.CreateAppBuildRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.RepoRef == "" {
		req.RepoRef = "main"
	}
	if err := validateProjectDir(req.ProjectDir); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	var violations []validation.Violation

	meta, err := gitutil.FetchAppMetadata(ctx, req.RepoURL, req.RepoRef, req.ProjectDir, "")
	if err != nil {
		violations = append(violations, validation.Violation{
			Line:    0,
			Content: req.RepoURL + "@" + req.RepoRef,
			Message: err.Error(),
		})
		c.JSON(http.StatusUnprocessableEntity, dto.FromValidationResult(validation.Result{Valid: false, Violations: violations}))
		return
	}

	// Check that the builder image key is known.
	if _, err := h.Cfg.BuilderImageFor(meta.BuilderImage); err != nil {
		violations = append(violations, validation.Violation{
			Line:    0,
			Content: meta.BuilderImage,
			Message: err.Error(),
		})
	}

	// Check DB for duplicate.
	if _, err := h.DB.GetVenvBuildByNameAndVersion(ctx, meta.Name, meta.Version); err == nil {
		violations = append(violations, validation.Violation{
			Line:    0,
			Content: fmt.Sprintf("%s:%s", meta.Name, meta.Version),
			Message: fmt.Sprintf("a build for '%s:%s' already exists", meta.Name, meta.Version),
		})
	} else if !errors.Is(err, pgx.ErrNoRows) {
		internalError(c, err)
		return
	}

	// Check fusion-index for existing version (read-only — no artifact is created here).
	fullName := indexclient.AppArtifactFullName(meta.Name)
	artifactID, found, err := h.IndexClient.FindArtifact(ctx, fullName)
	if err != nil {
		middleware.LoggerFromCtx(c).Error("find artifact", "name", fullName, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if found {
		exists, err := h.IndexClient.VersionExists(ctx, artifactID, meta.Version)
		if err != nil {
			internalError(c, err)
			return
		}
		if exists {
			violations = append(violations, validation.Violation{
				Line:    0,
				Content: fmt.Sprintf("%s:%s", meta.Name, meta.Version),
				Message: fmt.Sprintf("version %s already exists for %s in registry", meta.Version, meta.Name),
			})
		}
	}

	result := validation.Result{Valid: len(violations) == 0, Violations: violations}
	resp := dto.FromValidationResult(result)
	if result.Valid {
		c.JSON(http.StatusOK, resp)
	} else {
		c.JSON(http.StatusUnprocessableEntity, resp)
	}
}

// Get handles GET /api/v1/appbuilds/:id. Lazily syncs CIBuild CR status to the DB row.
func (h *AppBuildHandler) Get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	build, err := h.DB.GetVenvBuild(ctx, id)
	if err != nil {
		notFoundOrInternal(c, err, fmt.Sprintf("app build %d not found", id))
		return
	}

	if build.CIBuildName != nil && (build.Status == "PENDING" || build.Status == "BUILDING") {
		if newStatus, synced := syncStatusFromCR(ctx, h.K8sCRClient, h.Cfg.K8sNamespace, *build.CIBuildName); synced && newStatus != build.Status {
			if err := h.DB.UpdateStatus(ctx, id, newStatus); err != nil {
				middleware.LoggerFromCtx(c).Warn("sync status failed", "build_id", id, "error", err)
			} else {
				build.Status = newStatus
			}
		}
	}

	c.JSON(http.StatusOK, dto.ToResponse(build))
}

// GetLogs handles GET /api/v1/appbuilds/:id/logs.
func (h *AppBuildHandler) GetLogs(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	build, err := h.DB.GetVenvBuild(ctx, id)
	if err != nil {
		notFoundOrInternal(c, err, fmt.Sprintf("app build %d not found", id))
		return
	}
	if build.CIBuildName == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("no CIBuild CR found for app build %d", id)})
		return
	}

	var ciBuild buildv1alpha1.CIBuild
	if err := h.K8sCRClient.Get(ctx, types.NamespacedName{
		Name:      *build.CIBuildName,
		Namespace: h.Cfg.K8sNamespace,
	}, &ciBuild); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "CIBuild CR not found: " + err.Error()})
		return
	}
	if ciBuild.Status.JobName == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "build pod has not been created yet"})
		return
	}

	logs, err := podLogs(ctx, h.KubeClient, h.Cfg.K8sNamespace, ciBuild.Status.JobName)
	if err != nil {
		internalError(c, err)
		return
	}
	if logs == "" {
		c.Status(http.StatusNoContent)
		return
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(logs))
}
