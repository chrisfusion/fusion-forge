// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"

	buildv1alpha1 "fusion-platform.io/fusion-forge/api/v1alpha1"
	"fusion-platform.io/fusion-forge/internal/api/dto"
	"fusion-platform.io/fusion-forge/internal/api/middleware"
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
	meta, err := gitutil.FetchAppMetadata(ctx, req.RepoURL, req.RepoRef, req.ProjectDir)
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

	fullName := indexclient.AppArtifactFullName(meta.Name)
	artifactID, err := h.IndexClient.FindOrCreateArtifact(ctx, fullName, "")
	if err != nil {
		log.Error("find/create artifact", "name", fullName, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register artifact in registry: " + err.Error()})
		return
	}

	exists, err := h.IndexClient.VersionExists(ctx, artifactID, meta.Version)
	if err != nil {
		internalError(c, err)
		return
	}
	if exists {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("version %s already exists for %s in registry", meta.Version, meta.Name)})
		return
	}

	if err := h.IndexClient.CreateVersion(ctx, artifactID, meta.Version, ""); err != nil {
		log.Error("create version in registry", "name", meta.Name, "version", meta.Version, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create version in registry: " + err.Error()})
		return
	}

	artifactVersion := meta.Version
	runner := strPtr(meta.Runner)
	baseDepsURL := strPtr(meta.BaseDependencies)
	projectDir := strPtr(req.ProjectDir)
	creator := strPtr(callerUsername(c))

	buildID, err := h.DB.CreateAppBuild(ctx, db.CreateAppBuildParams{
		Name:                 meta.Name,
		Version:              meta.Version,
		CreatorID:            creator,
		RepoURL:              req.RepoURL,
		RepoRef:              req.RepoRef,
		ProjectDir:           projectDir,
		IndexArtifactID:      &artifactID,
		IndexArtifactVersion: &artifactVersion,
		PythonVersion:        meta.BuilderImage,
		Runner:               runner,
		BaseDependenciesURL:  baseDepsURL,
	})
	if err != nil {
		if isUniqueViolation(err) {
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("app build '%s:%s' already exists", meta.Name, meta.Version)})
		} else {
			internalError(c, err)
		}
		return
	}

	ciBuildName := fmt.Sprintf("forge-app-%d", buildID)
	if err := h.DB.UpdateCIBuildName(ctx, buildID, ciBuildName); err != nil {
		log.Error("update ci_build_name", "build_id", buildID, "error", err)
		_ = h.DB.UpdateStatus(ctx, buildID, "FAILED")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record build name"})
		return
	}

	ciBuild := buildv1alpha1.CIBuild{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ciBuildName,
			Namespace: h.Cfg.K8sNamespace,
		},
		Spec: buildv1alpha1.CIBuildSpec{
			BuilderImage:    builderImage,
			IndexBackendURL: h.Cfg.IndexBackendURL,
			BuildType:       "app",
			ArtifactName:    meta.Name,
			ArtifactVersion: meta.Version,
			AppSource: &buildv1alpha1.AppSourceSpec{
				URL:              req.RepoURL,
				Ref:              req.RepoRef,
				ProjectDir:       req.ProjectDir,
				BaseDependencies: meta.BaseDependencies,
			},
			ConfigData: map[string]string{},
			Env: []corev1.EnvVar{
				{Name: "ARTIFACT_ID", Value: fmt.Sprintf("%d", artifactID)},
				{Name: "ARTIFACT_VERSION", Value: meta.Version},
				{Name: "VENV_NAME", Value: meta.Name},
				{Name: "BUILD_TYPE", Value: "app"},
			},
		},
	}
	if err := h.K8sCRClient.Create(ctx, &ciBuild); err != nil {
		log.Error("create CIBuild CR", "name", ciBuildName, "error", err)
		_ = h.DB.UpdateStatus(ctx, buildID, "FAILED")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to submit build job: " + err.Error()})
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

	meta, err := gitutil.FetchAppMetadata(ctx, req.RepoURL, req.RepoRef, req.ProjectDir)
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
