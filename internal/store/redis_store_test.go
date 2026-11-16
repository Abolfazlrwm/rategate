package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/Abolfazlrwm/rategate/internal/algorithms"
)

// newMiniRedis returns a RedisStore backed by an in-process miniredis for
// deterministic, network-free testing of the Lua scripts.
func newMiniRedis(t *testing.T) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return NewRedisStoreWithClient(rdb, "rategate"), mr
}

func TestRedisStore_TokenBucketBasic(t *testing.T) {
	s, mr := newMiniRedis(t)
	defer s.Close()
	defer mr.Close()

	policy := algorithms.Policy{Limit: 3, RefillPerSecond: 1}
	now := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		d, err := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "u", policy, now)
		if err != nil {
			t.Fatalf("check %d: %v", i, err)
		}
		if !d.Allowed {
			t.Fatalf("check %d should be allowed", i)
		}
		wantRemaining := int64(3 - (i + 1))
		if d.Remaining != wantRemaining {
			t.Fatalf("check %d remaining=%d want=%d", i, d.Remaining, wantRemaining)
		}
	}
	// 4th denied.
	d, err := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "u", policy, now)
	if err != nil {
		t.Fatalf("4th: %v", err)
	}
	if d.Allowed {
		t.Fatal("4th should be denied")
	}
	if d.RetryAfter <= 0 {
		t.Fatalf("denied retry_after=%v want > 0", d.RetryAfter)
	}
}

func TestRedisStore_TokenBucketRefill(t *testing.T) {
	s, mr := newMiniRedis(t)
	defer s.Close()
	defer mr.Close()

	policy := algorithms.Policy{Limit: 2, RefillPerSecond: 2}
	t0 := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	// Drain.
	if d, _ := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "k", policy, t0); !d.Allowed {
		t.Fatal("1st should be allowed")
	}
	if d, _ := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "k", policy, t0); !d.Allowed {
		t.Fatal("2nd should be allowed")
	}
	// Denied immediately.
	if d, _ := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "k", policy, t0); d.Allowed {
		t.Fatal("3rd should be denied")
	}
	// After 500ms, 1 token refilled.
	t1 := t0.Add(500 * time.Millisecond)
	if d, _ := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "k", policy, t1); !d.Allowed {
		t.Fatal("should be allowed after 500ms (1 token)")
	}
	// Denied again.
	if d, _ := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "k", policy, t1); d.Allowed {
		t.Fatal("should be denied after single token consumed")
	}
}

func TestRedisStore_TokenBucketAtomicity(t *testing.T) {
	s, mr := newMiniRedis(t)
	defer s.Close()
	defer mr.Close()

	// Burst 1; many concurrent callers should admit exactly one.
	policy := algorithms.Policy{Limit: 1, RefillPerSecond: 0.001}
	now := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	const g = 32
	var wg sync.WaitGroup
	wg.Add(g)
	var allowed int64
	var mu sync.Mutex
	for i := 0; i < g; i++ {
		go func() {
			defer wg.Done()
			d, _ := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "shared", policy, now)
			if d.Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 1 {
		t.Fatalf("concurrent burst-1 allowed=%d want exactly 1 (race in Lua)", allowed)
	}
}

func TestRedisStore_SlidingWindowBasic(t *testing.T) {
	s, mr := newMiniRedis(t)
	defer s.Close()
	defer mr.Close()

	policy := algorithms.Policy{Limit: 2, Window: time.Minute}
	now := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 2; i++ {
		d, err := s.Check(context.Background(), algorithms.AlgorithmSlidingWindow, "u", policy, now)
		if err != nil {
			t.Fatalf("check %d: %v", i, err)
		}
		if !d.Allowed {
			t.Fatalf("check %d should be allowed", i)
		}
	}
	d, err := s.Check(context.Background(), algorithms.AlgorithmSlidingWindow, "u", policy, now)
	if err != nil {
		t.Fatalf("3rd: %v", err)
	}
	if d.Allowed {
		t.Fatal("3rd should be denied")
	}
	if d.RetryAfter <= 0 {
		t.Fatalf("denied retry_after=%v want > 0", d.RetryAfter)
	}
}

func TestRedisStore_SlidingWindowRolls(t *testing.T) {
	s, mr := newMiniRedis(t)
	defer s.Close()
	defer mr.Close()

	policy := algorithms.Policy{Limit: 1, Window: time.Second}
	t0 := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	if d, _ := s.Check(context.Background(), algorithms.AlgorithmSlidingWindow, "k", policy, t0); !d.Allowed {
		t.Fatal("1st should be allowed")
	}
	if d, _ := s.Check(context.Background(), algorithms.AlgorithmSlidingWindow, "k", policy, t0); d.Allowed {
		t.Fatal("2nd immediately should be denied")
	}
	// 1.2s later, window rolled.
	if d, _ := s.Check(context.Background(), algorithms.AlgorithmSlidingWindow, "k", policy, t0.Add(1200*time.Millisecond)); !d.Allowed {
		t.Fatal("should be allowed after window rolled")
	}
}

func TestRedisStore_UnknownAlgorithm(t *testing.T) {
	s, mr := newMiniRedis(t)
	defer s.Close()
	defer mr.Close()

	d, err := s.Check(context.Background(), "bogus", "k", algorithms.Policy{Limit: 1, RefillPerSecond: 1}, time.Now())
	if err == nil {
		t.Fatal("expected error for unknown algorithm")
	}
	if d.Allowed {
		t.Fatal("should not be allowed")
	}
}

func TestRedisStore_InvalidPolicy(t *testing.T) {
	s, mr := newMiniRedis(t)
	defer s.Close()
	defer mr.Close()

	_, err := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "k",
		algorithms.Policy{Limit: 0, RefillPerSecond: 1}, time.Now())
	if err == nil {
		t.Fatal("expected error for invalid policy")
	}
}

