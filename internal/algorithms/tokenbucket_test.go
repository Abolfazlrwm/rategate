package algorithms

import (
	"sync"
	"testing"
	"time"
)

func TestTokenBucket_BasicAllowThenDeny(t *testing.T) {
	tb := NewTokenBucket(16)
	policy := Policy{Limit: 5, RefillPerSecond: 1} // 1 token/sec, burst 5
	now := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	// First 5 should be allowed (bucket starts full).
	for i := 0; i < 5; i++ {
		d, err := tb.Check("user:1", policy, now)
		if err != nil {
			t.Fatalf("check %d: unexpected error: %v", i, err)
		}
		if !d.Allowed {
			t.Fatalf("check %d: expected allowed, got denied (remaining=%d)", i, d.Remaining)
		}
		wantRemaining := int64(5 - (i + 1))
		if d.Remaining != wantRemaining {
			t.Fatalf("check %d: remaining=%d want=%d", i, d.Remaining, wantRemaining)
		}
		if d.Limit != 5 {
			t.Fatalf("check %d: limit=%d want=5", i, d.Limit)
		}
		if d.RetryAfter != 0 {
			t.Fatalf("check %d: retry_after should be 0 when allowed, got %v", i, d.RetryAfter)
		}
	}

	// 6th should be denied.
	d, err := tb.Check("user:1", policy, now)
	if err != nil {
		t.Fatalf("6th check: unexpected error: %v", err)
	}
	if d.Allowed {
		t.Fatal("6th check: expected denied, got allowed")
	}
	if d.Remaining != 0 {
		t.Fatalf("6th check: remaining=%d want=0", d.Remaining)
	}
	if d.RetryAfter <= 0 {
		t.Fatalf("6th check: retry_after should be > 0 when denied, got %v", d.RetryAfter)
	}
}

func TestTokenBucket_RefillOverTime(t *testing.T) {
	tb := NewTokenBucket(16)
	policy := Policy{Limit: 2, RefillPerSecond: 2} // 2 tokens/sec, burst 2
	t0 := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	// Drain the bucket.
	d1, _ := tb.Check("k", policy, t0)
	d2, _ := tb.Check("k", policy, t0)
	if !d1.Allowed || !d2.Allowed {
		t.Fatal("expected both initial checks allowed")
	}

	// Denied immediately.
	d3, _ := tb.Check("k", policy, t0)
	if d3.Allowed {
		t.Fatal("expected denial after drain")
	}

	// After 500ms we should have exactly 1 token (refill rate 2/sec).
	t1 := t0.Add(500 * time.Millisecond)
	d4, _ := tb.Check("k", policy, t1)
	if !d4.Allowed {
		t.Fatal("expected allow after 500ms refill (1 token)")
	}

	// And denied again immediately after.
	d5, _ := tb.Check("k", policy, t1)
	if d5.Allowed {
		t.Fatal("expected denial after single-token refill consumed")
	}

	// After 1s more we should have 2 tokens (full).
	t2 := t1.Add(1 * time.Second)
	d6, _ := tb.Check("k", policy, t2)
	d7, _ := tb.Check("k", policy, t2)
	if !d6.Allowed || !d7.Allowed {
		t.Fatal("expected two allows after full refill")
	}
}

func TestTokenBucket_RefillCappedAtCapacity(t *testing.T) {
	tb := NewTokenBucket(8)
	policy := Policy{Limit: 3, RefillPerSecond: 100}
	t0 := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	// Drain.
	for i := 0; i < 3; i++ {
		if d, _ := tb.Check("k", policy, t0); !d.Allowed {
			t.Fatalf("check %d should be allowed", i)
		}
	}

	// Wait a long time; tokens should cap at 3, not exceed.
	t1 := t0.Add(1 * time.Hour)
	// 3 allowed, then denied.
	for i := 0; i < 3; i++ {
		if d, _ := tb.Check("k", policy, t1); !d.Allowed {
			t.Fatalf("post-wait check %d should be allowed (capped refill)", i)
		}
	}
	if d, _ := tb.Check("k", policy, t1); d.Allowed {
		t.Fatal("should be denied after capped refill consumed")
	}
}

func TestTokenBucket_IndependentKeys(t *testing.T) {
	tb := NewTokenBucket(8)
	policy := Policy{Limit: 1, RefillPerSecond: 0.001}
	now := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	da, _ := tb.Check("a", policy, now)
	db, _ := tb.Check("b", policy, now)
	if !da.Allowed || !db.Allowed {
		t.Fatal("two distinct keys should both be allowed")
	}
	// Second call on each should be denied (burst 1, slow refill).
	da2, _ := tb.Check("a", policy, now)
	db2, _ := tb.Check("b", policy, now)
	if da2.Allowed || db2.Allowed {
		t.Fatal("second call on each key should be denied")
	}
}

