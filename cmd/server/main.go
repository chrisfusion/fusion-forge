// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	migrationsFS "fusion-platform.io/fusion-forge/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"k8s.io/client-go/kubernetes"

	buildv1alpha1 "fusion-platform.io/fusion-forge/api/v1alpha1"
	"fusion-platform.io/fusion-forge/internal/api"
	appconfig "fusion-platform.io/fusion-forge/internal/config"
	"fusion-platform.io/fusion-forge/internal/db"
	"fusion-platform.io/fusion-forge/internal/indexclient"
	"fusion-platform.io/fusion-forge/internal/validation"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(buildv1alpha1.AddToScheme(scheme))
}

func main() {
	cfg := appconfig.Load()
	setupLogger(cfg)

	// Database
	pool, err := pgxpool.New(context.Background(), cfg.DBURL())
	if err != nil {
		slog.Error("connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(context.Background()); err != nil {
		slog.Error("ping database", "error", err)
		os.Exit(1)
	}
	slog.Info("database connected")

	runMigrations(cfg.DBURL())
	queries := db.New(pool)

	// Kubernetes client (reads/writes CIBuild CRs and pod logs).
	k8sCRClient, kubeClient := buildK8sClients()

	// Load builder images from the dedicated ConfigMap so all build types share one image map.
	builderImages, err := loadBuilderImages(context.Background(), kubeClient, cfg.K8sNamespace, cfg.BuilderImagesCM)
	if err != nil {
		slog.Error("load builder images ConfigMap", "name", cfg.BuilderImagesCM, "error", err)
		os.Exit(1)
	}
	cfg.BuilderImages = builderImages
	slog.Info("loaded builder images", "count", len(builderImages))

	// fusion-index client
	indexClient := indexclient.New(cfg.IndexBackendURL)

	// Validation rules
	rules := validation.LoadRules(cfg.RulesFile)
	slog.Info("loaded validation rules",
		"exact_pinning", rules.RequireExactPinning,
		"max_packages", rules.MaxPackages,
		"banned_count", len(rules.BannedPackages))

	gitRules := validation.LoadGitRules(cfg.GitRulesFile)
	slog.Info("loaded git rules",
		"require_pyproject", gitRules.RequirePyprojectToml,
		"require_src_dir", gitRules.RequireSrcDir)

	// HTTP server
	router := api.NewRouter(pool, queries, k8sCRClient, kubeClient, indexClient, rules, gitRules, cfg)
	addr := fmt.Sprintf(":%s", cfg.Port)
	slog.Info("starting fusion-forge server", "addr", addr)
	if err := router.Run(addr); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

func setupLogger(cfg *appconfig.Config) {
	var level slog.Level
	unknownLevel := false
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	case "info", "":
		level = slog.LevelInfo
	default:
		level = slog.LevelInfo
		unknownLevel = true
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if cfg.LogFormat == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(handler))

	if unknownLevel {
		slog.Warn("unrecognised LOG_LEVEL, defaulting to info", "value", cfg.LogLevel)
	}
}

func runMigrations(dbURL string) {
	src, err := iofs.New(migrationsFS.FS, ".")
	if err != nil {
		slog.Error("create migration source", "error", err)
		os.Exit(1)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dbURL)
	if err != nil {
		slog.Error("create migrator", "error", err)
		os.Exit(1)
	}
	defer m.Close()
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		slog.Error("run migrations", "error", err)
		os.Exit(1)
	}
	slog.Info("migrations applied")
}

// loadBuilderImages reads key→image-URL pairs from a K8s ConfigMap.
func loadBuilderImages(ctx context.Context, kubeClient kubernetes.Interface, namespace, cmName string) (map[string]string, error) {
	cm, err := kubeClient.CoreV1().ConfigMaps(namespace).Get(ctx, cmName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get ConfigMap %s/%s: %w", namespace, cmName, err)
	}
	images := make(map[string]string, len(cm.Data))
	for k, v := range cm.Data {
		if v != "" {
			images[k] = v
		}
	}
	return images, nil
}

// buildK8sClients sets up the controller-runtime CR client and the typed kubernetes client.
func buildK8sClients() (client.Client, kubernetes.Interface) {
	k8sCfg, err := ctrl.GetConfig()
	if err != nil {
		slog.Error("get kubernetes config", "error", err)
		os.Exit(1)
	}

	crClient, err := client.New(k8sCfg, client.Options{Scheme: scheme})
	if err != nil {
		slog.Error("create CR client", "error", err)
		os.Exit(1)
	}

	kubeClient, err := kubernetes.NewForConfig(k8sCfg)
	if err != nil {
		slog.Error("create kubernetes client", "error", err)
		os.Exit(1)
	}

	return crClient, kubeClient
}
