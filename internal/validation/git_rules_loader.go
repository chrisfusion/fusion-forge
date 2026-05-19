// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package validation

import (
	_ "embed"
	"log/slog"
	"os"

	"gopkg.in/yaml.v3"
)

//go:embed forge-git-rules.yaml
var defaultGitRulesYAML []byte

// LoadGitRules reads git structure rules from the file at path. If path is empty or
// the file does not exist, it falls back to the embedded forge-git-rules.yaml default.
func LoadGitRules(path string) GitRules {
	rules := DefaultGitRules()

	var data []byte
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("cannot read git rules file, using defaults", "path", path, "error", err)
		} else {
			data = b
		}
	}
	if data == nil {
		data = defaultGitRulesYAML
	}

	if err := yaml.Unmarshal(data, &rules); err != nil {
		slog.Warn("cannot parse git rules YAML, using defaults", "error", err)
		return DefaultGitRules()
	}
	return rules
}
