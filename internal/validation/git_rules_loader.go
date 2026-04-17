// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package validation

import (
	_ "embed"
	"log"
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
			log.Printf("forge: cannot read git rules file %q, using defaults: %v", path, err)
		} else {
			data = b
		}
	}
	if data == nil {
		data = defaultGitRulesYAML
	}

	if err := yaml.Unmarshal(data, &rules); err != nil {
		log.Printf("forge: cannot parse git rules YAML, using defaults: %v", err)
		return DefaultGitRules()
	}
	return rules
}
