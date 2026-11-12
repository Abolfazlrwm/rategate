package server

import (
        "bytes"
        "context"
        "io"
        "log/slog"
        "net/http"
        "net/http/httptest"
        "runtime"
        "sync"
        "sync/atomic"
        "testing"
        "time"

        "github.com/prometheus/client_golang/prometheus/promhttp"

        "github.com/Abolfazlrwm/rategate/internal/algorithms"
        "github.com/Abolfazlrwm/rategate/internal/config"
        "github.com/Abolfazlrwm/rategate/internal/observability"
        "github.com/Abolfazlrwm/rategate/internal/store"
)

// runtimeNumCPU returns GOMAXPROCS(0); used only in log output.
func runtimeNumCPU() int { return runtime.GOMAXPROCS(0) }

// benchLogger returns a slog.Logger that discards output for benchmarks so
// the logging middleware doesn't dominate the profile.
func benchLogger() *slog.Logger {
        return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// BenchmarkHTTPServer_CheckParallel measures end-to-end REST /v1/check
// throughput through the Gin stack (middleware + JSON + core + memory store).
//
// This is the headline number cited in the README's benchmark table.
func BenchmarkHTTPServer_CheckParallel(b *testing.B) {
        cfg := &config.Config{}
        cfg.Store.Backend = "memory"
        cfg.Store.DefaultAlgorithm = "tokenbucket"
        metrics, reg := observability.NewMetrics()
        s := store.NewMemoryStore(store.MemoryOptions{TBShards: 256})
        defer s.Close()
        core := New(cfg, s, metrics, benchLogger())
        core.MarkReady()
        rest := NewHTTPServer(core, promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
        engine := rest.Engine()

        body := []byte(`{"key":"bench","algorithm":"tokenbucket","limit":1000000000,"refill_per_second":1000000000}`)
        b.ReportAllocs()
        b.ResetTimer()
        b.RunParallel(func(pb *testing.PB) {
                for pb.Next() {
                        req := httptest.NewRequest(http.MethodPost, "/v1/check", bytes.NewReader(body))
                        req.Header.Set("Content-Type", "application/json")
                        w := httptest.NewRecorder()
                        engine.ServeHTTP(w, req)
                        if w.Code != http.StatusOK {
                                b.Fatalf("status=%d", w.Code)
                        }
                }
        })
}

// BenchmarkServer_CoreCheckParallel measures the core Check path without
// HTTP/JSON overhead — the upper bound on throughput.
func BenchmarkServer_CoreCheckParallel(b *testing.B) {
        cfg := &config.Config{}
        cfg.Store.Backend = "memory"
        cfg.Store.DefaultAlgorithm = "tokenbucket"
        metrics, _ := observability.NewMetrics()
        s := store.NewMemoryStore(store.MemoryOptions{TBShards: 256})
        defer s.Close()
        core := New(cfg, s, metrics, benchLogger())
        core.MarkReady()

        now := time.Now()
        policy := algorithms.Policy{Limit: 1 << 30, RefillPerSecond: 1 << 30}
        b.ReportAllocs()
        b.ResetTimer()
        b.RunParallel(func(pb *testing.PB) {
                for pb.Next() {
                        _, _ = core.Check(context.Background(), CheckRequest{
                                Key:       "bench",
                                Algorithm: "tokenbucket",
                                Policy:    policy,
                        }, now)
                }
        })
}

// TestEndToEnd_RPS drives the REST endpoint for 2s at high concurrency and
// reports achieved RPS. It is gated behind a short duration and skipped in
// -short mode so CI stays fast.
func TestEndToEnd_RPS(t *testing.T) {
        if testing.Short() {
                t.Skip("skipping end-to-end RPS test in short mode")
        }
        cfg := &config.Config{}
        cfg.Store.Backend = "memory"
        cfg.Store.DefaultAlgorithm = "tokenbucket"
        metrics, reg := observability.NewMetrics()
        s := store.NewMemoryStore(store.MemoryOptions{TBShards: 256})
        defer s.Close()
        core := New(cfg, s, metrics, benchLogger())
        core.MarkReady()
        rest := NewHTTPServer(core, promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
        engine := rest.Engine()

        srv := httptest.NewServer(engine)
        defer srv.Close()

        body := []byte(`{"key":"rps","algorithm":"tokenbucket","limit":1000000000,"refill_per_second":1000000000}`)
        client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 256}}

        const concurrency = 64
        const dur = 2 * time.Second
        ctx, cancel := context.WithTimeout(context.Background(), dur)
        defer cancel()

        var total int64
        var wg sync.WaitGroup
        wg.Add(concurrency)
        start := time.Now()
        for i := 0; i < concurrency; i++ {
                go func() {
                        defer wg.Done()
                        for {
                                if ctx.Err() != nil {
                                        return
                                }
                                req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/check", bytes.NewReader(body))
                                req.Header.Set("Content-Type", "application/json")
                                resp, err := client.Do(req)
                                if err != nil {
                                        return
                                }
                                _, _ = io.Copy(io.Discard, resp.Body)
                                _ = resp.Body.Close()
                                if resp.StatusCode == http.StatusOK {
                                        atomic.AddInt64(&total, 1)
                                }
                        }
                }()
        }
        wg.Wait()
        elapsed := time.Since(start).Seconds()
        rps := float64(total) / elapsed
        t.Logf("end-to-end REST RPS over %s: %.0f (concurrency=%d, cpu=%d)",
                dur, rps, concurrency, runtimeNumCPU())
        // No hard floor: the sandbox CPU varies. The README benchmark table
        // cites laptop-class numbers; this test exists to catch regressions in
        // the transport stack relative to itself, so we only assert the server
        // didn't melt (at least 10k RPS on any reasonable CI runner).
        if rps < 10000 {
                t.Fatalf("RPS %.0f below sanity floor 10k — transport regression likely", rps)
        }
}