func TestRedisStore_Name(t *testing.T) {
	s, mr := newMiniRedis(t)
	defer mr.Close()
	defer s.Close()
	if s.Name() != "redis" {
		t.Fatalf("name=%q want redis", s.Name())
	}
}

func TestRedisStore_KeyPrefixFallback(t *testing.T) {
	// NewRedisStoreWithClient with empty prefix falls back to "rategate".
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	s := NewRedisStoreWithClient(rdb, "")
	defer s.Close()
	if s.keyPrefix != "rategate" {
		t.Fatalf("prefix=%q want rategate", s.keyPrefix)
	}
}

func TestRedisStore_NewRedisStoreConstructor(t *testing.T) {
	mr := miniredis.RunT(t)
	s := NewRedisStore(RedisOptions{Addr: mr.Addr(), KeyPrefix: "rg"})
	defer s.Close()
	// Verify it works end to end.
	d, err := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "k",
		algorithms.Policy{Limit: 1, RefillPerSecond: 1}, time.Now())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !d.Allowed {
		t.Fatal("first should be allowed")
	}
	if s.keyPrefix != "rg" {
		t.Fatalf("prefix=%q want rg", s.keyPrefix)
	}
}

// TestRedisStore_EmptyPrefixFallback exercises the prefixed() fallback that
// defaults an empty key prefix to "rategate" when NewRedisStore is used with
// an unset KeyPrefix.
func TestRedisStore_EmptyPrefixFallback(t *testing.T) {
	mr := miniredis.RunT(t)
	s := NewRedisStore(RedisOptions{Addr: mr.Addr()}) // empty prefix
	defer s.Close()
	if s.keyPrefix != "" {
		t.Fatalf("expected empty prefix on struct, got %q", s.keyPrefix)
	}
	d, err := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "k",
		algorithms.Policy{Limit: 1, RefillPerSecond: 1}, time.Now())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !d.Allowed {
		t.Fatal("first should be allowed even with empty prefix (fallback)")
	}
}

// TestRedisStore_EvalErrorWhenClientClosed exercises the EVAL error paths in
// Check by closing the underlying Redis client and then issuing checks.
func TestRedisStore_EvalErrorWhenClientClosed(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	s := NewRedisStoreWithClient(rdb, "rg")
	// Close the underlying client directly (not via Store.Close, which is
	// idempotent and would mask this).
	if err := rdb.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}

	policy := algorithms.Policy{Limit: 1, RefillPerSecond: 1}
	_, err := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "k", policy, time.Now())
	if err == nil {
		t.Fatal("expected error from tokenbucket eval on closed client")
	}
	swPolicy := algorithms.Policy{Limit: 1, Window: time.Second}
	_, err = s.Check(context.Background(), algorithms.AlgorithmSlidingWindow, "k", swPolicy, time.Now())
	if err == nil {
		t.Fatal("expected error from slidingwindow eval on closed client")
	}
}

