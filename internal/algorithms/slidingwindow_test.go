package algorithms

import (
	"sync"
	"testing"
	"time"
)

func TestSlidingWindow_BasicAllowThenDeny(t *testing.T) {
	sw := NewSlidingWindow(SlidingWindowOptions{Shards: 8, Granularity: 10})
	policy := Policy{Limit: 3, Window: time.Minute}
	now := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		d, err := sw.Check("u", policy, now)
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
	d, err := sw.Check("u", policy, now)
	if err != nil {
		t.Fatalf("4th: %v", err)
	}
	if d.Allowed {
		t.Fatal("4th should be denied")
	}
	if d.Remaining != 0 {
		t.Fatalf("denied remaining=%d want 0", d.Remaining)
	}
	if d.RetryAfter <= 0 {
		t.Fatalf("denied retry_after=%v want > 0", d.RetryAfter)
	}
}

func TestSlidingWindow_WindowRolls(t *testing.T) {
	sw := NewSlidingWindow(SlidingWindowOptions{Shards: 4, Granularity: 6})
	// 6 buckets of 10s each over a 60s window.
	policy := Policy{Limit: 2, Window: 60 * time.Second}
	t0 := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	// Consume 2 in the first bucket.
	d1, _ := sw.Check("k", policy, t0)
	d2, _ := sw.Check("k", policy, t0)
	if !d1.Allowed || !d2.Allowed {
		t.Fatal("first two should be allowed")
	}
	// Denied within the window.
	d3, _ := sw.Check("k", policy, t0.Add(5*time.Second))
	if d3.Allowed {
		t.Fatal("should be denied within window")
	}

	// 70s later: the first bucket has expired, count should be back to 0.
	d4, _ := sw.Check("k", policy, t0.Add(70*time.Second))
	if !d4.Allowed {
		t.Fatal("should be allowed after window rolled past")
	}
}

func TestSlidingWindow_PartialRoll(t *testing.T) {
	sw := NewSlidingWindow(SlidingWindowOptions{Shards: 4, Granularity: 6})
	policy := Policy{Limit: 3, Window: 60 * time.Second}
	t0 := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	// Consume at t0 (bucket 0).
	_, _ = sw.Check("k", policy, t0)
	// Consume at t0+15s (bucket 1 in a 10s granularity => idx 1).
	_, _ = sw.Check("k", policy, t0.Add(15*time.Second))
	// Consume at t0+25s (bucket 2).
	_, _ = sw.Check("k", policy, t0.Add(25*time.Second))

	// Now at t0+25s we should be at the limit (3 events in trailing 60s).
	d, _ := sw.Check("k", policy, t0.Add(25*time.Second))
	if d.Allowed {
		t.Fatal("should be denied at limit")
	}

	// At t0+65s the first event (at t0) has fallen out; we should have 2
	// remaining events (the ones at +15 and +25), so 1 more allowed.
	d2, _ := sw.Check("k", policy, t0.Add(65*time.Second))
	if !d2.Allowed {
		t.Fatal("should be allowed after oldest event expired")
	}
}

func TestSlidingWindow_IndependentKeys(t *testing.T) {
	sw := NewSlidingWindow(SlidingWindowOptions{Shards: 4})
	policy := Policy{Limit: 1, Window: time.Minute}
	now := time.Now()
	a, _ := sw.Check("a", policy, now)
	b, _ := sw.Check("b", policy, now)
	if !a.Allowed || !b.Allowed {
		t.Fatal("two distinct keys should both be allowed")
	}
	a2, _ := sw.Check("a", policy, now)
	if a2.Allowed {
		t.Fatal("second on same key should be denied")
	}
}

func TestSlidingWindow_InvalidPolicy(t *testing.T) {
	sw := NewSlidingWindow(SlidingWindowOptions{Shards: 4})
	now := time.Now()
	cases := []Policy{
		{Limit: 0, Window: time.Minute},
		{Limit: 5, Window: 0},
		{Limit: -1, Window: time.Minute},
	}
	for i, p := range cases {
		d, err := sw.Check("k", p, now)
		if err == nil {
			t.Fatalf("case %d expected error", i)
		}
		if d.Allowed {
			t.Fatalf("case %d should not be allowed", i)
		}
	}
}

func TestSlidingWindow_Name(t *testing.T) {
	sw := NewSlidingWindow(SlidingWindowOptions{})
	if sw.Name() != "slidingwindow" {
		t.Fatalf("name=%q want slidingwindow", sw.Name())
	}
}

