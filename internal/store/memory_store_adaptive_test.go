package store

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Abolfazlrwm/rategate/internal/algorithms"
)

// TestMemoryStore_AdaptiveDispatch verifies the memory store routes the
// "adaptive" algorithm to its AdaptiveLimiter and that the AIMD behavior is
// reachable through the Store.Check interface (not just the algorithm directly).
func TestMemoryStore_AdaptiveDispatch(t *testing.T) {
	s := NewMemoryStore(MemoryOptions{AdaptiveShards: 8, AdaptiveMaxFactor: 4})
	defer s.Close()

	policy := algorithms.Policy{Limit: 4, Window: time.Second}
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

	// First window: behaves as a fixed window at the floor.
	for i := 0; i < 4; i++ {
		d, err := s.Check(context.Background(), algorithms.AlgorithmAdaptive, "k", policy, t0)
		if err != nil {
			t.Fatalf("check %d: %v", i, err)
		}
		if !d.Allowed {
			t.Fatalf("check %d should be allowed at floor", i)
		}
	}
	d, _ := s.Check(context.Background(), algorithms.AlgorithmAdaptive, "k", policy, t0)
	if d.Allowed {
		t.Fatal("5th should be denied at floor")
	}
	if d.Limit != 4 {
		t.Fatalf("floor limit=%d want 4", d.Limit)
	}

	// Close the window with rising demand so the limit grows; the first
	// request of the next window reports the grown effective limit.
	_, err := s.Check(context.Background(), algorithms.AlgorithmAdaptive, "k", policy, t0.Add(time.Second))
	if err != nil {
		t.Fatalf("window2 first: %v", err)
	}
	d, _ = s.Check(context.Background(), algorithms.AlgorithmAdaptive, "k", policy, t0.Add(time.Second))
	if d.Limit <= 4 {
		t.Fatalf("limit=%d should have grown above floor after rising demand", d.Limit)
	}
}

// TestMemoryStore_AdaptiveConcurrency proves the adaptive dispatch path is
// race-free under concurrent Check calls (the shard mutex must serialize
// per-key state). Exactly floor admits in the first window.
func TestMemoryStore_AdaptiveConcurrency(t *testing.T) {
	s := NewMemoryStore(MemoryOptions{AdaptiveShards: 16})
	defer s.Close()

	policy := algorithms.Policy{Limit: 6, Window: time.Second}
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

	const g = 48
	var wg sync.WaitGroup
	wg.Add(g)
	var admitted int64
	for i := 0; i < g; i++ {
		go func() {
			defer wg.Done()
			d, _ := s.Check(context.Background(), algorithms.AlgorithmAdaptive, "shared", policy, t0)
			if d.Allowed {
				atomic.AddInt64(&admitted, 1)
			}
		}()
	}
	wg.Wait()
	if admitted != 6 {
		t.Fatalf("concurrent admits=%d want exactly floor 6", admitted)
	}
}

// TestMemoryStore_AdaptiveReset confirms Reset clears adaptive state through
// the store, so the admin/test Reset path covers the adaptive limiter too.
func TestMemoryStore_AdaptiveReset(t *testing.T) {
	s := NewMemoryStore(MemoryOptions{AdaptiveShards: 4})
	defer s.Close()

	policy := algorithms.Policy{Limit: 2, Window: time.Second}
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	// Grow the limit above the floor.
	for w := 0; w < 4; w++ {
		_, _ = s.Check(context.Background(), algorithms.AlgorithmAdaptive, "k", policy, t0.Add(time.Duration(w)*time.Second))
	}
	s.Reset()
	// After reset, the first request starts fresh at the floor.
	d, _ := s.Check(context.Background(), algorithms.AlgorithmAdaptive, "k", policy, t0.Add(99*time.Second))
	if d.Limit != 2 {
		t.Fatalf("after reset, limit=%d want floor 2", d.Limit)
	}
}
