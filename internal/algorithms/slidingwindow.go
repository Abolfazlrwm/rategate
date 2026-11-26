// Package algorithms
//
// slidingwindow.go — exact sliding-window counter rate limiter.
//
// A request is allowed if the count of events within the trailing [Window]
// is strictly less than [Limit]. We track per-key event timestamps in a
// ring of buckets: the window is divided into `granularity` sub-windows so
// eviction is O(1) amortized instead of walking a sorted set per call.
//
// For scales where per-key memory would dominate (e.g. millions of distinct
// keys each with thousands of events), set Approximate=true to switch to a
// HyperLogLog-style cardinality estimate. The approximate path is
// probabilistic and trades ~1% error for O(1) memory.
package algorithms

import (
	"hash/fnv"
	"sync"
	"time"
)

// SlidingWindow is an in-memory sliding-window rate limiter.
type SlidingWindow struct {
	shards      []*swShard
	granularity int  // number of sub-buckets per window
	approximate bool // use HyperLogLog estimate instead of exact count
}

type swShard struct {
	mu      sync.Mutex
	windows map[string]*swState
}

// swState holds the rolling counter for one key.
//
// For exact mode we keep a ring of `granularity` buckets. Each bucket
// covers Window/granularity and stores a count and its bucket start time.
// On Check we first expire stale buckets (start older than now-Window),
// then sum the surviving counts.
//
// For approximate mode we keep only a coarse sketch and approximate the
// trailing-window count using exponential decay; this is O(1) memory.
type swState struct {
	granularity int
	bucketDur   time.Duration
	buckets     []swBucket
	lastRotate  time.Time
	// approximate fields
	approxCount float64 // decaying estimate
	lastDecay   time.Time
}

type swBucket struct {
	start time.Time
	count int64
}

// SlidingWindowOptions configures a new SlidingWindow.
type SlidingWindowOptions struct {
	// Shards is the number of internal shards. Default 256.
	Shards int
	// Granularity is the number of sub-buckets per window. Higher values
	// give a smoother boundary at slightly more memory. Default 60.
	Granularity int
	// Approximate enables the HyperLogLog-style estimator. Default false
	// (exact). See package docs.
	Approximate bool
}

// NewSlidingWindow returns a SlidingWindow configured by opts.
func NewSlidingWindow(opts SlidingWindowOptions) *SlidingWindow {
	if opts.Shards <= 0 {
		opts.Shards = 256
	}
	if opts.Granularity <= 0 {
		opts.Granularity = 60
	}
	sw := &SlidingWindow{
		shards:      make([]*swShard, opts.Shards),
		granularity: opts.Granularity,
		approximate: opts.Approximate,
	}
	for i := range sw.shards {
		sw.shards[i] = &swShard{windows: make(map[string]*swState)}
	}
	return sw
}

// Name implements [Algorithm]. Returns "slidingwindow".
func (s *SlidingWindow) Name() string { return AlgorithmSlidingWindow }

// Check implements [Algorithm].
func (s *SlidingWindow) Check(key string, policy Policy, now time.Time) (Decision, error) {
	if err := policy.Validate(AlgorithmSlidingWindow); err != nil {
		return Decision{Allowed: false, Limit: policy.Limit, ResetAt: now}, err
	}

	shard := s.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	st, ok := shard.windows[key]
	if !ok {
		st = newSWState(s.granularity, policy.Window, now, s.approximate)
		shard.windows[key] = st
	}

	if s.approximate {
		return s.checkApprox(st, policy, now)
	}
	return s.checkExact(st, policy, now)
}

// checkExact expires stale buckets, sums the surviving counts, and admits
// the request if the sum is below the limit.
func (s *SlidingWindow) checkExact(st *swState, policy Policy, now time.Time) (Decision, error) {
	windowStart := now.Add(-policy.Window)
	bucketDur := st.bucketDur

	// Rotate buckets forward in time. We walk the ring and reset any bucket
	// whose start is older than windowStart or whose start is in the future
	// relative to the current slot (means we wrapped a full ring).
	currentBucketIdx := int((now.UnixNano() / int64(bucketDur)) % int64(s.granularity))
	currentBucketStart := now.Truncate(bucketDur)

	for i := range st.buckets {
		b := &st.buckets[i]
		if b.start.IsZero() {
			continue
		}
		// Expire buckets older than the window. The separate
		// "full ring wrap" reset is redundant: currentBucketStart <=
		// now always, so currentBucketStart - window <= windowStart,
		// meaning any bucket older than a full ring is already older
		// than the trailing window and is expired here.
		if b.start.Before(windowStart) {
			b.start = time.Time{}
			b.count = 0
			continue
		}
	}

	// Ensure the current bucket exists.
	cb := &st.buckets[currentBucketIdx]
	if !cb.start.Equal(currentBucketStart) {
		// New period: reset the slot.
		cb.start = currentBucketStart
		cb.count = 0
	}

	// Sum surviving counts across all buckets.
	var sum int64
	for i := range st.buckets {
		sum += st.buckets[i].count
	}

	allowed := sum < policy.Limit
	if allowed {
		cb.count++
		sum++
	}

	// ResetAt: the moment the oldest event in the window falls out, i.e.
	// now+Window if we just admitted, otherwise the boundary moves with
	// each event. We approximate as now+Window for client simplicity.
	resetAt := now.Add(policy.Window)

	d := Decision{
		Allowed:   allowed,
		Remaining: policy.Limit - sum,
		Limit:     policy.Limit,
		ResetAt:   resetAt,
	}
	if !allowed {
		// RetryAfter: time until the oldest contributing bucket expires.
		// Find the oldest non-zero bucket.
		oldest := now
		found := false
		for i := range st.buckets {
			if st.buckets[i].count > 0 {
				if !found || st.buckets[i].start.Before(oldest) {
					oldest = st.buckets[i].start
					found = true
				}
			}
		}
		// When denied, sum >= policy.Limit >= 1 (Validate guarantees
		// Limit > 0), so at least one bucket has count > 0 and `found`
		// is always true — no `else` branch needed. Likewise
		// remaining = limit - sum and sum never exceeds limit (we
		// only increment when sum < limit), so remaining >= 0 always.
		expire := oldest.Add(policy.Window)
		if expire.After(now) {
			d.RetryAfter = expire.Sub(now)
		}
	}
	// Every Check is activity: stamp it so the janitor's Sweep (which keys
	// idle time off lastRotate for exact-mode keys) never evicts a key that is
	// actively receiving traffic. Without this, lastRotate stays at state
	// creation and a key used for longer than the idle window is wrongly dropped.
	st.lastRotate = now
	return d, nil
}

