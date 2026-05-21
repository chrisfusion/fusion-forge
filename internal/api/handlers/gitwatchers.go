// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package handlers

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	buildv1alpha1 "fusion-platform.io/fusion-forge/api/v1alpha1"
	"fusion-platform.io/fusion-forge/internal/api/dto"
	"fusion-platform.io/fusion-forge/internal/config"
	"fusion-platform.io/fusion-forge/internal/gitutil"
)

// GitWatcherHandler handles all /api/v1/gitwatchers endpoints.
type GitWatcherHandler struct {
	K8sCRClient client.Client
	Cfg         *config.Config
}

// List handles GET /api/v1/gitwatchers.
func (h *GitWatcherHandler) List(c *gin.Context) {
	page := parseIntDefault(c.Query("page"), 0)
	pageSize := parseIntDefault(c.Query("pageSize"), 20)
	if pageSize > 100 {
		pageSize = 100
	}

	ctx := c.Request.Context()
	var gwList buildv1alpha1.GitWatcherList
	if err := h.K8sCRClient.List(ctx, &gwList, client.InNamespace(h.Cfg.K8sNamespace)); err != nil {
		internalError(c, err)
		return
	}

	sort.Slice(gwList.Items, func(i, j int) bool {
		return gwList.Items[i].CreationTimestamp.After(gwList.Items[j].CreationTimestamp.Time)
	})

	total := int64(len(gwList.Items))
	start := page * pageSize
	if start > len(gwList.Items) {
		start = len(gwList.Items)
	}
	end := start + pageSize
	if end > len(gwList.Items) {
		end = len(gwList.Items)
	}

	items := make([]dto.GitWatcherResponse, end-start)
	for i, gw := range gwList.Items[start:end] {
		items[i] = dto.ToGitWatcherResponse(gw)
	}
	c.JSON(http.StatusOK, dto.GitWatcherPageResponse{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

// Create handles POST /api/v1/gitwatchers.
func (h *GitWatcherHandler) Create(c *gin.Context) {
	var req dto.CreateGitWatcherRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.RepoRef == "" {
		req.RepoRef = "main"
	}
	if err := validateGitWatcherSpec(req.BuildType, req.MetadataSource, req.ArtifactName, req.Version, req.PythonVersion); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateProjectDir(req.ProjectDir); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.BuildType == "git" && req.MetadataSource == "" {
		req.MetadataSource = "manual"
	}

	ctx := c.Request.Context()

	token, err := h.resolveToken(ctx, req.TokenSecretRef)
	if err != nil {
		tokenSecretError(c, err)
		return
	}
	if _, err := gitutil.FetchRemoteHEAD(ctx, req.RepoURL, req.RepoRef, token); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "repo not reachable: " + err.Error()})
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	gw := buildv1alpha1.GitWatcher{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: h.Cfg.K8sNamespace,
		},
		Spec: buildv1alpha1.GitWatcherSpec{
			RepoURL:        req.RepoURL,
			RepoRef:        req.RepoRef,
			BuildType:      req.BuildType,
			Enabled:        &enabled,
			Name:           req.ArtifactName,
			MetadataSource: req.MetadataSource,
			Version:        req.Version,
			PythonVersion:  req.PythonVersion,
			EntrypointFile: req.EntrypointFile,
			ProjectDir:     req.ProjectDir,
			Description:    req.Description,
		},
	}
	if req.TokenSecretRef != nil {
		gw.Spec.TokenSecretRef = &buildv1alpha1.SecretKeyRef{
			Name: req.TokenSecretRef.Name,
			Key:  req.TokenSecretRef.Key,
		}
	}

	if err := h.K8sCRClient.Create(ctx, &gw); err != nil {
		if k8serrors.IsAlreadyExists(err) {
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("GitWatcher %q already exists", req.Name)})
		} else {
			internalError(c, err)
		}
		return
	}

	c.JSON(http.StatusCreated, dto.ToGitWatcherResponse(gw))
}

// Get handles GET /api/v1/gitwatchers/:name.
func (h *GitWatcherHandler) Get(c *gin.Context) {
	name := c.Param("name")
	ctx := c.Request.Context()

	var gw buildv1alpha1.GitWatcher
	if err := h.K8sCRClient.Get(ctx, types.NamespacedName{Name: name, Namespace: h.Cfg.K8sNamespace}, &gw); err != nil {
		if k8serrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("GitWatcher %q not found", name)})
		} else {
			internalError(c, err)
		}
		return
	}

	c.JSON(http.StatusOK, dto.ToGitWatcherResponse(gw))
}

