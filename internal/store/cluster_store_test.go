package store

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/Abolfazlrwm/rategate/internal/algorithms"
)

// newMiniCluster returns a ClusterStore backed by an in-process miniredis via
// NewClusterStoreWithClient. A real Redis Cluster is not needed to exercise
// the Check path: the embedded RedisStore runs its single-key Lua scripts
// against any redis.Cmdable, and a miniredis *redis.Client satisfies that.
// This proves the delegation wiring and that the Lua scripts are slot-safe
// (single KEYS[1]) without requiring a multi-node fixture.
func newMiniCluster(t *testing.T) (*ClusterStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return NewClusterStoreWithClient(rdb, "rategate"), mr
}

func TestClusterStore_TokenBucket(t *testing.T) {
	s, mr := newMiniCluster(t)
	defer s.Close()
	defer mr.Close()

	policy := algorithms.Policy{Limit: 2, RefillPerSecond: 1}
	now := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 2; i++ {
		d, err := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "u", policy, now)
		if err != nil {
			t.Fatalf("check %d: %v", i, err)
		}
		if !d.Allowed {
			t.Fatalf("check %d should be allowed", i)
		}
		if d.Remaining != int64(2-(i+1)) {
			t.Fatalf("check %d remaining=%d want=%d", i, d.Remaining, 2-(i+1))
		}
	}
	d, err := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "u", policy, now)
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

func TestClusterStore_SlidingWindow(t *testing.T) {
	s, mr := newMiniCluster(t)
	defer s.Close()
	defer mr.Close()

	policy := algorithms.Policy{Limit: 2, Window: time.Minute}
	now := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 2; i++ {
		d, _ := s.Check(context.Background(), algorithms.AlgorithmSlidingWindow, "u", policy, now)
		if !d.Allowed {
			t.Fatalf("check %d should be allowed", i)
		}
	}
	d, _ := s.Check(context.Background(), algorithms.AlgorithmSlidingWindow, "u", policy, now)
	if d.Allowed {
		t.Fatal("3rd should be denied")
	}
	// Window rolls past -> allowed again.
	d2, _ := s.Check(context.Background(), algorithms.AlgorithmSlidingWindow, "u", policy, now.Add(90*time.Second))
	if !d2.Allowed {
		t.Fatal("should be allowed after window rolled")
	}
}

// TestClusterStore_AtomicallyDelegates proves the embedded Lua path is used:
// a burst-1 policy under heavy concurrency must admit exactly one, which only
// holds if the atomic EVAL is running (a non-atomic delegation would
// over-admit). This is the cluster-store analogue of
// TestRedisStore_TokenBucketAtomicity.
func TestClusterStore_AtomicallyDelegates(t *testing.T) {
	s, mr := newMiniCluster(t)
	defer s.Close()
	defer mr.Close()

	policy := algorithms.Policy{Limit: 1, RefillPerSecond: 0.001}
	now := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	const g = 32
	var wg sync.WaitGroup
	wg.Add(g)
	var allowed int64
	for i := 0; i < g; i++ {
		go func() {
			defer wg.Done()
			d, _ := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "shared", policy, now)
			if d.Allowed {
				atomic.AddInt64(&allowed, 1)
			}
		}()
	}
	wg.Wait()
	if allowed != 1 {
		t.Fatalf("concurrent burst-1 allowed=%d want exactly 1 (Lua not delegated atomically)", allowed)
	}
}

func TestClusterStore_Name(t *testing.T) {
	s, mr := newMiniCluster(t)
	defer mr.Close()
	defer s.Close()
	if s.Name() != "redis-cluster" {
		t.Fatalf("name=%q want redis-cluster", s.Name())
	}
	// Confirm the override actually shadows the embedded RedisStore.Name.
	if embedded := s.RedisStore.Name(); embedded != "redis" {
		t.Fatalf("embedded name=%q want redis (shadowed by override)", embedded)
	}
}

func TestClusterStore_Ping(t *testing.T) {
	s, mr := newMiniCluster(t)
	defer s.Close()
	defer mr.Close()
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("ping on live backend: %v", err)
	}
}

// TestClusterStore_PingError verifies the inherited Pinger surfaces backend
// failures (so Health() correctly reports NOT_SERVING when the cluster is
// unreachable).
func TestClusterStore_PingError(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	s := NewClusterStoreWithClient(rdb, "rg")
	mr.Close()
	if err := s.Ping(context.Background()); err == nil {
		t.Fatal("expected error pinging a closed backend")
	}
}

// TestClusterStore_CloseIdempotentOnInjectedClient verifies Close is safe to
// call twice and is a no-op when the injected Cmdable is not a *ClusterClient.
func TestClusterStore_CloseIdempotentOnInjectedClient(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	s := NewClusterStoreWithClient(rdb, "rg")
	if err := s.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

// TestClusterStore_UnknownAlgorithm verifies the embedded dispatch surfaces
// ErrUnknownAlgorithm rather than silently treating an unknown algo as a
// token-bucket check.
func TestClusterStore_UnknownAlgorithm(t *testing.T) {
	s, mr := newMiniCluster(t)
	defer s.Close()
	defer mr.Close()
	_, err := s.Check(context.Background(), "bogus", "k",
		algorithms.Policy{Limit: 1, RefillPerSecond: 1}, time.Now())
	if err == nil {
		t.Fatal("expected ErrUnknownAlgorithm")
	}
}

// TestClusterStore_InvalidPolicy verifies policy validation is inherited.
func TestClusterStore_InvalidPolicy(t *testing.T) {
	s, mr := newMiniCluster(t)
	defer s.Close()
	defer mr.Close()
	_, err := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "k",
		algorithms.Policy{Limit: 0, RefillPerSecond: 1}, time.Now())
	if err == nil {
		t.Fatal("expected error for invalid policy")
	}
}

// TestClusterStore_NewClusterStoreConstructor exercises the production
// constructor: it must build a working store whose client is a
// *redis.ClusterClient (so Close actually closes it). We cannot reach a real
// cluster from the sandbox, so we only assert the client type and that Close
// is callable without panicking.
func TestClusterStore_NewClusterStoreConstructor(t *testing.T) {
	s := NewClusterStore(ClusterOptions{
		Addrs:        []string{"127.0.0.1:7000"}, // unreachable, but client is built
		KeyPrefix:    "rg",
		MaxRedirects: 3,
	})
	if _, ok := s.client.(*redis.ClusterClient); !ok {
		t.Fatalf("expected *redis.ClusterClient, got %T", s.client)
	}
	if s.Name() != "redis-cluster" {
		t.Fatalf("name=%q want redis-cluster", s.Name())
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
