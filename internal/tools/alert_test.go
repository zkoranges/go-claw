package tools

import (
	"context"
	"testing"

	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/shared"
	"github.com/firebase/genkit/go/ai"
)

// alertTestPolicy implements policy.Checker for alert tests.
type alertTestPolicy struct {
	allowCap map[string]bool
}

func (p alertTestPolicy) AllowHTTPURL(string) bool { return true }
func (p alertTestPolicy) AllowCapability(cap string) bool {
	if p.allowCap == nil {
		return false
	}
	return p.allowCap[cap]
}
func (p alertTestPolicy) AllowPath(string) bool { return true }
func (p alertTestPolicy) PolicyVersion() string { return "alert-test-v1" }

// TestAlert_Input_InvalidSeverity tests that invalid severity values are rejected.
func TestAlert_Input_InvalidSeverity(t *testing.T) {
	testBus := bus.New()
	pol := alertTestPolicy{allowCap: map[string]bool{"tools.send_alert": true}}
	input := AlertInput{
		Severity: "invalid",
		Title:    "Test Alert",
		Body:     "Test message",
	}

	ctx := context.Background()
	toolCtx := &ai.ToolContext{Context: ctx}

	_, err := sendAlert(toolCtx, input, testBus, pol)
	if err == nil {
		t.Error("expected error for invalid severity, got nil")
	}
	if err.Error() != `invalid severity "invalid": must be one of "info", "warning", "critical"` {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestAlert_ValidInput_PublishesEvent tests that valid inputs publish events.
func TestAlert_ValidInput_PublishesEvent(t *testing.T) {
	testBus := bus.New()
	pol := alertTestPolicy{allowCap: map[string]bool{"tools.send_alert": true}}
	input := AlertInput{
		Severity: "warning",
		Title:    "Test Alert",
		Body:     "Test message",
	}

	agentID := "test-agent"
	ctx := shared.WithAgentID(context.Background(), agentID)
	toolCtx := &ai.ToolContext{Context: ctx}

	// Subscribe to agent alerts
	sub := testBus.Subscribe(bus.TopicAgentAlert)
	defer testBus.Unsubscribe(sub)

	output, err := sendAlert(toolCtx, input, testBus, pol)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !output.Delivered {
		t.Error("expected Delivered=true")
	}

	// Verify event was published
	select {
	case event := <-sub.Ch():
		if event.Topic != bus.TopicAgentAlert {
			t.Errorf("expected topic %q, got %q", bus.TopicAgentAlert, event.Topic)
		}

		payload, ok := event.Payload.(bus.AgentAlert)
		if !ok {
			t.Fatalf("expected AgentAlert payload, got %T", event.Payload)
		}

		if payload.Severity != "warning" {
			t.Errorf("expected severity warning, got %q", payload.Severity)
		}
		if payload.Message != "Test message" {
			t.Errorf("expected message %q, got %q", "Test message", payload.Message)
		}
	case <-ctx.Done():
		t.Error("context cancelled before event received")
	}
}

// TestAlert_Severities tests all valid severities succeed without error.
func TestAlert_Severities(t *testing.T) {
	severities := []string{"info", "warning", "critical"}

	for _, severity := range severities {
		t.Run(severity, func(t *testing.T) {
			testBus := bus.New()
			pol := alertTestPolicy{allowCap: map[string]bool{"tools.send_alert": true}}
			input := AlertInput{
				Severity: severity,
				Title:    "Test Alert",
				Body:     "Test message",
			}

			ctx := context.Background()
			toolCtx := &ai.ToolContext{Context: ctx}

			output, err := sendAlert(toolCtx, input, testBus, pol)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if !output.Delivered {
				t.Error("expected Delivered=true")
			}
		})
	}
}