// Update handles PUT /api/v1/gitwatchers/:name. Replaces the full spec.
func (h *GitWatcherHandler) Update(c *gin.Context) {
	name := c.Param("name")
	var req dto.UpdateGitWatcherRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.RepoRef == "" {
		req.RepoRef = "main"
	}
	if err := validateGitWatcherSpec(req.BuildType, req.MetadataSource, req.ArtifactName, req.Version, req.PythonVersion); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateProjectDir(req.ProjectDir); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.BuildType == "git" && req.MetadataSource == "" {
		req.MetadataSource = "manual"
	}

	ctx := c.Request.Context()

	var gw buildv1alpha1.GitWatcher
	if err := h.K8sCRClient.Get(ctx, types.NamespacedName{Name: name, Namespace: h.Cfg.K8sNamespace}, &gw); err != nil {
		if k8serrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("GitWatcher %q not found", name)})
		} else {
			internalError(c, err)
		}
		return
	}

	// Only pre-flight if the repo coordinates changed.
	if req.RepoURL != gw.Spec.RepoURL || req.RepoRef != gw.Spec.RepoRef {
		token, err := h.resolveToken(ctx, req.TokenSecretRef)
		if err != nil {
			tokenSecretError(c, err)
			return
		}
		if _, err := gitutil.FetchRemoteHEAD(ctx, req.RepoURL, req.RepoRef, token); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "repo not reachable: " + err.Error()})
			return
		}
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	gw.Spec = buildv1alpha1.GitWatcherSpec{
		RepoURL:        req.RepoURL,
		RepoRef:        req.RepoRef,
		BuildType:      req.BuildType,
		Enabled:        &enabled,
		Name:           req.ArtifactName,
		MetadataSource: req.MetadataSource,
		Version:        req.Version,
		PythonVersion:  req.PythonVersion,
		EntrypointFile: req.EntrypointFile,
		ProjectDir:     req.ProjectDir,
		Description:    req.Description,
	}
	if req.TokenSecretRef != nil {
		gw.Spec.TokenSecretRef = &buildv1alpha1.SecretKeyRef{
			Name: req.TokenSecretRef.Name,
			Key:  req.TokenSecretRef.Key,
		}
	}

	if err := h.K8sCRClient.Update(ctx, &gw); err != nil {
		if k8serrors.IsConflict(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "resource version conflict, please retry"})
		} else {
			internalError(c, err)
		}
		return
	}

	c.JSON(http.StatusOK, dto.ToGitWatcherResponse(gw))
}

// Delete handles DELETE /api/v1/gitwatchers/:name.
func (h *GitWatcherHandler) Delete(c *gin.Context) {
	name := c.Param("name")
	ctx := c.Request.Context()

	var gw buildv1alpha1.GitWatcher
	if err := h.K8sCRClient.Get(ctx, types.NamespacedName{Name: name, Namespace: h.Cfg.K8sNamespace}, &gw); err != nil {
		if k8serrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("GitWatcher %q not found", name)})
		} else {
			internalError(c, err)
		}
		return
	}

	if err := h.K8sCRClient.Delete(ctx, &gw); err != nil {
		internalError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// resolveToken reads the token value from the referenced K8s Secret.
func (h *GitWatcherHandler) resolveToken(ctx context.Context, ref *dto.SecretKeyRefRequest) (string, error) {
	if ref == nil {
		return "", nil
	}
	var secret corev1.Secret
	if err := h.K8sCRClient.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: h.Cfg.K8sNamespace}, &secret); err != nil {
		return "", err
	}
	val, ok := secret.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("key %q not found in secret %q", ref.Key, ref.Name)
	}
	return string(val), nil
}

// tokenSecretError maps a resolveToken error to the correct HTTP response.
// Caller errors (secret not found, key missing) become 400; K8s server errors
// (forbidden, internal) become 500 via internalError.
func tokenSecretError(c *gin.Context, err error) {
	if k8serrors.IsNotFound(err) || k8serrors.ReasonForError(err) == metav1.StatusReasonUnknown {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token secret: " + err.Error()})
		return
	}
	internalError(c, err)
}

// validateGitWatcherSpec validates build_type, metadata_source, and conditional required fields.
func validateGitWatcherSpec(buildType, metadataSource, artifactName, version, pythonVersion string) error {
	switch buildType {
	case "git", "app":
	default:
		return fmt.Errorf("build_type must be 'git' or 'app'")
	}

	if buildType == "app" && metadataSource != "" {
		return fmt.Errorf("metadata_source is not applicable for build_type 'app'")
	}

	if buildType == "git" {
		switch metadataSource {
		case "", "manual":
			if artifactName == "" {
				return fmt.Errorf("artifact_name is required when metadata_source is 'manual'")
			}
			if version == "" {
				return fmt.Errorf("version is required when metadata_source is 'manual'")
			}
		case "version":
			if artifactName == "" {
				return fmt.Errorf("artifact_name is required when metadata_source is 'version'")
			}
		case "full":
		default:
			return fmt.Errorf("metadata_source must be 'manual', 'version', or 'full'")
		}

		if pythonVersion != "" {
			if _, err := normalizePythonVersion(pythonVersion); err != nil {
				return err
			}
		}
	}

	return nil
}
