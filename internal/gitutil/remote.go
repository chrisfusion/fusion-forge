// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package gitutil

import (
	"context"
	"fmt"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	gogithttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
)

// FetchRemoteHEAD returns the HEAD commit SHA for the given ref without cloning
// the repository. It uses the git remote listing protocol (ls-refs / info/refs),
// which is far cheaper than a depth-1 clone.
//
// ref is tried first as a branch (refs/heads/<ref>), then as a tag (refs/tags/<ref>).
// token is optional; when non-empty it is sent as HTTP Basic Auth with username "oauth2".
func FetchRemoteHEAD(ctx context.Context, repoURL, ref, token string) (string, error) {
	rem := gogit.NewRemote(memory.NewStorage(), &gogitconfig.RemoteConfig{
		Name: "origin",
		URLs: []string{repoURL},
	})

	opts := &gogit.ListOptions{}
	if token != "" {
		opts.Auth = &gogithttp.BasicAuth{Username: "oauth2", Password: token}
	}

	refs, err := rem.ListContext(ctx, opts)
	if err != nil {
		return "", fmt.Errorf("list remote refs: %w", err)
	}

	branchRef := plumbing.NewBranchReferenceName(ref)
	tagRef := plumbing.NewTagReferenceName(ref)

	for _, r := range refs {
		if r.Name() == branchRef || r.Name() == tagRef {
			return r.Hash().String(), nil
		}
	}
	return "", fmt.Errorf("ref %q not found in remote", ref)
}
