// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

const loggerKey = "slog_logger"

// NewLoggingMiddleware returns a Gin middleware that generates a request ID,
// stamps a per-request *slog.Logger with {request_id, method, path, client_ip},
// stores it in gin.Context, and logs the access line after the handler returns.
func NewLoggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		b := make([]byte, 8)
		_, _ = rand.Read(b)
		reqID := hex.EncodeToString(b)

		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}

		logger := slog.Default().With(
			"request_id", reqID,
			"method", c.Request.Method,
			"path", path,
			"client_ip", c.ClientIP(),
		)
		c.Set(loggerKey, logger)

		c.Next()

		logger.Info("request",
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
		)
	}
}

// LoggerFromCtx returns the per-request logger set by NewLoggingMiddleware.
// Falls back to slog.Default() if the middleware was not applied.
func LoggerFromCtx(c *gin.Context) *slog.Logger {
	v, exists := c.Get(loggerKey)
	if !exists {
		return slog.Default()
	}
	if logger, ok := v.(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}
