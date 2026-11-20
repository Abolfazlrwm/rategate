package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAdmin_RESTSeparateAdminKey proves the REST check and admin APIs hold
// independent credentials: with both server.api_key and server.admin_api_key
// set, /v1/check accepts only the client key and /v1/admin/* accepts only the
// admin key (each rejects the other's key), while /healthz stays public.
func TestAdmin_RESTSeparateAdminKey(t *testing.T) {
	core, rest := newTestServer(t)
	core.cfg.Server.APIKey = "client-secret"
	core.cfg.Server.AdminAPIKey = "admin-secret"
	engine := rest.Engine()

	adminBody := `{"name":"p1","algorithm":"tokenbucket","limit":1,"refill_per_second":1}`
	checkBody := `{"key":"a","algorithm":"tokenbucket","limit":1,"refill_per_second":1}`

	post := func(path, body, key string) int {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("x-api-key", key)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w.Code
	}

	if code := post("/v1/admin/policies", adminBody, "admin-secret"); code != http.StatusCreated {
		t.Fatalf("admin with admin key: %d want 201", code)
	}
	if code := post("/v1/admin/policies", adminBody, "client-secret"); code != http.StatusUnauthorized {
		t.Fatalf("admin with client key: %d want 401", code)
	}
	if code := post("/v1/admin/policies", adminBody, ""); code != http.StatusUnauthorized {
		t.Fatalf("admin with no key: %d want 401", code)
	}
	if code := post("/v1/check", checkBody, "client-secret"); code != http.StatusOK {
		t.Fatalf("check with client key: %d want 200", code)
	}
	if code := post("/v1/check", checkBody, "admin-secret"); code != http.StatusUnauthorized {
		t.Fatalf("check with admin key: %d want 401 (admin key must not unlock check)", code)
	}
	if code := post("/v1/check", checkBody, ""); code != http.StatusUnauthorized {
		t.Fatalf("check with no key: %d want 401", code)
	}

	// Healthz stays public even with both keys configured (for kubelet probes).
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("healthz: %d want 200 (public)", w.Code)
	}
}

// TestAdmin_RESTAdminFallsBackToClientKey covers the backward-compatible
// path: when only server.api_key is set (no admin_api_key), admin routes
// accept the shared client key, matching Phase 2 behavior.
func TestAdmin_RESTAdminFallsBackToClientKey(t *testing.T) {
	core, rest := newTestServer(t)
	core.cfg.Server.APIKey = "shared-secret"
	// AdminAPIKey left empty on purpose (overrides the newTestServer default)
	// so the fallback-to-client-key path is what authenticates the call.
	core.cfg.Server.AdminAPIKey = ""
	engine := rest.Engine()

	body := `{"name":"p2","algorithm":"tokenbucket","limit":1,"refill_per_second":1}`
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/policies", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "shared-secret")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("admin with shared key (fallback): %d want 201", w.Code)
	}
}
