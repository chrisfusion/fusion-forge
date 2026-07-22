// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

// Package buildtrigger contains the shared logic for registering an artifact
// version in fusion-index, inserting a build row, and creating a CIBuild CR.
// It is used by both the REST handlers and the GitWatcher controller.
package buildtrigger

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/jackc/pgx/v5/pgconn"

	buildv1alpha1 "fusion-platform.io/fusion-forge/api/v1alpha1"
	"fusion-platform.io/fusion-forge/internal/config"
	"fusion-platform.io/fusion-forge/internal/db"
	"fusion-platform.io/fusion-forge/internal/gitutil"
	"fusion-platform.io/fusion-forge/internal/indexclient"
	"fusion-platform.io/fusion-forge/internal/validation"
)

// ErrConflict is returned when the artifact version already exists in the registry
// or database, indicating a 409-class condition.
var ErrConflict = errors.New("conflict")

// Deps holds the shared dependencies required to trigger a build.
type Deps struct {
	DB          *db.Queries
	K8sCRClient client.Client
	IndexClient *indexclient.Client
	Cfg         *config.Config
	GitRules    validation.GitRules
}

// GitBuildInput carries the fully resolved parameters for a git build.
// The caller is responsible for resolving name, version, and builder image
// before calling TriggerGitBuild.
type GitBuildInput struct {
	Name           string
	Version        string
	Description    string
	RepoURL        string
	RepoRef        string
	MetadataSource string
	EntrypointFile string
	ProjectDir     string
	PythonVersion  string
	BuilderImage   string
	CreatorID      string
	TokenSecretRef *buildv1alpha1.SecretKeyRef
}

// TriggerGitBuild registers the artifact in fusion-index, inserts a DB row, and
// creates the CIBuild CR. Returns (buildID, ciBuildName, error).
//
// On ErrConflict the version already exists (caller should respond 409).
// Any other error is an internal failure (caller should respond 500).
// If the DB row is created but the CIBuild CR fails, the row is marked FAILED.
func TriggerGitBuild(ctx context.Context, deps Deps, inp GitBuildInput) (int64, string, error) {
	fullName := indexclient.ArtifactFullName(inp.Name)
	artifactID, err := deps.IndexClient.FindOrCreateArtifact(ctx, fullName, inp.Description)
	if err != nil {
		return 0, "", fmt.Errorf("find/create artifact %q: %w", fullName, err)
	}

	exists, err := deps.IndexClient.VersionExists(ctx, artifactID, inp.Version)
	if err != nil {
		return 0, "", fmt.Errorf("check version in registry: %w", err)
	}
	if exists {
		return 0, "", fmt.Errorf("version %s already exists for %s in registry: %w", inp.Version, inp.Name, ErrConflict)
	}

	if err := deps.IndexClient.CreateVersion(ctx, artifactID, inp.Version, inp.Description); err != nil {
		return 0, "", fmt.Errorf("create version in registry: %w", err)
	}

	desc := strPtr(inp.Description)
	creator := strPtr(inp.CreatorID)
	entrypoint := strPtr(inp.EntrypointFile)
	projectDir := strPtr(inp.ProjectDir)
	artifactVersion := inp.Version
	buildID, err := deps.DB.CreateGitBuild(ctx, db.CreateGitBuildParams{
		Name:                 inp.Name,
		Version:              inp.Version,
		Description:          desc,
		CreatorID:            creator,
		RepoURL:              inp.RepoURL,
		RepoRef:              inp.RepoRef,
		EntrypointFile:       entrypoint,
		IndexArtifactID:      &artifactID,
		IndexArtifactVersion: &artifactVersion,
		MetadataSource:       inp.MetadataSource,
		ProjectDir:           projectDir,
		PythonVersion:        inp.PythonVersion,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return 0, "", fmt.Errorf("git build '%s:%s' already exists: %w", inp.Name, inp.Version, ErrConflict)
		}
		return 0, "", fmt.Errorf("insert build row: %w", err)
	}

	ciBuildName := fmt.Sprintf("forge-git-%d", buildID)
	if err := deps.DB.UpdateCIBuildName(ctx, buildID, ciBuildName); err != nil {
		_ = deps.DB.UpdateStatus(ctx, buildID, "FAILED")
		return 0, "", fmt.Errorf("record build name: %w", err)
	}

	ciBuild := buildv1alpha1.CIBuild{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ciBuildName,
			Namespace: deps.Cfg.K8sNamespace,
		},
		Spec: buildv1alpha1.CIBuildSpec{
			BuilderImage:    inp.BuilderImage,
			IndexBackendURL: deps.Cfg.IndexBackendURL,
			BuildType:       "git",
			ArtifactName:    inp.Name,
			ArtifactVersion: inp.Version,
			Description:     inp.Description,
			GitSource: &buildv1alpha1.GitSourceSpec{
				URL:            inp.RepoURL,
				Ref:            inp.RepoRef,
				EntrypointFile: inp.EntrypointFile,
				ProjectDir:     inp.ProjectDir,
				TokenSecretRef: inp.TokenSecretRef,
			},
			ConfigData: map[string]string{},
			Env: []corev1.EnvVar{
				{Name: "ARTIFACT_ID", Value: fmt.Sprintf("%d", artifactID)},
				{Name: "ARTIFACT_VERSION", Value: inp.Version},
				{Name: "VENV_NAME", Value: inp.Name},
				{Name: "BUILD_TYPE", Value: "git"},
				{Name: "PYTHON_VERSION", Value: inp.PythonVersion},
				{Name: "REQUIRE_PYPROJECT_TOML", Value: boolStr(deps.GitRules.RequirePyprojectToml)},
				{Name: "REQUIRE_SRC_DIR", Value: boolStr(deps.GitRules.RequireSrcDir)},
			},
		},
	}
	if err := deps.K8sCRClient.Create(ctx, &ciBuild); err != nil {
		_ = deps.DB.UpdateStatus(ctx, buildID, "FAILED")
		return 0, "", fmt.Errorf("create CIBuild CR %q: %w", ciBuildName, err)
	}

	return buildID, ciBuildName, nil
}

