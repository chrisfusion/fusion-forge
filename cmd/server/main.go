// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
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

	// Database
	pool, err := pgxpool.New(context.Background(), cfg.DBURL())
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(context.Background()); err != nil {
		log.Fatalf("ping database: %v", err)
	}
	log.Println("database connected")

	runMigrations(cfg.DBURL())
	queries := db.New(pool)

	// Kubernetes client (reads/writes CIBuild CRs and pod logs).
	k8sCRClient, kubeClient := buildK8sClients()

	// fusion-index client
	indexClient := indexclient.New(cfg.IndexBackendURL)

	// Validation rules
	rules := validation.LoadRules(cfg.RulesFile)
	log.Printf("forge: loaded validation rules (exactPin=%v, maxPkg=%d, banned=%d)",
		rules.RequireExactPinning, rules.MaxPackages, len(rules.BannedPackages))

	// HTTP server
	router := api.NewRouter(pool, queries, k8sCRClient, kubeClient, indexClient, rules, cfg)
	addr := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("starting fusion-forge server on %s", addr)
	if err := router.Run(addr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func runMigrations(dbURL string) {
	m, err := migrate.New("file://migrations", dbURL)
	if err != nil {
		log.Fatalf("create migrator: %v", err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		log.Fatalf("run migrations: %v", err)
	}
	log.Println("migrations applied")
}

// buildK8sClients sets up the controller-runtime CR client and the typed kubernetes client.
// Both are required for the server to create CIBuild CRs and read pod logs.
func buildK8sClients() (client.Client, kubernetes.Interface) {
	k8sCfg, err := ctrl.GetConfig()
	if err != nil {
		log.Fatalf("get kubernetes config: %v", err)
	}

	crClient, err := client.New(k8sCfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Fatalf("create CR client: %v", err)
	}

	kubeClient, err := kubernetes.NewForConfig(k8sCfg)
	if err != nil {
		log.Fatalf("create kubernetes client: %v", err)
	}

	return crClient, kubeClient
}
