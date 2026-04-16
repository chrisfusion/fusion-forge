// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package validation

// Rules holds the configurable validation rules loaded from forge-rules.yaml.
type Rules struct {
	RequireExactPinning bool     `yaml:"require-exact-pinning"`
	BannedPackages      []string `yaml:"banned-packages"`
	MaxPackages         int      `yaml:"max-packages"`
}

// DefaultRules returns the built-in default rules (safe, strict).
func DefaultRules() Rules {
	return Rules{
		RequireExactPinning: true,
		BannedPackages:      nil,
		MaxPackages:         100,
	}
}

// Violation describes a single invalid line in requirements.txt.
type Violation struct {
	Line    int    `json:"line"`
	Content string `json:"content"`
	Message string `json:"message"`
}

// Result is the outcome of validating a requirements.txt.
type Result struct {
	Valid      bool        `json:"valid"`
	Violations []Violation `json:"violations"`
}