// AppBuildInput carries the fully resolved parameters for an app build.
type AppBuildInput struct {
	RepoURL        string
	RepoRef        string
	ProjectDir     string
	CreatorID      string
	BuilderImage   string
	Meta           gitutil.AppMetadata
	TokenSecretRef *buildv1alpha1.SecretKeyRef
}

// TriggerAppBuild registers the artifact in fusion-index, inserts a DB row, and
// creates the CIBuild CR. Returns (buildID, ciBuildName, error).
//
// On ErrConflict the version already exists (caller should respond 409).
func TriggerAppBuild(ctx context.Context, deps Deps, inp AppBuildInput) (int64, string, error) {
	fullName := indexclient.AppArtifactFullName(inp.Meta.Name)
	artifactID, err := deps.IndexClient.FindOrCreateArtifact(ctx, fullName, "")
	if err != nil {
		return 0, "", fmt.Errorf("find/create artifact %q: %w", fullName, err)
	}

	exists, err := deps.IndexClient.VersionExists(ctx, artifactID, inp.Meta.Version)
	if err != nil {
		return 0, "", fmt.Errorf("check version in registry: %w", err)
	}
	if exists {
		return 0, "", fmt.Errorf("version %s already exists for %s in registry: %w", inp.Meta.Version, inp.Meta.Name, ErrConflict)
	}

	if err := deps.IndexClient.CreateVersion(ctx, artifactID, inp.Meta.Version, ""); err != nil {
		return 0, "", fmt.Errorf("create version in registry: %w", err)
	}

	artifactVersion := inp.Meta.Version
	runner := strPtr(inp.Meta.Runner)
	baseDepsURL := strPtr(inp.Meta.BaseDependencies)
	projectDir := strPtr(inp.ProjectDir)
	creator := strPtr(inp.CreatorID)

	buildID, err := deps.DB.CreateAppBuild(ctx, db.CreateAppBuildParams{
		Name:                 inp.Meta.Name,
		Version:              inp.Meta.Version,
		CreatorID:            creator,
		RepoURL:              inp.RepoURL,
		RepoRef:              inp.RepoRef,
		ProjectDir:           projectDir,
		IndexArtifactID:      &artifactID,
		IndexArtifactVersion: &artifactVersion,
		PythonVersion:        inp.Meta.BuilderImage,
		Runner:               runner,
		BaseDependenciesURL:  baseDepsURL,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return 0, "", fmt.Errorf("app build '%s:%s' already exists: %w", inp.Meta.Name, inp.Meta.Version, ErrConflict)
		}
		return 0, "", fmt.Errorf("insert build row: %w", err)
	}

	ciBuildName := fmt.Sprintf("forge-app-%d", buildID)
	if err := deps.DB.UpdateCIBuildName(ctx, buildID, ciBuildName); err != nil {
		_ = deps.DB.UpdateStatus(ctx, buildID, "FAILED")
		return 0, "", fmt.Errorf("record build name: %w", err)
	}

	ciBuild := buildv1alpha1.CIBuild{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ciBuildName,
			Namespace: deps.Cfg.K8sNamespace,
		},
		Spec: buildv1alpha1.CIBuildSpec{
			BuilderImage:    inp.BuilderImage,
			IndexBackendURL: deps.Cfg.IndexBackendURL,
			BuildType:       "app",
			ArtifactName:    inp.Meta.Name,
			ArtifactVersion: inp.Meta.Version,
			AppSource: &buildv1alpha1.AppSourceSpec{
				URL:              inp.RepoURL,
				Ref:              inp.RepoRef,
				ProjectDir:       inp.ProjectDir,
				BaseDependencies: inp.Meta.BaseDependencies,
				TokenSecretRef:   inp.TokenSecretRef,
			},
			ConfigData: map[string]string{},
			Env: []corev1.EnvVar{
				{Name: "ARTIFACT_ID", Value: fmt.Sprintf("%d", artifactID)},
				{Name: "ARTIFACT_VERSION", Value: inp.Meta.Version},
				{Name: "VENV_NAME", Value: inp.Meta.Name},
				{Name: "BUILD_TYPE", Value: "app"},
			},
		},
	}
	if err := deps.K8sCRClient.Create(ctx, &ciBuild); err != nil {
		_ = deps.DB.UpdateStatus(ctx, buildID, "FAILED")
		return 0, "", fmt.Errorf("create CIBuild CR %q: %w", ciBuildName, err)
	}

	return buildID, ciBuildName, nil
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