func TestTokenBucket_InvalidPolicy(t *testing.T) {
	tb := NewTokenBucket(4)
	now := time.Now()

	cases := []Policy{
		{Limit: 0, RefillPerSecond: 1},      // zero limit
		{Limit: -1, RefillPerSecond: 1},     // negative limit
		{Limit: 5, RefillPerSecond: 0},      // zero refill
		{Limit: 5, RefillPerSecond: -1},     // negative refill
	}
	for i, p := range cases {
		d, err := tb.Check("k", p, now)
		if err == nil {
			t.Fatalf("case %d: expected error for invalid policy %+v", i, p)
		}
		if d.Allowed {
			t.Fatalf("case %d: should not be allowed on invalid policy", i)
		}
		if d.Limit != p.Limit {
			t.Fatalf("case %d: decision.Limit=%d want=%d", i, d.Limit, p.Limit)
		}
	}
}

func TestTokenBucket_Name(t *testing.T) {
	tb := NewTokenBucket(2)
	if tb.Name() != "tokenbucket" {
		t.Fatalf("name=%q want tokenbucket", tb.Name())
	}
}

func TestTokenBucket_ResetAndSize(t *testing.T) {
	tb := NewTokenBucket(4)
	policy := Policy{Limit: 10, RefillPerSecond: 1}
	now := time.Now()

	_, _ = tb.Check("a", policy, now)
	_, _ = tb.Check("b", policy, now)
	_, _ = tb.Check("c", policy, now)
	if n := tb.ApproxSize(); n != 3 {
		t.Fatalf("size=%d want 3", n)
	}
	tb.Reset()
	if n := tb.ApproxSize(); n != 0 {
		t.Fatalf("size after reset=%d want 0", n)
	}
}

func TestTokenBucket_DefaultShardsWhenZero(t *testing.T) {
	tb := NewTokenBucket(0)
	if len(tb.shards) != 256 {
		t.Fatalf("default shards=%d want 256", len(tb.shards))
	}
}

func TestTokenBucket_ConcurrentSafe(t *testing.T) {
	tb := NewTokenBucket(16)
	policy := Policy{Limit: 1_000_000, RefillPerSecond: 1_000_000}
	now := time.Now()

	const goroutines = 64
	const perG = 500
	var wg sync.WaitGroup
	wg.Add(goroutines)
	allowed := int64(0)
	var aMu sync.Mutex
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				key := "shared"
				d, _ := tb.Check(key, policy, now)
				if d.Allowed {
					aMu.Lock()
					allowed++
					aMu.Unlock()
				}
			}
		}(i)
	}
	wg.Wait()

	// With a huge limit every call should be allowed and there must be no
	// panics or data races (verified by -race).
	if allowed != goroutines*perG {
		t.Fatalf("allowed=%d want=%d (race or logic bug)", allowed, goroutines*perG)
	}
}

func TestTokenBucket_ResetAtIncreasing(t *testing.T) {
	tb := NewTokenBucket(4)
	policy := Policy{Limit: 2, RefillPerSecond: 1}
	t0 := time.Date(2025, 11, 12, 10, 0, 0, 0, time.UTC)

	d1, _ := tb.Check("k", policy, t0)
	d2, _ := tb.Check("k", policy, t0)
	if !d1.Allowed || !d2.Allowed {
		t.Fatal("both should be allowed")
	}
	// After draining 2, full refill takes 2s at 1 token/sec.
	if !d2.ResetAt.Equal(t0.Add(2 * time.Second)) {
		t.Fatalf("reset_at=%v want %v", d2.ResetAt, t0.Add(2*time.Second))
	}
}

func TestPolicy_Validate(t *testing.T) {
	cases := []struct {
		p     Policy
		algo  string
		ok    bool
	}{
		{Policy{Limit: 10, RefillPerSecond: 1}, "tokenbucket", true},
		{Policy{Limit: 10, Window: time.Minute}, "slidingwindow", true},
		{Policy{Limit: 0, RefillPerSecond: 1}, "tokenbucket", false},
		{Policy{Limit: 10, RefillPerSecond: 0}, "tokenbucket", false},
		{Policy{Limit: 10, Window: 0}, "slidingwindow", false},
		{Policy{Limit: 0, Window: time.Minute}, "slidingwindow", false},
		{Policy{Limit: 10}, "unknown-algo", true}, // unknown algo: only limit checked
	}
	for i, c := range cases {
		err := c.p.Validate(c.algo)
		if c.ok && err != nil {
			t.Fatalf("case %d: unexpected error %v", i, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("case %d: expected error, got nil", i)
		}
	}
}

func TestErrInvalidPolicy_Message(t *testing.T) {
	e := ErrInvalidPolicy{Algo: "tokenbucket", Reason: "limit must be > 0"}
	if e.Error() == "" {
		t.Fatal("error message should not be empty")
	}
}
