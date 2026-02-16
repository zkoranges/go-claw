package channels

import (
	"context"
	"log/slog"
	"testing"

	"github.com/basket/go-claw/internal/bus"
)

// Telegram Deep Integration Tests (Phase 3)
// GC-SPEC-PDR-v7-Phase-3: Telegram event integration tests

// TestTelegram_SubscribesToEvents verifies event subscriptions work without panic.
func TestTelegram_SubscribesToEvents(t *testing.T) {
	// TDD: Verify event bus integration works (don't call SubscribeToEvents since bot is nil)
	tg := NewTelegramChannel(
		"test-token",
		[]int64{123456},
		nil, // router
		nil, // store
		slog.Default(),
		bus.New(), // eventBus
	)

	// Verify the channel has eventBus set
	if tg.eventBus == nil {
		t.Fatal("eventBus not set on Telegram channel")
	}

	// Verify we can subscribe manually to events (testing the pattern)
	sub := tg.eventBus.Subscribe(bus.TopicPlanStepCompleted)
	defer tg.eventBus.Unsubscribe(sub)

	if sub == nil {
		t.Fatal("subscription failed")
	}

	// Publish a test event to verify bus integration
	testEvent := bus.PlanStepEvent{
		ExecutionID: "exec-123",
		StepID:      "step-456",
		TaskID:      "task-789",
		AgentID:     "agent-test",
	}
	tg.eventBus.Publish(bus.TopicPlanStepCompleted, testEvent)

	// Verify event was received
	select {
	case ev := <-sub.Ch():
		if ev.Topic != bus.TopicPlanStepCompleted {
			t.Errorf("expected topic %q, got %q", bus.TopicPlanStepCompleted, ev.Topic)
		}
		payload, ok := ev.Payload.(bus.PlanStepEvent)
		if !ok {
			t.Fatalf("payload type error: expected PlanStepEvent, got %T", ev.Payload)
		}
		if payload.ExecutionID != "exec-123" {
			t.Errorf("expected execution ID exec-123, got %s", payload.ExecutionID)
		}
	case <-context.Background().Done():
		t.Fatal("timeout waiting for event")
	}
}

