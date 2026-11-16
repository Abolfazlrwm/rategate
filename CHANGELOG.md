# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Configurable store failure mode: `store.fail_open` (default `false`). When
  the backing store errors (e.g. Redis unreachable), the default (fail-closed)
  surfaces the error and denies the request; setting `fail_open=true` admits
  the request instead so a limiter outage does not take down the protected
  service (availability over safety — an explicit operator tradeoff).
- Separate admin API key: `server.admin_api_key`. Admin endpoints
  (`/v1/admin/*` REST, `rategate.v1.AdminService` gRPC) accept a credential
  distinct from check traffic; empty falls back to `server.api_key` (Phase 2
  behavior). A dedicated admin key is recommended in production.
- gRPC server reflection: RateGate gRPC servers are now discoverable by
  `grpcurl` (lists `RateGateService`, `AdminService`, health, reflection).
- gRPC keepalive enforcement + server parameters (`MinTime` 10s, `Time` 30s,
  `Timeout` 10s) to reap stale connections and reject overly frequent pings.
- `--version` flag on `rategate-server`.
- Policy-count gauge (`rategate_policies`) and admin operation counter
  (`rategate_admin_operations_total{operation,status}`) exported on `/metrics`.
- `SECURITY.md` and `CODE_OF_CONDUCT.md`.

### Changed
- REST auth is now per-route-group: `/v1/check` is gated by `server.api_key`,
  `/v1/admin/*` by `server.admin_api_key` (fallback `server.api_key`), and
  `/healthz`, `/readyz`, `/metrics` are public (kubelet probes and a scraped
  metrics endpoint no longer require a credential).
- `server.New` seeds the `rategate_policies` gauge with the file-loaded policy
  count at startup, so the gauge is correct before any admin mutation.

### Security
- The gRPC transport is no longer unauthenticated when `server.api_key` is set:
  a unary server interceptor enforces `server.api_key` on `CheckRateLimit` and
  `server.admin_api_key` on the `AdminService`, mirroring the REST auth gate.
  Health and reflection remain public. Previously a gRPC client could call
  `CheckRateLimit` / `AdminService` with no credential even when REST auth was
  enabled.

### Planned (Phase 3)
- Per-tenant quotas with serial enforcement (multi-tenancy).
- gRPC streaming for live rate-limit decision observation.
- Congestion-response adaptive limiting (downstream latency feedback via
  an `Algorithm.Record` completion hook — see ADR-0004 scope note).
- HCL-based policy configuration.
- WASM filter for Envoy.
- Head-to-head benchmarks against Stripe, Cloudflare, Upstash.

## [0.2.0] - 2026-11-16

### Added
- Redis Cluster backend (`store.backend: redis-cluster`) — reuses the
  single-node atomic Lua scripts via `redis.Cmdable`; every `EVAL` is
  single-key so it maps to one cluster slot and runs atomically on the
  owning node. See [ADR-0003](docs/adr/0003-redis-cluster-single-key-lua.md).
- Adaptive (AIMD) limiter (`algorithm: adaptive`) — demand-aware effective
  limit in `[floor, floor*MaxFactor]`: additively grows headroom on rising
  demand, multiplicatively retracts toward the floor on falling demand,
  holds steady under constant load (hysteresis). In-memory only. See
  [ADR-0004](docs/adr/0004-adaptive-aimd-limiter.md).
- Runtime admin API (REST + gRPC): CRUD on policies backed by a thread-safe
  `internal/admin.Registry`; created/updated policies are immediately visible
  to `CheckRateLimit` / `/v1/check` without a restart. REST:
  `/v1/admin/policies` (GET list, POST create, GET/PUT/DELETE `:name`). gRPC:
  `rategate.v1.AdminService` (ListPolicies / CreatePolicy / GetPolicy /
  UpdatePolicy / DeletePolicy). Both transports share one Registry.
- `cluster.*` and `store.memory_adaptive_*` config sections; an adaptive
  policy example and a cluster section in `config.example.yaml`.

### Changed
- Toolchain: Go 1.26; CI pins Go 1.26 + `golangci-lint` v2.15.0; the
  `.golangci.yml` was migrated to the v2 schema (formatters moved under
  `formatters:`, `examples/` path exclusion fixed).
- The server holds policies in a thread-safe `admin.Registry` (was a plain
  map), so admin mutations are safe concurrent with Check traffic.
- `TestEndToEnd_RPS` floor is now CPU-scaled (`500/cpu`) instead of a fixed
  10k, matching its own "no hard floor" intent and stopping failures on
  small/throttled runners.
- `internal/algorithms` and `internal/store` remain at 100% test coverage
  (enforced in CI); the whole tree was re-gofmt'd to Go 1.26 `gofmt`.

## [0.1.0] - 2025-11-12

### Added
- Initial public release.
- `tokenbucket` algorithm with configurable refill rate and burst size.
- `slidingwindow` algorithm with exact counter (and optional HyperLogLog
  approximation for large scales).
- `memory` store with sharded mutex for single-node operation.
- `redis` store backed by atomic Lua scripts (no race conditions).
- gRPC service `RateGateService` with `CheckRateLimit` and `HealthCheck`.
- REST API: `POST /v1/check`, `GET /healthz`, `GET /readyz`, `GET /metrics`.
- HTTP middleware: auth (API key), structured logging (`slog`), panic recovery.
- Prometheus metrics: `requests_total`, `allowed_total`, `denied_total`,
  `latency_histogram`, `store_latency`.
- OpenTelemetry tracing spans for every request.
- Viper-based configuration (env, file, flags).
- `rategate-bench` load-testing tool with concurrency control.
- Go client SDK (`pkg/client`) and HTTP middleware for Gin/Echo/stdlib.
- Multi-stage distroless Dockerfile and docker-compose.
- Kubernetes `Deployment` + `Service` manifests.
- CI: `ci.yml` (test + lint + race), `bench.yml` (per-PR benchmark diff),
  `release.yml` (goreleaser).
- Docs: architecture, benchmarks, API reference, ADR-0001 (token bucket
  default), ADR-0002 (Redis Lua scripts).
- Apache-2.0 license.

### Performance
- Baseline on a single laptop node (see `docs/benchmarks.md`):
  in-memory token-bucket at ~1.2M RPS with p99 < 1ms in micro-benchmarks;
  Redis-backed token-bucket at ~120k+ RPS with p99 < 5ms in end-to-end
  bench (single Redis on loopback).
