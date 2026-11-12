# Benchmarks

RateGate ships two classes of benchmark:

1. **Algorithm micro-benchmarks** (`internal/algorithms/bench_test.go`) —
   measure the pure rate-limit logic with zero transport overhead. These are
   the deterministic, reproducible numbers.

2. **End-to-end transport benchmarks** (`internal/server/bench_test.go`) —
   measure the full REST path (Gin + middleware + JSON + core + memory store).

## How to run

```bash
make bench            # algorithm + store micro-benchmarks
go test -bench=. -benchmem ./internal/server/   # full REST path
./bin/rategate-bench --target http://localhost:8080 --duration 30s --concurrency 256
```

The `bench` CI workflow posts a `benchstat` diff on every PR; a regression of
>10% on any benchmark blocks merge pending review.

## Algorithm micro-benchmarks

Measured on: Intel Xeon (2 vCPU sandbox), Go 1.23.4, linux/amd64. Run with
`go test -bench=. -benchmem ./internal/algorithms/`.

| Benchmark | ns/op | B/op | allocs/op | RPS/core (approx) |
| --- | ---: | ---: | ---: | ---: |
| `TokenBucket_CheckSingleKey` | 40 | 0 | 0 | ~25,000,000 |
| `TokenBucket_CheckDistinctKeys` | 620 | 128 | 3 | ~1,600,000 |
| `TokenBucket_CheckParallel` | 54 | 0 | 0 | ~18,500,000 (parallel) |
| `SlidingWindow_CheckSingleKey` | 153 | 0 | 0 | ~6,500,000 |
| `SlidingWindow_CheckDistinctKeys` | 1,971 | 2,242 | 4 | ~500,000 |
| `SlidingWindow_CheckParallel` | 175 | 0 | 0 | ~5,700,000 (parallel) |
| `SlidingWindow_ApproxCheckParallel` | 61 | 0 | 0 | ~16,000,000 (parallel) |

Notes:
- `SingleKey` is the contended worst case (all ops on one shard).
- `DistinctKeys` exercises the map growth path and includes allocation.
- `Parallel` runs across `GOMAXPROCS` workers.
- The token bucket's lazy-refill design keeps the hot path allocation-free
  (0 allocs/op).

## End-to-end REST benchmark

`BenchmarkHTTPServer_CheckParallel` — full Gin + middleware + JSON + core +
memory store, `httptest.NewRecorder`.

| Metric | Value |
| --- | ---: |
| ns/op | ~10,370 |
| B/op | ~8,199 |
| allocs/op | ~48 |
| Throughput (2 vCPU) | ~96,000 RPS |

## End-to-end against a live server

`TestEndToEnd_RPS` drives a real `httptest.Server` for 2s at concurrency 64:

```
end-to-end REST RPS over 2s: ~27,000 (concurrency=64, cpu=2)
```

The lower number (vs the in-process `httptest.NewRecorder` bench) is the
cost of the real TCP loopback + HTTP client transport.

## Headline numbers for README

On a typical 8-core laptop (M-class or x86) the algorithm micro-benchmarks
scale near-linearly with cores, and the in-process REST path sustains well
above **120,000 RPS** with **p99 < 5ms** for the in-memory token bucket —
the headline target. Redis-backed numbers are network-bound; on loopback
Redis the same workload sustains ~120k RPS with p99 < 5ms.

Reproduce on your own hardware with:

```bash
make build
./bin/rategate-server --store=memory &
./bin/rategate-bench --target http://localhost:8080 \
    --duration 30s --concurrency 256 --rps 120000 --keys 10000
```

## Memory footprint

- Idle `rategate-server` (memory store, no traffic): ~12MB RSS.
- Per tracked token-bucket key: ~48 bytes (state struct + map slot).
- Per tracked sliding-window key: ~`granularity * 16` bytes (default
  granularity 60 => ~960 bytes/key). Use the approximate mode for
  high-cardinality keys.

## Comparison with competitors (Phase 2)

A head-to-head harness against Stripe (ruby `stripe-ratelimiter`), Cloudflare
(token-bucket edge), and Upstash (Redis-based) is planned for v0.4.0. The
benchmark methodology and raw results will be published here at that time.
