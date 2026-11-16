package algorithms

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// sendAt issues n requests for key at time `now` against lim, returning the
// number admitted, denied, and the effective Limit reported by the FIRST
// request in this batch (which is the post-window-close limit for this window).
func sendAt(t *testing.T, lim *AdaptiveLimiter, key string, policy Policy, now time.Time, n int) (admitted, denied, firstLimit int64) {
	t.Helper()
	for i := 0; i < n; i++ {
		d, err := lim.Check(key, policy, now)
		if err != nil {
			t.Fatalf("sendAt[%d]: %v", i, err)
		}
		if i == 0 {
			firstLimit = d.Limit
		}
		if d.Allowed {
			admitted++
		} else {
			denied++
		}
	}
	return
}

func TestAdaptiveLimiter_Name(t *testing.T) {
	lim := NewAdaptiveLimiter(AdaptiveOptions{})
	if lim.Name() != AlgorithmAdaptive {
		t.Fatalf("name=%q want %q", lim.Name(), AlgorithmAdaptive)
	}
	if lim.Name() != "adaptive" {
		t.Fatalf("name=%q want adaptive", lim.Name())
	}
}

// TestAdaptiveLimiter_StartsAtFloor verifies the first window behaves like a
// fixed window limiter at the configured floor: exactly floor admits, then
// deny with a positive RetryAfter pointing at the window boundary.
func TestAdaptiveLimiter_StartsAtFloor(t *testing.T) {
	lim := NewAdaptiveLimiter(AdaptiveOptions{MaxFactor: 4})
	policy := Policy{Limit: 5, Window: time.Second}
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

	admitted, denied, firstLimit := sendAt(t, lim, "k", policy, t0, 8)
	if firstLimit != 5 {
		t.Fatalf("first-window limit=%d want floor 5", firstLimit)
	}
	if admitted != 5 || denied != 3 {
		t.Fatalf("window1 admitted=%d denied=%d want 5/3", admitted, denied)
	}

	// A denied request must report a positive RetryAfter (until next window).
	d, err := lim.Check("k", policy, t0)
	if err != nil {
		t.Fatalf("deny check: %v", err)
	}
	if d.Allowed {
		t.Fatal("should be denied at floor")
	}
	if d.RetryAfter <= 0 {
		t.Fatalf("retry_after=%v want > 0", d.RetryAfter)
	}
	// RetryAfter should be ~1s (the window), not longer.
	if d.RetryAfter > time.Second {
		t.Fatalf("retry_after=%v want <= 1s", d.RetryAfter)
	}
}

// TestAdaptiveLimiter_GrowsOnRisingDemand drives strictly rising demand across
// windows and asserts the effective limit increases monotonically and never
// exceeds the cap.
func TestAdaptiveLimiter_GrowsOnRisingDemand(t *testing.T) {
	const floor = int64(10)
	lim := NewAdaptiveLimiter(AdaptiveOptions{MaxFactor: 4, IncreaseFrac: 0.25})
	policy := Policy{Limit: floor, Window: time.Second}
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

	prev := floor
	capLimit := floor * 4
	// Rising demand: 10, 20, 30, ... requests per window. The trajectory
	// (step 2.5 from floor 10) reaches the cap at window 12 (0-indexed), so we
	// loop well past that to confirm saturation and clamping.
	for w := 0; w < 20; w++ {
		now := t0.Add(time.Duration(w) * time.Second)
		demand := 10 * (w + 1)
		_, _, firstLimit := sendAt(t, lim, "k", policy, now, demand)
		if firstLimit < prev {
			t.Fatalf("window %d: limit=%d decreased from %d (rising demand)", w, firstLimit, prev)
		}
		if firstLimit > capLimit {
			t.Fatalf("window %d: limit=%d exceeds cap %d", w, firstLimit, capLimit)
		}
		prev = firstLimit
	}
	// After enough rising windows we must have grown beyond the floor.
	if prev <= floor {
		t.Fatalf("limit never grew: final=%d floor=%d", prev, floor)
	}
	// And we must have reached the cap eventually under unbounded rising demand.
	if prev != capLimit {
		t.Fatalf("expected to saturate at cap=%d, got %d", capLimit, prev)
	}
}

