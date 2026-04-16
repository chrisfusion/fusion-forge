// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

// Package indexclient provides an HTTP client for the fusion-index artifact registry.
package indexclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"time"
)

// Client calls the fusion-index REST API.
type Client struct {
	baseURL string
	http    *http.Client
}

// New creates a new Client for the given base URL.
func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// --- Request / response types -------------------------------------------------

type createArtifactRequest struct {
	FullName    string `json:"fullName"`
	Description string `json:"description"`
}

type artifactResponse struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
}

type artifactListResponse struct {
	Items []artifactResponse `json:"items"`
}

type createVersionRequest struct {
	Version string   `json:"version"`
	Config  string   `json:"config,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}

type versionResponse struct {
	ID      int64  `json:"id"`
	Version string `json:"version"`
}

type versionListResponse struct {
	Items []versionResponse `json:"items"`
}

// --- API methods --------------------------------------------------------------

// FindOrCreateArtifact looks up an artifact by full_name (prefix match) and creates it
// if it does not exist. Returns the artifact ID.
func (c *Client) FindOrCreateArtifact(ctx context.Context, fullName, description string) (int64, error) {
	// List artifacts filtering by exact name.
	listURL := c.baseURL + "/api/v1/artifacts?name=" + url.QueryEscape(fullName)
	var list artifactListResponse
	if err := c.getJSON(ctx, listURL, &list); err != nil {
		return 0, fmt.Errorf("list artifacts: %w", err)
	}
	for _, a := range list.Items {
		if a.FullName == fullName {
			return a.ID, nil
		}
	}

	// Not found — create it.
	body := createArtifactRequest{FullName: fullName, Description: description}
	var created artifactResponse
	if err := c.postJSON(ctx, c.baseURL+"/api/v1/artifacts", body, &created); err != nil {
		return 0, fmt.Errorf("create artifact: %w", err)
	}
	return created.ID, nil
}

// VersionExists returns true if the given semver version already exists for artifactID.
// It probes the specific version endpoint directly rather than listing all versions.
func (c *Client) VersionExists(ctx context.Context, artifactID int64, semver string) (bool, error) {
	reqURL := fmt.Sprintf("%s/api/v1/artifacts/%d/versions/%s",
		c.baseURL, artifactID, url.PathEscape(semver))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return false, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("probe version: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		b, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("probe version returned %d: %s", resp.StatusCode, string(b))
	}
}

// CreateVersion creates a new version for the given artifact. Returns the version ID.
func (c *Client) CreateVersion(ctx context.Context, artifactID int64, semver, description string) error {
	reqURL := fmt.Sprintf("%s/api/v1/artifacts/%d/versions", c.baseURL, artifactID)
	body := createVersionRequest{Version: semver, Config: description}
	var resp versionResponse
	return c.postJSON(ctx, reqURL, body, &resp)
}

// UploadFile uploads data as a multipart file to the given artifact version.
func (c *Client) UploadFile(ctx context.Context, artifactID int64, semver, filename string, data io.Reader) error {
	reqURL := fmt.Sprintf("%s/api/v1/artifacts/%d/versions/%s/files",
		c.baseURL, artifactID, url.PathEscape(semver))

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, data); err != nil {
		return fmt.Errorf("write file data: %w", err)
	}
	writer.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, body)
	if err != nil {
		return fmt.Errorf("build upload request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("upload request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload returned %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// --- Helpers ------------------------------------------------------------------

func (c *Client) getJSON(ctx context.Context, rawURL string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET %s returned %d: %s", rawURL, resp.StatusCode, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) postJSON(ctx context.Context, rawURL string, in, out interface{}) error {
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST %s returned %d: %s", rawURL, resp.StatusCode, string(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// ArtifactFullName returns the canonical fusion-index full_name for a venv artifact.
// Convention: "venv.{name}" using dot-separated namespace.
func ArtifactFullName(name string) string {
	return "venv." + name
}

// ArchiveFilename returns the tar.gz filename for a given venv name and version.
func ArchiveFilename(name, version string) string {
	return filepath.Base(name) + "-" + version + ".tar.gz"
}
