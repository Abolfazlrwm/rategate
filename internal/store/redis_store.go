// Package store
//
// redis_store.go — distributed Store backed by Redis.
//
// Atomicity across many server instances is achieved by running the entire
// "read state -> mutate -> write state" sequence inside a single Lua script
// via EVAL. Redis executes Lua atomically (single-threaded), so two
// concurrent EVALs for the same key are serialized — no token-bucket race,
// no sliding-window lost-update. See ADR-0002.
//
// State layout (per key, namespaced under "rategate:{algo}:{key}"):
//   - tokenbucket: a HASH with `tokens` (float-string) and `last` (unix-ms).
//   - slidingwindow: a SORTED SET of timestamps within the window, plus a
//     TTL so idle keys are reaped. The exact-counter variant uses ZSET;
//     the approximate variant is not implemented server-side (use the
//     in-memory approximate path or Redis Bloom/HLL in Phase 2).
package store

import (
        "context"
        "errors"
        "fmt"
        "sync"
        "sync/atomic"
        "time"

        "github.com/redis/go-redis/v9"

        "github.com/Abolfazlrwm/rategate/internal/algorithms"
)

// tokenBucketScript implements an atomic token-bucket check in Lua.
//
// KEYS[1] = the bucket hash key
// ARGV    = [capacity, refill_per_second (as string), now_ms]
//
// Returns: {allowed (0/1), remaining (int), retry_after_ms (int)}
//
// The script is atomic because Redis executes EVAL single-threaded.
var tokenBucketScript = redis.NewScript(`
-- Note: redis.loglevel and redis.log are available for debugging.
local key       = KEYS[1]
local capacity  = tonumber(ARGV[1])
local refill    = tonumber(ARGV[2])
local now_ms    = tonumber(ARGV[3])

local state = redis.call('HMGET', key, 'tokens', 'last')
local tokens = tonumber(state[1])
local last   = tonumber(state[2])

if tokens == nil then
  tokens = capacity
  last   = now_ms
end

-- Refill: elapsed in seconds, capped at capacity.
local elapsed_ms = now_ms - last
if elapsed_ms > 0 then
  local elapsed_sec = elapsed_ms / 1000.0
  tokens = tokens + elapsed_sec * refill
  if tokens > capacity then tokens = capacity end
  last = now_ms
end

local allowed = 0
local retry_after_ms = 0
if tokens >= 1.0 then
  tokens = tokens - 1.0
  allowed = 1
else
  -- time until 1 token available, in ms
  local need = 1.0 - tokens
  retry_after_ms = math.ceil((need / refill) * 1000.0)
end

-- Persist. We set a TTL of (capacity/refill + 60) seconds so idle keys
-- expire; a bucket that has been empty for longer than its full refill
-- window is effectively reset anyway.
redis.call('HMSET', key, 'tokens', tostring(tokens), 'last', tostring(last))
local ttl_sec = math.ceil(capacity / refill) + 60
redis.call('EXPIRE', key, ttl_sec)

return {allowed, tostring(math.floor(tokens)), tostring(retry_after_ms)}
`)

// slidingWindowScript implements an atomic exact sliding-window check in Lua.
//
// KEYS[1] = the sorted-set key
// ARGV    = [limit, window_ms, now_ms, member_id (unique per request)]
//
// Returns: {allowed (0/1), remaining (int), retry_after_ms (int)}
var slidingWindowScript = redis.NewScript(`
local key       = KEYS[1]
local limit     = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local now_ms    = tonumber(ARGV[3])
local member    = ARGV[4]

-- Drop expired members.
local cutoff = now_ms - window_ms
redis.call('ZREMRANGEBYSCORE', key, '-inf', '(' .. cutoff)

local count = redis.call('ZCARD', key)
local allowed = 0
local retry_after_ms = 0

if count < limit then
  redis.call('ZADD', key, now_ms, member)
  count = count + 1
  allowed = 1
else
  -- Oldest member's score is the earliest expiry.
  local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
  if oldest[2] ~= nil then
    local expire_ms = tonumber(oldest[2]) + window_ms
    retry_after_ms = expire_ms - now_ms
    if retry_after_ms < 0 then retry_after_ms = 0 end
  end
end

-- TTL so idle keys are reaped.
redis.call('PEXPIRE', key, window_ms + 1000)

return {allowed, tostring(limit - count), tostring(retry_after_ms)}
`)

