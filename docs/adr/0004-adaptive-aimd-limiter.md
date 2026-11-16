# ADR-0004: Adaptive (AIMD) limiter — demand-aware, not congestion-response

- **Status**: Accepted
- **Date**: 2026-11-16
- **Supersedes**: none
- **Related**: ADR-0001 (token bucket default)

## Context

Phase 1 ships two fixed-limit algorithms: TokenBucket (burst + refill) and
SlidingWindow (fixed count per window). Both reject at a hard boundary
regardless of whether the protected downstream is actually struggling.

Operators want a third option that **adapts**: granting more headroom when
traffic patterns suggest a burst can be safely absorbed, and retracting when
demand falls so reserved capacity is released. This is especially useful for
sidecar deployments fronting services with spiky, bursty traffic.

## Decision

We add an `AdaptiveLimiter` algorithm (`"adaptive"`) that maintains a per-key
**effective limit** in `[floor, floor*MaxFactor]`, where `floor = Policy.Limit`
(the configured steady-state target) and `MaxFactor` defaults to 4.

The adaptation signal is **demand trend**, computed per sampling window
(`Policy.Window`):

- **Additive Increase**: when this window's total request count exceeds the
  previous window's (demand rising), the effective limit grows by
  `max(1, floor*IncreaseFrac)` (default `IncreaseFrac = 0.25`), capped at
  `floor*MaxFactor`. This absorbs bursts by tentatively granting headroom.
- **Multiplicative Decrease**: when this window's total is below the previous
  window's (demand falling), the effective limit is multiplied by
  `DecreaseFactor` (default 0.5), floored at `Policy.Limit`. This reclaims
  reserved capacity quickly when the burst passes.
- **Hysteresis**: when total is unchanged across windows, the limit holds
  steady. This prevents oscillation under constant load.

The admit decision within a window is a simple count-vs-effective-limit check,
mirroring SlidingWindow's per-window accounting.

## Scope decision — why demand trend, not downstream latency

A true congestion-response adaptive limiter (e.g. Netflix Gradient2, gRPC's
adaptive concurrency limiter) reacts to a downstream signal — request latency,
error rate, or queue depth. RateGate's `Algorithm.Check(key, policy, now)`
interface carries no such signal: a Check is a single atomic evaluation with
no completion callback to report how the admitted request fared downstream.

Building a congestion-response limiter therefore requires either (a) extending
the `Algorithm` interface with a `Record(key, outcome)` completion hook and
wiring callers to call it, or (b) a sidecar-observed signal (e.g. a
latency histogram the server reads). Both are Phase 3 scope. We deliberately
ship the demand-aware variant now because:

1. It fits the existing interface with zero caller-side changes.
2. It is useful as-is: it smooths bursty traffic and reclaims idle headroom,
   which is the common ask for sidecar rate limiting.
3. Its semantics are honest and bounded: it never grants less than the
   configured floor (so protection is never weaker than a plain SlidingWindow
   at `Policy.Limit`) and never more than `floor*MaxFactor` (so the bound is
   predictable).

The cost is that it cannot shed load *below* the floor in response to real
downstream degradation — that is exactly what the configured floor protects
against, and what a future congestion-response limiter would tighten further.

## Consequences

**Positive**

- New `"adaptive"` algorithm selectable per-policy and as the default, with no
  changes to the Check call path or client SDKs.
- Bounded: effective limit always in `[floor, floor*MaxFactor]`; behavior at
  the floor is identical to SlidingWindow, so it is a strict superset of
  protection.
- Deterministic and clock-driven (caller-supplied `now`), so it is fully
  unit-testable without time-based flakiness.

**Negative / tradeoffs**

- Not a substitute for circuit breaking: it does not respond to downstream
  errors or latency. Operators needing that should layer a circuit breaker
  (e.g. in the Gin middleware) alongside RateGate.
- Effective limit is per-key per-process (in-memory only); there is no Redis
  adaptive variant in Phase 2 because the AIMD state and the demand signal are
  inherently per-instance. A distributed adaptive limiter is a Phase 3 problem.
- Under perfectly constant high demand the limit holds at whatever value it
  reached; it does not probe downward. This is intentional (hysteresis) but
  means a one-time burst can leave the limit elevated until demand actually
  drops — acceptable, since the floor still protects.
