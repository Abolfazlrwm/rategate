// Package algorithms
//
// adaptive.go — demand-aware AIMD rate limiter for in-memory use.
//
// Unlike TokenBucket (fixed burst + refill) and SlidingWindow (fixed window
// count), AdaptiveLimiter varies its effective limit over time in response to
// observed demand trends: it additively grows headroom when demand is rising
// (to absorb predictable bursts without rejecting) and multiplicatively
// retracts toward the configured floor when demand falls (to release reserved
// capacity). The configured Policy.Limit is the steady-state floor; the
// effective limit lives in [floor, floor*MaxFactor].
//
// The signal is demand trend (this window's total vs the previous window's),
// NOT downstream congestion (latency/errors). A congestion-response limiter
// needs an external signal that the Check-only Algorithm interface does not
// carry; see ADR-0004 for that scope decision and the Phase 3 hook that would
// close it.
//
// Safe for concurrent use via per-key mutexes sharded by FNV-1a, mirroring
// TokenBucket.
package algorithms

import (
	"hash/fnv"
	"math"
	"sync"
	"time"
)

// AdaptiveOptions configures an [AdaptiveLimiter].
type AdaptiveOptions struct {
	// Shards is the number of internal mutex shards. Default 256.
	Shards int

	// MaxFactor bounds the effective limit above the floor: cap = floor *
	// MaxFactor. Default 4 (so a floor of 100 can grow to at most 400 under
	// sustained rising demand).
	MaxFactor float64

	// IncreaseFrac is the additive growth step, expressed as a fraction of the
	// floor: step = max(1, floor * IncreaseFrac). Default 0.25.
	IncreaseFrac float64

	// DecreaseFactor is the multiplicative retraction applied when demand
	// falls: limit *= DecreaseFactor, clamped to the floor. Default 0.5.
	DecreaseFactor float64
}

// AdaptiveLimiter is the in-memory adaptive (AIMD) rate limiter.
//
// One instance serves many keys; each key has its own effective-limit state,
// sharded across `Shards` mutexes. The clock is caller-supplied so tests are
// deterministic.
type AdaptiveLimiter struct {
	shards []*alShard
	opts   resolvedAdaptiveOpts
}

type alShard struct {
	mu     sync.Mutex
	states map[string]*alState
}

// alState is the per-key adaptive state.
type alState struct {
	limit       float64   // current effective limit, in [floor, cap]
	windowStart time.Time // start of the current sampling window
	lastSeen    time.Time // updated on every Check; the idle stamp Sweep uses
	prevTotal   int64     // total requests observed in the previous window
	count       int64     // admitted requests in the current window
	rejects     int64     // rejected requests in the current window
}

type resolvedAdaptiveOpts struct {
	shards         int
	maxFactor      float64
	increaseFrac   float64
	decreaseFactor float64
}

// NewAdaptiveLimiter returns an AdaptiveLimiter with the given options. Zero
// values fall back to documented defaults.
func NewAdaptiveLimiter(opts AdaptiveOptions) *AdaptiveLimiter {
	if opts.Shards <= 0 {
		opts.Shards = 256
	}
	if opts.MaxFactor <= 1 {
		opts.MaxFactor = 4
	}
	if opts.IncreaseFrac <= 0 {
		opts.IncreaseFrac = 0.25
	}
	if opts.DecreaseFactor <= 0 || opts.DecreaseFactor >= 1 {
		opts.DecreaseFactor = 0.5
	}
	a := &AdaptiveLimiter{
		shards: make([]*alShard, opts.Shards),
		opts: resolvedAdaptiveOpts{
			shards:         opts.Shards,
			maxFactor:      opts.MaxFactor,
			increaseFrac:   opts.IncreaseFrac,
			decreaseFactor: opts.DecreaseFactor,
		},
	}
	for i := range a.shards {
		a.shards[i] = &alShard{states: make(map[string]*alState)}
	}
	return a
}

// Name implements [Algorithm]. Returns "adaptive".
func (a *AdaptiveLimiter) Name() string { return AlgorithmAdaptive }