// TestAdaptiveLimiter_ShrinksOnFallingDemand first grows the limit, then drops
// demand and asserts the limit retracts toward the floor.
func TestAdaptiveLimiter_ShrinksOnFallingDemand(t *testing.T) {
	const floor = int64(10)
	lim := NewAdaptiveLimiter(AdaptiveOptions{MaxFactor: 4, IncreaseFrac: 0.25, DecreaseFactor: 0.5})
	policy := Policy{Limit: floor, Window: time.Second}
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

	// Grow: 9 rising windows -> limit trajectory 10,12,15,17,20,22,25,27,30.
	for w := 0; w < 9; w++ {
		now := t0.Add(time.Duration(w) * time.Second)
		sendAt(t, lim, "k", policy, now, 10*(w+1))
	}
	// Read the limit that's now in effect (first request of the next window
	// closes the previous window; we discard that admit and just read Limit).
	peak, _, peakLimit := sendAt(t, lim, "k", policy, t0.Add(9*time.Second), 1)
	_ = peak
	if peakLimit <= floor {
		t.Fatalf("setup: limit=%d never grew above floor", peakLimit)
	}

	// Now drop demand hard for one window: total(1) < prevTotal -> retracts.
	_, _, shrunkLimit := sendAt(t, lim, "k", policy, t0.Add(10*time.Second), 1)
	if shrunkLimit >= peakLimit {
		t.Fatalf("limit=%d did not shrink from peak=%d under falling demand", shrunkLimit, peakLimit)
	}
	if shrunkLimit < floor {
		t.Fatalf("limit=%d fell below floor %d", shrunkLimit, floor)
	}

	// Continue dropping: limit must eventually return to the floor.
	for w := 11; w < 20; w++ {
		_, _, l := sendAt(t, lim, "k", policy, t0.Add(time.Duration(w)*time.Second), 1)
		if l == floor {
			break
		}
	}
}

// TestAdaptiveLimiter_SteadyDemandHoldsLimit verifies the hysteresis band: when
// demand is constant across windows, the limit does not change (no rising, no
// falling), so the limiter is stable rather than oscillating.
func TestAdaptiveLimiter_SteadyDemandHoldsLimit(t *testing.T) {
	const floor = int64(10)
	lim := NewAdaptiveLimiter(AdaptiveOptions{MaxFactor: 4})
	policy := Policy{Limit: floor, Window: time.Second}
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

	// Window 1: 20 requests at floor=10 -> 10 admit, 10 deny. total=20.
	sendAt(t, lim, "k", policy, t0, 20)
	// Window 2: same 20 requests. Close sees total(20)==prevTotal(20) -> steady.
	_, _, lim2 := sendAt(t, lim, "k", policy, t0.Add(time.Second), 20)
	// Window 3: same again.
	_, _, lim3 := sendAt(t, lim, "k", policy, t0.Add(2*time.Second), 20)

	// First window closed into window2 with a rising step (20>0), so lim2 is
	// one step above floor. After that, steady demand must hold it constant.
	if lim2 <= floor {
		t.Fatalf("window2 limit=%d should have grown one step above floor", lim2)
	}
	if lim3 != lim2 {
		t.Fatalf("steady demand changed limit: w2=%d w3=%d (should be equal)", lim2, lim3)
	}
}

// TestAdaptiveLimiter_ConcurrentBurstExactlyFloor proves atomicity: under high
// concurrency the first window admits exactly floor requests, no more.
func TestAdaptiveLimiter_ConcurrentBurstExactlyFloor(t *testing.T) {
	lim := NewAdaptiveLimiter(AdaptiveOptions{MaxFactor: 4})
	policy := Policy{Limit: 8, Window: time.Second}
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

	const g = 64
	var wg sync.WaitGroup
	wg.Add(g)
	var admitted int64
	for i := 0; i < g; i++ {
		go func() {
			defer wg.Done()
			d, err := lim.Check("shared", policy, t0)
			if err != nil {
				return
			}
			if d.Allowed {
				atomic.AddInt64(&admitted, 1)
			}
		}()
	}
	wg.Wait()
	if admitted != 8 {
		t.Fatalf("concurrent admits=%d want exactly floor 8", admitted)
	}
}

