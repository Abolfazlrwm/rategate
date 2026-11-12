// Command rategate-bench is a load-testing tool for a running RateGate
// server. It drives the REST /v1/check endpoint at a configurable
// concurrency and prints a latency histogram + RPS summary.
//
// Usage:
//
//	rategate-bench --target http://localhost:8080 \
//	               --duration 30s --concurrency 256 --rps 120000
//
// When --rps is 0, the tool drives as fast as the workers can go (closed-loop
// throughput). When --rps > 0, a token-pacing limiter approximates the
// target rate.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type benchResult struct {
	total      int64
	allowed    int64
	denied     int64
	errors     int64
	durations  []time.Duration
}

func main() {
	target := flag.String("target", "http://localhost:8080", "target base URL")
	path := flag.String("path", "/v1/check", "check endpoint path")
	duration := flag.Duration("duration", 10*time.Second, "test duration")
	concurrency := flag.Int("concurrency", 64, "concurrent workers")
	rps := flag.Int("rps", 0, "target requests per second (0 = closed-loop)")
	keyCardinality := flag.Int("keys", 1000, "number of distinct keys to spread load over")
	limit := flag.Int64("limit", 1000000, "inline limit per check (high so most are allowed)")
	refill := flag.Float64("refill", 1000000, "inline refill_per_second")
	apiKey := flag.String("api-key", "", "x-api-key header value")
	flag.Parse()

	if err := run(*target, *path, *duration, *concurrency, *rps, *keyCardinality, *limit, *refill, *apiKey); err != nil {
		fmt.Fprintf(os.Stderr, "rategate-bench: %v\n", err)
		os.Exit(1)
	}
}

func run(target, path string, duration time.Duration, concurrency, rps, keys int, limit int64, refill float64, apiKey string) error {
	url := target + path

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var res benchResult
	res.durations = make([]time.Duration, 0, 1<<20)
	var mu sync.Mutex

	// Token-pacing channel for rate-limited mode.
	var ticker <-chan time.Time
	if rps > 0 {
		interval := time.Second / time.Duration(rps)
		t := time.NewTicker(interval)
		defer t.Stop()
		ticker = t.C
	}

	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        concurrency * 2,
			MaxIdleConnsPerHost: concurrency * 2,
			MaxConnsPerHost:     concurrency * 2,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	var wg sync.WaitGroup
	wg.Add(concurrency)
	fmt.Fprintf(os.Stderr, "bench: target=%s concurrency=%d rps=%d duration=%s keys=%d\n",
		url, concurrency, rps, duration, keys)

	start := time.Now()
	for i := 0; i < concurrency; i++ {
		go func(workerID int) {
			defer wg.Done()
			for {
				// Rate-limited mode: wait for a token.
				if ticker != nil {
					select {
					case <-ctx.Done():
						return
					case <-ticker:
					}
				}
				if ctx.Err() != nil {
					return
				}

				key := fmt.Sprintf("bench:%d", rand.Intn(keys))
				body, _ := json.Marshal(map[string]any{
					"key":               key,
					"algorithm":         "tokenbucket",
					"limit":             limit,
					"refill_per_second": refill,
				})
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
				if err != nil {
					atomic.AddInt64(&res.errors, 1)
					continue
				}
				req.Header.Set("Content-Type", "application/json")
				if apiKey != "" {
					req.Header.Set("x-api-key", apiKey)
				}

				reqStart := time.Now()
				resp, err := client.Do(req)
				reqDur := time.Since(reqStart)

				mu.Lock()
				res.durations = append(res.durations, reqDur)
				mu.Unlock()
				atomic.AddInt64(&res.total, 1)

				if err != nil {
					atomic.AddInt64(&res.errors, 1)
					continue
				}
				switch resp.StatusCode {
				case http.StatusOK:
					atomic.AddInt64(&res.allowed, 1)
				case http.StatusTooManyRequests:
					atomic.AddInt64(&res.denied, 1)
				default:
					atomic.AddInt64(&res.errors, 1)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	printReport(res, elapsed)
	return nil
}

// printReport writes a summary with RPS and percentile latency.
func printReport(res benchResult, elapsed time.Duration) {
	durs := res.durations
	if len(durs) == 0 {
		fmt.Println("no samples collected")
		return
	}
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })

	pct := func(p float64) time.Duration {
		idx := int(float64(len(durs)-1) * p)
		if idx < 0 {
			idx = 0
		}
		return durs[idx]
	}
	rps := float64(len(durs)) / elapsed.Seconds()

	fmt.Println()
	fmt.Println("=== RateGate bench results ===")
	fmt.Printf("duration:        %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("total requests:  %d\n", len(durs))
	fmt.Printf("allowed:         %d\n", res.allowed)
	fmt.Printf("denied:          %d\n", res.denied)
	fmt.Printf("errors:          %d\n", res.errors)
	fmt.Printf("achieved RPS:    %.0f\n", rps)
	fmt.Printf("latency p50:     %s\n", pct(0.50).Round(time.Microsecond))
	fmt.Printf("latency p90:     %s\n", pct(0.90).Round(time.Microsecond))
	fmt.Printf("latency p95:     %s\n", pct(0.95).Round(time.Microsecond))
	fmt.Printf("latency p99:     %s\n", pct(0.99).Round(time.Microsecond))
	fmt.Printf("latency max:     %s\n", durs[len(durs)-1].Round(time.Microsecond))
}
