// Package server implements the gRPC + REST server for RateGate.
//
// The package is organized around a single [Server] type that owns the
// store, policy registry, observability, and both transport listeners.
// gRPC handlers live in grpc.go, REST handlers in http.go, and transport
// middleware (auth, logging, recovery) in middleware.go.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/Abolfazlrwm/rategate/internal/algorithms"
	"github.com/Abolfazlrwm/rategate/internal/config"
	"github.com/Abolfazlrwm/rategate/internal/observability"
	"github.com/Abolfazlrwm/rategate/internal/store"
)

// Server is the core RateGate service. It is transport-agnostic; both the
// gRPC and REST handlers delegate to its Check method.
type Server struct {
	cfg       *config.Config
	store     store.Store
	policies  map[string]config.PolicyConfig
	metrics   *observability.Metrics
	logger    *slog.Logger
	ready     atomic.Bool
	startedAt time.Time
}

// New constructs a Server from the resolved config. The caller is
// responsible for constructing the store (so it can choose memory vs redis
// based on cfg.Store.Backend) and passing it in.
func New(cfg *config.Config, s store.Store, m *observability.Metrics, logger *slog.Logger) *Server {
	return &Server{
		cfg:       cfg,
		store:     s,
		policies:  cfg.PolicyMap(),
		metrics:   m,
		logger:    logger,
		startedAt: time.Now(),
	}
}

// MarkReady flips the readiness flag to true. Called after both listeners
// are bound.
func (s *Server) MarkReady() { s.ready.Store(true) }

// MarkNotReady flips the readiness flag to false. Called on shutdown.
func (s *Server) MarkNotReady() { s.ready.Store(false) }

// IsReady reports whether the server should receive traffic.
func (s *Server) IsReady() bool { return s.ready.Load() }

// CheckRequest is the transport-agnostic input to a Check.
//
// Either PolicyName or (Algorithm + Policy) must be supplied. When PolicyName
// is set, the named pre-configured policy overrides the inline fields.
type CheckRequest struct {
	Key        string
	PolicyName string
	Algorithm  string
	Policy     algorithms.Policy
}

// CheckResult mirrors [algorithms.Decision] plus the algorithm name that was
// applied, for transport-layer serialization.
type CheckResult struct {
	Allowed    bool
	Remaining  int64
	Limit      int64
	ResetAt    time.Time
	RetryAfter time.Duration
	Algorithm  string
}

// Check resolves the policy for a request and delegates to the store.
//
// If req.PolicyName is non-empty, the named policy is looked up; otherwise
// the inline req.Algorithm + req.Policy are used (algorithm defaults to the
// configured default). now is the authoritative clock for the check; pass
// time.Now() in production and a fixed time in tests.
func (s *Server) Check(ctx context.Context, req CheckRequest, now time.Time) (CheckResult, error) {
	if req.Key == "" {
		return CheckResult{}, fmt.Errorf("key is required")
	}

	algo := req.Algorithm
	policy := req.Policy

	if req.PolicyName != "" {
		p, ok := s.policies[req.PolicyName]
		if !ok {
			return CheckResult{}, fmt.Errorf("unknown policy %q", req.PolicyName)
		}
		algo = p.Algorithm
		policy = algorithms.Policy{
			Limit:           p.Limit,
			RefillPerSecond: p.RefillPerSecond,
			Window:          p.Window,
		}
	}
	if algo == "" {
		algo = s.cfg.Store.DefaultAlgorithm
	}

	storeStart := time.Now()
	d, err := s.store.Check(ctx, algo, req.Key, policy, now)
	storeLatency := time.Since(storeStart).Seconds()

	decision := "denied"
	if d.Allowed {
		decision = "allowed"
	}
	if s.metrics != nil {
		s.metrics.RecordDecision(s.store.Name(), algo, decision)
		s.metrics.ObserveStoreLatency(s.store.Name(), "check", storeLatency)
	}
	if err != nil {
		return CheckResult{Algorithm: algo, Limit: policy.Limit}, err
	}
	return CheckResult{
		Allowed:    d.Allowed,
		Remaining:  d.Remaining,
		Limit:      d.Limit,
		ResetAt:    d.ResetAt,
		RetryAfter: d.RetryAfter,
		Algorithm:  algo,
	}, nil
}

// Health returns the serving status. The server is SERVING when ready and
// the store is reachable.
func (s *Server) Health(ctx context.Context) HealthStatus {
	if !s.IsReady() {
		return HealthNotServing
	}
	// For the memory store this is trivially true; for redis we probe with a
	// lightweight no-op (PING) when the store implements Pinger.
	if p, ok := s.store.(Pinger); ok {
		pingCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		if err := p.Ping(pingCtx); err != nil {
			s.logger.Warn("store ping failed", "error", err)
			return HealthNotServing
		}
	}
	return HealthServing
}

// Pinger is implemented by stores that can liveness-probe their backend.
type Pinger interface {
	Ping(ctx context.Context) error
}

// HealthStatus is the coarse serving status.
type HealthStatus int

const (
	HealthUnknown    HealthStatus = 0
	HealthServing    HealthStatus = 1
	HealthNotServing HealthStatus = 2
)

// String returns the proto-compatible status name.
func (h HealthStatus) String() string {
	switch h {
	case HealthServing:
		return "SERVING"
	case HealthNotServing:
		return "NOT_SERVING"
	default:
		return "SERVING_STATUS_UNSPECIFIED"
	}
}

// StartedAt returns the process start time, for /healthz and /readyz output.
func (s *Server) StartedAt() time.Time { return s.startedAt }

// Close releases store resources.
func (s *Server) Close() error {
	s.MarkNotReady()
	return s.store.Close()
}
