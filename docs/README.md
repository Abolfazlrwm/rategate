# RateGate documentation

This is the written documentation for RateGate. For the **interactive**
experience, open the [live playground](https://abolfazlrwm.github.io/rategate/)
— it runs a faithful port of the server's algorithms in your browser.

## Start here

- [Architecture](architecture.md) — components, request flow, store contract, observability.
- [Benchmarks](benchmarks.md) — methodology, the full result table, and how to reproduce.
- [API reference](api-reference.md) — REST and gRPC endpoints, request/response shapes, auth.
- [`config.example.yaml`](../config.example.yaml) — every config key with comments.

## Algorithms &amp; design (ADRs)

- [ADR-0001 — why token bucket is the default](adr/0001-why-token-bucket-default.md)
- [ADR-0002 — Redis Lua scripts for atomicity](adr/0002-redis-lua-scripts.md)
- [ADR-0003 — Redis Cluster via single-key Lua reuse](adr/0003-redis-cluster-single-key-lua.md)
- [ADR-0004 — adaptive (AIMD) limiter, demand-aware](adr/0004-adaptive-aimd-limiter.md)

## Running it

- [Docker &amp; Kubernetes](../deployments/) — distroless multi-arch image, compose, k8s manifests.
- [Examples](../examples/) — Gin middleware, standalone embedded server, client SDK.
- [`Makefile`](../Makefile) — `make check`, `make bench`, `make test-cover`, `make proto`.

## Project

- [Contributing](../CONTRIBUTING.md) · [Code of conduct](../CODE_OF_CONDUCT.md) · [Security policy](../SECURITY.md)
- [Changelog](../CHANGELOG.md) · [License (Apache-2.0)](../LICENSE) · [Citation](../CITATION.cff)
