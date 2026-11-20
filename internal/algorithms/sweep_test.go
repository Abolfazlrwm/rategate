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

func TestSlidingWindowSweepUsesDecayStampForApproximate(t *testing.T) {
	// In approximate mode lastDecay advances on every Check while
	// lastRotate stays at state creation, so Sweep must consider the
	// newer of the two stamps when deciding idleness.
	sw := NewSlidingWindow(SlidingWindowOptions{Shards: 4, Granularity: 6, Approximate: true})
	now := time.Now()

	// "old" is touched once and abandoned; "fresh" gets a second hit at
	// now+30s, pushing its lastDecay (30s) past its lastRotate (0s).
	if _, err := sw.Check("old", Policy{Limit: 3, Window: time.Minute}, now); err != nil {
		t.Fatalf("check old: %v", err)
	}
	if _, err := sw.Check("fresh", Policy{Limit: 3, Window: time.Minute}, now); err != nil {
		t.Fatalf("check fresh: %v", err)
	}
	later := now.Add(30 * time.Second)
	if _, err := sw.Check("fresh", Policy{Limit: 3, Window: time.Minute}, later); err != nil {
		t.Fatalf("re-check fresh: %v", err)
	}

	// Sweep at now+55s with idle 30s: "old" has been idle 55s -> evicted;
	// "fresh" saw traffic 25s ago -> kept thanks to the decay stamp.
	if got := sw.Sweep(now.Add(55*time.Second), 30*time.Second); got != 1 {
		t.Fatalf("removed %d, want 1 (only 'old')", got)
	}
	if sw.ApproxSize() != 1 {
		t.Fatalf("size=%d want 1 ('fresh' survives)", sw.ApproxSize())
	}
	// A later sweep past the fresh key's idle window evicts it too.
	if got := sw.Sweep(later.Add(2*time.Minute), time.Minute); got != 1 {
		t.Fatalf("second sweep removed %d, want 1", got)
	}
	if sw.ApproxSize() != 0 {
		t.Fatalf("size=%d want 0 after both sweeps", sw.ApproxSize())
	}
}
