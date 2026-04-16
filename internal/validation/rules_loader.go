// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package validation

import (
	_ "embed"
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

//go:embed forge-rules.yaml
var defaultRulesYAML []byte

// LoadRules reads rules from the file at path. If path is empty or the file does not
// exist, it falls back to the embedded forge-rules.yaml default.
func LoadRules(path string) Rules {
	rules := DefaultRules()

	var data []byte
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			log.Printf("forge: cannot read rules file %q, using defaults: %v", path, err)
		} else {
			data = b
		}
	}
	if data == nil {
		data = defaultRulesYAML
	}

	if err := yaml.Unmarshal(data, &rules); err != nil {
		log.Printf("forge: cannot parse rules YAML, using defaults: %v", err)
		return DefaultRules()
	}
	return rules
}
