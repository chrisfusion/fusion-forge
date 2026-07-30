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
//	BUILD_TYPE          "requirements" (default), "git", or "app".
//
// Additional variables for BUILD_TYPE=git:
//
//	GIT_REPO_URL            HTTPS URL of the git repository to clone.
//	GIT_REF                 Branch or tag to check out (default: "main").
//	GIT_PROJECT_DIR         Optional: relative path to the Python project within the repo
//	                        (monorepo support). When set, pyproject.toml, src/, and
//	                        ENTRYPOINT_FILE are resolved relative to this directory.
//	ENTRYPOINT_FILE         Optional: name of a Python file at the project root to upload
//	                        as a second artefact alongside the venv archive.
//	REQUIRE_PYPROJECT_TOML  "true"/"false" — enforce pyproject.toml presence (default: "true").
//	REQUIRE_SRC_DIR         "true"/"false" — enforce src/ directory presence (default: "true").
//	GIT_TOKEN               Optional: authenticates the clone of a private repository via a
//	                        git credential helper (username fixed to "oauth2"). Never logged
//	                        or embedded in the clone URL.
//
// Additional variables for BUILD_TYPE=app:
//
//	GIT_REPO_URL            HTTPS URL of the git repository to clone.
//	GIT_REF                 Branch or tag to check out (default: "main").
//	GIT_PROJECT_DIR         Optional: relative path within the repo for monorepo support.
//	APP_BASE_DEPENDENCIES   Optional: URL of an existing venvpack to use as base.
//	                        When set, the venvpack is downloaded and extracted; the project's
//	                        requirements.txt is installed on top. When empty, a fresh venv is built.
//	                        An unreachable URL causes the build to fail.
//	APP_FILE_UPLOAD_MODE    "legacy" (default), "auto", or "list". Selects which loose Python
//	                        files are uploaded to fusion-index alongside the venv archive:
//	                        "legacy" requires and uploads exactly main.py (pre-existing
//	                        behavior); "auto" uploads every top-level *.py file found in the
//	                        project; "list" uploads exactly the files named in APP_FILES.
//	APP_FILES               Comma-separated filenames to upload. Only read when
//	                        APP_FILE_UPLOAD_MODE=list.
//	GIT_TOKEN               Optional: same as for BUILD_TYPE=git.
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
	"strings"
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

	// PYTHON_VERSION is informational — the actual Python interpreter is determined by which
	// builder image was selected (python:3.12-slim or python:3.10-slim). The env var is logged
	// so the build record is visible in pod logs without inspecting the image tag.
	pythonVersion := envDefault("PYTHON_VERSION", "3.12")
	log.Printf("[forge-builder] starting: type=%s artifact=%s version=%s python=%s", buildType, venvName, artifactVersion, pythonVersion)

	switch buildType {
	case "git":
		buildFromGit(ctx, uploadURL, archiveName, archivePath)
	case "app":
		buildFromApp(ctx, uploadURL, archiveName, archivePath)
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
	repoURL          := mustEnv("GIT_REPO_URL")
	repoRef          := envDefault("GIT_REF", "main")
	projectDir       := envDefault("GIT_PROJECT_DIR", "")
	entrypointFile   := envDefault("ENTRYPOINT_FILE", "")
	requirePyproject := envDefault("REQUIRE_PYPROJECT_TOML", "true") == "true"
	requireSrc       := envDefault("REQUIRE_SRC_DIR", "true") == "true"

	// Step 1: clone the repository.
	log.Printf("[forge-builder] cloning %s @ %s", repoURL, repoRef)
	run("git", gitCloneArgs(repoURL, repoRef, srcDir)...)

	// projectRoot is the directory that contains pyproject.toml and src/.
	// For monorepos this is a subdirectory of the clone; otherwise it is the clone root.
	projectRoot := srcDir
	if projectDir != "" {
		projectRoot = filepath.Join(srcDir, projectDir)
		log.Printf("[forge-builder] using project directory: %s", projectDir)
	}

	// Step 2: validate repository structure (fails the build early on bad layout).
	validateGitStructure(projectRoot, requirePyproject, requireSrc, entrypointFile)

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
	run(pip, "wheel", "--no-cache-dir", "-w", distDir, projectRoot)

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
	// The entrypoint path is relative to projectRoot, not to the repo root.
	if entrypointFile != "" {
		uploadProjectFile(ctx, uploadURL, projectRoot, entrypointFile)
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
// gitCloneArgs returns the argument list for "git clone" of repoURL into dir at repoRef.
// When GIT_TOKEN is set, a credential helper reads it from the environment at
// credential-request time (username fixed to "oauth2", same convention as the
// GitWatcher's own metadata pre-fetch in internal/gitutil) — unlike embedding the
// token directly in the clone URL, the token then never appears in argv (visible via
// ps) or in git's own stderr output, which can echo the URL it failed to reach.
func gitCloneArgs(repoURL, repoRef, dir string) []string {
	var args []string
	if os.Getenv("GIT_TOKEN") != "" {
		args = append(args, "-c", `credential.helper=!f() { echo username=oauth2; echo "password=$GIT_TOKEN"; }; f`)
	}
	return append(args, "clone", "--single-branch", "--depth=1", "--branch", repoRef, repoURL, dir)
}

func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("[forge-builder] command %q failed: %v", name, err)
	}
}

