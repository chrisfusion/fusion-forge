// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

// forge-builder creates a Python virtual environment, archives it, and uploads
// the resulting tar.gz to the fusion-index artifact registry.
//
// Required environment variables (all build types):
//
//	INDEX_BACKEND_URL   Base URL of the fusion-index service.
//	ARTIFACT_ID         Numeric ID of the artifact in fusion-index.
//	ARTIFACT_VERSION    Semver version string (e.g. "1.2.3").
//	VENV_NAME           Package name used for naming the archive.
//	BUILD_TYPE          "requirements" (default) or "git".
//
// Additional variables for BUILD_TYPE=git:
//
//	GIT_REPO_URL            HTTPS URL of the git repository to clone.
//	GIT_REF                 Branch or tag to check out (default: "main").
//	ENTRYPOINT_FILE         Optional: name of a Python file at the repo root to upload
//	                        as a second artefact alongside the venv archive.
//	REQUIRE_PYPROJECT_TOML  "true"/"false" — enforce pyproject.toml presence (default: "true").
//	REQUIRE_SRC_DIR         "true"/"false" — enforce src/ directory presence (default: "true").
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	workspace        = "/workspace"
	venvDir          = workspace + "/venv"
	requirementsFile = workspace + "/requirements.txt"
	srcDir           = workspace + "/src"
	distDir          = workspace + "/dist"
)

func main() {
	buildType := envDefault("BUILD_TYPE", "requirements")

	indexURL        := mustEnv("INDEX_BACKEND_URL")
	artifactID      := mustEnv("ARTIFACT_ID")
	artifactVersion := mustEnv("ARTIFACT_VERSION")
	venvName        := mustEnv("VENV_NAME")

	archiveName := venvName + "-" + artifactVersion + ".tar.gz"
	archivePath := filepath.Join(workspace, archiveName)

	uploadURL := fmt.Sprintf("%s/api/v1/artifacts/%s/versions/%s/files",
		indexURL, artifactID, url.PathEscape(artifactVersion))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	log.Printf("[forge-builder] starting: type=%s artifact=%s version=%s", buildType, venvName, artifactVersion)

	switch buildType {
	case "git":
		buildFromGit(ctx, uploadURL, archiveName, archivePath)
	default:
		buildFromRequirements(ctx, uploadURL, archiveName, archivePath)
	}

	log.Println("[forge-builder] build complete")
}

// buildFromRequirements installs packages from /workspace/requirements.txt into a venv,
// archives the venv, and uploads the archive to fusion-index.
func buildFromRequirements(ctx context.Context, uploadURL, archiveName, archivePath string) {
	log.Println("[forge-builder] creating virtual environment")
	run("python3", "-m", "venv", venvDir)

	pip := filepath.Join(venvDir, "bin", "pip")
	log.Println("[forge-builder] upgrading pip")
	run(pip, "install", "--no-cache-dir", "--quiet", "--upgrade", "pip")
	log.Println("[forge-builder] installing packages from requirements.txt")
	run(pip, "install", "--no-cache-dir", "-r", requirementsFile)

	archiveAndUpload(ctx, uploadURL, archiveName, archivePath)
}