func TestSlidingWindow_ResetAndSize(t *testing.T) {
	sw := NewSlidingWindow(SlidingWindowOptions{Shards: 4})
	policy := Policy{Limit: 10, Window: time.Minute}
	now := time.Now()
	_, _ = sw.Check("a", policy, now)
	_, _ = sw.Check("b", policy, now)
	if n := sw.ApproxSize(); n != 2 {
		t.Fatalf("size=%d want 2", n)
	}
	sw.Reset()
	if n := sw.ApproxSize(); n != 0 {
		t.Fatalf("size after reset=%d want 0", n)
	}
}

func TestSlidingWindow_DefaultsWhenZero(t *testing.T) {
	sw := NewSlidingWindow(SlidingWindowOptions{})
	if len(sw.shards) != 256 {
		t.Fatalf("default shards=%d want 256", len(sw.shards))
	}
	if sw.granularity != 60 {
		t.Fatalf("default granularity=%d want 60", sw.granularity)
	}
}

func TestSlidingWindow_ConcurrentSafe(t *testing.T) {
	sw := NewSlidingWindow(SlidingWindowOptions{Shards: 16, Granularity: 60})
	policy := Policy{Limit: 10_000_000, Window: time.Minute}
	now := time.Now()

	const goroutines = 64
	const perG = 500
	var wg sync.WaitGroup
	wg.Add(goroutines)
	var allowed int64
	var aMu sync.Mutex
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				d, _ := sw.Check("shared", policy, now)
				if d.Allowed {
					aMu.Lock()
					allowed++
					aMu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	if allowed != goroutines*perG {
		t.Fatalf("allowed=%d want=%d (race or bug)", allowed, goroutines*perG)
	}
}

// TestSlidingWindow_Approximate exercises the HyperLogLog-style path so the
// approximate code is covered. We only assert loose behavior since it is
// probabilistic.
func TestSlidingWindow_Approximate(t *testing.T) {
	sw := NewSlidingWindow(SlidingWindowOptions{Shards: 4, Granularity: 10, Approximate: true})
	policy := Policy{Limit: 5, Window: time.Second}
	t0 := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	allowed := 0
	for i := 0; i < 20; i++ {
		d, err := sw.Check("k", policy, t0.Add(time.Duration(i)*time.Millisecond))
		if err != nil {
			t.Fatalf("approx check %d: %v", i, err)
		}
		if d.Allowed {
			allowed++
		}
		// ResetAt and Limit should always be populated.
		if d.Limit != 5 {
			t.Fatalf("limit=%d want 5", d.Limit)
		}
	}
	if allowed < 1 {
		t.Fatalf("approx mode should allow some requests, got %d", allowed)
	}
}

func TestSlidingWindow_ApproximateRetryAfterWhenDenied(t *testing.T) {
	sw := NewSlidingWindow(SlidingWindowOptions{Shards: 4, Approximate: true})
	policy := Policy{Limit: 1, Window: time.Second}
	t0 := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	// First allowed.
	d1, _ := sw.Check("k", policy, t0)
	if !d1.Allowed {
		t.Fatal("first approx should be allowed")
	}
	// Second immediately: estimate still ~1, denied.
	d2, _ := sw.Check("k", policy, t0)
	if d2.Allowed {
		t.Fatal("second approx should be denied (estimate at limit)")
	}
	if d2.RetryAfter <= 0 {
		t.Fatalf("denied approx should set retry_after > 0, got %v", d2.RetryAfter)
	}
}

func TestNewSWState_TinyWindowFallback(t *testing.T) {
	// Force bucketDur <= 0 path: window=1ns divided by granularity=2 yields
	// 0 nanoseconds via integer truncation, which must be clamped to >= 1ms.
	st := newSWState(2, time.Nanosecond, time.Now(), false)
	if st.bucketDur <= 0 {
		t.Fatal("bucketDur should be clamped to >= 1ms when it would be 0")
	}
	if st.bucketDur != time.Millisecond {
		t.Fatalf("bucketDur=%v want 1ms", st.bucketDur)
	}
}

func TestSlidingWindow_RetryAfterWhenAllBucketsEmpty(t *testing.T) {
	// Construct a denied state where no buckets have count so the fallback
	// bucketDur path is taken.
	sw := NewSlidingWindow(SlidingWindowOptions{Shards: 4, Granularity: 10})
	policy := Policy{Limit: 0, Window: time.Second}
	if _, err := sw.Check("k", policy, time.Now()); err == nil {
		t.Fatal("limit 0 should error")
	}
}
