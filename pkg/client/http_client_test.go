package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestServer(t *testing.T, status int, resp any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"alive"}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Retry-After", "2")
		w.Header().Set("X-RateLimit-Limit", "5")
		w.Header().Set("X-RateLimit-Remaining", "4")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestHTTPClient_CheckAllowed(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, CheckResponse{
		Allowed: true, Remaining: 4, Limit: 5, Algorithm: "tokenbucket",
	})
	defer srv.Close()

	c := NewHTTPClient(srv.URL, WithHTTPAPIKey("k"), WithHTTPTimeout(time.Second))
	resp, err := c.Check(context.Background(), CheckRequest{Key: "u", Algorithm: "tokenbucket", Limit: 5, RefillPerSecond: 1})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !resp.Allowed {
		t.Fatal("expected allowed")
	}
	// Retry-After header should populate RetryAfterMs.
	if resp.RetryAfterMs != 2000 {
		t.Fatalf("retry_after_ms=%d want 2000 (from header)", resp.RetryAfterMs)
	}
}

func TestHTTPClient_CheckDenied(t *testing.T) {
	// The 429 body carries the structured decision; the Retry-After header
	// carries the backoff. Both must reach the caller on the *HTTPError.
	srv := newTestServer(t, http.StatusTooManyRequests, CheckResponse{
		Allowed: false, Remaining: 0, Limit: 5, RetryAfterMs: 1500, ResetAtUnixMs: 999, Algorithm: "tokenbucket",
	})
	defer srv.Close()

	c := NewHTTPClient(srv.URL)
	resp, err := c.Check(context.Background(), CheckRequest{Key: "u"})
	if resp != nil {
		t.Fatalf("expected nil resp on 429, got %+v", resp)
	}
	if err == nil {
		t.Fatal("expected error on 429")
	}
	httpErr, ok := err.(*HTTPError)
	if !ok {
		t.Fatalf("err type=%T want *HTTPError", err)
	}
	if !httpErr.IsDenied() {
		t.Fatal("IsDenied should be true for 429")
	}
	if httpErr.Status != http.StatusTooManyRequests {
		t.Fatalf("status=%d want 429", httpErr.Status)
	}
	// Rate-limit metadata must survive the 429 so callers can back off.
	if httpErr.Limit != 5 {
		t.Fatalf("limit=%d want 5 (from body)", httpErr.Limit)
	}
	if httpErr.Remaining != 0 {
		t.Fatalf("remaining=%d want 0 (from body)", httpErr.Remaining)
	}
	if httpErr.ResetAtUnixMs != 999 {
		t.Fatalf("reset_at_unix_ms=%d want 999 (from body)", httpErr.ResetAtUnixMs)
	}
	// Retry-After header (2s) takes precedence over the body's 1500ms.
	if httpErr.RetryAfterMs != 2000 {
		t.Fatalf("retry_after_ms=%d want 2000 (Retry-After header overrides body)", httpErr.RetryAfterMs)
	}
}

func TestHTTPClient_CheckDeniedHeaderOnly(t *testing.T) {
	// A 429 with a bare error body (no structured fields) still surfaces the
	// Retry-After header so the caller can back off.
	srv := newTestServer(t, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
	defer srv.Close()

	c := NewHTTPClient(srv.URL)
	_, err := c.Check(context.Background(), CheckRequest{Key: "u"})
	if err == nil {
		t.Fatal("expected error on 429")
	}
	httpErr, ok := err.(*HTTPError)
	if !ok {
		t.Fatalf("err type=%T want *HTTPError", err)
	}
	if !httpErr.IsDenied() {
		t.Fatal("IsDenied should be true for 429")
	}
	if httpErr.RetryAfterMs != 2000 {
		t.Fatalf("retry_after_ms=%d want 2000 (from Retry-After header)", httpErr.RetryAfterMs)
	}
	if httpErr.Limit != 0 || httpErr.Remaining != 0 {
		t.Fatalf("rate-limit fields should be zero for a bare error body, got limit=%d remaining=%d", httpErr.Limit, httpErr.Remaining)
	}
}

func TestHTTPClient_CheckServerError(t *testing.T) {
	srv := newTestServer(t, http.StatusInternalServerError, map[string]string{"error": "boom"})
	defer srv.Close()

	c := NewHTTPClient(srv.URL)
	_, err := c.Check(context.Background(), CheckRequest{Key: "u"})
	if err == nil {
		t.Fatal("expected error on 500")
	}
	httpErr, ok := err.(*HTTPError)
	if !ok {
		t.Fatalf("err type=%T want *HTTPError", err)
	}
	if httpErr.IsDenied() {
		t.Fatal("IsDenied should be false for 500")
	}
}

func TestHTTPClient_HealthOK(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, nil)
	defer srv.Close()
	c := NewHTTPClient(srv.URL)
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("health: %v", err)
	}
}

func TestHTTPClient_HealthBad(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := NewHTTPClient(srv.URL)
	if err := c.Health(context.Background()); err == nil {
		t.Fatal("expected error on unhealthy")
	}
}

func TestHTTPClient_WithHTTPClient(t *testing.T) {
	custom := &http.Client{Timeout: 5 * time.Second}
	c := NewHTTPClient("http://example", WithHTTPClient(custom))
	if c.http != custom {
		t.Fatal("custom http client not applied")
	}
}

func TestHTTPError_MessageFormats(t *testing.T) {
	e1 := &HTTPError{Status: 429, Message: "rate limit exceeded"}
	if e1.Error() == "" {
		t.Fatal("error message empty")
	}
	e2 := &HTTPError{Status: 500, Body: "raw body"}
	if e2.Error() == "" {
		t.Fatal("error message empty for body-only")
	}
}
