// Package config loads RateGate configuration from environment variables,
// config files, and command-line flags using Viper.
//
// Precedence (highest first): explicit override (flag) -> env (RATEGATE_*)
// -> config file -> defaults.
//
// Environment variables are uppercased and prefixed with RATEGATE_, with
// underscores separating nested keys. For example:
//
//	RATEGATE_SERVER_GRPC_PORT=9090
//	RATEGATE_STORE_BACKEND=redis
//	RATEGATE_REDIS_ADDR=redis:6379
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Defaults centralize the default values applied before env/file loading.
const (
	DefaultGRPCHost = "0.0.0.0"
	DefaultGRPCPort = 9090
	DefaultRESTHost = "0.0.0.0"
	DefaultRESTPort = 8080

	DefaultStoreBackend   = "memory"
	DefaultAlgorithm      = "tokenbucket"
	DefaultLogLevel       = "info"
	DefaultMetricsEnabled = true
	DefaultTracingEnabled = false

	DefaultRedisAddr     = "localhost:6379"
	DefaultRedisDB       = 0
	DefaultRedisPoolSize = 0 // 0 => runtime.GOMAXPROCS * 10
	DefaultRedisPrefix   = "rategate"

	DefaultAPIKey = "" // empty disables API-key auth
)

// Config is the fully-resolved configuration for a rategate-server process.
type Config struct {
	Server        ServerConfig        `mapstructure:"server"`
	Store         StoreConfig         `mapstructure:"store"`
	Redis         RedisConfig         `mapstructure:"redis"`
	Cluster       RedisClusterConfig  `mapstructure:"cluster"`
	Policies      []PolicyConfig      `mapstructure:"policies"`
	Observability ObservabilityConfig `mapstructure:"observability"`
	Log           LogConfig           `mapstructure:"log"`
}

// ServerConfig holds the network listener settings.
type ServerConfig struct {
	GRPCHost string `mapstructure:"grpc_host"`
	GRPCPort int    `mapstructure:"grpc_port"`
	RESTHost string `mapstructure:"rest_host"`
	RESTPort int    `mapstructure:"rest_port"`

	// APIKey, if non-empty, requires clients to send `x-api-key` (REST) or
	// an `x-api-key` metadata key (gRPC) matching this value. Empty disables
	// auth — suitable only for trusted sidecar deployments.
	APIKey string `mapstructure:"api_key"`

	// ReadTimeout / WriteTimeout bound the REST handlers.
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
}

// StoreConfig selects the persistence backend.
type StoreConfig struct {
	// Backend is "memory" or "redis".
	Backend string `mapstructure:"backend"`

	// DefaultAlgorithm is applied when a Check request omits it.
	DefaultAlgorithm string `mapstructure:"default_algorithm"`

	// Memory options (only used when Backend == "memory").
	MemoryShards   int  `mapstructure:"memory_shards"`
	MemorySWShards int  `mapstructure:"memory_sw_shards"`
	MemorySWGran   int  `mapstructure:"memory_sw_granularity"`
	MemorySWApprox bool `mapstructure:"memory_sw_approximate"`
}

// RedisConfig holds the Redis connection parameters.
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
	PoolSize int    `mapstructure:"pool_size"`
	Prefix   string `mapstructure:"prefix"`
}

// RedisClusterConfig holds Redis Cluster connection parameters. It is used
// only when store.backend == "redis-cluster". At least one seed Addr is
// required; the go-redis ClusterClient discovers the remaining nodes via
// CLUSTER SLOTS. See ADR-0003 for the single-key-Lua rationale that lets the
// cluster backend reuse the RedisStore scripts unchanged.
type RedisClusterConfig struct {
	// Addrs is the seed list of cluster nodes (host:port). Required for the
	// redis-cluster backend.
	Addrs []string `mapstructure:"addrs"`

	// Password for AUTH, applied to every node in the cluster.
	Password string `mapstructure:"password"`

	// PoolSize is the per-node connection pool size. Zero falls back to
	// 10 * runtime.GOMAXPROCS.
	PoolSize int `mapstructure:"pool_size"`

	// RouteRandomly spreads commands across masters and replicas. See
	// store.ClusterOptions.RouteRandomly for the rate-limiting tradeoff.
	RouteRandomly bool `mapstructure:"route_randomly"`

	// MaxRedirects bounds MOVED/ASK retries. Defaults to 3.
	MaxRedirects int `mapstructure:"max_redirects"`

	// Prefix namespaces all keys. Defaults to "rategate".
	Prefix string `mapstructure:"prefix"`
}

// PolicyConfig is a named, pre-loaded limit policy that clients can reference
// by name in a Check request instead of supplying the limit inline.
type PolicyConfig struct {
	Name            string        `mapstructure:"name"`
	Algorithm       string        `mapstructure:"algorithm"`
	Limit           int64         `mapstructure:"limit"`
	RefillPerSecond float64       `mapstructure:"refill_per_second"`
	Window          time.Duration `mapstructure:"window"`
}

// ObservabilityConfig toggles metrics/tracing.
type ObservabilityConfig struct {
	MetricsEnabled bool   `mapstructure:"metrics_enabled"`
	TracingEnabled bool   `mapstructure:"tracing_enabled"`
	OTLPEndpoint   string `mapstructure:"otlp_endpoint"`
	ServiceName    string `mapstructure:"service_name"`
}

