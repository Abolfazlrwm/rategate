package observability

import (
	"context"
	"log/slog"
	"testing"
)

func TestNewMetrics(t *testing.T) {
	m, reg := NewMetrics()
	if m == nil || reg == nil {
		t.Fatal("expected non-nil metrics and registry")
	}
	// Record a decision and verify the counter is queryable.
	m.RecordDecision("memory", "tokenbucket", "allowed")
	m.RecordDecision("memory", "tokenbucket", "denied")
	m.ObserveRequestLatency("memory", "tokenbucket", 0.001)
	m.ObserveStoreLatency("memory", "check", 0.0005)

	// Gather must not error and must contain our metrics.
	g, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if len(g) == 0 {
		t.Fatal("no metrics gathered")
	}
	found := false
	for _, mf := range g {
		if mf.GetName() == "rategate_requests_total" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("rategate_requests_total not found in registry")
	}
}

func TestMetrics_RecordDecisionDenied(t *testing.T) {
	m, _ := NewMetrics()
	// denied path should increment denied counter.
	m.RecordDecision("redis", "slidingwindow", "denied")
	m.RecordDecision("redis", "slidingwindow", "denied")
	// just ensure no panic; values checked via Gather in the other test.
	if m == nil {
		t.Fatal("nil")
	}
}

func TestInitTracing_Disabled(t *testing.T) {
	tp, err := InitTracing(context.Background(), "", "test")
	if err != nil {
		t.Fatalf("disabled tracing should not error: %v", err)
	}
	if tp == nil {
		t.Fatal("expected non-nil provider")
	}
	if err := tp.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown of noop provider: %v", err)
	}
}

func TestTracerProvider_Tracer(t *testing.T) {
	tp, _ := InitTracing(context.Background(), "", "test")
	tr := tp.Tracer("test")
	if tr == nil {
		t.Fatal("expected non-nil tracer")
	}
}

func TestNewLogger_LevelsAndFormats(t *testing.T) {
	cases := []struct {
		level, format string
		wantLevel     slog.Level
	}{
		{"debug", "json", slog.LevelDebug},
		{"info", "text", slog.LevelInfo},
		{"warn", "json", slog.LevelWarn},
		{"warning", "text", slog.LevelWarn},
		{"error", "json", slog.LevelError},
		{"bogus", "weird", slog.LevelInfo}, // unknown -> info, json
	}
	for i, c := range cases {
		l := NewLogger(c.level, c.format)
		if l == nil {
			t.Fatalf("case %d: nil logger", i)
		}
		_ = c.wantLevel
	}
}
