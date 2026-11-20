package algorithms

import (
	"testing"
	"time"
)

func TestTokenBucketSweepEvictsIdleKeys(t *testing.T) {
	tb := NewTokenBucket(4)
	now := time.Now()
	if _, err := tb.Check("a", Policy{Limit: 5, RefillPerSecond: 1}, now); err != nil {
		t.Fatalf("check a: %v", err)
	}
	if _, err := tb.Check("b", Policy{Limit: 5, RefillPerSecond: 1}, now); err != nil {
		t.Fatalf("check b: %v", err)
	}
	if tb.ApproxSize() != 2 {
		t.Fatalf("size=%d want 2", tb.ApproxSize())
	}
	// sweeping with a tiny idle window removes both; a longer one removes none
	if got := tb.Sweep(now.Add(time.Minute), 2*time.Minute); got != 0 {
		t.Fatalf("fresh sweep removed %d, want 0", got)
	}
	if got := tb.Sweep(now.Add(3*time.Minute), 2*time.Minute); got != 2 {
		t.Fatalf("idle sweep removed %d, want 2", got)
	}
	if tb.ApproxSize() != 0 {
		t.Fatalf("size=%d want 0 after sweep", tb.ApproxSize())
	}
	// a swept key starts a fresh full bucket on its next Check
	d, err := tb.Check("a", Policy{Limit: 5, RefillPerSecond: 1}, now.Add(4*time.Minute))
	if err != nil || !d.Allowed {
		t.Fatalf("post-sweep check: allowed=%v err=%v", d.Allowed, err)
	}
}

func TestSlidingWindowSweepEvictsIdleKeys(t *testing.T) {
	sw := NewSlidingWindow(SlidingWindowOptions{Shards: 4, Granularity: 6})
	now := time.Now()
	if _, err := sw.Check("k", Policy{Limit: 3, Window: time.Minute}, now); err != nil {
		t.Fatalf("check: %v", err)
	}
	if sw.ApproxSize() != 1 {
		t.Fatalf("size=%d want 1", sw.ApproxSize())
	}
	if got := sw.Sweep(now.Add(2*time.Minute), time.Minute); got != 1 {
		t.Fatalf("removed %d, want 1", got)
	}
	if sw.ApproxSize() != 0 {
		t.Fatalf("size=%d want 0 after sweep", sw.ApproxSize())
	}
}

func TestAdaptiveLimiterSweepEvictsIdleKeys(t *testing.T) {
	al := NewAdaptiveLimiter(AdaptiveOptions{Shards: 4})
	now := time.Now()
	if _, err := al.Check("k", Policy{Limit: 3, Window: time.Minute}, now); err != nil {
		t.Fatalf("check: %v", err)
	}
	if al.ApproxSize() != 1 {
		t.Fatalf("size=%d want 1", al.ApproxSize())
	}
	if got := al.Sweep(now.Add(2*time.Minute), time.Minute); got != 1 {
		t.Fatalf("removed %d, want 1", got)
	}
	if al.ApproxSize() != 0 {
		t.Fatalf("size=%d want 0 after sweep", al.ApproxSize())
	}
}
