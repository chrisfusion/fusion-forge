// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

// Package gitutil provides helpers for reading metadata from remote git repositories.
package gitutil

import (
	"context"
	"fmt"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/pelletier/go-toml/v2"
)

// PyprojectMeta holds the name and version extracted from a pyproject.toml [project] table.
type PyprojectMeta struct {
	Name    string
	Version string
}

type pyprojectTOML struct {
	Project struct {
		Name    string `toml:"name"`
		Version string `toml:"version"`
	} `toml:"project"`
}

// FetchPyprojectMeta does a depth-1 in-memory clone of repoURL at the given ref
// (branch or tag), reads pyproject.toml from projectDir (or the repo root when
// projectDir is empty), and returns the [project].name and [project].version values.
//
// ref is tried first as a tag (refs/tags/<ref>), then as a branch (refs/heads/<ref>).
func FetchPyprojectMeta(ctx context.Context, repoURL, ref, projectDir string) (PyprojectMeta, error) {
	r, err := cloneRef(ctx, repoURL, ref)
	if err != nil {
		return PyprojectMeta{}, fmt.Errorf("clone repository: %w", err)
	}

	head, err := r.Head()
	if err != nil {
		return PyprojectMeta{}, fmt.Errorf("resolve HEAD: %w", err)
	}
	commit, err := r.CommitObject(head.Hash())
	if err != nil {
		return PyprojectMeta{}, fmt.Errorf("read commit: %w", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return PyprojectMeta{}, fmt.Errorf("read tree: %w", err)
	}

	pyprojectPath := "pyproject.toml"
	if projectDir != "" {
		pyprojectPath = projectDir + "/pyproject.toml"
	}
	f, err := tree.File(pyprojectPath)
	if err != nil {
		if projectDir != "" {
			return PyprojectMeta{}, fmt.Errorf("pyproject.toml not found in %s", projectDir)
		}
		return PyprojectMeta{}, fmt.Errorf("pyproject.toml not found at repository root")
	}
	contents, err := f.Contents()
	if err != nil {
		return PyprojectMeta{}, fmt.Errorf("read pyproject.toml: %w", err)
	}

	return parsePyproject(contents)
}

// cloneRef tries to shallow-clone repoURL at ref, first as a tag then as a branch.
func cloneRef(ctx context.Context, repoURL, ref string) (*gogit.Repository, error) {
	opts := &gogit.CloneOptions{
		URL:          repoURL,
		Depth:        1,
		SingleBranch: true,
		NoCheckout:   true,
	}

	for _, refName := range []plumbing.ReferenceName{
		plumbing.NewTagReferenceName(ref),
		plumbing.NewBranchReferenceName(ref),
	} {
		opts.ReferenceName = refName
		r, err := gogit.CloneContext(ctx, memory.NewStorage(), nil, opts)
		if err == nil {
			return r, nil
		}
		// Continue to next candidate only on ref-not-found errors.
		if !isRefNotFound(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("ref %q not found as tag or branch", ref)
}

func isRefNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "reference not found") ||
		strings.Contains(msg, "couldn't find remote ref")
}

func parsePyproject(content string) (PyprojectMeta, error) {
	var p pyprojectTOML
	if err := toml.Unmarshal([]byte(content), &p); err != nil {
		return PyprojectMeta{}, fmt.Errorf("parse pyproject.toml: %w", err)
	}
	if p.Project.Name == "" {
		return PyprojectMeta{}, fmt.Errorf("pyproject.toml [project].name is not set")
	}
	if p.Project.Version == "" {
		return PyprojectMeta{}, fmt.Errorf("pyproject.toml [project].version is not set (dynamic versions are not supported)")
	}
	return PyprojectMeta{Name: p.Project.Name, Version: p.Project.Version}, nil
}
