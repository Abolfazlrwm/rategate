# ADR-0002: Redis Lua scripts for atomicity

Date: 2025-11-12
Status: Accepted

## Context

RateGate's Redis store must allow many server instances to check the same
key concurrently without race conditions. A naive read-then-write
(GET tokens -> compute -> SET tokens) is racy: two instances can both read
`tokens=1`, both decide to allow, and both decrement to 0, admitting two
requests when the limit was 1.

Redis offers several atomicity mechanisms:

1. **`WATCH`/`MULTI`/`EXEC` (optimistic locking)** — watch the key, read,
   in a MULTI block write, EXEC. If the key changed between WATCH and EXEC,
   the transaction aborts and the client retries. Correct, but retry loops
   add latency under contention and require client-side logic.

2. **`INCRBY`/`DECRBY` with expiries (built-in commands)** — atomic for
   simple counters, but token bucket needs floating-point token math
   (fractional refill) and sliding window needs sorted-set membership
   pruning. Built-ins don't express that.

3. **Lua scripts via `EVAL`** — Redis executes the entire script
   atomically on its single-threaded command loop. No other command runs
   between the script's reads and writes. Expressive enough for both
   algorithms.

## Decision

The Redis store implements both algorithms as Lua scripts run via `EVAL`.

- Token bucket: `HMGET` -> lazy refill -> conditional `HSET` -> `EXPIRE`,
  all inside one script.
- Sliding window: `ZREMRANGEBYSCORE` (prune) -> `ZCARD` (count) ->
  conditional `ZADD` -> `PEXPIRE`, all inside one script.

## Rationale

1. **True atomicity, not optimistic.** Two concurrent EVALs for the same
   key are serialized by Redis's single thread. No retry, no abort, no
   client-side coordination. The worst case is that one EVAL waits for the
   other — which is exactly the serialization we want.

2. **Expressiveness.** Lua gives us conditional logic ("if tokens >= 1
   then decrement and allow else deny") and floating-point math in one
   round-trip. The built-in command set cannot express this without
   multiple round-trips and races.

3. **One round-trip.** Each Check is a single `EVAL` call. Under load this
   is the difference between 1 and 3+ Redis round-trips per check; on a
   0.5ms-network this is 1.5ms saved per request, which is significant when
   targeting p99 < 5ms.

4. **Script caching.** `go-redis` `*redis.Script` uses `EVALSHA` after the
   first load, so subsequent calls don't resend the script body. The
   one-time `EVAL` (on cold start) falls back to `EVAL` if `SCRIPT LOAD`
   was flushed.

5. **Familiar pattern.** This is how Stripe, Upstash, and the Redis
   documentation itself recommend building rate limiters. Reviewers
   recognize the pattern.

## Consequences

- The Lua scripts live in `internal/store/redis_store.go` as Go string
  constants. They are version-controlled and reviewed alongside the Go
  code.
- Tests use `miniredis` to exercise the scripts in-process, so CI doesn't
  need a real Redis and the scripts are covered by 100% statement
  coverage on the Go side (plus behavioral coverage via miniredis).
- `redis.call` errors propagate as wrapped Go errors from `Check`.
- The sliding-window member ID must be unique per request (we use
  `nowMs:nanos:atomic-seq`) so concurrent same-instant calls don't
  collapse into one `ZADD` update.

## Reversal

If we move to Redis Cluster in Phase 2, the scripts stay valid (Cluster
routes `EVAL` to the slot of KEYS[1] as long as all keys hash to the same
slot — which they do, since we use one key per Check). No reversal
anticipated.
