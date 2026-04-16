// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	Port        string
	DBHost      string
	DBPort      string
	DBName      string
	DBUser      string
	DBPassword  string
	DBSSLMode   string
	K8sNamespace string

	// IndexBackendURL is the base URL of the fusion-index artifact registry.
	IndexBackendURL string

	// BuilderImage is the container image used to execute builds.
	BuilderImage string

	// JobTTLSeconds is how long K8s keeps finished Jobs before GC.
	JobTTLSeconds int32

	// Auth
	AuthEnabled    bool
	AuthAudience   string
	AuthAllowedSAs []string

	// RulesFile is the path to forge-rules.yaml. Empty = use embedded default.
	RulesFile string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	return &Config{
		Port:            getEnv("HTTP_PORT", "8080"),
		DBHost:          getEnv("DB_HOST", "localhost"),
		DBPort:          getEnv("DB_PORT", "5432"),
		DBName:          getEnv("DB_NAME", "fusion_forge"),
		DBUser:          getEnv("DB_USERNAME", "fusion"),
		DBPassword:      getEnv("DB_PASSWORD", "fusion"),
		DBSSLMode:       getEnv("DB_SSLMODE", "disable"),
		K8sNamespace:    getEnv("K8S_NAMESPACE", "fusion"),
		IndexBackendURL: getEnv("INDEX_BACKEND_URL", "http://index-backend.fusion.svc.cluster.local:8080"),
		BuilderImage:    getEnv("BUILDER_IMAGE", "ghcr.io/fusion-platform/venv-builder:latest"),
		JobTTLSeconds:   86400,
		AuthEnabled:     getEnv("AUTH_ENABLED", "false") == "true",
		AuthAudience:    getEnv("AUTH_AUDIENCE", ""),
		AuthAllowedSAs:  splitCSV(getEnv("AUTH_ALLOWED_SA", "")),
		RulesFile:       getEnv("FORGE_RULES_FILE", ""),
	}
}

// DBURL returns the PostgreSQL connection URL.
func (c *Config) DBURL() string {
	return fmt.Sprintf("postgresql://%s:%s@%s:%s/%s?sslmode=%s",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName, c.DBSSLMode)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