// uploadFile POSTs the file at path as a multipart/form-data upload.
// The file is streamed directly from disk — the content is never loaded into memory.
// Content-Length is pre-computed from the multipart header size + file size + footer size
// so proxies and servers receive a properly framed request without chunked encoding.
func uploadFile(ctx context.Context, uploadURL, filename, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}

	// Write only the part header into a small buffer to measure its exact byte count.
	var headerBuf bytes.Buffer
	mw := multipart.NewWriter(&headerBuf)
	if _, err := mw.CreateFormFile("file", filename); err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	footer := "\r\n--" + mw.Boundary() + "--\r\n"
	totalSize := int64(headerBuf.Len()) + fi.Size() + int64(len(footer))

	body := io.MultiReader(&headerBuf, f, strings.NewReader(footer))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.ContentLength = totalSize
	req.Header.Set("Content-Type", mw.FormDataContentType())

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

// buildFromApp clones a git repository containing metadata.yaml + requirements.txt + main.py,
// optionally downloads and extracts a base venvpack, installs project requirements on top,
// archives the venv, and uploads the venvpack, main.py, and metadata.yaml to fusion-index.
func buildFromApp(ctx context.Context, uploadURL, archiveName, archivePath string) {
	repoURL        := mustEnv("GIT_REPO_URL")
	repoRef        := envDefault("GIT_REF", "main")
	projectDir     := envDefault("GIT_PROJECT_DIR", "")
	baseDepsURL    := envDefault("APP_BASE_DEPENDENCIES", "")
	fileUploadMode := envDefault("APP_FILE_UPLOAD_MODE", "legacy")
	listedFiles    := splitCSV(envDefault("APP_FILES", ""))

	// Step 1: clone the repository.
	log.Printf("[forge-builder] cloning %s @ %s", repoURL, repoRef)
	run("git", gitCloneArgs(repoURL, repoRef, srcDir)...)

	projectRoot := srcDir
	if projectDir != "" {
		projectRoot = filepath.Join(srcDir, projectDir)
		log.Printf("[forge-builder] using project directory: %s", projectDir)
	}

	// Step 2: validate app structure.
	validateAppStructure(projectRoot, fileUploadMode, listedFiles)

	pip := filepath.Join(venvDir, "bin", "pip")

	// Step 3: prepare the virtual environment.
	if baseDepsURL != "" {
		// Download and extract the base venvpack, then layer project requirements on top.
		basePath := filepath.Join(workspace, "base.tar.gz")
		log.Printf("[forge-builder] downloading base venvpack from %s", baseDepsURL)
		downloadFile(ctx, baseDepsURL, basePath)
		log.Println("[forge-builder] extracting base venvpack")
		run("tar", "xzf", basePath, "-C", workspace)
	} else {
		// Build a fresh venv from scratch.
		log.Println("[forge-builder] creating virtual environment")
		run("python3", "-m", "venv", venvDir)
		log.Println("[forge-builder] upgrading pip")
		run(pip, "install", "--no-cache-dir", "--quiet", "--upgrade", "pip")
	}

	// Step 4: install project dependencies from requirements.txt.
	reqFile := filepath.Join(projectRoot, "requirements.txt")
	log.Println("[forge-builder] installing packages from requirements.txt")
	run(pip, "install", "--no-cache-dir", "-r", reqFile)

	// Step 5: copy project source packages into site-packages so they are
	// importable at runtime without a pyproject.toml.
	// Every subdirectory in the project root (except known non-source dirs) is
	// copied — e.g. internals/ becomes an importable package inside the venv.
	sitePackages := findSitePackages()
	log.Printf("[forge-builder] copying project source into %s", sitePackages)
	entries, rdErr := os.ReadDir(projectRoot)
	if rdErr != nil {
		log.Fatalf("[forge-builder] read project dir: %v", rdErr)
	}
	skipDirs := map[string]bool{"venv": true, "dist": true, "build": true, "__pycache__": true}
	for _, e := range entries {
		if !e.IsDir() || skipDirs[e.Name()] || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		log.Printf("[forge-builder] copying %s → site-packages", e.Name())
		run("cp", "-r", filepath.Join(projectRoot, e.Name()), filepath.Join(sitePackages, e.Name()))
	}

	// Step 6: archive and upload the venv (source is now inside site-packages).
	archiveAndUpload(ctx, uploadURL, archiveName, archivePath)

	// Step 7: upload loose Python file(s) per fileUploadMode.
	switch fileUploadMode {
	case "auto":
		pyFiles, err := discoverTopLevelPyFiles(projectRoot)
		if err != nil {
			log.Fatalf("[forge-builder] discover python files: %v", err)
		}
		log.Printf("[forge-builder] auto-discovered %d python file(s): %v", len(pyFiles), pyFiles)
		for _, name := range pyFiles {
			uploadProjectFile(ctx, uploadURL, projectRoot, name)
		}
	case "list":
		for _, name := range listedFiles {
			uploadProjectFile(ctx, uploadURL, projectRoot, name)
		}
	default: // "legacy"
		uploadProjectFile(ctx, uploadURL, projectRoot, "main.py")
	}

	// Step 8: upload metadata.yaml.
	uploadProjectFile(ctx, uploadURL, projectRoot, "metadata.yaml")
}

