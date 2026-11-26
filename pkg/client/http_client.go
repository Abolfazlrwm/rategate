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

// Check performs a rate-limit check. Returns the parsed response, or an error
// when the call could not be completed. A denied request (HTTP 429) comes back
// as an *HTTPError whose IsDenied reports true and whose Remaining, Limit,
// ResetAtUnixMs and RetryAfterMs fields carry the server's backoff signal,
// parsed from the response body and the Retry-After header.
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
	var errBody struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &errBody)

	// A 429 is a rate-limit denial, not a transport failure: the body carries
	// the structured decision (allowed:false, remaining, limit, retry_after_ms,
	// reset_at_unix_ms) and the Retry-After header carries the backoff hint.
	// Surface them on the *HTTPError so callers can honour the backoff instead
	// of retrying blindly. Other >=400 statuses stay plain transport errors.
	if resp.StatusCode == http.StatusTooManyRequests {
		var d CheckResponse
		_ = json.Unmarshal(raw, &d) // best-effort: body may be a bare error
		hErr := &HTTPError{
			Status:        resp.StatusCode,
			Message:       errBody.Error,
			Body:          string(raw),
			Remaining:     d.Remaining,
			Limit:         d.Limit,
			ResetAtUnixMs: d.ResetAtUnixMs,
			RetryAfterMs:  d.RetryAfterMs,
		}
		// Retry-After header (seconds) takes precedence, matching the success path.
		if v := resp.Header.Get("Retry-After"); v != "" {
			if secs, err := strconv.Atoi(v); err == nil {
				hErr.RetryAfterMs = int64(secs) * 1000
			}
		}
		return nil, hErr
	}
	if resp.StatusCode >= 400 {
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
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
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

// HTTPError is returned for non-2xx responses. For a 429 denial it also
// carries the rate-limit metadata parsed from the response body and the
// Retry-After header (Remaining, Limit, ResetAtUnixMs, RetryAfterMs) so
// callers can honour the server's backoff. These fields are zero on other
// (transport) errors.
type HTTPError struct {
	Status  int
	Message string
	Body    string
	// Populated on 429; zero on other errors.
	Remaining     int64
	Limit         int64
	ResetAtUnixMs int64
	RetryAfterMs  int64
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
