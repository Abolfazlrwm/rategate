// Package server
//
// middleware.go — Gin middleware: auth, structured logging, panic recovery.
package server

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"
)

// AuthMiddleware rejects requests that don't carry `x-api-key: <expected>`.
// When expected is empty the middleware is a no-op (the caller shouldn't
// install it — see HTTPServer.Engine).
func AuthMiddleware(expected string) gin.HandlerFunc {
	return func(c *gin.Context) {
		got := c.GetHeader("x-api-key")
		if got == "" || got != expected {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or missing api key"})
			return
		}
		c.Next()
	}
}

// LoggingMiddleware logs each request at info level with structured fields.
// It records method, path, status, latency, and client IP.
func LoggingMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()
		fields := []any{
			"method", method,
			"path", path,
			"status", status,
			"latency_ms", latency.Milliseconds(),
			"client_ip", c.ClientIP(),
			"bytes", c.Writer.Size(),
		}
		switch {
		case status >= 500:
			logger.Error("request completed with server error", fields...)
		case status >= 400:
			logger.Warn("request completed with client error", fields...)
		default:
			logger.Info("request completed", fields...)
		}
	}
}

// RecoveryMiddleware catches panics, logs the stack trace, and returns 500.
// No request path is allowed to panic the process — this is a non-negotiable
// production guarantee (see CONTRIBUTING.md "Non-Negotiable Standards").
func RecoveryMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic recovered",
					"error", r,
					"method", c.Request.Method,
					"path", c.Request.URL.Path,
					"stack", string(debug.Stack()),
				)
				if !c.Writer.Written() {
					c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
				}
			}
		}()
		c.Next()
	}
}
