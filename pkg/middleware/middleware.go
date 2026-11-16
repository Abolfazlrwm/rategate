// Package middleware provides HTTP framework integrations that gate requests
// through a RateGate client.
//
// Three adapters ship in Phase 1:
//
//   - [GinMiddleware] — for github.com/gin-gonic/gin
//   - [StdlibMiddleware] — for net/http
//   - [EchoMiddleware] — stub for labstack/echo (gated behind a build tag in
//     Phase 2; the v0.4.0 release wires it fully)
//
// The middleware calls Check once per request with a caller-supplied
// KeyFunc. On denial it writes 429 with Retry-After; on error it fails open
// (allows the request) and logs, because a rate-limiter outage should not
// take down the protected service. Override with [WithFailClosed].
package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Abolfazlrwm/rategate/pkg/client"
)

// Limiter is the minimal subset of *client.HTTPClient (or GRPCClient) that
// the middleware needs. Both clients satisfy it.
type Limiter interface {
	Check(ctx context.Context, req client.CheckRequest) (*client.CheckResponse, error)
}

// Options configures the middleware.
type Options struct {
	// KeyFunc extracts the rate-limit key from the request (e.g. user ID,
	// tenant ID, IP). Required.
	KeyFunc func(r *http.Request) string

	// PolicyName references a pre-configured server-side policy. If empty,
	// the inline Algorithm/Limit/Refill/Window are used.
	PolicyName string

	// Inline policy (used when PolicyName is empty).
	Algorithm string
	Limit     int64
	Refill    float64
	Window    time.Duration

	// FailClosed, when true, denies the request (503) if the Check call
	// errors. Default false (fail open).
	FailClosed bool

	// Logger for fail-open/fail-closed events.
	Logger *slog.Logger
}

// GinMiddleware returns a Gin handler that rate-limits incoming requests.
func GinMiddleware(l Limiter, opts Options) gin.HandlerFunc { //nolint:gocritic // value keeps the public API unchanged; the 80B copy happens once per registration, not per request
	if opts.KeyFunc == nil {
		opts.KeyFunc = func(r *http.Request) string { return r.RemoteAddr }
	}
	return func(c *gin.Context) {
		key := opts.KeyFunc(c.Request)
		req := buildRequest(key, &opts)
		resp, err := l.Check(c.Request.Context(), req)
		if err != nil {
			handleError(c, &opts, err)
			return
		}
		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", resp.Limit))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", resp.Remaining))
		if resp.ResetAtUnixMs > 0 {
			c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", resp.ResetAtUnixMs/1000))
		}
		if !resp.Allowed {
			c.Header("Retry-After", fmt.Sprintf("%d", retrySeconds(resp.RetryAfterMs)))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":          "rate limit exceeded",
				"retry_after_ms": resp.RetryAfterMs,
			})
			return
		}
		c.Next()
	}
}

// StdlibMiddleware wraps an http.Handler with rate limiting.
func StdlibMiddleware(l Limiter, opts Options, h http.Handler) http.Handler { //nolint:gocritic // value keeps the public API unchanged; the 80B copy happens once per registration, not per request
	if opts.KeyFunc == nil {
		opts.KeyFunc = func(r *http.Request) string { return r.RemoteAddr }
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := opts.KeyFunc(r)
		req := buildRequest(key, &opts)
		resp, err := l.Check(r.Context(), req)
		if err != nil {
			if opts.FailClosed {
				http.Error(w, "rate limiter unavailable", http.StatusServiceUnavailable)
				return
			}
			if opts.Logger != nil {
				opts.Logger.Warn("rate limiter error, failing open", "error", err)
			}
			h.ServeHTTP(w, r)
			return
		}
		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", resp.Limit))
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", resp.Remaining))
		if resp.ResetAtUnixMs > 0 {
			w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", resp.ResetAtUnixMs/1000))
		}
		if !resp.Allowed {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retrySeconds(resp.RetryAfterMs)))
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limit exceeded"}`))
			return
		}
		h.ServeHTTP(w, r)
	})
}

// buildRequest constructs a client.CheckRequest from the middleware options.
func buildRequest(key string, opts *Options) client.CheckRequest {
	return client.CheckRequest{
		Key:             key,
		LimitName:       opts.PolicyName,
		Algorithm:       opts.Algorithm,
		Limit:           opts.Limit,
		RefillPerSecond: opts.Refill,
		WindowMs:        opts.Window.Milliseconds(),
	}
}

// handleError is shared by the Gin middleware for the error path.
func handleError(c *gin.Context, opts *Options, err error) {
	if opts.FailClosed {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "rate limiter unavailable"})
		return
	}
	if opts.Logger != nil {
		opts.Logger.Warn("rate limiter error, failing open", "error", err)
	}
	c.Next()
}

// retrySeconds converts a retry-after-ms value to whole seconds (ceil),
// bounded to a minimum of 1 for the HTTP Retry-After header.
func retrySeconds(retryAfterMs int64) int {
	s := retryAfterMs / 1000
	if retryAfterMs%1000 != 0 {
		s++
	}
	if s < 1 {
		s = 1
	}
	return int(s)
}