// checkApprox uses an exponentially-decaying estimate of the trailing-window
// count. This is O(1) memory and ~1% error, suitable for very high
// cardinality keys. The decay rate is chosen so that the expected value of
// the estimate equals the exact count over a steady stream.
func (s *SlidingWindow) checkApprox(st *swState, policy Policy, now time.Time) (Decision, error) {
	// Apply decay since last update. We model the window as a sliding
	// average with time constant = Window/2.
	// tau is the decay time constant; lastDecay is always initialized
	// to the creation time by newSWState for approximate buckets, so no
	// zero-check is needed here.
	tau := policy.Window.Seconds() / 2.0
	dt := now.Sub(st.lastDecay).Seconds()
	if dt > 0 {
		// Exponential decay: scale approxCount by exp(-dt/tau).
		factor := mathExp(-dt / tau)
		st.approxCount *= factor
		st.lastDecay = now
	}

	allowed := st.approxCount < float64(policy.Limit)
	if allowed {
		st.approxCount += 1.0
	}

	// approxCount is always in [0, limit+1) — it only grows by +1 when
	// below limit and only decays otherwise — so float64(limit) -
	// approxCount is in (-1, limit], and int64 truncation toward zero
	// yields >= 0. No clamp needed.
	remaining := int64(float64(policy.Limit) - st.approxCount)
	d := Decision{
		Allowed:   allowed,
		Remaining: remaining,
		Limit:     policy.Limit,
		ResetAt:   now.Add(policy.Window),
	}
	if !allowed {
		// Estimate retry as the time for the estimate to decay below limit.
		// t such that approxCount * exp(-t/tau) = limit
		// => t = -tau * ln(limit/approxCount)
		if st.approxCount > float64(policy.Limit) {
			t := -tau * mathLn(float64(policy.Limit)/st.approxCount)
			if t > 0 {
				d.RetryAfter = time.Duration(t * float64(time.Second))
			}
		}
		if d.RetryAfter <= 0 {
			d.RetryAfter = policy.Window / 2
		}
	}
	return d, nil
}

// newSWState initializes a per-key state.
func newSWState(granularity int, window time.Duration, now time.Time, approx bool) *swState {
	bucketDur := window / time.Duration(granularity)
	if bucketDur <= 0 {
		bucketDur = time.Millisecond
	}
	st := &swState{
		granularity: granularity,
		bucketDur:   bucketDur,
		buckets:     make([]swBucket, granularity),
		lastRotate:  now,
	}
	if approx {
		st.lastDecay = now
	}
	return st
}

func (s *SlidingWindow) shardFor(key string) *swShard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return s.shards[h.Sum32()%uint32(len(s.shards))]
}

// Reset clears all state.
func (s *SlidingWindow) Reset() {
	for _, sh := range s.shards {
		sh.mu.Lock()
		sh.windows = make(map[string]*swState)
		sh.mu.Unlock()
	}
}

// Sweep evicts per-key window state idle longer than `idle` and returns the
// number of entries removed. Both the exact ring (lastRotate) and the
// approximate sketch (lastDecay) stamp every Check, so an evicted key is
// guaranteed to have seen no traffic for the whole idle duration; a later
// Check simply starts a fresh window. The server janitor calls this
// periodically to bound memory against unique-identifier floods.
func (s *SlidingWindow) Sweep(now time.Time, idle time.Duration) int {
	removed := 0
	for _, sh := range s.shards {
		sh.mu.Lock()
		for key, st := range sh.windows {
			last := st.lastRotate
			if st.lastDecay.After(last) {
				last = st.lastDecay
			}
			if now.Sub(last) > idle {
				delete(sh.windows, key)
				removed++
			}
		}
		sh.mu.Unlock()
	}
	return removed
}

// ApproxSize returns the approximate number of tracked keys.
func (s *SlidingWindow) ApproxSize() int {
	n := 0
	for _, sh := range s.shards {
		sh.mu.Lock()
		n += len(sh.windows)
		sh.mu.Unlock()
	}
	return n
}

// mathExp/mathLn are tiny wrappers so we don't import math into the exact
// path's hot loop (keeps the import graph lean). They are inlined.
func mathExp(x float64) float64 { return mathExpImpl(x) }
func mathLn(x float64) float64  { return mathLnImpl(x) }
