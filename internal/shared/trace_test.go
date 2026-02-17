package shared

import (
	"context"
	"testing"
)

func TestMessageDepth_RoundTrip(t *testing.T) {
	ctx := context.Background()

	// Default is 0.
	if got := MessageDepth(ctx); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}

	// Set and retrieve.
	ctx = WithMessageDepth(ctx, 4)
	if got := MessageDepth(ctx); got != 4 {
		t.Fatalf("expected 4, got %d", got)
	}

	// Overwrite.
	ctx = WithMessageDepth(ctx, 7)
	if got := MessageDepth(ctx); got != 7 {
		t.Fatalf("expected 7, got %d", got)
	}
}

func TestDelegationHop_RoundTrip(t *testing.T) {
	ctx := context.Background()
	if got := DelegationHop(ctx); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
	ctx = WithDelegationHop(ctx, 2)
	if got := DelegationHop(ctx); got != 2 {
		t.Fatalf("expected 2, got %d", got)
	}
}

func TestAgentID_DefaultEmpty(t *testing.T) {
	ctx := context.Background()
	if got := AgentID(ctx); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	ctx = WithAgentID(ctx, "test-agent")
	if got := AgentID(ctx); got != "test-agent" {
		t.Fatalf("expected test-agent, got %q", got)
	}
}
