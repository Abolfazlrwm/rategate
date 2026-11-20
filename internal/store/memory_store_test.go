package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Abolfazlrwm/rategate/internal/algorithms"
)

func TestMemoryStore_TokenBucket(t *testing.T) {
	s := NewMemoryStore(MemoryOptions{TBShards: 8})
	defer s.Close()

	if s.Name() != "memory" {
		t.Fatalf("name=%q want memory", s.Name())
	}

	policy := algorithms.Policy{Limit: 3, RefillPerSecond: 1}
	now := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		d, err := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "k", policy, now)
		if err != nil {
			t.Fatalf("check %d: %v", i, err)
		}
		if !d.Allowed {
			t.Fatalf("check %d should be allowed", i)
		}
	}
	d, err := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "k", policy, now)
	if err != nil {
		t.Fatalf("4th: %v", err)
	}
	if d.Allowed {
		t.Fatal("4th should be denied")
	}
}

func TestMemoryStore_SlidingWindow(t *testing.T) {
	s := NewMemoryStore(MemoryOptions{SWShards: 8, SWGranularity: 10})
	defer s.Close()

	policy := algorithms.Policy{Limit: 2, Window: time.Minute}
	now := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 2; i++ {
		d, err := s.Check(context.Background(), algorithms.AlgorithmSlidingWindow, "k", policy, now)
		if err != nil {
			t.Fatalf("check %d: %v", i, err)
		}
		if !d.Allowed {
			t.Fatalf("check %d should be allowed", i)
		}
	}
	d, _ := s.Check(context.Background(), algorithms.AlgorithmSlidingWindow, "k", policy, now)
	if d.Allowed {
		t.Fatal("3rd should be denied")
	}
}

func TestMemoryStore_UnknownAlgorithm(t *testing.T) {
	s := NewMemoryStore(MemoryOptions{})
	defer s.Close()

	d, err := s.Check(context.Background(), "weird", "k", algorithms.Policy{Limit: 1, RefillPerSecond: 1}, time.Now())
	if err == nil {
		t.Fatal("expected error for unknown algorithm")
	}
	if d.Allowed {
		t.Fatal("should not be allowed for unknown algorithm")
	}
	if _, ok := err.(ErrUnknownAlgorithm); !ok {
		t.Fatalf("err type=%T want ErrUnknownAlgorithm", err)
	}
}

func TestMemoryStore_InvalidPolicy(t *testing.T) {
	s := NewMemoryStore(MemoryOptions{})
	defer s.Close()

	_, err := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "k",
		algorithms.Policy{Limit: 0, RefillPerSecond: 1}, time.Now())
	if err == nil {
		t.Fatal("expected error for invalid policy")
	}
}

func TestMemoryStore_Reset(t *testing.T) {
	s := NewMemoryStore(MemoryOptions{TBShards: 4})
	defer s.Close()

	policy := algorithms.Policy{Limit: 1, RefillPerSecond: 0.001}
	now := time.Now()
	_, _ = s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "k", policy, now)
	// Second should be denied.
	d, _ := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "k", policy, now)
	if d.Allowed {
		t.Fatal("second should be denied before reset")
	}
	s.Reset()
	// After reset, bucket starts full again.
	d2, _ := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "k", policy, now)
	if !d2.Allowed {
		t.Fatal("first after reset should be allowed")
	}
}

func TestMemoryStore_DefaultsWhenZero(t *testing.T) {
	s := NewMemoryStore(MemoryOptions{})
	defer s.Close()
	// Just exercise that zero-value options don't panic.
	d, _ := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "k",
		algorithms.Policy{Limit: 5, RefillPerSecond: 1}, time.Now())
	if !d.Allowed {
		t.Fatal("default memory store should allow first request")
	}
}

func TestMemoryStore_ConcurrentSafe(t *testing.T) {
	s := NewMemoryStore(MemoryOptions{TBShards: 16})
	defer s.Close()

	policy := algorithms.Policy{Limit: 1 << 30, RefillPerSecond: 1 << 30}
	now := time.Now()
	const g = 32
	const per = 200
	var wg sync.WaitGroup
	wg.Add(g)
	var allowed int64
	var mu sync.Mutex
	for i := 0; i < g; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < per; j++ {
				d, _ := s.Check(context.Background(), algorithms.AlgorithmTokenBucket, "shared", policy, now)
				if d.Allowed {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	if allowed != g*per {
		t.Fatalf("allowed=%d want=%d", allowed, g*per)
	}
}

func TestErrUnknownAlgorithm_Message(t *testing.T) {
	e := ErrUnknownAlgorithm{Algo: "x"}
	if e.Error() == "" {
		t.Fatal("error message empty")
	}
}

func TestMemoryStore_SweepEvictsIdleKeysAcrossAlgorithms(t *testing.T) {
	s := NewMemoryStore(MemoryOptions{TBShards: 8, SWShards: 8, SWGranularity: 6, AdaptiveShards: 8})
	defer s.Close()

	policy := algorithms.Policy{Limit: 5, RefillPerSecond: 1, Window: time.Minute}
	now := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	algos := []string{algorithms.AlgorithmTokenBucket, algorithms.AlgorithmSlidingWindow, algorithms.AlgorithmAdaptive}
	for _, algo := range algos {
		if _, err := s.Check(context.Background(), algo, "k", policy, now); err != nil {
			t.Fatalf("check %s: %v", algo, err)
		}
	}

	// A generous idle removes nothing from keys that just saw traffic.
	if got := s.Sweep(now.Add(time.Minute), 2*time.Minute); got != 0 {
		t.Fatalf("fresh sweep removed %d, want 0", got)
	}
	// Once idle past the window, one entry is evicted per algorithm.
	if got := s.Sweep(now.Add(3*time.Minute), 2*time.Minute); got != 3 {
		t.Fatalf("idle sweep removed %d, want 3", got)
	}
	// Swept keys start from scratch and are admitted again.
	for _, algo := range algos {
		d, err := s.Check(context.Background(), algo, "k", policy, now.Add(4*time.Minute))
		if err != nil || !d.Allowed {
			t.Fatalf("post-sweep check %s: allowed=%v err=%v", algo, d.Allowed, err)
		}
	}
}