// RedisStore is the distributed, Redis-backed Store implementation.
type RedisStore struct {
        rdb       redis.Cmdable
        tb        *redis.Script
        sw        *redis.Script
        keyPrefix string
        seq       uint64 // atomic, monotonically increasing per-request counter
        closeOnce sync.Once
        closeErr  error
}

// RedisOptions configures the RedisStore.
type RedisOptions struct {
        // Addr is the Redis host:port. Required.
        Addr string
        // Password for AUTH. Optional.
        Password string
        // DB index. Default 0.
        DB int
        // PoolSize is the connection pool size per CPU. Default 10 * runtime.GOMAXPROCS.
        PoolSize int
        // KeyPrefix namespaces all keys. Default "rategate".
        KeyPrefix string
}

// NewRedisStore returns a RedisStore backed by a new redis.Client.
func NewRedisStore(opts RedisOptions) *RedisStore {
        rdb := redis.NewClient(&redis.Options{
                Addr:     opts.Addr,
                Password: opts.Password,
                DB:       opts.DB,
                PoolSize: opts.PoolSize,
        })
        return &RedisStore{
                rdb: rdb,
                tb:  tokenBucketScript,
                sw:  slidingWindowScript,
                keyPrefix: opts.KeyPrefix,
        }
}

// NewRedisStoreWithClient returns a RedisStore backed by the supplied
// redis.Cmdable (useful for testing with miniredis or for Redis Cluster).
func NewRedisStoreWithClient(rdb redis.Cmdable, keyPrefix string) *RedisStore {
        if keyPrefix == "" {
                keyPrefix = "rategate"
        }
        return &RedisStore{rdb: rdb, tb: tokenBucketScript, sw: slidingWindowScript, keyPrefix: keyPrefix}
}

// Name implements [Store]. Returns "redis".
func (r *RedisStore) Name() string { return "redis" }

// Check implements [Store].
//
// now is used as the authoritative clock for refill/window computations so
// that the server can keep a single monotonic clock source; this is
// important because different server instances may have slightly skewed
// clocks, and the server normalizes via NTP. Redis only stores relative
// state, so clock skew affects only the boundary, not correctness.
func (r *RedisStore) Check(ctx context.Context, algo, key string, policy algorithms.Policy, now time.Time) (algorithms.Decision, error) {
        if err := policy.Validate(algo); err != nil {
                return algorithms.Decision{Allowed: false, Limit: policy.Limit, ResetAt: now}, err
        }

        nowMs := now.UnixMilli()

        switch algo {
        case algorithms.AlgorithmTokenBucket:
                redisKey := r.prefixed("tokenbucket", key)
                res, err := r.tb.Run(ctx, r.rdb, []string{redisKey},
                        policy.Limit,
                        fmt.Sprintf("%g", policy.RefillPerSecond),
                        nowMs,
                ).Result()
                if err != nil {
                        return algorithms.Decision{Allowed: false, Limit: policy.Limit, ResetAt: now}, fmt.Errorf("redis tokenbucket eval: %w", err)
                }
                return parseTokenBucketResult(res, policy, now)

        case algorithms.AlgorithmSlidingWindow:
                redisKey := r.prefixed("slidingwindow", key)
                // The ZSET member must be unique per request; otherwise two checks
                // at the same instant would collapse into one ZADD update and the
                // count would undercount. We combine the nanosecond timestamp with
                // a per-process atomic counter so even identical `now` values
                // produce distinct members.
                seq := atomic.AddUint64(&r.seq, 1)
                member := fmt.Sprintf("%d:%d:%d", nowMs, now.Nanosecond(), seq)
                res, err := r.sw.Run(ctx, r.rdb, []string{redisKey},
                        policy.Limit,
                        policy.Window.Milliseconds(),
                        nowMs,
                        member,
                ).Result()
                if err != nil {
                        return algorithms.Decision{Allowed: false, Limit: policy.Limit, ResetAt: now}, fmt.Errorf("redis slidingwindow eval: %w", err)
                }
                return parseSlidingWindowResult(res, policy, now)

        default:
                return algorithms.Decision{Allowed: false, Limit: policy.Limit, ResetAt: now}, ErrUnknownAlgorithm{Algo: algo}
        }
}

