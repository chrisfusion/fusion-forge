// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package gitutil

import (
	"context"
	"fmt"

	"gopkg.in/yaml.v3"
)

// AppMetadata holds the fields extracted from a metadata.yaml file in an app build repository.
type AppMetadata struct {
	Name             string
	Version          string
	BuilderImage     string
	BaseDependencies string
	Runner           string
	Raw              []byte // original YAML bytes uploaded as-is to fusion-index
}

type appMetadataYAML struct {
	Name             string      `yaml:"name"`
	Version          string      `yaml:"version"`
	BuilderImage     string      `yaml:"builderImage"`
	BaseDependencies string      `yaml:"basedependencies"`
	Runner           interface{} `yaml:"runner"`
}

// FetchAppMetadata does a depth-1 in-memory clone of repoURL at the given ref
// (branch or tag), reads metadata.yaml from projectDir (or the repo root when
// projectDir is empty), and returns the parsed AppMetadata.
// token is optional; when non-empty it is used as HTTP Basic Auth with username "oauth2".
func FetchAppMetadata(ctx context.Context, repoURL, ref, projectDir, token string) (AppMetadata, error) {
	r, err := cloneRef(ctx, repoURL, ref, token)
	if err != nil {
		return AppMetadata{}, fmt.Errorf("clone repository: %w", err)
	}

	head, err := r.Head()
	if err != nil {
		return AppMetadata{}, fmt.Errorf("resolve HEAD: %w", err)
	}
	commit, err := r.CommitObject(head.Hash())
	if err != nil {
		return AppMetadata{}, fmt.Errorf("read commit: %w", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return AppMetadata{}, fmt.Errorf("read tree: %w", err)
	}

	metadataPath := "metadata.yaml"
	if projectDir != "" {
		metadataPath = projectDir + "/metadata.yaml"
	}
	f, err := tree.File(metadataPath)
	if err != nil {
		if projectDir != "" {
			return AppMetadata{}, fmt.Errorf("metadata.yaml not found in %s", projectDir)
		}
		return AppMetadata{}, fmt.Errorf("metadata.yaml not found at repository root")
	}
	contents, err := f.Contents()
	if err != nil {
		return AppMetadata{}, fmt.Errorf("read metadata.yaml: %w", err)
	}

	return parseAppMetadata(contents)
}

func parseAppMetadata(content string) (AppMetadata, error) {
	var m appMetadataYAML
	if err := yaml.Unmarshal([]byte(content), &m); err != nil {
		return AppMetadata{}, fmt.Errorf("parse metadata.yaml: %w", err)
	}
	if m.Name == "" {
		return AppMetadata{}, fmt.Errorf("metadata.yaml: name is not set")
	}
	if m.Version == "" {
		return AppMetadata{}, fmt.Errorf("metadata.yaml: version is not set")
	}
	if m.BuilderImage == "" {
		return AppMetadata{}, fmt.Errorf("metadata.yaml: builderImage is not set")
	}
	return AppMetadata{
		Name:             m.Name,
		Version:          m.Version,
		BuilderImage:     m.BuilderImage,
		BaseDependencies: m.BaseDependencies,
		Runner:           runnerType(m.Runner),
		Raw:              []byte(content),
	}, nil
}

// runnerType extracts a flat string from the runner field.
// Supports both plain strings ("streamlit") and nested objects (runner.type).
func runnerType(v interface{}) string {
	switch r := v.(type) {
	case string:
		return r
	case map[string]interface{}:
		if t, ok := r["type"].(string); ok {
			return t
		}
	}
	return ""
}