// LogConfig controls structured logging.
type LogConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"` // "json" or "text"
}

// New returns a Viper instance pre-configured with defaults, env binding,
// and config-file search paths. Callers should Unmarshal into *Config.
func New() *viper.Viper {
	v := viper.New()

	// Defaults
	v.SetDefault("server.grpc_host", DefaultGRPCHost)
	v.SetDefault("server.grpc_port", DefaultGRPCPort)
	v.SetDefault("server.rest_host", DefaultRESTHost)
	v.SetDefault("server.rest_port", DefaultRESTPort)
	v.SetDefault("server.api_key", DefaultAPIKey)
	v.SetDefault("server.read_timeout", 5*time.Second)
	v.SetDefault("server.write_timeout", 5*time.Second)

	v.SetDefault("store.backend", DefaultStoreBackend)
	v.SetDefault("store.default_algorithm", DefaultAlgorithm)
	v.SetDefault("store.memory_shards", 256)
	v.SetDefault("store.memory_sw_shards", 256)
	v.SetDefault("store.memory_sw_granularity", 60)
	v.SetDefault("store.memory_sw_approximate", false)

	v.SetDefault("redis.addr", DefaultRedisAddr)
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", DefaultRedisDB)
	v.SetDefault("redis.pool_size", DefaultRedisPoolSize)
	v.SetDefault("redis.prefix", DefaultRedisPrefix)

	// Redis Cluster defaults (used only when store.backend == "redis-cluster").
	// addrs defaults to empty so Validate can surface a clear error if the
	// cluster backend is selected without any seed nodes configured.
	v.SetDefault("cluster.addrs", []string{})
	v.SetDefault("cluster.password", "")
	v.SetDefault("cluster.pool_size", DefaultRedisPoolSize)
	v.SetDefault("cluster.route_randomly", false)
	v.SetDefault("cluster.max_redirects", 3)
	v.SetDefault("cluster.prefix", DefaultRedisPrefix)

	v.SetDefault("observability.metrics_enabled", DefaultMetricsEnabled)
	v.SetDefault("observability.tracing_enabled", DefaultTracingEnabled)
	v.SetDefault("observability.otlp_endpoint", "")
	v.SetDefault("observability.service_name", "rategate")

	v.SetDefault("log.level", DefaultLogLevel)
	v.SetDefault("log.format", "json")

	// Environment: RATEGATE_SERVER_GRPC_PORT etc.
	v.SetEnvPrefix("RATEGATE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Config file search
	v.SetConfigName("rategate")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("./config")
	v.AddConfigPath("/etc/rategate")

	return v
}

// Load reads configuration using v and returns a fully-resolved *Config.
// It is an error only if a config file is present but unreadable; a missing
// config file is fine (defaults + env apply).
func Load(v *viper.Viper) (*Config, error) {
	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("read config: %w", err)
		}
		// missing file is fine
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks the resolved config for internal consistency.
func (c *Config) Validate() error {
	switch c.Store.Backend {
	case "memory", "redis", "redis-cluster":
	default:
		return fmt.Errorf("invalid store.backend %q: want memory|redis|redis-cluster", c.Store.Backend)
	}
	if c.Store.Backend == "redis-cluster" && len(c.Cluster.Addrs) == 0 {
		return fmt.Errorf("cluster.addrs is required when store.backend=redis-cluster")
	}
	switch c.Store.DefaultAlgorithm {
	case "tokenbucket", "slidingwindow":
	default:
		return fmt.Errorf("invalid store.default_algorithm %q: want tokenbucket|slidingwindow", c.Store.DefaultAlgorithm)
	}
	if c.Server.GRPCPort < 0 || c.Server.GRPCPort > 65535 {
		return fmt.Errorf("invalid server.grpc_port %d", c.Server.GRPCPort)
	}
	if c.Server.RESTPort < 0 || c.Server.RESTPort > 65535 {
		return fmt.Errorf("invalid server.rest_port %d", c.Server.RESTPort)
	}
	for i, p := range c.Policies {
		if p.Name == "" {
			return fmt.Errorf("policies[%d]: name is required", i)
		}
		if p.Limit <= 0 {
			return fmt.Errorf("policies[%d] %s: limit must be > 0", i, p.Name)
		}
		switch p.Algorithm {
		case "tokenbucket":
			if p.RefillPerSecond <= 0 {
				return fmt.Errorf("policies[%d] %s: refill_per_second must be > 0 for tokenbucket", i, p.Name)
			}
		case "slidingwindow":
			if p.Window <= 0 {
				return fmt.Errorf("policies[%d] %s: window must be > 0 for slidingwindow", i, p.Name)
			}
		default:
			return fmt.Errorf("policies[%d] %s: unknown algorithm %q", i, p.Name, p.Algorithm)
		}
	}
	return nil
}

// PolicyMap indexes the configured policies by name for O(1) lookup in the
// hot path.
func (c *Config) PolicyMap() map[string]PolicyConfig {
	m := make(map[string]PolicyConfig, len(c.Policies))
	for _, p := range c.Policies {
		m[p.Name] = p
	}
	return m
}