// parseTokenBucketResult converts the Lua return array into a Decision.
func parseTokenBucketResult(res any, policy algorithms.Policy, now time.Time) (algorithms.Decision, error) {
        arr, ok := res.([]any)
        if !ok || len(arr) < 3 {
                return algorithms.Decision{Allowed: false, Limit: policy.Limit, ResetAt: now}, errors.New("redis tokenbucket: malformed result")
        }
        allowedIface := arr[0]
        remainingStr, _ := arr[1].(string)
        retryStr, _ := arr[2].(string)

        allowed := false
        switch v := allowedIface.(type) {
        case int64:
                allowed = v == 1
        case string:
                allowed = v == "1"
        }

        var remaining int64
        fmt.Sscanf(remainingStr, "%d", &remaining)
        var retryMs int64
        fmt.Sscanf(retryStr, "%d", &retryMs)

        d := algorithms.Decision{
                Allowed:   allowed,
                Remaining: remaining,
                Limit:     policy.Limit,
                ResetAt:   now.Add(time.Duration(float64(policy.Limit)/policy.RefillPerSecond) * time.Second),
        }
        if !allowed && retryMs > 0 {
                d.RetryAfter = time.Duration(retryMs) * time.Millisecond
        }
        return d, nil
}

// parseSlidingWindowResult converts the Lua return array into a Decision.
func parseSlidingWindowResult(res any, policy algorithms.Policy, now time.Time) (algorithms.Decision, error) {
        arr, ok := res.([]any)
        if !ok || len(arr) < 3 {
                return algorithms.Decision{Allowed: false, Limit: policy.Limit, ResetAt: now}, errors.New("redis slidingwindow: malformed result")
        }
        allowedIface := arr[0]
        remainingStr, _ := arr[1].(string)
        retryStr, _ := arr[2].(string)

        allowed := false
        switch v := allowedIface.(type) {
        case int64:
                allowed = v == 1
        case string:
                allowed = v == "1"
        }

        var remaining int64
        fmt.Sscanf(remainingStr, "%d", &remaining)
        if remaining < 0 {
                remaining = 0
        }
        var retryMs int64
        fmt.Sscanf(retryStr, "%d", &retryMs)

        d := algorithms.Decision{
                Allowed:   allowed,
                Remaining: remaining,
                Limit:     policy.Limit,
                ResetAt:   now.Add(policy.Window),
        }
        if !allowed && retryMs > 0 {
                d.RetryAfter = time.Duration(retryMs) * time.Millisecond
        }
        return d, nil
}

// prefixed builds the full Redis key for an algorithm+key pair.
func (r *RedisStore) prefixed(algo, key string) string {
        pfx := r.keyPrefix
        if pfx == "" {
                pfx = "rategate"
        }
        return pfx + ":" + algo + ":" + key
}

// Close implements [Store], closing the underlying Redis client. It is
// idempotent: subsequent calls are no-ops and return the error (if any)
// from the first close.
func (r *RedisStore) Close() error {
        r.closeOnce.Do(func() {
                if c, ok := r.rdb.(*redis.Client); ok {
                        r.closeErr = c.Close()
                }
        })
        return r.closeErr
}

// Ping probes the Redis backend with a PING command. Implements the
// server.Pinger interface so Health() can liveness-check the store.
//
// go-redis's Ping always returns a non-nil *StatusCmd (the error, if any,
// is carried inside it), so no nil-check is needed.
func (r *RedisStore) Ping(ctx context.Context) error {
        return r.rdb.Ping(ctx).Err()
}
