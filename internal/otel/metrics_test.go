package otel

import (
	"context"
	"testing"
)

func TestNewMetrics_AllInstrumentsCreated(t *testing.T) {
	p, err := Init(context.Background(), Config{
		Enabled:  true,
		Exporter: "none",
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer p.Shutdown(context.Background())

	m, err := NewMetrics(p.Meter)
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}

	if m.RequestDuration == nil {
		t.Error("RequestDuration is nil")
	}
	if m.TaskDuration == nil {
		t.Error("TaskDuration is nil")
	}
	if m.LLMCallDuration == nil {
		t.Error("LLMCallDuration is nil")
	}
	if m.TokensUsed == nil {
		t.Error("TokensUsed is nil")
	}
	if m.ToolCallDuration == nil {
		t.Error("ToolCallDuration is nil")
	}
	if m.ToolCallErrors == nil {
		t.Error("ToolCallErrors is nil")
	}
	if m.ActiveLoops == nil {
		t.Error("ActiveLoops is nil")
	}
	if m.LoopStepsTotal == nil {
		t.Error("LoopStepsTotal is nil")
	}
	if m.StreamTokens == nil {
		t.Error("StreamTokens is nil")
	}
	if m.RateLimitRejects == nil {
		t.Error("RateLimitRejects is nil")
	}
}

func TestNewMetrics_NoopMeter(t *testing.T) {
	// Disabled OTel returns noop meter — metrics should still create without error.
	p, err := Init(context.Background(), Config{Enabled: false})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer p.Shutdown(context.Background())

	m, err := NewMetrics(p.Meter)
	if err != nil {
		t.Fatalf("NewMetrics with noop: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil Metrics")
	}
}
