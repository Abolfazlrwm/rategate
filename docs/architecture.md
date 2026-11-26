# Architecture

RateGate is a distributed rate-limiter designed to run as a sidecar or
gateway middleware. It exposes a gRPC service and a REST API, supports three
limiting algorithms (token bucket, sliding window, and adaptive AIMD), and
persists state either in-process (memory) or in Redis (atomic Lua scripts,
single-node or Cluster). A Go SDK (`pkg/client`) and Gin/net/http middleware
(`pkg/middleware`) ship for callers.

## High-level diagram

```
                    ┌──────────────────────────────────────┐
                    │           rategate-server            │
   gRPC :9090  ───▶ │  ┌──────────┐   ┌────────────────┐  │
                    │  │ gRPC     │   │  Server (core) │  │
                    │  │ adapter  │──▶│  - policy map  │  │
                    │  └──────────┘   │  - Check()     │  │
                    │                 └───────┬────────┘  │
   REST :8080  ───▶ │  ┌──────────┐           │           │
                    │  │ Gin REST │──────────▶│           │
                    │  │ + mw     │           ▼           │
                    │  └──────────┘   ┌────────────────┐  │
                    │                 │  Store         │  │
                    │                 │  memory | redis│  │
                    │                 └────────┬───────┘  │
                    │                          │          │
                    │   /metrics   /healthz    │          │
                    │   /readyz                │          │
                    └──────────────────────────┼──────────┘
                                               │
                              ┌────────────────┴────────────┐
                              │ memory: in-process sharded  │
                              │   mutexes (256 shards)      │
                              │                             │
                              │ redis: EVAL Lua scripts     │
                              │   (single-threaded atomic)  │
                              └─────────────────────────────┘
```

## Layers

1. **Transport** (`internal/server/grpc.go`, `http.go`) — adapts the core
   `Server.Check` to gRPC and REST. Both paths share the same `CheckRequest`
   and the same observability hooks, so a request is counted identically
   regardless of transport.

2. **Middleware** (`internal/server/middleware.go`) — recovery (no request
   path may panic the process), structured logging (`slog`), and optional
   API-key auth. Middleware is transport-specific (Gin handlers) but the
   concerns are transport-agnostic.

3. **Core** (`internal/server/server.go`) — resolves the policy (named or
   inline), delegates to the store, records metrics, and returns a
   transport-neutral `CheckResult`. The `Health()` method pings the store
   when it implements `Pinger`.

4. **Store** (`internal/store/`) — the persistence contract. Three impls:
   - **MemoryStore** wraps `algorithms.TokenBucket`,
     `algorithms.SlidingWindow` and `algorithms.AdaptiveLimiter`, all sharded
     by FNV-1a hash. It is the only backend that supports the adaptive
     algorithm.
   - **RedisStore** runs the token-bucket and sliding-window algorithms inside
     Lua scripts via `EVAL`. Atomicity comes from Redis's single-threaded
     script execution. **ClusterStore** reuses the same single-key scripts over
     a Redis Cluster client, so each EVAL routes to one hash slot and stays
     atomic; cross-slot scripts are never issued (see ADR-0003).

5. **Algorithms** (`internal/algorithms/`) — pure rate-limit logic,
   clock-parameterized for deterministic tests. The token bucket uses lazy
   refill (no background goroutine); the sliding window uses a ring of
   sub-buckets for O(1) amortized eviction, with an optional
   exponentially-decaying approximation for O(1) memory; the adaptive (AIMD)
   limiter varies its effective limit between a configured floor and cap in
   response to demand trends between sampling windows.

6. **Observability** (`internal/observability/`) — Prometheus metrics on a
   private registry (exposed at `/metrics`) and OpenTelemetry tracing
   (10% sampling, OTLP/gRPC exporter).

7. **Config** (`internal/config/`) — Viper-based. Precedence: flag > env
   (`RATEGATE_*`) > file > defaults. A missing file is fine.

## Atomicity guarantees

- **Memory store**: sharded mutex per key. Two concurrent `Check` calls for
  the same key serialize on the same shard mutex.
- **Redis store**: the entire read-mutate-write happens in one `EVAL`. Redis
  executes Lua atomically (single-threaded), so concurrent EVALs for the same
  key serialize server-side. No `WATCH`/`MULTI` is needed.

See [ADR-0002](adr/0002-redis-lua-scripts.md) for the full rationale.

## Failure modes

| Failure | Behavior |
| --- | --- |
| Redis unreachable | `/readyz` returns 503; gRPC HealthCheck returns NOT_SERVING. The server keeps running and will recover when Redis returns. |
| Panic in a handler | `RecoveryMiddleware` logs the stack and returns 500. The process survives. |
| Bad policy in config | `config.Validate()` fails at startup; the server refuses to start. |
| Unknown policy name in a request | `400 Bad Request` (REST) / `InvalidArgument` (gRPC). |
| Invalid inline limit (<=0) | `400 Bad Request` / `InvalidArgument`. |
| Clock skew across instances | Affects only the sliding-window boundary, not correctness — Redis stores relative state and the server supplies the authoritative `now`. |

## Threading model

- The REST server (Gin) uses one goroutine per request.
- The gRPC server uses a fixed worker pool (default Go runtime).
- The memory store's sharded mutexes bound contention to ~1/shards of keys.
- No background goroutines for refill — token refill is computed lazily on
  each `Check`, so a quiet bucket costs zero CPU.

## Extension points

- **New algorithm**: implement `algorithms.Algorithm` and add a case in
  `MemoryStore.Check` (and a Lua script + case for `RedisStore`).
- **New store**: implement `store.Store` (+ optionally `Pinger`).
- **New transport**: wrap `server.Server` with a new adapter; the core is
  transport-agnostic.
- **New framework middleware**: add to `pkg/middleware`, which ships Gin and
  `net/http` adapters in Phase 1 (an Echo adapter is stubbed behind a build
  tag for a later release).

## Phase 2 hooks

The interfaces are designed to admit the planned Phase 2 features without
breaking changes:

- `Pinger` already exists for health probes; multi-region replication will
  extend `Store` with a `Replicate` method.
- Per-tenant quotas will be a policy-table concern, not an algorithm change.
- gRPC streaming will add a new RPC to the proto without touching the
  unary `CheckRateLimit`.
