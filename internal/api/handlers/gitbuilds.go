// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

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

// GitBuildHandler handles all /api/v1/gitbuilds endpoints.
type GitBuildHandler struct {
	DB          *db.Queries
	K8sCRClient client.Client
	KubeClient  kubernetes.Interface
	IndexClient *indexclient.Client
	GitRules    validation.GitRules
	Cfg         *config.Config
}

// List handles GET /api/v1/gitbuilds.
func (h *GitBuildHandler) List(c *gin.Context) {
	page := parseIntDefault(c.Query("page"), 0)
	pageSize := parseIntDefault(c.Query("pageSize"), 20)
	if pageSize > 100 {
		pageSize = 100
	}

	params := db.ListParams{
		Page:      page,
		PageSize:  pageSize,
		BuildType: "git",
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

// Create handles POST /api/v1/gitbuilds.
func (h *GitBuildHandler) Create(c *gin.Context) {
	var req dto.CreateGitBuildRequest
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
	if err := normalizeMetadataSource(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	pythonVersion, err := normalizePythonVersion(req.PythonVersion)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	builderImage, err := h.Cfg.BuilderImageFor(pythonVersion)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	// Resolve name/version from pyproject.toml when requested.
	if req.MetadataSource != "manual" {
		if err := resolveMetadata(ctx, &req); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
	}

	if _, err := h.DB.GetVenvBuildByNameAndVersion(ctx, req.Name, req.Version); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("git build '%s:%s' already exists", req.Name, req.Version)})
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		internalError(c, err)
		return
	}

	buildID, _, err := buildtrigger.TriggerGitBuild(ctx, buildtrigger.Deps{
		DB:          h.DB,
		K8sCRClient: h.K8sCRClient,
		IndexClient: h.IndexClient,
		Cfg:         h.Cfg,
		GitRules:    h.GitRules,
	}, buildtrigger.GitBuildInput{
		Name:           req.Name,
		Version:        req.Version,
		Description:    req.Description,
		RepoURL:        req.RepoURL,
		RepoRef:        req.RepoRef,
		MetadataSource: req.MetadataSource,
		EntrypointFile: req.EntrypointFile,
		ProjectDir:     req.ProjectDir,
		PythonVersion:  pythonVersion,
		BuilderImage:   builderImage,
		CreatorID:      callerUsername(c),
	})
	if err != nil {
		if errors.Is(err, buildtrigger.ErrConflict) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		} else {
			middleware.LoggerFromCtx(c).Error("trigger git build", "name", req.Name, "version", req.Version, "error", err)
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

// Validate handles POST /api/v1/gitbuilds/validate.
// It validates the request format and checks for conflicts in the DB and fusion-index.
// When metadata_source is "version" or "full", it also fetches pyproject.toml to resolve
// name/version and reports any fetch or parse errors as violations.
// Repository structure (pyproject.toml, src/) is validated by the builder binary after cloning.
func (h *GitBuildHandler) Validate(c *gin.Context) {
	var req dto.CreateGitBuildRequest
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
	if err := normalizeMetadataSource(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	var violations []validation.Violation

	// For pyproject modes, resolve name/version by fetching the remote repo.
	// A fetch failure is itself a violation — we cannot check conflicts without the values.
	if req.MetadataSource != "manual" {
		if err := resolveMetadata(ctx, &req); err != nil {
			violations = append(violations, validation.Violation{
				Line:    0,
				Content: req.RepoURL + "@" + req.RepoRef,
				Message: err.Error(),
			})
			result := validation.Result{Valid: false, Violations: violations}
			c.JSON(http.StatusUnprocessableEntity, dto.FromValidationResult(result))
			return
		}
	}

	// Check DB for existing build with same name+version.
	if _, err := h.DB.GetVenvBuildByNameAndVersion(ctx, req.Name, req.Version); err == nil {
		violations = append(violations, validation.Violation{
			Line:    0,
			Content: fmt.Sprintf("%s:%s", req.Name, req.Version),
			Message: fmt.Sprintf("a build for '%s:%s' already exists", req.Name, req.Version),
		})
	} else if !errors.Is(err, pgx.ErrNoRows) {
		internalError(c, err)
		return
	}

	// Check fusion-index for existing version (read-only — no artifact is created here).
	fullName := indexclient.ArtifactFullName(req.Name)
	artifactID, found, err := h.IndexClient.FindArtifact(ctx, fullName)
	if err != nil {
		middleware.LoggerFromCtx(c).Error("find artifact", "name", fullName, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if found {
		exists, err := h.IndexClient.VersionExists(ctx, artifactID, req.Version)
		if err != nil {
			internalError(c, err)
			return
		}
		if exists {
			violations = append(violations, validation.Violation{
				Line:    0,
				Content: fmt.Sprintf("%s:%s", req.Name, req.Version),
				Message: fmt.Sprintf("version %s already exists for %s in registry", req.Version, req.Name),
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

// Get handles GET /api/v1/gitbuilds/:id. Lazily syncs CIBuild CR status to the DB row.
func (h *GitBuildHandler) Get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	build, err := h.DB.GetVenvBuild(ctx, id)
	if err != nil {
		notFoundOrInternal(c, err, fmt.Sprintf("git build %d not found", id))
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

// GetLogs handles GET /api/v1/gitbuilds/:id/logs.
func (h *GitBuildHandler) GetLogs(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	build, err := h.DB.GetVenvBuild(ctx, id)
	if err != nil {
		notFoundOrInternal(c, err, fmt.Sprintf("git build %d not found", id))
		return
	}
	if build.CIBuildName == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("no CIBuild CR found for git build %d", id)})
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

// normalizeMetadataSource validates and normalises req.MetadataSource to one of
// "manual", "version", or "full", and enforces which other fields are required.
func normalizeMetadataSource(req *dto.CreateGitBuildRequest) error {
	switch req.MetadataSource {
	case "", "manual":
		req.MetadataSource = "manual"
		if req.Name == "" {
			return fmt.Errorf("name is required when metadata_source is 'manual'")
		}
		if req.Version == "" {
			return fmt.Errorf("version is required when metadata_source is 'manual'")
		}
	case "version":
		if req.Name == "" {
			return fmt.Errorf("name is required when metadata_source is 'version'")
		}
	case "full":
		// name and version both come from pyproject.toml — nothing required here
	default:
		return fmt.Errorf("metadata_source must be 'manual', 'version', or 'full'")
	}
	return nil
}

// resolveMetadata fetches pyproject.toml from the remote repository and populates
// req.Name (for "full") and req.Version (for "version" and "full").
func resolveMetadata(ctx context.Context, req *dto.CreateGitBuildRequest) error {
	meta, err := gitutil.FetchPyprojectMeta(ctx, req.RepoURL, req.RepoRef, req.ProjectDir, "")
	if err != nil {
		return err
	}
	if req.MetadataSource == "full" {
		req.Name = meta.Name
	}
	req.Version = meta.Version
	return nil
}

// validateProjectDir rejects absolute paths and any path that would escape the repo root.
func validateProjectDir(p string) error {
	if p == "" {
		return nil
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("project_dir must be a relative path")
	}
	cleaned := filepath.Clean(p)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("project_dir must not escape the repository root")
	}
	return nil
}
