// Package observability wires Prometheus metrics and OpenTelemetry tracing
// for the rategate-server process.
//
// The metrics are intentionally minimal and aligned with the README's stated
// surface:
//
//   - rategate_requests_total{store,algorithm,decision}
//   - rategate_allowed_total{store,algorithm}
//   - rategate_denied_total{store,algorithm}
//   - rategate_request_duration_seconds{store,algorithm} (histogram)
//   - rategate_store_latency_seconds{store,operation} (histogram)
//   - rategate_policies (gauge — current registered policy count)
//   - rategate_admin_operations_total{operation,status}
//
// All metrics are registered on a single *prometheus.Registry so the server
// can expose them at /metrics without colliding with the default registry.
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all RateGate Prometheus collectors.
type Metrics struct {
	registry *prometheus.Registry

	RequestsTotal  *prometheus.CounterVec
	AllowedTotal   *prometheus.CounterVec
	DeniedTotal    *prometheus.CounterVec
	RequestLatency *prometheus.HistogramVec
	StoreLatency   *prometheus.HistogramVec

	// Policies is the current number of registered policies (a gauge so it
	// tracks Create/Delete over the process lifetime). Set by Server.New and
	// refreshed after every size-changing admin mutation.
	Policies prometheus.Gauge

	// AdminOpsTotal counts admin API calls by operation and outcome, so
	// operators can alert on policy churn or admin error rates.
	AdminOpsTotal *prometheus.CounterVec
}

// NewMetrics returns a Metrics set registered on its own registry. Pass the
// registry to the /metrics handler.
func NewMetrics() (*Metrics, *prometheus.Registry) {
	reg := prometheus.NewRegistry()
	factory := promauto.With(reg)

	m := &Metrics{
		registry: reg,
		RequestsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: "rategate",
			Name:      "requests_total",
			Help:      "Total rate-limit checks, partitioned by store/algorithm/decision.",
		}, []string{"store", "algorithm", "decision"}),
		AllowedTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: "rategate",
			Name:      "allowed_total",
			Help:      "Total admitted checks.",
		}, []string{"store", "algorithm"}),
		DeniedTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: "rategate",
			Name:      "denied_total",
			Help:      "Total denied checks.",
		}, []string{"store", "algorithm"}),
		RequestLatency: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "rategate",
			Name:      "request_duration_seconds",
			Help:      "End-to-end Check latency.",
			Buckets:   prometheus.ExponentialBuckets(0.0005, 2, 16), // 0.5ms -> ~16s
		}, []string{"store", "algorithm"}),
		StoreLatency: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "rategate",
			Name:      "store_latency_seconds",
			Help:      "Per-store backend latency.",
			Buckets:   prometheus.ExponentialBuckets(0.0001, 2, 16), // 0.1ms -> ~3s
		}, []string{"store", "operation"}),

		Policies: factory.NewGauge(prometheus.GaugeOpts{
			Namespace: "rategate",
			Name:      "policies",
			Help:      "Number of currently registered rate-limit policies.",
		}),

		AdminOpsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: "rategate",
			Name:      "admin_operations_total",
			Help:      "Admin API operations, partitioned by operation and outcome.",
		}, []string{"operation", "status"}),
	}
	return m, reg
}

// RecordDecision increments the appropriate counters for a Check result.
// decision is "allowed" or "denied".
func (m *Metrics) RecordDecision(store, algorithm, decision string) {
	m.RequestsTotal.WithLabelValues(store, algorithm, decision).Inc()
	if decision == "allowed" {
		m.AllowedTotal.WithLabelValues(store, algorithm).Inc()
	} else {
		m.DeniedTotal.WithLabelValues(store, algorithm).Inc()
	}
}

// ObserveRequestLatency records the end-to-end Check duration.
func (m *Metrics) ObserveRequestLatency(store, algorithm string, seconds float64) {
	m.RequestLatency.WithLabelValues(store, algorithm).Observe(seconds)
}

// ObserveStoreLatency records a single store-backend operation duration.
func (m *Metrics) ObserveStoreLatency(store, operation string, seconds float64) {
	m.StoreLatency.WithLabelValues(store, operation).Observe(seconds)
}

// RecordAdminOp increments the admin operations counter. operation is one of
// list|get|create|update|delete; status is "success" or "error".
func (m *Metrics) RecordAdminOp(operation, status string) {
	m.AdminOpsTotal.WithLabelValues(operation, status).Inc()
}

// SetPolicyCount sets the policy-count gauge to the current Registry size.
func (m *Metrics) SetPolicyCount(n int) {
	m.Policies.Set(float64(n))
}
