// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	buildv1alpha1 "fusion-platform.io/fusion-forge/api/v1alpha1"
	appconfig "fusion-platform.io/fusion-forge/internal/config"
	"fusion-platform.io/fusion-forge/internal/controller"
	"fusion-platform.io/fusion-forge/internal/db"
	"fusion-platform.io/fusion-forge/internal/indexclient"
	"fusion-platform.io/fusion-forge/internal/validation"

	"github.com/jackc/pgx/v5/pgxpool"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(buildv1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr string
		probeAddr   string
		namespace   string
		leaderElect bool
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8083", "Address the metrics endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8084", "Address the health probe endpoint binds to.")
	flag.StringVar(&namespace, "namespace", "fusion", "Kubernetes namespace the watcher manages.")
	flag.BoolVar(&leaderElect, "leader-elect", false, "Enable leader election for the watcher manager.")
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	logger := ctrl.Log.WithName("main")

	cfg := appconfig.Load()

	// Database
	pool, err := pgxpool.New(context.Background(), cfg.DBURL())
	if err != nil {
		logger.Error(err, "connect to database")
		os.Exit(1)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		logger.Error(err, "ping database")
		os.Exit(1)
	}
	queries := db.New(pool)

	// Kubernetes clients
	k8sCfg, err := ctrl.GetConfig()
	if err != nil {
		logger.Error(err, "get kubernetes config")
		os.Exit(1)
	}
	kubeClient, err := kubernetes.NewForConfig(k8sCfg)
	if err != nil {
		logger.Error(err, "create kubernetes client")
		os.Exit(1)
	}

	// Builder images ConfigMap
	builderImages, err := loadBuilderImages(context.Background(), kubeClient, cfg.K8sNamespace, cfg.BuilderImagesCM)
	if err != nil {
		logger.Error(err, "load builder images ConfigMap", "name", cfg.BuilderImagesCM)
		os.Exit(1)
	}
	cfg.BuilderImages = builderImages
	logger.Info("loaded builder images", "count", len(builderImages))

	// fusion-index client
	idxClient := indexclient.New(cfg.IndexBackendURL)

	// Git structure rules
	gitRules := validation.LoadGitRules(cfg.GitRulesFile)
	logger.Info("loaded git rules",
		"require_pyproject", gitRules.RequirePyprojectToml,
		"require_src_dir", gitRules.RequireSrcDir)

	// controller-runtime client (for CIBuild / GitWatcher CRs)
	crClient, err := client.New(k8sCfg, client.Options{Scheme: scheme})
	if err != nil {
		logger.Error(err, "create CR client")
		os.Exit(1)
	}
	_ = crClient // manager provides its own cached client; crClient unused here

	pollInterval := time.Duration(cfg.WatcherPollInterval) * time.Second
	maxFailures := cfg.WatcherMaxFailures
	if maxFailures <= 0 {
		maxFailures = 2
	}

	mgr, err := ctrl.NewManager(k8sCfg, ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "watcher.build.fusion-platform.io",
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				namespace: {},
			},
		},
	})
	if err != nil {
		logger.Error(err, "unable to create manager")
		os.Exit(1)
	}

	if err := (&controller.GitWatcherReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		DB:           queries,
		IndexClient:  idxClient,
		Cfg:          cfg,
		GitRules:     gitRules,
		PollInterval: pollInterval,
		MaxFailures:  maxFailures,
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to set up GitWatcher controller")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		logger.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		logger.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	logger.Info("starting fusion-forge watcher",
		"namespace", namespace,
		"pollInterval", pollInterval,
		"maxFailures", maxFailures)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Error(err, "problem running watcher manager")
		os.Exit(1)
	}
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
