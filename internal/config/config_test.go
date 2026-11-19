package config

import (
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestConfig_DefaultsAndEnv(t *testing.T) {
	v := New()
	t.Setenv("RATEGATE_SERVER_GRPC_PORT", "9999")
	t.Setenv("RATEGATE_STORE_BACKEND", "redis")
	cfg, err := Load(v)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Server.GRPCPort != 9999 {
		t.Fatalf("grpc_port=%d want 9999 (env)", cfg.Server.GRPCPort)
	}
	if cfg.Store.Backend != "redis" {
		t.Fatalf("backend=%q want redis (env)", cfg.Store.Backend)
	}
	if cfg.Store.DefaultAlgorithm != "tokenbucket" {
		t.Fatalf("default algorithm=%q want tokenbucket", cfg.Store.DefaultAlgorithm)
	}
	if cfg.Redis.Addr != "localhost:6379" {
		t.Fatalf("redis addr default=%q", cfg.Redis.Addr)
	}
}

func TestConfig_ValidateInvalidBackend(t *testing.T) {
	cfg := &Config{}
	cfg.Store.Backend = "weird"
	cfg.Store.DefaultAlgorithm = "tokenbucket"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid backend")
	}
}

func TestConfig_ValidateInvalidAlgorithm(t *testing.T) {
	cfg := &Config{}
	cfg.Store.Backend = "memory"
	cfg.Store.DefaultAlgorithm = "bogus"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid default algorithm")
	}
}

func TestConfig_ValidatePorts(t *testing.T) {
	cfg := &Config{}
	cfg.Store.Backend = "memory"
	cfg.Store.DefaultAlgorithm = "tokenbucket"
	cfg.Server.GRPCPort = 99999
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid grpc port")
	}
	cfg.Server.GRPCPort = 9090
	cfg.Server.RESTPort = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid rest port")
	}
}

func TestConfig_ValidatePolicies(t *testing.T) {
	cfg := &Config{}
	cfg.Store.Backend = "memory"
	cfg.Store.DefaultAlgorithm = "tokenbucket"
	cfg.Policies = []PolicyConfig{
		{Name: "", Algorithm: "tokenbucket", Limit: 1, RefillPerSecond: 1},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for empty policy name")
	}

	cfg.Policies = []PolicyConfig{
		{Name: "p", Algorithm: "tokenbucket", Limit: 0, RefillPerSecond: 1},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for zero limit")
	}

	cfg.Policies = []PolicyConfig{
		{Name: "p", Algorithm: "tokenbucket", Limit: 1, RefillPerSecond: 0},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for zero refill")
	}

	cfg.Policies = []PolicyConfig{
		{Name: "p", Algorithm: "slidingwindow", Limit: 1, Window: 0},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for zero window")
	}

	cfg.Policies = []PolicyConfig{
		{Name: "p", Algorithm: "unknown", Limit: 1, RefillPerSecond: 1},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for unknown algorithm")
	}

	// Valid policy.
	cfg.Policies = []PolicyConfig{
		{Name: "ok", Algorithm: "tokenbucket", Limit: 10, RefillPerSecond: 1, Window: time.Minute},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfig_PolicyMap(t *testing.T) {
	cfg := &Config{}
	cfg.Policies = []PolicyConfig{
		{Name: "a", Algorithm: "tokenbucket", Limit: 1, RefillPerSecond: 1},
		{Name: "b", Algorithm: "slidingwindow", Limit: 5, Window: time.Minute},
	}
	m := cfg.PolicyMap()
	if len(m) != 2 {
		t.Fatalf("len=%d want 2", len(m))
	}
	if _, ok := m["a"]; !ok {
		t.Fatal("missing a")
	}
}

func TestConfig_LoadMissingFileOK(t *testing.T) {
	// A missing config file is fine (defaults + env apply).
	v := New()
	v.SetConfigFile("/nonexistent/rategate.yaml")
	cfg, err := Load(v)
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
}

func TestConfig_EnvKeyReplacer(t *testing.T) {
	// Verify nested keys map to underscored env vars.
	v := New()
	t.Setenv("RATEGATE_REDIS_ADDR", "myredis:6380")
	t.Setenv("RATEGATE_REDIS_PASSWORD", "secret")
	cfg, err := Load(v)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Redis.Addr != "myredis:6380" {
		t.Fatalf("redis addr=%q want myredis:6380", cfg.Redis.Addr)
	}
	if cfg.Redis.Password != "secret" {
		t.Fatalf("redis password=%q want secret", cfg.Redis.Password)
	}
}

// TestConfig_ExampleFileLoadsAndValidates is a regression guard: the shipped
// config.example.yaml must parse and pass Validate, and must exercise the
// Phase 2 additions (an adaptive policy and a seeded cluster section) so a
// future edit that breaks the example is caught here.
func TestConfig_ExampleFileLoadsAndValidates(t *testing.T) {
	v := New()
	v.SetConfigFile("../../config.example.yaml")
	cfg, err := Load(v)
	if err != nil {
		t.Fatalf("example config failed to load/validate: %v", err)
	}
	if cfg.Store.Backend != "memory" {
		t.Fatalf("example backend=%q want memory", cfg.Store.Backend)
	}
	foundAdaptive := false
	for _, p := range cfg.Policies {
		if p.Algorithm == "adaptive" {
			foundAdaptive = true
		}
	}
	if !foundAdaptive {
		t.Fatal("example config should include an adaptive policy")
	}
	if len(cfg.Cluster.Addrs) == 0 {
		t.Fatal("example config should seed cluster.addrs")
	}
	if cfg.Store.MemoryAdaptiveMaxFactor != 4.0 {
		t.Fatalf("adaptive max factor=%v want 4.0", cfg.Store.MemoryAdaptiveMaxFactor)
	}
}

// Ensure viper is imported even if only used transitively.
var _ = viper.New