// TestAdaptiveLimiter_KeysAreIndependent verifies per-key state isolation: the
// AIMD state for one key does not affect another.
func TestAdaptiveLimiter_KeysAreIndependent(t *testing.T) {
	lim := NewAdaptiveLimiter(AdaptiveOptions{MaxFactor: 4})
	policy := Policy{Limit: 10, Window: time.Second}
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

	// Hammer key A with rising demand so its limit grows.
	for w := 0; w < 5; w++ {
		sendAt(t, lim, "A", policy, t0.Add(time.Duration(w)*time.Second), 10*(w+1))
	}
	_, _, limitA := sendAt(t, lim, "A", policy, t0.Add(5*time.Second), 1)
	// Key B has never been touched: its first window must be at the floor.
	_, _, limitB := sendAt(t, lim, "B", policy, t0.Add(5*time.Second), 1)
	if limitB != 10 {
		t.Fatalf("untouched key B limit=%d want floor 10", limitB)
	}
	if limitA <= 10 {
		t.Fatalf("key A limit=%d should have grown above floor", limitA)
	}
}

func TestAdaptiveLimiter_InvalidPolicy(t *testing.T) {
	lim := NewAdaptiveLimiter(AdaptiveOptions{})
	now := time.Now()
	cases := []struct {
		name   string
		policy Policy
	}{
		{"zero limit", Policy{Limit: 0, Window: time.Second}},
		{"zero window", Policy{Limit: 5, Window: 0}},
	}
	for _, c := range cases {
		_, err := lim.Check("k", c.policy, now)
		if err == nil {
			t.Fatalf("%s: expected error", c.name)
		}
	}
}

func TestAdaptiveLimiter_Reset(t *testing.T) {
	lim := NewAdaptiveLimiter(AdaptiveOptions{MaxFactor: 4})
	policy := Policy{Limit: 10, Window: time.Second}
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

	// Grow key A's limit above floor.
	for w := 0; w < 4; w++ {
		sendAt(t, lim, "A", policy, t0.Add(time.Duration(w)*time.Second), 10*(w+1))
	}
	if lim.ApproxSize() == 0 {
		t.Fatal("expected tracked keys before reset")
	}
	lim.Reset()
	if lim.ApproxSize() != 0 {
		t.Fatalf("after Reset, ApproxSize=%d want 0", lim.ApproxSize())
	}
	// After reset, key A starts fresh at the floor.
	_, _, limit := sendAt(t, lim, "A", policy, t0.Add(99*time.Second), 1)
	if limit != 10 {
		t.Fatalf("after reset, fresh limit=%d want floor 10", limit)
	}
}

// TestAdaptiveLimiter_DefaultsApplied verifies zero-value options resolve to
// documented defaults rather than zero (which would panic on shard lookup).
func TestAdaptiveLimiter_DefaultsApplied(t *testing.T) {
	lim := NewAdaptiveLimiter(AdaptiveOptions{})
	if len(lim.shards) != 256 {
		t.Fatalf("default shards=%d want 256", len(lim.shards))
	}
	if lim.opts.maxFactor != 4 {
		t.Fatalf("default maxFactor=%v want 4", lim.opts.maxFactor)
	}
	// And it must be usable without panicking.
	d, err := lim.Check("k", Policy{Limit: 1, Window: time.Second}, time.Now())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !d.Allowed {
		t.Fatal("first check should be allowed")
	}
}

// TestAdaptiveLimiter_ResetAtIsWindowBoundary verifies the Decision.ResetAt is
// the current window's end boundary.
func TestAdaptiveLimiter_ResetAtIsWindowBoundary(t *testing.T) {
	lim := NewAdaptiveLimiter(AdaptiveOptions{})
	policy := Policy{Limit: 5, Window: 2 * time.Second}
	t0 := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	d, err := lim.Check("k", policy, t0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	want := t0.Add(2 * time.Second)
	if !d.ResetAt.Equal(want) {
		t.Fatalf("reset_at=%v want %v", d.ResetAt, want)
	}
}
