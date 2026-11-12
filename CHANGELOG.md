# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Planned (Phase 2)
- Multi-region replication (Redis Cluster / Sentinel support).
- Adaptive rate limiting based on downstream latency feedback.
- Per-tenant quotas with serial enforcement (multi-tenancy).
- gRPC streaming for live rate-limit decision observation.
- Runtime Admin API for policy management.
- HCL-based policy configuration.
- WASM filter for Envoy.
- Head-to-head benchmarks against Stripe, Cloudflare, Upstash.

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
