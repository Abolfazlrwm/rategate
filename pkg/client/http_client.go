// Package client provides a Go SDK for talking to a RateGate server over
// either gRPC or REST.
//
// Two clients ship in Phase 1:
//
//   - [HTTPClient] — a thin JSON-over-HTTP client for the REST API. Use this
//     for quick integrations and for services that already speak HTTP.
//   - [GRPCClient] — a generated gRPC client wrapper with the same surface.
//     Use this for the lowest-latency integration in Go services.
//
// Both clients are safe for concurrent use.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// HTTPClient talks to the RateGate REST API.
type HTTPClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// HTTPOption configures a new HTTPClient.
type HTTPOption func(*HTTPClient)

// WithHTTPAPIKey sets the x-api-key header on every request.
func WithHTTPAPIKey(key string) HTTPOption {
	return func(c *HTTPClient) { c.apiKey = key }
}

// WithHTTPTimeout sets the per-request timeout. Default 2s.
func WithHTTPTimeout(d time.Duration) HTTPOption {
	return func(c *HTTPClient) { c.http.Timeout = d }
}

// WithHTTPClient supplies a pre-configured *http.Client (e.g. for custom
// transports or connection pooling).
func WithHTTPClient(h *http.Client) HTTPOption {
	return func(c *HTTPClient) { c.http = h }
}

// NewHTTPClient returns an HTTPClient targeting baseURL.
func NewHTTPClient(baseURL string, opts ...HTTPOption) *HTTPClient {
	c := &HTTPClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 2 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// CheckRequest is the JSON body for a check call.
type CheckRequest struct {
	Key             string  `json:"key"`
	LimitName       string  `json:"limit_name,omitempty"`
	Algorithm       string  `json:"algorithm,omitempty"`
	Limit           int64   `json:"limit,omitempty"`
	RefillPerSecond float64 `json:"refill_per_second,omitempty"`
	WindowMs        int64   `json:"window_ms,omitempty"`
}

// CheckResponse mirrors the server's response.
type CheckResponse struct {
	Allowed       bool   `json:"allowed"`
	Remaining     int64  `json:"remaining"`
	Limit         int64  `json:"limit"`
	ResetAtUnixMs int64  `json:"reset_at_unix_ms"`
	RetryAfterMs  int64  `json:"retry_after_ms"`
	Algorithm     string `json:"algorithm"`
}

// Check performs a rate-limit check. Returns the parsed response or an error
// (including a *RateLimitError when the request was denied, so callers can
// type-switch).
func (c *HTTPClient) Check(ctx context.Context, req CheckRequest) (*CheckResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/check", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("x-api-key", c.apiKey)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &errBody)
		return nil, &HTTPError{Status: resp.StatusCode, Message: errBody.Error, Body: string(raw)}
	}

	var out CheckResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	// Populate Retry-After / X-RateLimit-* from headers if present.
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			out.RetryAfterMs = int64(secs) * 1000
		}
	}
	return &out, nil
}

// Health pings the /healthz endpoint and returns nil if healthy.
func (c *HTTPClient) Health(ctx context.Context) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", http.NoBody)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz status %d", resp.StatusCode)
	}
	return nil
}

// HTTPError is returned for non-2xx responses.
type HTTPError struct {
	Status  int
	Message string
	Body    string
}

// Error implements error.
func (e *HTTPError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("rategate http %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("rategate http %d: %s", e.Status, e.Body)
}

// IsDenied returns true when the error represents a 429 denial.
func (e *HTTPError) IsDenied() bool { return e != nil && e.Status == http.StatusTooManyRequests }
