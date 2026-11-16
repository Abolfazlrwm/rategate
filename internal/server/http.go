// Package server
//
// http.go — REST transport (Gin-based) for RateGate.
//
// Routes:
//
//	POST /v1/check          -> check handler (JSON body)
//	GET  /healthz           -> liveness (always 200 once the process is up)
//	GET  /readyz            -> readiness (200 when Server.IsReady())
//	GET  /metrics           -> Prometheus exposition
package server

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Abolfazlrwm/rategate/internal/algorithms"
)

// HTTPServer adapts a [Server] to a Gin engine.
type HTTPServer struct {
	core      *Server
	metricsNS *promHTTPWrapper
}

// promHTTPWrapper lets us inject a custom registry into the promhttp handler.
type promHTTPWrapper struct {
	handler gin.HandlerFunc
}

// NewHTTPServer returns an HTTPServer. metricsHandler is the /metrics
// handler bound to the server's private registry (created by
// observability.NewMetrics).
func NewHTTPServer(core *Server, metricsHandler http.Handler) *HTTPServer {
	return &HTTPServer{
		core:      core,
		metricsNS: &promHTTPWrapper{handler: gin.WrapH(metricsHandler)},
	}
}

// checkRequest is the JSON body for POST /v1/check. It mirrors the gRPC
// CheckRateLimitRequest.
type checkRequest struct {
	Key             string  `json:"key"             binding:"required"`
	LimitName       string  `json:"limit_name"`
	Algorithm       string  `json:"algorithm"`
	Limit           int64   `json:"limit"`
	RefillPerSecond float64 `json:"refill_per_second"`
	WindowMs        int64   `json:"window_ms"`
}

// checkResponse mirrors the gRPC CheckRateLimitResponse.
type checkResponse struct {
	Allowed       bool   `json:"allowed"`
	Remaining     int64  `json:"remaining"`
	Limit         int64  `json:"limit"`
	ResetAtUnixMs int64  `json:"reset_at_unix_ms"`
	RetryAfterMs  int64  `json:"retry_after_ms"`
	Algorithm     string `json:"algorithm"`
}

// Engine returns a configured Gin engine with all routes registered.
func (h *HTTPServer) Engine() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	// Middleware order matters: recovery first (so panics don't kill the
	// process), then logging, then auth.
	r.Use(RecoveryMiddleware(h.core.logger))
	r.Use(LoggingMiddleware(h.core.logger))
	if h.core.cfg.Server.APIKey != "" {
		r.Use(AuthMiddleware(h.core.cfg.Server.APIKey))
	}

	r.POST("/v1/check", h.handleCheck)
	r.GET("/healthz", h.handleHealthz)
	r.GET("/readyz", h.handleReadyz)
	r.GET("/metrics", h.metricsNS.handler)
	h.registerAdminRoutes(r)
	return r
}

// handleCheck implements POST /v1/check.
func (h *HTTPServer) handleCheck(c *gin.Context) {
	var body checkRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	policy := algorithms.Policy{
		Limit:           body.Limit,
		RefillPerSecond: body.RefillPerSecond,
		Window:          time.Duration(body.WindowMs) * time.Millisecond,
	}
	req := CheckRequest{
		Key:        body.Key,
		PolicyName: body.LimitName,
		Algorithm:  body.Algorithm,
		Policy:     policy,
	}

	result, err := h.core.Check(c.Request.Context(), req, time.Now())
	if err != nil {
		if isUnknownPolicyErr(err) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := checkResponse{
		Allowed:       result.Allowed,
		Remaining:     result.Remaining,
		Limit:         result.Limit,
		ResetAtUnixMs: result.ResetAt.UnixMilli(),
		RetryAfterMs:  int64(result.RetryAfter / time.Millisecond),
		Algorithm:     result.Algorithm,
	}
	code := http.StatusOK
	if !result.Allowed {
		code = http.StatusTooManyRequests
		c.Header("Retry-After", formatRetrySeconds(result.RetryAfter))
	}
	// Surface rate-limit headers for client convenience.
	c.Header("X-RateLimit-Limit", itoa(result.Limit))
	c.Header("X-RateLimit-Remaining", itoa(result.Remaining))
	c.Header("X-RateLimit-Reset", itoa(result.ResetAt.Unix()))
	c.JSON(code, resp)
}

// handleHealthz implements GET /healthz (liveness).
func (h *HTTPServer) handleHealthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":     "alive",
		"started_at": h.core.StartedAt().UTC().Format(time.RFC3339),
	})
}

// handleReadyz implements GET /readyz (readiness).
func (h *HTTPServer) handleReadyz(c *gin.Context) {
	st := h.core.Health(c.Request.Context())
	if st == HealthServing {
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
		return
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready"})
}

// itoa avoids importing strconv in the hot path indirection; strconv is
// fine, but a tiny helper keeps the import list tight here.
func itoa(n int64) string {
	// Use FormatInt via a one-liner; we don't avoid strconv, we just alias.
	return strconvFormatInt(n)
}

// formatRetrySeconds converts a Duration to the integer seconds form expected
// by the HTTP Retry-After header (ceil to the next second).
func formatRetrySeconds(d time.Duration) string {
	s := int64((d + time.Second - 1) / time.Second)
	if s < 1 {
		s = 1
	}
	return strconvFormatInt(s)
}