// uploadProjectFile stats and uploads a single file relative to projectRoot,
// failing the build if the file is missing or the upload fails.
func uploadProjectFile(ctx context.Context, uploadURL, projectRoot, name string) {
	path := filepath.Join(projectRoot, name)
	fi, err := os.Stat(path)
	if err != nil {
		log.Fatalf("[forge-builder] %s not found: %v", name, err)
	}
	log.Printf("[forge-builder] uploading %s (%d bytes)", name, fi.Size())
	if err := uploadFile(ctx, uploadURL, name, path); err != nil {
		log.Fatalf("[forge-builder] %s upload failed: %v", name, err)
	}
}

// discoverTopLevelPyFiles returns the names (not paths) of every *.py file
// directly in projectRoot, non-recursive.
func discoverTopLevelPyFiles(projectRoot string) ([]string, error) {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".py") {
			files = append(files, e.Name())
		}
	}
	return files, nil
}

// splitCSV splits a comma-separated list, trimming whitespace and dropping
// empty entries. Returns nil for an empty input string.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// findSitePackages returns the site-packages path inside the venv.
func findSitePackages() string {
	matches, err := filepath.Glob(filepath.Join(venvDir, "lib", "python*", "site-packages"))
	if err != nil || len(matches) == 0 {
		log.Fatalf("[forge-builder] cannot locate site-packages in %s", venvDir)
	}
	return matches[0]
}

// validateAppStructure checks that the repository contains the required app files.
// main.py is only required in "legacy" mode; in "list" mode every listed file must
// exist upfront so the build fails fast instead of after building the venv.
func validateAppStructure(repoDir, fileUploadMode string, listedFiles []string) {
	log.Println("[forge-builder] validating app structure")
	required := []string{"metadata.yaml", "requirements.txt"}
	if fileUploadMode == "legacy" {
		required = append(required, "main.py")
	}
	for _, name := range required {
		if _, err := os.Stat(filepath.Join(repoDir, name)); os.IsNotExist(err) {
			log.Fatalf("[forge-builder] structure check failed: %s not found at project root", name)
		}
	}
	if fileUploadMode == "list" {
		for _, name := range listedFiles {
			if _, err := os.Stat(filepath.Join(repoDir, name)); os.IsNotExist(err) {
				log.Fatalf("[forge-builder] structure check failed: listed file %q not found at project root", name)
			}
		}
	}
	log.Println("[forge-builder] app structure validation passed")
}

// downloadFile downloads the URL to destPath using a streaming HTTP GET.
// Exits the process on any error.
func downloadFile(ctx context.Context, rawURL, destPath string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		log.Fatalf("[forge-builder] build download request: %v", err)
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("[forge-builder] download %s: %v", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Fatalf("[forge-builder] download %s returned HTTP %d", rawURL, resp.StatusCode)
	}
	f, err := os.Create(destPath)
	if err != nil {
		log.Fatalf("[forge-builder] create download dest %s: %v", destPath, err)
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		log.Fatalf("[forge-builder] write download %s: %v", destPath, err)
	}
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
