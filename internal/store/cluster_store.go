// Package store
//
// cluster_store.go — Redis Cluster backend for distributed rate-limit state.
//
// Redis Cluster shards keys across multiple Redis nodes by 16384 hash slots.
// The atomic Lua scripts defined in redis_store.go (tokenBucketScript /
// slidingWindowScript) touch exactly one key (KEYS[1]) per EVAL, so every
// evaluation maps to a single slot. Redis Cluster therefore routes each EVAL
// to the owning node and executes it atomically there — the same correctness
// guarantee as the single-node RedisStore, just sharded. Cross-slot scripts
// would raise a CROSSSLOT error; we never issue any. See ADR-0003.
//
// Because both *redis.Client and *redis.ClusterClient satisfy redis.Cmdable,
// ClusterStore reuses the [RedisStore] Check/parse path verbatim by embedding
// it. Only the lifecycle differs: ClusterStore owns the ClusterClient's Close
// and reports a distinct Name ("redis-cluster") so metrics labels distinguish
// the two distributed backends.
package store

import (
	"sync"

	"github.com/redis/go-redis/v9"
)

// ClusterOptions configures a Redis Cluster backend.
type ClusterOptions struct {
	// Addrs is the seed list of cluster nodes (host:port). At least one is
	// required; the client discovers the full topology via CLUSTER SLOTS on
	// first use and refreshes it periodically.
	Addrs []string

	// Password for AUTH, if the cluster requires it. Applied to every node.
	Password string

	// PoolSize is the per-node connection pool size. Zero falls back to
	// 10 * runtime.GOMAXPROCS, matching the single-node RedisStore default.
	PoolSize int

	// RouteRandomly, when true, spreads read commands across masters and
	// replicas. Rate-limit Check traffic mutates state (token decrement /
	// ZADD), so it is NOT a pure read; however the EVAL result is the source
	// of truth and replicas lag only the already-conservative direction
	// (under-counting near the boundary). For check-heavy workloads the
	// throughput win is usually worth the millisecond-scale staleness.
	RouteRandomly bool

	// MaxRedirects bounds the number of MOVED/ASK redirection retries the
	// client follows per command. Zero lets go-redis use its default (3),
	// which covers normal resharding events.
	MaxRedirects int

	// KeyPrefix namespaces all keys. Default "rategate" (applied by the
	// embedded RedisStore when empty).
	KeyPrefix string
}

// ClusterStore is the Redis Cluster-backed Store. It embeds a [RedisStore] so
// that Check, Ping, and the Lua-script machinery are inherited unchanged;
// only Name and Close are overridden to reflect the cluster lifecycle.
//
// All cluster-specific behavior (slot routing, MOVED/ASK retries, topology
// discovery) is handled inside the go-redis ClusterClient. This type is the
// thin adapter that makes a ClusterClient usable wherever a [Store] is
// expected.
type ClusterStore struct {
	*RedisStore

	client    redis.Cmdable
	closeOnce sync.Once
	closeErr  error
}

// NewClusterStore returns a ClusterStore backed by a new redis.ClusterClient
// bootstrapped from opts.Addrs.
//
// The returned store owns the client: Close will close it.
func NewClusterStore(opts ClusterOptions) *ClusterStore {
	client := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs:         opts.Addrs,
		Password:      opts.Password,
		PoolSize:      opts.PoolSize,
		RouteRandomly: opts.RouteRandomly,
		MaxRedirects:  opts.MaxRedirects,
	})
	return newClusterStore(client, opts.KeyPrefix)
}

// NewClusterStoreWithClient wraps an existing redis.Cmdable as a ClusterStore.
// It is intended for tests (e.g. a miniredis-backed *redis.Client exercises the
// full Check/Lua path without a real cluster) and for callers that manage
// their own ClusterClient lifecycle.
//
// When the injected Cmdable is a *redis.ClusterClient, Close will close it;
// otherwise Close is a no-op (the caller owns the client).
func NewClusterStoreWithClient(rdb redis.Cmdable, keyPrefix string) *ClusterStore {
	return newClusterStore(rdb, keyPrefix)
}

func newClusterStore(rdb redis.Cmdable, keyPrefix string) *ClusterStore {
	inner := NewRedisStoreWithClient(rdb, keyPrefix)
	return &ClusterStore{RedisStore: inner, client: rdb}
}

// Name implements [Store]. Returns "redis-cluster" so that metrics labels and
// logs distinguish the cluster backend from the single-node "redis" one.
//
// This shadows the embedded RedisStore.Name (which returns "redis"); the
// override is intentional because the operational characteristics (resharding,
// multi-node failure modes) differ even though the Check logic is shared.
func (c *ClusterStore) Name() string { return "redis-cluster" }

// Close implements [Store]. It closes the underlying *redis.ClusterClient when
// one was created by NewClusterStore; for an injected non-cluster Cmdable
// (tests, externally-managed clients) it is a no-op. Idempotent.
//
// The embedded RedisStore.Close is deliberately not called: it only closes a
// *redis.Client and would be a no-op for a ClusterClient, and calling both
// would risk a double-close on the shared connection pool.
func (c *ClusterStore) Close() error {
	c.closeOnce.Do(func() {
		if cc, ok := c.client.(*redis.ClusterClient); ok {
			c.closeErr = cc.Close()
		}
	})
	return c.closeErr
}
