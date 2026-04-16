// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

// forge-builder creates a Python virtual environment, archives it, and uploads
// the resulting tar.gz to the fusion-index artifact registry.
//
// Required environment variables:
//
//	INDEX_BACKEND_URL   Base URL of the fusion-index service.
//	ARTIFACT_ID         Numeric ID of the artifact in fusion-index.
//	ARTIFACT_VERSION    Semver version string (e.g. "1.2.3").
//	VENV_NAME           Package name used for naming the archive.
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
	workspace     = "/workspace"
	venvDir       = workspace + "/venv"
	requirementsFile = workspace + "/requirements.txt"
)

func main() {
	indexURL := mustEnv("INDEX_BACKEND_URL")
	artifactID := mustEnv("ARTIFACT_ID")
	artifactVersion := mustEnv("ARTIFACT_VERSION")
	venvName := mustEnv("VENV_NAME")

	archiveName := venvName + "-" + artifactVersion + ".tar.gz"
	archivePath := filepath.Join(workspace, archiveName)

	log.Printf("[forge-builder] starting: artifact=%s version=%s", venvName, artifactVersion)

	// Step 1: create virtual environment.
	log.Println("[forge-builder] creating virtual environment")
	run("python3", "-m", "venv", venvDir)

	// Step 2: upgrade pip and install packages.
	pip := filepath.Join(venvDir, "bin", "pip")
	log.Println("[forge-builder] upgrading pip")
	run(pip, "install", "--no-cache-dir", "--quiet", "--upgrade", "pip")
	log.Println("[forge-builder] installing packages")
	run(pip, "install", "--no-cache-dir", "-r", requirementsFile)

	// Step 3: archive the venv.
	log.Printf("[forge-builder] creating archive %s", archiveName)
	run("tar", "czf", archivePath, "-C", workspace, "venv")
	fi, err := os.Stat(archivePath)
	if err != nil {
		log.Fatalf("[forge-builder] archive stat failed: %v", err)
	}
	log.Printf("[forge-builder] archive size: %d bytes", fi.Size())

	// Step 4: upload to fusion-index.
	uploadURL := fmt.Sprintf("%s/api/v1/artifacts/%s/versions/%s/files",
		indexURL, artifactID, url.PathEscape(artifactVersion))
	log.Printf("[forge-builder] uploading to %s", uploadURL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := uploadFile(ctx, uploadURL, archiveName, archivePath); err != nil {
		log.Fatalf("[forge-builder] upload failed: %v", err)
	}

	log.Println("[forge-builder] build complete")
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
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return fmt.Errorf("copy archive data: %w", err)
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

	// Log the response (file metadata).
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
