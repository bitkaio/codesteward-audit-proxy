package telemetry

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// InstrumentationName is the tracer/meter name used across the proxy.
const InstrumentationName = "llm-audit-proxy"

// Setup initialises the global TracerProvider and W3C TextMapPropagator.
//
// If OTEL_EXPORTER_OTLP_ENDPOINT is not set the function returns immediately,
// leaving the default no-op TracerProvider in place — zero overhead, no
// goroutines, no network connections.
//
// When an endpoint is configured the function:
//   - Creates an OTLP/HTTP exporter (reads OTEL_EXPORTER_OTLP_ENDPOINT,
//     OTEL_EXPORTER_OTLP_HEADERS, OTEL_EXPORTER_OTLP_TIMEOUT automatically)
//   - Builds a resource with OTEL_SERVICE_NAME (default: "llm-audit-proxy")
//   - Installs a batching TracerProvider as the global provider
//   - Installs W3C traceparent + baggage propagation as the global propagator
//
// The returned shutdown function must be called on process exit to flush any
// pending spans. It is safe to call even when OTel was not configured.
func Setup(ctx context.Context) (shutdown func(context.Context) error, err error) {
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return func(context.Context) error { return nil }, nil
	}

	// W3C trace context + baggage so agents that propagate traces become the
	// parent of proxy spans, and gateway proxies downstream can correlate.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("otel: create OTLP/HTTP exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName()),
		),
	)
	if err != nil {
		// Non-fatal: fall back to a bare resource.
		res = resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(serviceName()))
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}

func serviceName() string {
	if v := os.Getenv("OTEL_SERVICE_NAME"); v != "" {
		return v
	}
	return InstrumentationName
}
