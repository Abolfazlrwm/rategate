package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Abolfazlrwm/rategate/internal/algorithms"
	"github.com/Abolfazlrwm/rategate/internal/config"
	"github.com/Abolfazlrwm/rategate/internal/observability"
	"github.com/Abolfazlrwm/rategate/internal/store"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// newTestServer builds a Server backed by an in-memory store for tests.
// Logs are discarded to keep test output clean.
func newTestServer(t *testing.T) (*Server, *HTTPServer) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Store.Backend = "memory"
	cfg.Store.DefaultAlgorithm = "tokenbucket"
	// Admin routes are deny-by-default, so admin CRUD tests authenticate
	// with this key; tests that exercise the disabled path override it.
	cfg.Server.AdminAPIKey = "admin-test-key"
	cfg.Policies = []config.PolicyConfig{
		{Name: "strict", Algorithm: "tokenbucket", Limit: 2, RefillPerSecond: 1},
		{Name: "sw", Algorithm: "slidingwindow", Limit: 2, Window: time.Minute},
	}

	metrics, reg := observability.NewMetrics()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := store.NewMemoryStore(store.MemoryOptions{TBShards: 4, SWShards: 4})
	t.Cleanup(func() { _ = s.Close() })
	core := New(cfg, s, metrics, logger)
	core.MarkReady()

	metricsHandler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg})
	rest := NewHTTPServer(core, metricsHandler)
	return core, rest
}

func TestHTTPServer_CheckAllowed(t *testing.T) {
	_, rest := newTestServer(t)
	engine := rest.Engine()

	body := `{"key":"u1","algorithm":"tokenbucket","limit":3,"refill_per_second":1}`
	req := httptest.NewRequest(http.MethodPost, "/v1/check", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200, body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["allowed"] != true {
		t.Fatalf("allowed=%v want true", resp["allowed"])
	}
	if resp["algorithm"] != "tokenbucket" {
		t.Fatalf("algorithm=%v want tokenbucket", resp["algorithm"])
	}
	if w.Header().Get("X-RateLimit-Limit") != "3" {
		t.Fatalf("limit header=%q want 3", w.Header().Get("X-RateLimit-Limit"))
	}
}

func TestHTTPServer_CheckDenied429(t *testing.T) {
	_, rest := newTestServer(t)
	engine := rest.Engine()

	// Policy "strict" allows 2 then denies.
	for i := 0; i < 2; i++ {
		body := `{"key":"d","limit_name":"strict"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/check", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("check %d status=%d want 200", i, w.Code)
		}
	}
	// 3rd denied.
	body := `{"key":"d","limit_name":"strict"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/check", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd status=%d want 429", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header should be set on 429")
	}
}

func TestHTTPServer_CheckBadKey(t *testing.T) {
	_, rest := newTestServer(t)
	engine := rest.Engine()

	// Missing key -> 400.
	body := `{"algorithm":"tokenbucket","limit":1,"refill_per_second":1}`
	req := httptest.NewRequest(http.MethodPost, "/v1/check", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

func TestHTTPServer_CheckUnknownPolicy(t *testing.T) {
	_, rest := newTestServer(t)
	engine := rest.Engine()

	body := `{"key":"k","limit_name":"does-not-exist"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/check", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 (unknown policy)", w.Code)
	}
}

func TestHTTPServer_HealthzReady(t *testing.T) {
	_, rest := newTestServer(t)
	engine := rest.Engine()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("healthz status=%d want 200", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("readyz status=%d want 200", w.Code)
	}
}

func TestHTTPServer_ReadyNotServingWhenUnready(t *testing.T) {
	core, rest := newTestServer(t)
	engine := rest.Engine()

	core.MarkNotReady()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz status=%d want 503 when not ready", w.Code)
	}
}

func TestHTTPServer_Metrics(t *testing.T) {
	_, rest := newTestServer(t)
	engine := rest.Engine()

	// Make a check so the counters are non-zero.
	body := `{"key":"m","algorithm":"tokenbucket","limit":1,"refill_per_second":1}`
	req := httptest.NewRequest(http.MethodPost, "/v1/check", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	// Scrape metrics.
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("metrics status=%d want 200", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("rategate_requests_total")) {
		t.Fatal("metrics output missing rategate_requests_total")
	}
}

func TestHTTPServer_AuthMiddlewareRejects(t *testing.T) {
	core, rest := newTestServer(t)
	core.cfg.Server.APIKey = "secret"
	engine := rest.Engine()

	body := `{"key":"a","algorithm":"tokenbucket","limit":1,"refill_per_second":1}`
	req := httptest.NewRequest(http.MethodPost, "/v1/check", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 without api key", w.Code)
	}

	// With correct key it passes.
	req = httptest.NewRequest(http.MethodPost, "/v1/check", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "secret")
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 with api key", w.Code)
	}
}

func TestServer_CheckInline(t *testing.T) {
	core, _ := newTestServer(t)
	now := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	// Inline token bucket.
	d, err := core.Check(context.Background(), CheckRequest{
		Key:       "inline",
		Algorithm: "tokenbucket",
		Policy:    algorithms.Policy{Limit: 1, RefillPerSecond: 1},
	}, now)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !d.Allowed {
		t.Fatal("first should be allowed")
	}
	if d.Algorithm != "tokenbucket" {
		t.Fatalf("algorithm=%q want tokenbucket", d.Algorithm)
	}

	// Second denied.
	d2, _ := core.Check(context.Background(), CheckRequest{
		Key:       "inline",
		Algorithm: "tokenbucket",
		Policy:    algorithms.Policy{Limit: 1, RefillPerSecond: 1},
	}, now)
	if d2.Allowed {
		t.Fatal("second should be denied")
	}
}

func TestServer_CheckDefaultAlgorithmWhenEmpty(t *testing.T) {
	core, _ := newTestServer(t)
	d, err := core.Check(context.Background(), CheckRequest{
		Key:    "def",
		Policy: algorithms.Policy{Limit: 5, RefillPerSecond: 1},
	}, time.Now())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if d.Algorithm != "tokenbucket" {
		t.Fatalf("default algorithm=%q want tokenbucket", d.Algorithm)
	}
}

func TestServer_CheckRequiresKey(t *testing.T) {
	core, _ := newTestServer(t)
	_, err := core.Check(context.Background(), CheckRequest{}, time.Now())
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestServer_HealthString(t *testing.T) {
	if HealthServing.String() != "SERVING" {
		t.Fatal("bad serving string")
	}
	if HealthNotServing.String() != "NOT_SERVING" {
		t.Fatal("bad not-serving string")
	}
	if HealthUnknown.String() != "SERVING_STATUS_UNSPECIFIED" {
		t.Fatal("bad unknown string")
	}
}

func TestIsUnknownPolicyErr(t *testing.T) {
	if !isUnknownPolicyErr(errUnknownPolicy("foo")) {
		t.Fatal("should match")
	}
	if isUnknownPolicyErr(errOther()) {
		t.Fatal("should not match other error")
	}
}

// helpers to construct errors for the matcher test.
func errUnknownPolicy(name string) error {
	return &policyErr{msg: "unknown policy \"" + name + "\""}
}
func errOther() error { return &policyErr{msg: "something else"} }

type policyErr struct{ msg string }

func (e *policyErr) Error() string { return e.msg }
