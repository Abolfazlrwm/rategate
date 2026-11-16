# ADR-0003: Redis Cluster backend via single-key Lua reuse

- **Status**: Accepted
- **Date**: 2026-11-16
- **Supersedes**: none
- **Related**: ADR-0002 (Redis Lua scripts for atomicity)

## Context

Phase 1 ships a single-node Redis backend (`RedisStore`) that runs each
rate-limit check as an atomic Lua `EVAL` (see ADR-0002). As deployments grow
beyond what one Redis node can serve, the natural next step is a Redis Cluster,
which shards keys across N nodes by 16384 hash slots.

The question is whether the cluster backend needs its own algorithm
implementation, or whether it can reuse the Phase 1 Lua scripts.

## Decision

We add a `ClusterStore` that wraps a `redis.ClusterClient` and **reuses the
`RedisStore` Lua scripts unchanged**, by embedding `*RedisStore` (both
`*redis.Client` and `*redis.ClusterClient` satisfy `redis.Cmdable`).

This is safe because every script we issue touches exactly one key:

- `tokenBucketScript` — `KEYS[1]` = the bucket hash.
- `slidingWindowScript` — `KEYS[1]` = the sorted set.

Redis Cluster requires that all keys referenced by a single command (or Lua
script) hash to the same slot; a single-key `EVAL` trivially satisfies this, so
the cluster routes it to the owning node and runs it atomically there. We never
issue a cross-slot script, so we never hit a `CROSSSLOT` error.

`ClusterStore` therefore only overrides:

- `Name()` → `"redis-cluster"` (so metrics labels distinguish the two
  distributed backends, which have different failure modes — resharding,
  multi-node partitions).
- `Close()` → closes the `ClusterClient` (the embedded `RedisStore.Close` is a
  no-op for a `*ClusterClient`).

Everything else — `Check`, `Ping`, the parse helpers, the scripts — is
inherited.

## Consequences

**Positive**

- No algorithm duplication: one Lua implementation, two transports. A fix to
  the token-bucket script lands for both single-node and cluster deployments.
- Correctness is unchanged: atomicity is still single-node-EVAL atomicity, just
  scoped to the slot owner.
- The cluster client handles slot routing, `MOVED`/`ASK` redirections, and
  topology discovery internally; the store code stays simple.

**Negative / tradeoffs**

- `RouteRandomly` can route a check to a replica that lags the master by a few
  milliseconds. For rate limiting this is conservative (a stale read may
  under-count near the boundary, denying a request that would have been
  admitted on the master) rather than dangerous (it never over-admits beyond
  the bucket's true state on the master, because the write still goes to the
  master via `EVAL`). Operators who need strict accuracy disable
  `RouteRandomly`.
- A real cluster fixture is not available in unit tests; we therefore test the
  delegation and Lua path against a miniredis `*redis.Client` injected via
  `NewClusterStoreWithClient`, and rely on the slot-safety argument above for
  cluster-specific behavior. Integration tests against a real 3-master cluster
  are a future CI addition.
- Multi-key operations (e.g. a future "check N keys in one round-trip" batch
  API) would require hash tags to force same-slot placement; this is out of
  scope for Phase 2.