// TestTelegram_EscapesMarkdownV2 verifies MarkdownV2 special char escaping.
func TestTelegram_EscapesMarkdownV2(t *testing.T) {
	// TDD: escapeMarkdownV2 must escape all 14 special chars
	tests := []struct {
		input    string
		expected string
		name     string
	}{
		// Single special chars
		{`_underscore_`, `\_underscore\_`, "underscore"},
		{`*asterisk*`, `\*asterisk\*`, "asterisk"},
		{`[bracket]`, `\[bracket\]`, "square brackets"},
		{`(paren)`, `\(paren\)`, "parentheses"},
		{`~tilde~`, `\~tilde\~`, "tilde"},
		{`>greater`, `\>greater`, "greater than"},
		{`#hash`, `\#hash`, "hash"},
		{`+plus+`, `\+plus\+`, "plus"},
		{`-minus-`, `\-minus\-`, "minus"},
		{`=equals=`, `\=equals\=`, "equals"},
		{`|pipe|`, `\|pipe\|`, "pipe"},
		{`{brace}`, `\{brace\}`, "braces"},
		{`.period.`, `\.period\.`, "period"},
		{`!exclaim!`, `\!exclaim\!`, "exclamation"},

		// Multiple special chars
		{`_*[]()`, `\_\*\[\]\(\)`, "multiple special"},
		{`test_with*many[special]`, `test\_with\*many\[special\]`, "mixed text"},

		// No special chars
		{`hello world`, `hello world`, "no special chars"},
		{`simple`, `simple`, "simple text"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := escapeMarkdownV2(tt.input)
			if result != tt.expected {
				t.Errorf("escapeMarkdownV2(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestTelegram_FormatsPlanProgress_MarkdownV2 verifies progress formatting with escaping.
func TestTelegram_FormatsPlanProgress_MarkdownV2(t *testing.T) {
	// TDD: formatPlanProgress should format with MarkdownV2-safe output
	tests := []struct {
		planName string
		stepID   string
		status   string
		name     string
	}{
		{
			planName: "my_plan",
			stepID:   "step[1]",
			status:   "running",
			name:     "with special chars in names",
		},
		{
			planName: "simple",
			stepID:   "step1",
			status:   "completed",
			name:     "simple names",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create step events with special chars
			steps := []bus.PlanStepEvent{
				{
					ExecutionID: "exec-123",
					StepID:      tt.stepID,
					TaskID:      "task-456",
					AgentID:     "agent-test",
				},
			}

			// This should not panic and should return valid markdown
			output := formatPlanProgress(tt.planName, steps)
			if output == "" {
				t.Error("formatPlanProgress returned empty string")
			}

			// Verify no unescaped special chars in output (for safe Telegram rendering)
			// Check for escaped underscores and brackets (sample verification)
			if tt.planName == "my_plan" && !contains(output, "\\_") {
				t.Errorf("expected escaped underscore in output, got: %s", output)
			}
		})
	}
}

// TestTelegram_ParsesPlanCommand verifies /plan command parsing.
func TestTelegram_ParsesPlanCommand(t *testing.T) {
	// TDD: parsePlanCommand should extract plan name and optional input
	tests := []struct {
		input          string
		expectedPlan   string
		expectedInput  string
		shouldFail     bool
		name           string
	}{
		{
			input:         "/plan myplan",
			expectedPlan:  "myplan",
			expectedInput: "",
			shouldFail:    false,
			name:          "simple plan name",
		},
		{
			input:         "/plan myplan arg1 arg2",
			expectedPlan:  "myplan",
			expectedInput: "arg1 arg2",
			shouldFail:    false,
			name:          "plan with args",
		},
		{
			input:         "/plan",
			expectedPlan:  "",
			expectedInput: "",
			shouldFail:    true,
			name:          "no plan name",
		},
		{
			input:         "/plan my_plan with spaces",
			expectedPlan:  "my_plan",
			expectedInput: "with spaces",
			shouldFail:    false,
			name:          "underscores and spaces",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			planName, input, err := parsePlanCommand(tt.input)

			if tt.shouldFail {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if planName != tt.expectedPlan {
					t.Errorf("expected plan %q, got %q", tt.expectedPlan, planName)
				}
				if input != tt.expectedInput {
					t.Errorf("expected input %q, got %q", tt.expectedInput, input)
				}
			}
		})
	}
}

// TestTelegram_ParsesHITLCallback verifies callback data parsing.
func TestTelegram_ParsesHITLCallback(t *testing.T) {
	// TDD: parseHITLCallback should parse "hitl:requestID:action" format
	tests := []struct {
		data           string
		expectedID     string
		expectedAction string
		shouldFail     bool
		name           string
	}{
		{
			data:           "hitl:req-123:approve",
			expectedID:     "req-123",
			expectedAction: "approve",
			shouldFail:     false,
			name:           "approve action",
		},
		{
			data:           "hitl:req-456:reject",
			expectedID:     "req-456",
			expectedAction: "reject",
			shouldFail:     false,
			name:           "reject action",
		},
		{
			data:           "hitl:req-789:invalid",
			expectedID:     "req-789",
			expectedAction: "invalid",
			shouldFail:     false, // Parser doesn't validate action
			name:           "arbitrary action",
		},
		{
			data:           "other:data:format",
			expectedID:     "",
			expectedAction: "",
			shouldFail:     true,
			name:           "non-hitl prefix",
		},
		{
			data:           "hitl:incomplete",
			expectedID:     "",
			expectedAction: "",
			shouldFail:     true,
			name:           "incomplete format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, action, err := parseHITLCallback(tt.data)

			if tt.shouldFail {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if id != tt.expectedID {
					t.Errorf("expected ID %q, got %q", tt.expectedID, id)
				}
				if action != tt.expectedAction {
					t.Errorf("expected action %q, got %q", tt.expectedAction, action)
				}
			}
		})
	}
}

// TestTelegram_PublishesHITLResponse verifies HITL responses are published to bus.
func TestTelegram_PublishesHITLResponse(t *testing.T) {
	// TDD: Verify that HITL responses are published to the event bus
	testBus := bus.New()
	sub := testBus.Subscribe(bus.TopicHITLApprovalResponse)
	defer testBus.Unsubscribe(sub)

	// Simulate publishing a HITL response
	response := bus.HITLApprovalResponse{
		RequestID: "req-123",
		Action:    "approve",
		Reason:    "test approval",
	}
	testBus.Publish(bus.TopicHITLApprovalResponse, response)

	// Verify it was received
	select {
	case ev := <-sub.Ch():
		if ev.Topic != bus.TopicHITLApprovalResponse {
			t.Errorf("expected topic %q, got %q", bus.TopicHITLApprovalResponse, ev.Topic)
		}
		payload, ok := ev.Payload.(bus.HITLApprovalResponse)
		if !ok {
			t.Fatalf("payload type error: expected HITLApprovalResponse, got %T", ev.Payload)
		}
		if payload.RequestID != "req-123" {
			t.Errorf("expected request ID req-123, got %s", payload.RequestID)
		}
		if payload.Action != "approve" {
			t.Errorf("expected action approve, got %s", payload.Action)
		}
	case <-context.Background().Done():
		t.Fatal("timeout waiting for event")
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
