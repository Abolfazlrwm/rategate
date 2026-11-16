// Package algorithms implements the rate-limiting strategies supported by
// RateGate.
//
// Every algorithm implements the [Algorithm] interface so that the store and
// server layers can compose them uniformly. Two strategies ship in Phase 1:
//
//   - [TokenBucket] — classic token bucket with a configurable refill rate
//     and burst size. Good default for most APIs (see ADR-0001).
//   - [SlidingWindow] — exact sliding-window counter, with an optional
//     HyperLogLog approximation for very high cardinality keys.
//
// All implementations must be safe for concurrent use.
package algorithms

import "time"

// Decision is the result of a single rate-limit check.
//
// It deliberately uses value types so that stores can return it without
// heap allocation on the hot path.
type Decision struct {
	// Allowed is true when the request is admitted under the configured limit.
	Allowed bool

	// Remaining is the number of units left in the current window/bucket
	// after this check. Always >= 0.
	Remaining int64

	// Limit is the configured maximum for this key/policy. Useful for
	// surfacing X-RateLimit-* headers to clients.
	Limit int64

	// ResetAt is the time at which the limit fully resets (tokens fully
	// refilled / window fully rolled past). Clients may use this for
	// Retry-After.
	ResetAt time.Time

	// RetryAfter is the suggested wait duration before the next attempt
	// when Allowed is false. Zero when Allowed is true.
	RetryAfter time.Duration
}

// Algorithm is the strategy-agnostic contract for a rate limiter.
//
// Implementations are pure with respect to the policy: the policy (limit,
// refill, window) is supplied per-call via the [Policy] argument so that a
// single instance can serve many keys with different limits.
type Algorithm interface {
	// Check evaluates whether one unit may be consumed for the given key
	// under the given policy at time `now`. The store/algorithm is expected
	// to mutate its internal state atomically with respect to concurrent
	// callers for the same key.
	//
	// Implementations must never panic on a bad key; they should return a
	// zero-value Decision with Allowed=false and a wrapped error instead.
	Check(key string, policy Policy, now time.Time) (Decision, error)

	// Name returns a stable lowercase identifier for the algorithm, used in
	// metrics labels and config ("tokenbucket" / "slidingwindow").
	Name() string
}

// Policy describes the limit applied to a key.
//
// Fields are interpreted algorithm-specifically:
//
//   - TokenBucket: Limit is the bucket capacity (burst size); RefillPerSecond
//     is the token refill rate. Burst = Limit.
//   - SlidingWindow: Limit is the max events in any Window-sized sliding
//     window. RefillPerSecond is ignored.
//   - Adaptive: Limit is the steady-state floor (minimum effective limit);
//     Window is the AIMD sampling window. RefillPerSecond is ignored. The
//     effective limit varies in [Limit, Limit*MaxFactor]. See adaptive.go.
//
// All fields must be > 0 for a well-formed policy; Check will reject a
// zero-value Policy with ErrInvalidPolicy.
type Policy struct {
	// Limit is the maximum allowed units (burst capacity / window count).
	Limit int64

	// RefillPerSecond is the token refill rate (token bucket only).
	RefillPerSecond float64

	// Window is the sliding-window duration (sliding window only).
	Window time.Duration
}

// Validate returns ErrInvalidPolicy if the policy is not usable for the
// given algorithm. It is called by each Algorithm's Check.
func (p Policy) Validate(algo string) error {
	if p.Limit <= 0 {
		return ErrInvalidPolicy{Algo: algo, Reason: "limit must be > 0"}
	}
	switch algo {
	case "tokenbucket":
		if p.RefillPerSecond <= 0 {
			return ErrInvalidPolicy{Algo: algo, Reason: "refill_per_second must be > 0"}
		}
	case "slidingwindow":
		if p.Window <= 0 {
			return ErrInvalidPolicy{Algo: algo, Reason: "window must be > 0"}
		}
	case "adaptive":
		if p.Window <= 0 {
			return ErrInvalidPolicy{Algo: algo, Reason: "window must be > 0"}
		}
	}
	return nil
}

// ErrInvalidPolicy is returned when a [Policy] is malformed for the
// requested algorithm.
type ErrInvalidPolicy struct {
	Algo   string
	Reason string
}

// Error implements error.
func (e ErrInvalidPolicy) Error() string {
	return "rategate: invalid policy for " + e.Algo + ": " + e.Reason
}

// AlgorithmName constants centralize the strings used in config and metrics.
const (
	AlgorithmTokenBucket   = "tokenbucket"
	AlgorithmSlidingWindow = "slidingwindow"
	AlgorithmAdaptive      = "adaptive"
)
