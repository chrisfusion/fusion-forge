// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package validation

// GitRules holds the configurable structural validation rules for git-repo builds.
// These rules are loaded from forge-git-rules.yaml at startup and forwarded to the
// builder pod as environment variables so the builder can enforce them before building.
type GitRules struct {
	RequirePyprojectToml bool `yaml:"require-pyproject-toml"`
	RequireSrcDir        bool `yaml:"require-src-dir"`
}

// DefaultGitRules returns the built-in default git structure rules.
func DefaultGitRules() GitRules {
	return GitRules{
		RequirePyprojectToml: true,
		RequireSrcDir:        true,
	}
}
