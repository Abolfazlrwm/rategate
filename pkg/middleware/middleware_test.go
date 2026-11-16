package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Abolfazlrwm/rategate/pkg/client"
)

// stubLimiter is a test double for the Limiter interface.
type stubLimiter struct {
	resp *client.CheckResponse
	err  error
}

func (s *stubLimiter) Check(_ context.Context, _ client.CheckRequest) (*client.CheckResponse, error) {
	return s.resp, s.err
}

type stubErr struct{ msg string }

func (e *stubErr) Error() string { return e.msg }

func TestStdlibMiddleware_Allow(t *testing.T) {
	l := &stubLimiter{resp: &client.CheckResponse{Allowed: true, Remaining: 4, Limit: 5, ResetAtUnixMs: 1700000000000}}
	called := false
	h := StdlibMiddleware(l, Options{
		KeyFunc:   func(r *http.Request) string { return "k" },
		Algorithm: "tokenbucket",
		Limit:     5,
		Refill:    1,
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !called {
		t.Fatal("downstream handler not called on allow")
	}
	if w.Header().Get("X-RateLimit-Limit") != "5" {
		t.Fatalf("limit header=%q want 5", w.Header().Get("X-RateLimit-Limit"))
	}
}

func TestStdlibMiddleware_Deny(t *testing.T) {
	l := &stubLimiter{resp: &client.CheckResponse{Allowed: false, Remaining: 0, Limit: 5, RetryAfterMs: 2500}}
	called := false
	h := StdlibMiddleware(l, Options{KeyFunc: func(r *http.Request) string { return "k" }},
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if called {
		t.Fatal("downstream should NOT be called on deny")
	}
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d want 429", w.Code)
	}
	if w.Header().Get("Retry-After") != "3" {
		t.Fatalf("retry-after=%q want 3 (ceil of 2500ms)", w.Header().Get("Retry-After"))
	}
}

func TestStdlibMiddleware_FailOpenOnError(t *testing.T) {
	l := &stubLimiter{err: &stubErr{msg: "boom"}}
	called := false
	h := StdlibMiddleware(l, Options{KeyFunc: func(r *http.Request) string { return "k" }},
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !called {
		t.Fatal("should fail open and call downstream on error")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 (fail open)", w.Code)
	}
}

func TestStdlibMiddleware_FailClosedOnError(t *testing.T) {
	l := &stubLimiter{err: &stubErr{msg: "boom"}}
	called := false
	h := StdlibMiddleware(l, Options{
		KeyFunc:    func(r *http.Request) string { return "k" },
		FailClosed: true,
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if called {
		t.Fatal("should NOT call downstream when fail-closed")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", w.Code)
	}
}

func TestRetrySeconds(t *testing.T) {
	cases := []struct {
		ms   int64
		want int
	}{
		{0, 1},
		{1, 1},
		{999, 1},
		{1000, 1},
		{1001, 2},
		{2500, 3},
		{60000, 60},
	}
	for _, c := range cases {
		got := retrySeconds(c.ms)
		if got != c.want {
			t.Fatalf("retrySeconds(%d)=%d want %d", c.ms, got, c.want)
		}
	}
}

func TestBuildRequest(t *testing.T) {
	req := buildRequest("mykey", Options{
		PolicyName: "strict",
		Algorithm:  "tokenbucket",
		Limit:      10,
		Refill:     2,
	})
	if req.Key != "mykey" || req.LimitName != "strict" || req.Algorithm != "tokenbucket" {
		t.Fatalf("buildRequest mismatch: %+v", req)
	}
	if req.Limit != 10 || req.RefillPerSecond != 2 {
		t.Fatalf("limit/refill mismatch: %+v", req)
	}
}