// buildFromGit clones a git repository, validates its structure, builds a wheel,
// installs it into a venv, archives the venv, and uploads both the venv archive
// and (optionally) the entrypoint file to fusion-index.
func buildFromGit(ctx context.Context, uploadURL, archiveName, archivePath string) {
	repoURL        := mustEnv("GIT_REPO_URL")
	repoRef        := envDefault("GIT_REF", "main")
	entrypointFile := envDefault("ENTRYPOINT_FILE", "")
	requirePyproject := envDefault("REQUIRE_PYPROJECT_TOML", "true") == "true"
	requireSrc      := envDefault("REQUIRE_SRC_DIR", "true") == "true"

	// Step 1: clone the repository.
	log.Printf("[forge-builder] cloning %s @ %s", repoURL, repoRef)
	run("git", "clone", "--single-branch", "--depth=1", "--branch", repoRef, repoURL, srcDir)

	// Step 2: validate repository structure (fails the build early on bad layout).
	validateGitStructure(srcDir, requirePyproject, requireSrc, entrypointFile)

	// Step 3: create the virtual environment.
	log.Println("[forge-builder] creating virtual environment")
	run("python3", "-m", "venv", venvDir)

	pip := filepath.Join(venvDir, "bin", "pip")
	log.Println("[forge-builder] upgrading pip")
	run(pip, "install", "--no-cache-dir", "--quiet", "--upgrade", "pip")

	// Step 4: build a wheel from the project (reads pyproject.toml).
	log.Println("[forge-builder] building wheel from pyproject.toml")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		log.Fatalf("[forge-builder] create dist dir: %v", err)
	}
	run(pip, "wheel", "--no-cache-dir", "-w", distDir, srcDir)

	// Step 5: install the wheel (pip resolves and installs all dependencies).
	wheels, err := filepath.Glob(filepath.Join(distDir, "*.whl"))
	if err != nil || len(wheels) == 0 {
		log.Fatalf("[forge-builder] no wheel found in %s after build", distDir)
	}
	log.Printf("[forge-builder] installing %s", filepath.Base(wheels[0]))
	installArgs := append([]string{"install", "--no-cache-dir"}, wheels...)
	run(pip, installArgs...)

	// Step 6: archive and upload the venv.
	archiveAndUpload(ctx, uploadURL, archiveName, archivePath)

	// Step 7: upload the entrypoint file as a second artefact (if configured).
	if entrypointFile != "" {
		entrypointPath := filepath.Join(srcDir, entrypointFile)
		fi, err := os.Stat(entrypointPath)
		if err != nil {
			log.Fatalf("[forge-builder] entrypoint file %q not found: %v", entrypointFile, err)
		}
		log.Printf("[forge-builder] uploading entrypoint %s (%d bytes)", entrypointFile, fi.Size())
		if err := uploadFile(ctx, uploadURL, entrypointFile, entrypointPath); err != nil {
			log.Fatalf("[forge-builder] entrypoint upload failed: %v", err)
		}
	}
}

// validateGitStructure checks that the cloned repository satisfies the configured layout rules.
// It calls log.Fatalf on the first violation, failing the build with a clear message.
func validateGitStructure(repoDir string, requirePyproject, requireSrc bool, entrypointFile string) {
	log.Println("[forge-builder] validating repository structure")

	if requirePyproject {
		if _, err := os.Stat(filepath.Join(repoDir, "pyproject.toml")); os.IsNotExist(err) {
			log.Fatalf("[forge-builder] structure check failed: pyproject.toml not found at repository root")
		}
	}
	if requireSrc {
		fi, err := os.Stat(filepath.Join(repoDir, "src"))
		if os.IsNotExist(err) {
			log.Fatalf("[forge-builder] structure check failed: src/ directory not found at repository root")
		}
		if err == nil && !fi.IsDir() {
			log.Fatalf("[forge-builder] structure check failed: src exists but is not a directory")
		}
	}
	if entrypointFile != "" {
		if _, err := os.Stat(filepath.Join(repoDir, entrypointFile)); os.IsNotExist(err) {
			log.Fatalf("[forge-builder] structure check failed: entrypoint file %q not found at repository root", entrypointFile)
		}
	}

	log.Println("[forge-builder] structure validation passed")
}

// archiveAndUpload creates the venv tar.gz and uploads it to fusion-index.
func archiveAndUpload(ctx context.Context, uploadURL, archiveName, archivePath string) {
	log.Printf("[forge-builder] creating archive %s", archiveName)
	run("tar", "czf", archivePath, "-C", workspace, "venv")

	fi, err := os.Stat(archivePath)
	if err != nil {
		log.Fatalf("[forge-builder] archive stat failed: %v", err)
	}
	log.Printf("[forge-builder] archive size: %d bytes", fi.Size())

	log.Printf("[forge-builder] uploading archive to %s", uploadURL)
	if err := uploadFile(ctx, uploadURL, archiveName, archivePath); err != nil {
		log.Fatalf("[forge-builder] upload failed: %v", err)
	}
}

// run executes a command and streams its output to stdout/stderr. Exits on failure.
func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("[forge-builder] command %q failed: %v", name, err)
	}
}

// uploadFile POSTs the file at path as a multipart/form-data upload.
func uploadFile(ctx context.Context, uploadURL, filename, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return fmt.Errorf("copy file data: %w", err)
	}
	writer.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upload request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upload returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var meta map[string]interface{}
	if err := json.Unmarshal(respBody, &meta); err == nil {
		if id, ok := meta["id"]; ok {
			log.Printf("[forge-builder] uploaded file id=%v", id)
		}
	}
	return nil
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("[forge-builder] required env var %q is not set", key)
	}
	return v
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
