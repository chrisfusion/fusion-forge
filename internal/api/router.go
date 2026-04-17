// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"fusion-platform.io/fusion-forge/internal/api/handlers"
	"fusion-platform.io/fusion-forge/internal/api/middleware"
	"fusion-platform.io/fusion-forge/internal/config"
	"fusion-platform.io/fusion-forge/internal/db"
	"fusion-platform.io/fusion-forge/internal/indexclient"
	"fusion-platform.io/fusion-forge/internal/validation"
)

// NewRouter wires up the Gin router with all routes and middleware.
func NewRouter(
	pool *pgxpool.Pool,
	queries *db.Queries,
	k8sCRClient client.Client,
	kubeClient kubernetes.Interface,
	indexClient *indexclient.Client,
	rules validation.Rules,
	gitRules validation.GitRules,
	cfg *config.Config,
) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(corsMiddleware())

	// Health probes (compatible with Quarkus Smallrye Health path convention).
	r.GET("/q/health/live", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "UP"})
	})
	r.GET("/q/health/ready", func(c *gin.Context) {
		if err := pool.Ping(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "DOWN", "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "UP"})
	})

	vh := &handlers.VenvHandler{
		DB:          queries,
		K8sCRClient: k8sCRClient,
		KubeClient:  kubeClient,
		IndexClient: indexClient,
		Rules:       rules,
		Cfg:         cfg,
	}

	gh := &handlers.GitBuildHandler{
		DB:          queries,
		K8sCRClient: k8sCRClient,
		KubeClient:  kubeClient,
		IndexClient: indexClient,
		GitRules:    gitRules,
		Cfg:         cfg,
	}

	v1 := r.Group("/api/v1")
	v1.Use(middleware.NewAuthMiddleware(cfg))

	v1.GET("/venvs", vh.List)
	v1.POST("/venvs", vh.Create)
	v1.POST("/venvs/validate", vh.Validate)
	v1.GET("/venvs/:id", vh.Get)
	v1.GET("/venvs/:id/logs", vh.GetLogs)

	v1.GET("/gitbuilds", gh.List)
	v1.POST("/gitbuilds", gh.Create)
	v1.POST("/gitbuilds/validate", gh.Validate)
	v1.GET("/gitbuilds/:id", gh.Get)
	v1.GET("/gitbuilds/:id/logs", gh.GetLogs)

	return r
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "accept,authorization,content-type,x-requested-with")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
