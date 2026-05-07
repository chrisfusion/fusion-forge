// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package handlers

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	buildv1alpha1 "fusion-platform.io/fusion-forge/api/v1alpha1"
	"fusion-platform.io/fusion-forge/internal/api/dto"
	"fusion-platform.io/fusion-forge/internal/config"
	"fusion-platform.io/fusion-forge/internal/db"
	"fusion-platform.io/fusion-forge/internal/indexclient"
	"fusion-platform.io/fusion-forge/internal/validation"

	corev1 "k8s.io/api/core/v1"
)

const (
	maxRequirementsBytes = 100 * 1024 // 100 KB
)

// VenvHandler handles all /api/v1/venvs endpoints.
type VenvHandler struct {
	DB          *db.Queries
	K8sCRClient client.Client
	KubeClient  kubernetes.Interface
	IndexClient *indexclient.Client
	Rules       validation.Rules
	Cfg         *config.Config
}

// List handles GET /api/v1/venvs.
func (h *VenvHandler) List(c *gin.Context) {
	page := parseIntDefault(c.Query("page"), 0)
	pageSize := parseIntDefault(c.Query("pageSize"), 20)
	if pageSize > 100 {
		pageSize = 100
	}

	params := db.ListParams{
		Page:      page,
		PageSize:  pageSize,
		BuildType: "requirements",
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

// Create handles POST /api/v1/venvs.
func (h *VenvHandler) Create(c *gin.Context) {
	var req dto.CreateVenvRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	pythonVersion, err := normalizePythonVersion(req.PythonVersion)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	requirementsTxt, ok := readRequirementsFile(c, req)
	if !ok {
		return
	}

	result := validation.Validate(requirementsTxt, h.Rules)
	if !result.Valid {
		c.JSON(http.StatusBadRequest, dto.FromValidationResult(result))
		return
	}

	ctx := c.Request.Context()

	if _, err := h.DB.GetVenvBuildByNameAndVersion(ctx, req.Name, req.Version); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("venv '%s:%s' already exists", req.Name, req.Version)})
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		internalError(c, err)
		return
	}

	fullName := indexclient.ArtifactFullName(req.Name)
	artifactID, err := h.IndexClient.FindOrCreateArtifact(ctx, fullName, req.Description)
	if err != nil {
		log.Printf("forge: find/create artifact %q: %v", fullName, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register artifact in registry: " + err.Error()})
		return
	}

	exists, err := h.IndexClient.VersionExists(ctx, artifactID, req.Version)
	if err != nil {
		internalError(c, err)
		return
	}
	if exists {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("version %s already exists for %s in registry", req.Version, req.Name)})
		return
	}

	if err := h.IndexClient.CreateVersion(ctx, artifactID, req.Version, req.Description); err != nil {
		log.Printf("forge: create version %s/%s in registry: %v", req.Name, req.Version, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create version in registry: " + err.Error()})
		return
	}

	desc := strPtr(req.Description)
	creator := strPtr(callerUsername(c))
	artifactVersion := req.Version
	buildID, err := h.DB.CreateVenvBuild(ctx, db.CreateParams{
		Name:                 req.Name,
		Version:              req.Version,
		Description:          desc,
		CreatorID:            creator,
		IndexArtifactID:      &artifactID,
		IndexArtifactVersion: &artifactVersion,
		PythonVersion:        pythonVersion,
	})
	if err != nil {
		if isUniqueViolation(err) {
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("venv '%s:%s' already exists", req.Name, req.Version)})
		} else {
			internalError(c, err)
		}
		return
	}

	ciBuildName := fmt.Sprintf("forge-venv-%d", buildID)
	if err := h.DB.UpdateCIBuildName(ctx, buildID, ciBuildName); err != nil {
		log.Printf("forge: update ci_build_name for build %d: %v", buildID, err)
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
			BuilderImage:    h.Cfg.BuilderImageForVersion(pythonVersion),
			IndexBackendURL: h.Cfg.IndexBackendURL,
			BuildType:       "requirements",
			ArtifactName:    req.Name,
			ArtifactVersion: req.Version,
			Description:     req.Description,
			ConfigData: map[string]string{
				"requirements.txt": requirementsTxt,
			},
			Env: []corev1.EnvVar{
				{Name: "ARTIFACT_ID", Value: fmt.Sprintf("%d", artifactID)},
				{Name: "ARTIFACT_VERSION", Value: req.Version},
				{Name: "VENV_NAME", Value: req.Name},
				{Name: "BUILD_TYPE", Value: "requirements"},
				{Name: "PYTHON_VERSION", Value: pythonVersion},
			},
		},
	}
	if err := h.K8sCRClient.Create(ctx, &ciBuild); err != nil {
		log.Printf("forge: create CIBuild CR %q: %v", ciBuildName, err)
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

// Validate handles POST /api/v1/venvs/validate.
func (h *VenvHandler) Validate(c *gin.Context) {
	var req dto.CreateVenvRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	requirementsTxt, ok := readRequirementsFile(c, req)
	if !ok {
		return
	}
	result := validation.Validate(requirementsTxt, h.Rules)
	resp := dto.FromValidationResult(result)
	if result.Valid {
		c.JSON(http.StatusOK, resp)
	} else {
		c.JSON(http.StatusUnprocessableEntity, resp)
	}
}

// Get handles GET /api/v1/venvs/:id. Lazily syncs CIBuild CR status to the DB row.
func (h *VenvHandler) Get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	build, err := h.DB.GetVenvBuild(ctx, id)
	if err != nil {
		notFoundOrInternal(c, err, fmt.Sprintf("venv build %d not found", id))
		return
	}

	if build.CIBuildName != nil && (build.Status == "PENDING" || build.Status == "BUILDING") {
		if newStatus, synced := syncStatusFromCR(ctx, h.K8sCRClient, h.Cfg.K8sNamespace, *build.CIBuildName); synced && newStatus != build.Status {
			if err := h.DB.UpdateStatus(ctx, id, newStatus); err != nil {
				log.Printf("forge: sync status for build %d: %v", id, err)
			} else {
				build.Status = newStatus
			}
		}
	}

	c.JSON(http.StatusOK, dto.ToResponse(build))
}

// GetLogs handles GET /api/v1/venvs/:id/logs.
func (h *VenvHandler) GetLogs(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	build, err := h.DB.GetVenvBuild(ctx, id)
	if err != nil {
		notFoundOrInternal(c, err, fmt.Sprintf("venv build %d not found", id))
		return
	}
	if build.CIBuildName == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("no CIBuild CR found for build %d", id)})
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

func readRequirementsFile(c *gin.Context, req dto.CreateVenvRequest) (string, bool) {
	if req.Requirements == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "requirements file is required"})
		return "", false
	}
	if req.Requirements.Size == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "requirements file must not be empty"})
		return "", false
	}
	if req.Requirements.Size > maxRequirementsBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "requirements file exceeds maximum allowed size of 100 KB"})
		return "", false
	}

	f, err := req.Requirements.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot open uploaded file"})
		return "", false
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxRequirementsBytes+1))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot read uploaded file"})
		return "", false
	}
	return string(data), true
}
