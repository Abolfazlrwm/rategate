package algorithms

import (
	"fmt"
	"testing"
	"time"
)

// BenchmarkTokenBucket_CheckSingleKey measures the hot path of a single
// contended key. The shard contention here is intentional.
func BenchmarkTokenBucket_CheckSingleKey(b *testing.B) {
	tb := NewTokenBucket(256)
	policy := Policy{Limit: 1 << 30, RefillPerSecond: 1 << 30}
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tb.Check("bench:single", policy, now)
	}
}

// BenchmarkTokenBucket_CheckDistinctKeys uses a unique key per op so there
// is no contention and the map grows — measures the steady-state cost.
func BenchmarkTokenBucket_CheckDistinctKeys(b *testing.B) {
	tb := NewTokenBucket(256)
	policy := Policy{Limit: 1 << 30, RefillPerSecond: 1 << 30}
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tb.Check(fmt.Sprintf("bench:%d", i), policy, now)
	}
}

// BenchmarkTokenBucket_CheckParallel exercises the sharded mutex under real
// parallel load.
func BenchmarkTokenBucket_CheckParallel(b *testing.B) {
	tb := NewTokenBucket(256)
	policy := Policy{Limit: 1 << 30, RefillPerSecond: 1 << 30}
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = tb.Check("bench:parallel", policy, now)
		}
	})
}

// BenchmarkSlidingWindow_CheckSingleKey
func BenchmarkSlidingWindow_CheckSingleKey(b *testing.B) {
	sw := NewSlidingWindow(SlidingWindowOptions{Shards: 256, Granularity: 60})
	policy := Policy{Limit: 1 << 30, Window: time.Minute}
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = sw.Check("bench:single", policy, now)
	}
}

// BenchmarkSlidingWindow_CheckDistinctKeys
func BenchmarkSlidingWindow_CheckDistinctKeys(b *testing.B) {
	sw := NewSlidingWindow(SlidingWindowOptions{Shards: 256, Granularity: 60})
	policy := Policy{Limit: 1 << 30, Window: time.Minute}
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = sw.Check(fmt.Sprintf("bench:%d", i), policy, now)
	}
}

// BenchmarkSlidingWindow_CheckParallel
func BenchmarkSlidingWindow_CheckParallel(b *testing.B) {
	sw := NewSlidingWindow(SlidingWindowOptions{Shards: 256, Granularity: 60})
	policy := Policy{Limit: 1 << 30, Window: time.Minute}
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = sw.Check("bench:parallel", policy, now)
		}
	})
}

// BenchmarkSlidingWindow_ApproxCheckParallel exercises the O(1) memory
// approximate path.
func BenchmarkSlidingWindow_ApproxCheckParallel(b *testing.B) {
	sw := NewSlidingWindow(SlidingWindowOptions{Shards: 256, Granularity: 60, Approximate: true})
	policy := Policy{Limit: 1 << 30, Window: time.Minute}
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = sw.Check("bench:approx", policy, now)
		}
	})
}
