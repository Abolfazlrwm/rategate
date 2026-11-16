package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// doJSON issues a request with an optional JSON body against engine and
// returns the recorder and the decoded JSON map (or nil on empty body).
func doJSON(t *testing.T, engine http.Handler, method, path string, body any) (rec *httptest.ResponseRecorder, parsed map[string]any) {
	t.Helper()
	var buf *bytes.Buffer
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		buf = bytes.NewBuffer(b)
	} else {
		buf = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, path, buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	var out map[string]any
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &out)
	}
	return w, out
}

func TestAdmin_ListSeededPolicies(t *testing.T) {
	_, rest := newTestServer(t)
	engine := rest.Engine()
	w, out := doJSON(t, engine, http.MethodGet, "/v1/admin/policies", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	arr, _ := out["policies"].([]any)
	if len(arr) != 2 {
		t.Fatalf("policies len=%d want 2 (strict, sw)", len(arr))
	}
}

func TestAdmin_CreateAndGet(t *testing.T) {
	_, rest := newTestServer(t)
	engine := rest.Engine()

	body := map[string]any{
		"name": "newpol", "algorithm": "tokenbucket",
		"limit": 7, "refill_per_second": 2,
	}
	w, resp := doJSON(t, engine, http.MethodPost, "/v1/admin/policies", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	if resp["name"] != "newpol" {
		t.Fatalf("create response name=%v want newpol", resp["name"])
	}

	// GET it back.
	w, resp = doJSON(t, engine, http.MethodGet, "/v1/admin/policies/newpol", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get status=%d", w.Code)
	}
	if resp["limit"].(float64) != 7 {
		t.Fatalf("limit=%v want 7", resp["limit"])
	}
}

func TestAdmin_CreateDuplicateConflict(t *testing.T) {
	_, rest := newTestServer(t)
	engine := rest.Engine()
	body := map[string]any{
		"name": "strict", "algorithm": "tokenbucket",
		"limit": 1, "refill_per_second": 1,
	}
	w, _ := doJSON(t, engine, http.MethodPost, "/v1/admin/policies", body)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate create status=%d want 409", w.Code)
	}
}

func TestAdmin_CreateInvalidPolicy(t *testing.T) {
	_, rest := newTestServer(t)
	engine := rest.Engine()
	body := map[string]any{
		"name": "bad", "algorithm": "tokenbucket",
		"limit": 0, "refill_per_second": 1,
	}
	w, _ := doJSON(t, engine, http.MethodPost, "/v1/admin/policies", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid create status=%d want 400", w.Code)
	}
}

func TestAdmin_UpdateExisting(t *testing.T) {
	_, rest := newTestServer(t)
	engine := rest.Engine()
	body := map[string]any{
		"name": "strict", "algorithm": "tokenbucket",
		"limit": 50, "refill_per_second": 10,
	}
	w, resp := doJSON(t, engine, http.MethodPut, "/v1/admin/policies/strict", body)
	if w.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", w.Code, w.Body.String())
	}
	if resp["limit"].(float64) != 50 {
		t.Fatalf("update response limit=%v want 50", resp["limit"])
	}
	// Verify via GET.
	_, resp = doJSON(t, engine, http.MethodGet, "/v1/admin/policies/strict", nil)
	if resp["limit"].(float64) != 50 {
		t.Fatalf("post-update get limit=%v want 50", resp["limit"])
	}
}

func TestAdmin_UpdateMissingNotFound(t *testing.T) {
	_, rest := newTestServer(t)
	engine := rest.Engine()
	body := map[string]any{
		"name": "ghost", "algorithm": "tokenbucket",
		"limit": 1, "refill_per_second": 1,
	}
	w, _ := doJSON(t, engine, http.MethodPut, "/v1/admin/policies/ghost", body)
	if w.Code != http.StatusNotFound {
		t.Fatalf("update missing status=%d want 404", w.Code)
	}
}

func TestAdmin_UpdateNameMismatch(t *testing.T) {
	_, rest := newTestServer(t)
	engine := rest.Engine()
	body := map[string]any{
		"name": "other", "algorithm": "tokenbucket",
		"limit": 1, "refill_per_second": 1,
	}
	w, _ := doJSON(t, engine, http.MethodPut, "/v1/admin/policies/strict", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("name mismatch status=%d want 400", w.Code)
	}
}

func TestAdmin_DeleteThenGet(t *testing.T) {
	_, rest := newTestServer(t)
	engine := rest.Engine()
	w, _ := doJSON(t, engine, http.MethodDelete, "/v1/admin/policies/sw", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d want 204", w.Code)
	}
	w, _ = doJSON(t, engine, http.MethodGet, "/v1/admin/policies/sw", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete status=%d want 404", w.Code)
	}
}

func TestAdmin_DeleteMissingNotFound(t *testing.T) {
	_, rest := newTestServer(t)
	engine := rest.Engine()
	w, _ := doJSON(t, engine, http.MethodDelete, "/v1/admin/policies/ghost", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("delete missing status=%d want 404", w.Code)
	}
}

// TestAdmin_CreatedPolicyIsUsableInCheck is the key integration test: a policy
// created at runtime via the admin API must be immediately resolvable by the
// /v1/check hot path, proving the Registry is the single source of truth and
// that admin mutations are visible without a restart.
func TestAdmin_CreatedPolicyIsUsableInCheck(t *testing.T) {
	_, rest := newTestServer(t)
	engine := rest.Engine()

	// Create a fresh policy via admin.
	body := map[string]any{
		"name": "dynamic", "algorithm": "tokenbucket",
		"limit": 1, "refill_per_second": 1,
	}
	w, _ := doJSON(t, engine, http.MethodPost, "/v1/admin/policies", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}

	// First check against the new policy must succeed (200).
	checkBody := `{"key":"dyn","limit_name":"dynamic"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/check", bytes.NewBufferString(checkBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first check status=%d want 200 (dynamic policy usable), body=%s", w.Code, w.Body.String())
	}

	// Second check must be denied (limit=1).
	req = httptest.NewRequest(http.MethodPost, "/v1/check", bytes.NewBufferString(checkBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second check status=%d want 429", w.Code)
	}
}

// TestAdmin_DeletePolicyRemovesFromHotPath verifies that after deletion, a
// Check referencing the deleted policy fails with the unknown-policy error
// (400), not a silent allow/deny.
func TestAdmin_DeletePolicyRemovesFromHotPath(t *testing.T) {
	_, rest := newTestServer(t)
	engine := rest.Engine()

	// Delete "strict".
	w, _ := doJSON(t, engine, http.MethodDelete, "/v1/admin/policies/strict", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", w.Code)
	}
	// Check referencing strict must now be 400 (unknown policy).
	checkBody := `{"key":"k","limit_name":"strict"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/check", bytes.NewBufferString(checkBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("check after delete status=%d want 400 (unknown policy)", w.Code)
	}
}
