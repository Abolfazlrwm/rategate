// Package algorithms
//
// tokenbucket.go — classic token-bucket rate limiter for in-memory use.
//
// The implementation is safe for concurrent use and uses a per-key mutex
// sharded by FNV-1a hash of the key to avoid a single global lock. Token
// refill is computed lazily on each Check (no background goroutine), which
// keeps the hot path allocation-free and deterministic with respect to a
// caller-supplied clock.
package algorithms

import (
	"hash/fnv"
	"math"
	"sync"
	"time"
)

// TokenBucket is an in-memory token-bucket rate limiter.
//
// One TokenBucket instance can serve an arbitrary number of keys; each key
// gets its own bucket state, sharded across `shards` mutexes to reduce
// contention. For distributed deployments use the Redis-backed store which
// runs an equivalent algorithm inside a Lua script (see ADR-0002).
type TokenBucket struct {
	shards []*tbShard
}

type tbShard struct {
	mu      sync.Mutex
	buckets map[string]*tbState
}

type tbState struct {
	tokens   float64 // current token count, fractional because of refill
	lastFill time.Time
}

// NewTokenBucket returns a TokenBucket with `shards` internal shards.
//
// A good default is 256 shards for up to ~1M active keys. More shards reduce
// contention at the cost of slightly more memory.
func NewTokenBucket(shards int) *TokenBucket {
	if shards <= 0 {
		shards = 256
	}
	tb := &TokenBucket{shards: make([]*tbShard, shards)}
	for i := range tb.shards {
		tb.shards[i] = &tbShard{buckets: make(map[string]*tbState)}
	}
	return tb
}

// Name implements [Algorithm]. Returns "tokenbucket".
func (t *TokenBucket) Name() string { return AlgorithmTokenBucket }

// Check implements [Algorithm].
//
// The policy's Limit is the bucket capacity (burst size); RefillPerSecond
// is the steady-state refill rate. A Check consumes exactly one token if
// available.
//
// The clock is `now`, supplied by the caller so tests can be deterministic.
// In production the server injects time.Now().
func (t *TokenBucket) Check(key string, policy Policy, now time.Time) (Decision, error) {
	if err := policy.Validate(AlgorithmTokenBucket); err != nil {
		return Decision{Allowed: false, Limit: policy.Limit, ResetAt: now}, err
	}

	shard := t.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	b, ok := shard.buckets[key]
	if !ok {
		// New bucket starts full so a fresh client can burst up to `Limit`.
		b = &tbState{tokens: float64(policy.Limit), lastFill: now}
		shard.buckets[key] = b
	}

	// Lazy refill: add tokens proportional to elapsed time since lastFill,
	// capped at capacity. Use sub-second precision.
	elapsed := now.Sub(b.lastFill).Seconds()
	if elapsed > 0 {
		refill := elapsed * policy.RefillPerSecond
		b.tokens = math.Min(float64(policy.Limit), b.tokens+refill)
		b.lastFill = now
	}

	// On denial lastFill deliberately stays where it is: the partial
	// refill already happened above and the caller should wait until a
	// full token is available.
	allowed := b.tokens >= 1.0
	if allowed {
		b.tokens -= 1.0
	}

	remaining := int64(b.tokens)
	// ResetAt: when the bucket would be full again from the current state.
	// b.tokens is always <= policy.Limit (capped by math.Min on refill), so
	// tokensNeeded >= 0 and secondsToFull >= 0 — no clamp needed.
	tokensNeeded := float64(policy.Limit) - b.tokens
	secondsToFull := tokensNeeded / policy.RefillPerSecond
	resetAt := now.Add(time.Duration(secondsToFull * float64(time.Second)))

	d := Decision{
		Allowed:   allowed,
		Remaining: remaining,
		Limit:     policy.Limit,
		ResetAt:   resetAt,
	}
	if !allowed {
		// Time until one token is available. When denied b.tokens < 1.0, so
		// (1 - b.tokens) > 0 and secs > 0 — no clamp needed.
		secs := (1.0 - b.tokens) / policy.RefillPerSecond
		d.RetryAfter = time.Duration(secs * float64(time.Second))
	}
	return d, nil
}

// shardFor returns the shard index for a key using FNV-1a. We use the
// 32-bit variant because shard counts are small.
func (t *TokenBucket) shardFor(key string) *tbShard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return t.shards[h.Sum32()%uint32(len(t.shards))] //nolint:gosec // shard count is a small slice sized once in NewTokenBucket; len cannot exceed uint32
}

// Reset clears all bucket state. Useful in tests; not exposed in the public
// API of the server.
func (t *TokenBucket) Reset() {
	for _, s := range t.shards {
		s.mu.Lock()
		s.buckets = make(map[string]*tbState)
		s.mu.Unlock()
	}
}

// ApproxSize returns the approximate total number of tracked keys across
// all shards. It locks each shard briefly; do not call on the hot path.
func (t *TokenBucket) ApproxSize() int {
	n := 0
	for _, s := range t.shards {
		s.mu.Lock()
		n += len(s.buckets)
		s.mu.Unlock()
	}
	return n
}
