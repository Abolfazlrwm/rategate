# ADR-0001: Token bucket as the default algorithm

Date: 2025-11-12
Status: Accepted

## Context

RateGate ships two algorithms in Phase 1: token bucket and sliding window.
We must pick a default so that a `Check` request with no `algorithm` field
has well-defined behavior.

The two algorithms have different cost/accuracy tradeoffs:

- **Token bucket** allows bursts up to the bucket capacity, then refills at
  a steady rate. State per key is tiny (two floats). Refill is lazy and
  O(1). It is forgiving of traffic spikes.
- **Sliding window** enforces a hard cap over any trailing window. State
  per key is larger (a ring of sub-buckets). It is stricter and more
  predictable for steady-state quotas.

## Decision

The default algorithm is **token bucket**.

## Rationale

1. **Burst tolerance.** Most APIs serve bursty traffic (a user opens the
   app and fires several requests in quick succession, then idles). Token
   bucket admits the burst and then throttles — the behavior users expect
   from `X-RateLimit-*` headers. Sliding window would hard-deny the third
   request in a 1-second cluster even when the long-run rate is far below
   the limit.

2. **Memory.** Token bucket is ~48 bytes/key vs ~960 bytes/key for a
   default-granularity sliding window. For a service that may track millions
   of distinct keys (per-IP, per-user), this 20x difference is decisive.

3. **Hot-path cost.** The token bucket micro-benchmark is ~40 ns/op with
   zero allocations; the sliding window is ~150 ns/op. The default should
   be the cheapest path.

4. **Convention.** Token bucket is the default in Stripe's limiter,
   Cloudflare's edge rate limiting, and the Go `golang.org/x/time/rate`
   package. Users' mental model matches.

5. **Sliding window remains available** for the cases where a hard cap is
   required (e.g. "no more than 100 SMS per minute per user, period").
   The caller opts in by name.

## Consequences

- A `Check` with no `algorithm` and no `limit_name` runs the token bucket
  with the inline `limit`/`refill_per_second`.
- The README quickstart uses token bucket.
- Documentation calls out the difference explicitly so users don't
  misattribute burst behavior to a bug.
- Sliding window is a first-class algorithm with full test coverage and a
  Redis Lua implementation; it is not deprecated or discouraged, merely not
  the default.

## Reversal

If a future Phase 2 feature (e.g. adaptive rate limiting) makes sliding
window strictly better for the common case, this ADR is revisited. The
default is a config value, not a code path, so reversal is a one-line
change plus a CHANGELOG entry.
