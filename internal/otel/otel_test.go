package otel

import (
	"context"
	"testing"
)

func TestInit_Disabled(t *testing.T) {
	p, err := Init(context.Background(), Config{Enabled: false})
	if err != nil {
		t.Fatalf("Init disabled: %v", err)
	}
	defer p.Shutdown(context.Background())

	if p.Tracer == nil {
		t.Fatal("expected non-nil tracer (noop)")
	}
	if p.Meter == nil {
		t.Fatal("expected non-nil meter (noop)")
	}
}

func TestInit_Disabled_ShutdownNoop(t *testing.T) {
	p, err := Init(context.Background(), Config{Enabled: false})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Shutdown should be a no-op and not error
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestInit_NoneExporter(t *testing.T) {
	p, err := Init(context.Background(), Config{
		Enabled:  true,
		Exporter: "none",
	})
	if err != nil {
		t.Fatalf("Init with none exporter: %v", err)
	}
	defer p.Shutdown(context.Background())

	if p.TracerProvider == nil {
		t.Fatal("expected non-nil TracerProvider")
	}
	if p.Tracer == nil {
		t.Fatal("expected non-nil Tracer")
	}
	if p.Meter == nil {
		t.Fatal("expected non-nil Meter")
	}
}

func TestInit_UnknownExporter(t *testing.T) {
	_, err := Init(context.Background(), Config{
		Enabled:  true,
		Exporter: "magic-pixie-dust",
	})
	if err == nil {
		t.Fatal("expected error for unknown exporter")
	}
}

func TestInit_ServiceNameDefault(t *testing.T) {
	p, err := Init(context.Background(), Config{
		Enabled:  true,
		Exporter: "none",
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer p.Shutdown(context.Background())
	// Service name defaults to "goclaw" — no way to assert from outside,
	// but we verify no error on init.
}

func TestInit_CustomServiceName(t *testing.T) {
	p, err := Init(context.Background(), Config{
		Enabled:     true,
		Exporter:    "none",
		ServiceName: "my-custom-service",
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer p.Shutdown(context.Background())
}

func TestInit_SampleRate(t *testing.T) {
	p, err := Init(context.Background(), Config{
		Enabled:    true,
		Exporter:   "none",
		SampleRate: 0.5,
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer p.Shutdown(context.Background())
}

func TestInit_TracerCreatesSpans(t *testing.T) {
	p, err := Init(context.Background(), Config{
		Enabled:  true,
		Exporter: "none",
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer p.Shutdown(context.Background())

	ctx, span := p.Tracer.Start(context.Background(), "test.span")
	if span == nil {
		t.Fatal("expected non-nil span")
	}
	span.End()
	_ = ctx
}

func TestSpanHelpers(t *testing.T) {
	p, err := Init(context.Background(), Config{
		Enabled:  true,
		Exporter: "none",
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer p.Shutdown(context.Background())

	// StartSpan
	ctx, span := StartSpan(context.Background(), p.Tracer, "test.internal",
		AttrAgentID.String("test-agent"),
		AttrTaskID.String("test-task"),
	)
	span.End()
	_ = ctx

	// StartServerSpan
	ctx2, span2 := StartServerSpan(context.Background(), p.Tracer, "test.server")
	span2.End()
	_ = ctx2

	// StartClientSpan
	ctx3, span3 := StartClientSpan(context.Background(), p.Tracer, "test.client",
		AttrModel.String("gemini-2.5-pro"),
	)
	span3.End()
	_ = ctx3
}
