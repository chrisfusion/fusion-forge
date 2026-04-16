// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

// Package validation validates requirements.txt content line by line.
//
// Always-on rules:
//   - Pip options (-r, --flag, -e) are rejected.
//   - VCS / URL dependencies are rejected.
//   - Package name must match PEP 508 naming rules.
//   - A version specifier must be present (bare package names are rejected).
//
// Configurable rules (forge-rules.yaml):
//   - require-exact-pinning: only == is accepted as version specifier.
//   - banned-packages: disallowed package names (case-insensitive, normalised).
//   - max-packages: upper bound on the number of valid requirement entries.
package validation

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// PEP 508 package name: starts and ends with alphanumeric, allows .-_ in between.
	rePackageName = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`)

	// Matches a requirement line. Groups: (1) name, (2) extras, (3) version spec, (4) env marker.
	reRequirementLine = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)\s*(\[[^\]]*\])?\s*([^;#]*)?(;.*)?$`)

	// Exact pin: ==X.Y.Z — the only allowed specifier when require-exact-pinning is true.
	reExactPin = regexp.MustCompile(`^\s*==\s*\S+$`)

	// Any version specifier present.
	reAnySpecifier = regexp.MustCompile(`[><=!~]`)

	// VCS or URL dependency prefix.
	reVCSOrURL = regexp.MustCompile(`(?i)^(https?://|ftp://|git\+|hg\+|svn\+|bzr\+)`)
)

// Validate processes requirementsTxt line by line and returns a Result.
func Validate(requirementsTxt string, rules Rules) Result {
	lines := strings.Split(strings.ReplaceAll(requirementsTxt, "\r\n", "\n"), "\n")
	var violations []Violation
	packageCount := 0

	for i, raw := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(raw)

		// Skip blank lines and comments.
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Reject pip options (-r, --flag, -e, …).
		if strings.HasPrefix(trimmed, "-") {
			token := strings.Fields(trimmed)[0]
			violations = append(violations, Violation{lineNum, raw,
				"pip options are not allowed (found: " + token + ")"})
			continue
		}

		// Reject VCS / URL dependencies.
		if reVCSOrURL.MatchString(trimmed) {
			violations = append(violations, Violation{lineNum, raw,
				"VCS and URL dependencies are not allowed"})
			continue
		}

		// Strip inline comment for parsing.
		spec := trimmed
		if idx := strings.Index(trimmed, "#"); idx >= 0 {
			spec = strings.TrimSpace(trimmed[:idx])
		}

		m := reRequirementLine.FindStringSubmatch(spec)
		if m == nil {
			violations = append(violations, Violation{lineNum, raw, "invalid pip requirement syntax"})
			continue
		}

		name := m[1]
		versionPart := strings.TrimSpace(m[3])

		// Validate package name (PEP 508).
		if !rePackageName.MatchString(name) {
			violations = append(violations, Violation{lineNum, raw,
				fmt.Sprintf("invalid package name %q (must match PEP 508: letters, digits, hyphens, dots, underscores)", name)})
			continue
		}

		// Banned packages (case-insensitive, normalise hyphens/underscores/dots → _).
		normalised := normalizeName(name)
		if isBanned(normalised, rules.BannedPackages) {
			violations = append(violations, Violation{lineNum, raw,
				fmt.Sprintf("package %q is not allowed", name)})
			continue
		}

		// Version specifier checks.
		versionOK := checkVersion(lineNum, raw, name, versionPart, rules, &violations)

		if versionOK {
			packageCount++
		}
	}

	if packageCount > rules.MaxPackages {
		violations = append(violations, Violation{0, "",
			fmt.Sprintf("too many packages: %d entries found, maximum is %d", packageCount, rules.MaxPackages)})
	}

	return Result{Valid: len(violations) == 0, Violations: violations}
}

func checkVersion(lineNum int, raw, name, versionPart string, rules Rules, violations *[]Violation) bool {
	if versionPart == "" {
		*violations = append(*violations, Violation{lineNum, raw,
			fmt.Sprintf("version specifier is required — bare package names are not allowed (e.g. use %s==1.0.0)", name)})
		return false
	}
	if rules.RequireExactPinning {
		if !reExactPin.MatchString(versionPart) {
			*violations = append(*violations, Violation{lineNum, raw,
				fmt.Sprintf("exact version pin required — use == (found: %s)", strings.TrimSpace(versionPart))})
			return false
		}
	} else {
		if !reAnySpecifier.MatchString(versionPart) {
			*violations = append(*violations, Violation{lineNum, raw,
				fmt.Sprintf("version specifier is required (found no operator in: %s)", strings.TrimSpace(versionPart))})
			return false
		}
	}
	return true
}

func normalizeName(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, ".", "_")
	return s
}

func isBanned(normalised string, bannedPackages []string) bool {
	for _, b := range bannedPackages {
		if normalised == normalizeName(b) {
			return true
		}
	}
	return false
}
