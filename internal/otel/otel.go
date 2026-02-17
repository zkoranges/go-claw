// Package otel provides OpenTelemetry integration for GoClaw.
// It wraps trace and metric providers with configurable exporters.
// When disabled, all operations are no-ops with zero overhead.
package otel

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
)

const (
	// TracerName is the instrumentation scope name for GoClaw traces.
	TracerName = "goclaw"
	// MeterName is the instrumentation scope name for GoClaw metrics.
	MeterName = "goclaw"
	// Version is the GoClaw version reported in telemetry.
	Version = "v0.5-dev"
)

// Config holds OTel configuration.
type Config struct {
	Enabled     bool    `yaml:"enabled"`
	Exporter    string  `yaml:"exporter"`
	Endpoint    string  `yaml:"endpoint"`
	ServiceName string  `yaml:"service_name"`
	SampleRate  float64 `yaml:"sample_rate"`
	// MetricsEnabled enables metrics export alongside traces.
	MetricsEnabled *bool `yaml:"metrics_enabled,omitempty"`
}

// Provider wraps OTel tracer and meter providers with cleanup.
type Provider struct {
	TracerProvider *sdktrace.TracerProvider
	MeterProvider  metric.MeterProvider
	Tracer         trace.Tracer
	Meter          metric.Meter
	shutdown       func(context.Context) error
}

// Init sets up OpenTelemetry with the given config.
// Returns a Provider that must be Shutdown() on exit.
// If config.Enabled is false, returns a no-op provider.
func Init(ctx context.Context, cfg Config) (*Provider, error) {
	if !cfg.Enabled {
		return &Provider{
			Tracer:        nooptrace.NewTracerProvider().Tracer(TracerName),
			Meter:         noop.NewMeterProvider().Meter(MeterName),
			MeterProvider: noop.NewMeterProvider(),
			shutdown:      func(context.Context) error { return nil },
		}, nil
	}

	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "goclaw"
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			attribute.String("goclaw.version", Version),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	exporter, err := createExporter(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create exporter: %w", err)
	}

	sampleRate := cfg.SampleRate
	if sampleRate <= 0 {
		sampleRate = 1.0
	}
	sampler := sdktrace.ParentBased(
		sdktrace.TraceIDRatioBased(sampleRate),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)
	otel.SetTracerProvider(tp)

	// Metrics provider
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
	)

	return &Provider{
		TracerProvider: tp,
		MeterProvider:  mp,
		Tracer:         tp.Tracer(TracerName),
		Meter:          mp.Meter(MeterName),
		shutdown: func(ctx context.Context) error {
			tErr := tp.Shutdown(ctx)
			mErr := mp.Shutdown(ctx)
			if tErr != nil {
				return tErr
			}
			return mErr
		},
	}, nil
}

// Shutdown flushes and shuts down the provider.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p.shutdown == nil {
		return nil
	}
	return p.shutdown(ctx)
}

func createExporter(ctx context.Context, cfg Config) (sdktrace.SpanExporter, error) {
	switch cfg.Exporter {
	case "otlp-http", "":
		endpoint := cfg.Endpoint
		if endpoint == "" {
			endpoint = "localhost:4318"
		}
		return otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(endpoint),
			otlptracehttp.WithInsecure(),
		)
	case "stdout":
		return stdouttrace.New(stdouttrace.WithPrettyPrint())
	case "none":
		// Return a no-op exporter that discards all spans.
		return &noopExporter{}, nil
	default:
		return nil, fmt.Errorf("unknown exporter: %s (supported: otlp-http, stdout, none)", cfg.Exporter)
	}
}

// noopExporter discards all spans. Used for exporter=none.
type noopExporter struct{}

func (e *noopExporter) ExportSpans(_ context.Context, _ []sdktrace.ReadOnlySpan) error {
	return nil
}
func (e *noopExporter) Shutdown(_ context.Context) error { return nil }