// Check implements [Algorithm].
//
// Policy.Limit is the steady-state floor (the minimum effective limit).
// Policy.Window is the AIMD sampling window. RefillPerSecond is ignored.
//
// On each call the limiter first closes the just-ended sampling window (if
// `now` has crossed the boundary): it compares the window's total request
// count to the previous window's and applies the AIMD update to the effective
// limit, then resets the window counters. It then admits the request iff the
// current window's admit count is below the effective limit.
func (a *AdaptiveLimiter) Check(key string, policy Policy, now time.Time) (Decision, error) {
	if err := policy.Validate(AlgorithmAdaptive); err != nil {
		return Decision{Allowed: false, Limit: policy.Limit, ResetAt: now}, err
	}

	floor := float64(policy.Limit)
	limitCap := floor * a.opts.maxFactor
	increaseStep := math.Max(1.0, floor*a.opts.increaseFrac)

	shard := a.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	st, ok := shard.states[key]
	if !ok {
		st = &alState{limit: floor, windowStart: now, lastSeen: now}
		shard.states[key] = st
	}

	// Close the window if it has elapsed.
	windowEnd := st.windowStart.Add(policy.Window)
	if !now.Before(windowEnd) {
		total := st.count + st.rejects
		switch {
		case total > st.prevTotal && st.limit < limitCap:
			// Demand rising: additively grow headroom toward the cap.
			st.limit = math.Min(limitCap, st.limit+increaseStep)
		case total < st.prevTotal && st.limit > floor:
			// Demand falling: multiplicatively retract toward the floor.
			st.limit = math.Max(floor, st.limit*a.opts.decreaseFactor)
		}
		st.prevTotal = total
		st.windowStart = now
		st.count = 0
		st.rejects = 0
		windowEnd = now.Add(policy.Window)
	}

	// st.limit is clamped to [floor, cap] and floor = policy.Limit, which
	// Validate guarantees is >= 1, so int64(st.limit) is always >= 1: no
	// further floor guard is needed (or reachable).
	effLimit := int64(st.limit)

	allowed := st.count < effLimit
	var remaining int64
	if allowed {
		st.count++
		remaining = effLimit - st.count
	} else {
		st.rejects++
		remaining = 0
	}

	d := Decision{
		Allowed:   allowed,
		Remaining: remaining,
		Limit:     effLimit,
		ResetAt:   windowEnd,
	}
	if !allowed {
		// Retry at the next window boundary, where counters reset. windowEnd
		// is always in the future here: the close branch resets it to
		// now+Window, and the no-close branch only runs when now < windowEnd.
		d.RetryAfter = windowEnd.Sub(now)
	}
	// Every Check is activity: stamp it so Sweep (which measures idle time
	// off lastSeen) never evicts a key mid-window. windowStart only advances
	// on window rollover, so without this a key with a long Policy.Window
	// could be dropped while still actively receiving traffic.
	st.lastSeen = now
	return d, nil
}

func (a *AdaptiveLimiter) shardFor(key string) *alShard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return a.shards[h.Sum32()%uint32(len(a.shards))]
}

// Reset clears all per-key state. Used by tests and the admin API.
func (a *AdaptiveLimiter) Reset() {
	for _, s := range a.shards {
		s.mu.Lock()
		s.states = make(map[string]*alState)
		s.mu.Unlock()
	}
}

// Sweep evicts adaptive state for keys idle longer than `idle` (measured from
// the last Check, which every call refreshes via lastSeen) and returns the
// number of entries removed. Called by the server janitor to bound memory; an
// evicted key starts over at the policy floor on its next Check.
func (a *AdaptiveLimiter) Sweep(now time.Time, idle time.Duration) int {
	removed := 0
	for _, s := range a.shards {
		s.mu.Lock()
		for key, st := range s.states {
			if now.Sub(st.lastSeen) > idle {
				delete(s.states, key)
				removed++
			}
		}
		s.mu.Unlock()
	}
	return removed
}

// ApproxSize returns the approximate number of tracked keys. Locks each shard
// briefly; not for the hot path.
func (a *AdaptiveLimiter) ApproxSize() int {
	n := 0
	for _, s := range a.shards {
		s.mu.Lock()
		n += len(s.states)
		s.mu.Unlock()
	}
	return n
}
