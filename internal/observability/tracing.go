// Package observability
//
// tracing.go — OpenTelemetry tracing setup.
//
// If tracing is disabled in config, the returned TracerProvider is a no-op
// that records nothing, so callers can instrument unconditionally.
package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// TracerProvider wraps the OTel SDK provider so the caller can shut it down
// cleanly on server exit.
type TracerProvider struct {
	tp *sdktrace.TracerProvider
}

// InitTracing configures the global TracerProvider. When endpoint is empty,
// tracing is disabled (no-op provider) and the returned Shutdown is a no-op.
//
// serviceName is attached as the OTel resource attribute service.name.
func InitTracing(ctx context.Context, endpoint, serviceName string) (*TracerProvider, error) {
	if endpoint == "" {
		// No-op provider: instrumentation calls become cheap and discard spans.
		tp := noop.NewTracerProvider()
		otel.SetTracerProvider(tp)
		return &TracerProvider{tp: nil}, nil
	}

	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}

	res, err := resource.Merge(resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL,
			semconv.ServiceName(serviceName),
		))
	if err != nil {
		return nil, fmt.Errorf("build trace resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(0.1)), // 10% sampling
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	return &TracerProvider{tp: tp}, nil
}

// Tracer returns a named tracer for instrumentation.
func (p *TracerProvider) Tracer(name string) trace.Tracer {
	return otel.GetTracerProvider().Tracer(name)
}

// Shutdown flushes pending spans and closes the exporter. Safe to call on a
// disabled (nil-provider) TracerProvider.
func (p *TracerProvider) Shutdown(ctx context.Context) error {
	if p == nil || p.tp == nil {
		return nil
	}
	// Give the batcher up to 5s to flush.
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return p.tp.Shutdown(shutdownCtx)
}
