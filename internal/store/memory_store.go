// Package store
//
// memory_store.go — single-node in-memory Store.
//
// It wraps an [algorithms.TokenBucket] and [algorithms.SlidingWindow] and
// dispatches Check by algorithm name. Because both wrapped algorithms are
// already safe for concurrent use (sharded mutexes), MemoryStore adds no
// additional locking — it is effectively a zero-overhead dispatcher.
package store

import (
	"context"
	"time"

	"github.com/Abolfazlrwm/rategate/internal/algorithms"
)

// MemoryStore is the in-process, single-node Store implementation.
//
// It is the default store when no Redis is configured. All state lives in
// the process and is lost on restart — suitable for sidecar deployments
// where a single instance fronts each service, or for testing.
type MemoryStore struct {
	tokenBucket   *algorithms.TokenBucket
	slidingWindow *algorithms.SlidingWindow
}

// MemoryOptions configures the internal algorithm shards/granularity.
type MemoryOptions struct {
	TBShards      int
	SWShards      int
	SWGranularity int
	SWApproximate bool
}

// NewMemoryStore returns a MemoryStore with the given options.
//
// Zero-value options fall back to the algorithms' own defaults (256 shards,
// granularity 60, exact counting).
func NewMemoryStore(opts MemoryOptions) *MemoryStore {
	return &MemoryStore{
		tokenBucket:   algorithms.NewTokenBucket(opts.TBShards),
		slidingWindow: algorithms.NewSlidingWindow(algorithms.SlidingWindowOptions{Shards: opts.SWShards, Granularity: opts.SWGranularity, Approximate: opts.SWApproximate}),
	}
}

// Name implements [Store]. Returns "memory".
func (m *MemoryStore) Name() string { return "memory" }

// Check implements [Store]. ctx is accepted for interface symmetry but is
// not consulted (the in-memory algorithms are non-blocking).
func (m *MemoryStore) Check(_ context.Context, algo, key string, policy algorithms.Policy, now time.Time) (algorithms.Decision, error) {
	switch algo {
	case algorithms.AlgorithmTokenBucket:
		return m.tokenBucket.Check(key, policy, now)
	case algorithms.AlgorithmSlidingWindow:
		return m.slidingWindow.Check(key, policy, now)
	default:
		return algorithms.Decision{Allowed: false, Limit: policy.Limit, ResetAt: now}, ErrUnknownAlgorithm{Algo: algo}
	}
}

// Close implements [Store]; no-op for the memory store.
func (m *MemoryStore) Close() error { return nil }

// Reset clears all in-memory state. Used by tests and the admin API.
func (m *MemoryStore) Reset() {
	m.tokenBucket.Reset()
	m.slidingWindow.Reset()
}
