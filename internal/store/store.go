// Package store defines the persistence contract for rate-limit state and
// provides two implementations:
//
//   - [MemoryStore] — single-node, sharded-mutex, in-process. Used by the
//     in-memory [algorithms.TokenBucket] / [algorithms.SlidingWindow] directly,
//     but also exposed here so the server can treat it uniformly.
//   - [RedisStore] — distributed, backed by atomic Lua scripts so that
//     concurrent Check calls across many server instances are race-free.
//     See ADR-0002 for the rationale behind the Lua-script approach.
//
// A Store is responsible for the *durability and atomicity* of a Check under
// the requested algorithm. The server picks the store based on config; the
// algorithm is picked per-policy.
package store

import (
	"context"
	"time"

	"github.com/Abolfazlrwm/rategate/internal/algorithms"
)

// Store is the strategy-agnostic persistence contract.
//
// Every implementation must be safe for concurrent use and must guarantee
// atomicity of a single Check: no partial state should be observable to a
// concurrent caller for the same key.
type Store interface {
	// Check runs the named algorithm for the given key and policy at time
	// `now`, mutating stored state atomically, and returns the Decision.
	//
	// Implementations should respect ctx for cancellation/timeout where the
	// backing store supports it (Redis: EVAL with ctx). In-memory stores
	// ignore ctx after the algorithm name lookup.
	Check(ctx context.Context, algo string, key string, policy algorithms.Policy, now time.Time) (algorithms.Decision, error)

	// Name returns a stable lowercase identifier ("memory" / "redis") used
	// in metrics labels and logs.
	Name() string

	// Close releases any resources held by the store. It is idempotent.
	Close() error
}

// ErrUnknownAlgorithm is returned when a Store.Check is called with an
// algorithm name the store does not recognize.
type ErrUnknownAlgorithm struct {
	Algo string
}

// Error implements error.
func (e ErrUnknownAlgorithm) Error() string {
	return "rategate: unknown algorithm: " + e.Algo
}