func TestRedisStore_CloseNonClientNoOp(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	s := NewRedisStoreWithClient(rdb, "rg")
	// Wrap in a Cmdable that is not *redis.Client to exercise the type
	// assertion fallback in Close. We use the UniversalClient-style path
	// by passing the underlying *redis.Client directly here, then also
	// check that calling Close twice is safe.
	if err := s.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestRedisStore_MalformedResultFallback(t *testing.T) {
	// We can't easily inject a malformed Lua result, but we can exercise
	// the parseSlidingWindowResult remaining<0 clamp path by calling it
	// directly with a negative remaining string.
	d, err := parseSlidingWindowResult([]any{int64(0), "-5", "100"},
		algorithms.Policy{Limit: 1, Window: time.Second}, time.Now())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.Remaining != 0 {
		t.Fatalf("remaining=%d want 0 (clamped)", d.Remaining)
	}
	if d.Allowed {
		t.Fatal("should not be allowed")
	}
	if d.RetryAfter != 100*time.Millisecond {
		t.Fatalf("retry_after=%v want 100ms", d.RetryAfter)
	}
}

func TestRedisStore_ParseTokenBucketResultBadShape(t *testing.T) {
	// Non-array result should error.
	if _, err := parseTokenBucketResult("not-an-array",
		algorithms.Policy{Limit: 1, RefillPerSecond: 1}, time.Now()); err == nil {
		t.Fatal("expected error for non-array result")
	}
	// Short array should error.
	if _, err := parseTokenBucketResult([]any{int64(1)},
		algorithms.Policy{Limit: 1, RefillPerSecond: 1}, time.Now()); err == nil {
		t.Fatal("expected error for too-short array")
	}
	// string allowed iface path: denied with retry.
	d, _ := parseTokenBucketResult([]any{"0", "0", "500"},
		algorithms.Policy{Limit: 1, RefillPerSecond: 1}, time.Now())
	if d.Allowed {
		t.Fatal("string '0' should be denied")
	}
	if d.RetryAfter != 500*time.Millisecond {
		t.Fatalf("retry_after=%v want 500ms", d.RetryAfter)
	}
	// int64 allowed iface path: allowed.
	d2, _ := parseTokenBucketResult([]any{int64(1), "2", "0"},
		algorithms.Policy{Limit: 3, RefillPerSecond: 1}, time.Now())
	if !d2.Allowed {
		t.Fatal("int64 1 should be allowed")
	}
	if d2.Remaining != 2 {
		t.Fatalf("remaining=%d want 2", d2.Remaining)
	}
}

func TestRedisStore_ParseSlidingWindowResultBadShape(t *testing.T) {
	if _, err := parseSlidingWindowResult("bad",
		algorithms.Policy{Limit: 1, Window: time.Second}, time.Now()); err == nil {
		t.Fatal("expected error for non-array")
	}
	// string allowed iface path
	d, _ := parseSlidingWindowResult([]any{"1", "1", "0"},
		algorithms.Policy{Limit: 1, Window: time.Second}, time.Now())
	if !d.Allowed {
		t.Fatal("string '1' should be allowed")
	}
}

func TestRedisStore_ConcurrentSlidingWindow(t *testing.T) {
	s, mr := newMiniRedis(t)
	defer s.Close()
	defer mr.Close()

	policy := algorithms.Policy{Limit: 5, Window: time.Minute}
	now := time.Now()

	const g = 16
	const per = 5
	var wg sync.WaitGroup
	wg.Add(g)
	var allowed int64
	var mu sync.Mutex
	for i := 0; i < g; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < per; j++ {
				d, _ := s.Check(context.Background(), algorithms.AlgorithmSlidingWindow, "shared", policy, now)
				if d.Allowed {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	// Exactly `limit` should be admitted because the Lua script serializes.
	if allowed != policy.Limit {
		t.Fatalf("concurrent sliding-window allowed=%d want=%d", allowed, policy.Limit)
	}
}

// TestRedisStore_Ping verifies the Pinger implementation used by Health().
func TestRedisStore_Ping(t *testing.T) {
	s, mr := newMiniRedis(t)
	defer s.Close()
	defer mr.Close()

	// Happy path: a live miniredis returns nil.
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("ping on live redis: %v", err)
	}
}

// TestRedisStore_PingErrorOnClosedClient exercises the error return path of
// Ping when the backend is unreachable.
func TestRedisStore_PingErrorOnClosedClient(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	s := NewRedisStoreWithClient(rdb, "rg")
	// Close the miniredis so the client can't reach it.
	mr.Close()
	err := s.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error pinging a closed redis")
	}
}
