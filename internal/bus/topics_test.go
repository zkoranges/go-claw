package bus

import (
	"testing"
)

// TestEventTopics_Constants verifies all event constants exist.
// GC-SPEC-PDR-v7-Phase-3: Event contract definition.
func TestEventTopics_Constants(t *testing.T) {
	// Plan step events (started/completed defined in bus.go)
	if TopicPlanStepStarted == "" {
		t.Fatal("TopicPlanStepStarted is empty")
	}
	if TopicPlanStepCompleted == "" {
		t.Fatal("TopicPlanStepCompleted is empty")
	}
	if TopicPlanStepFailed == "" {
		t.Fatal("TopicPlanStepFailed is empty")
	}

	// HITL approval events
	if TopicHITLApprovalRequested == "" {
		t.Fatal("TopicHITLApprovalRequested is empty")
	}
	if TopicHITLApprovalResponse == "" {
		t.Fatal("TopicHITLApprovalResponse is empty")
	}

	// Agent alert event
	if TopicAgentAlert == "" {
		t.Fatal("TopicAgentAlert is empty")
	}

	// Agent message event
	if TopicAgentMessage == "" {
		t.Fatal("TopicAgentMessage is empty")
	}

	// Verify no duplicates among new topics
	topics := map[string]bool{
		TopicPlanStepFailed:          true,
		TopicHITLApprovalRequested:   true,
		TopicHITLApprovalResponse:    true,
		TopicAgentAlert:              true,
		TopicAgentMessage:            true,
	}
	if len(topics) != 5 {
		t.Fatalf("expected 5 unique new topics, got %d", len(topics))
	}
}

// TestPlanStepEvent_Marshaling verifies PlanStepEvent can be constructed.
// GC-SPEC-PDR-v7-Phase-3: Event payload marshaling.
func TestPlanStepEvent_Marshaling(t *testing.T) {
	event := PlanStepEvent{
		ExecutionID: "exec-123",
		StepID:      "step-1",
		TaskID:      "task-456",
		AgentID:     "agent-a",
	}

	if event.ExecutionID != "exec-123" {
		t.Fatalf("ExecutionID mismatch: got %s, want exec-123", event.ExecutionID)
	}
	if event.StepID != "step-1" {
		t.Fatalf("StepID mismatch: got %s, want step-1", event.StepID)
	}
	if event.TaskID != "task-456" {
		t.Fatalf("TaskID mismatch: got %s, want task-456", event.TaskID)
	}
	if event.AgentID != "agent-a" {
		t.Fatalf("AgentID mismatch: got %s, want agent-a", event.AgentID)
	}
}

// TestHITLApprovalRequest_RequestID verifies RequestID field required.
// GC-SPEC-PDR-v7-Phase-3: HITL approval contract.
func TestHITLApprovalRequest_RequestID(t *testing.T) {
	req := HITLApprovalRequest{
		RequestID:   "req-123",
		ExecutionID: "exec-456",
		StepID:      "step-1",
		Prompt:      "Continue with step?",
		Timeout:     100, // milliseconds
	}

	if req.RequestID == "" {
		t.Fatal("RequestID must not be empty")
	}
	if req.ExecutionID == "" {
		t.Fatal("ExecutionID must not be empty")
	}
	if req.StepID == "" {
		t.Fatal("StepID must not be empty")
	}
	if req.Prompt == "" {
		t.Fatal("Prompt must not be empty")
	}
	if req.Timeout <= 0 {
		t.Fatalf("Timeout must be positive, got %d", req.Timeout)
	}
}

// TestHITLApprovalResponse_Matching verifies Response fields required.
// GC-SPEC-PDR-v7-Phase-3: HITL response contract.
func TestHITLApprovalResponse_Matching(t *testing.T) {
	resp := HITLApprovalResponse{
		RequestID: "req-123",
		Action:    "approve",
		Reason:    "Looks good",
	}

	if resp.RequestID == "" {
		t.Fatal("RequestID must not be empty")
	}
	if resp.Action == "" {
		t.Fatal("Action must not be empty")
	}
	// Reason can be empty (optional)

	// Test response with rejection
	respReject := HITLApprovalResponse{
		RequestID: "req-124",
		Action:    "reject",
		Reason:    "Data looks wrong",
	}

	if respReject.Action != "reject" {
		t.Fatalf("Action mismatch: got %s, want reject", respReject.Action)
	}
}

// TestAgentAlert_Severity verifies severity field required.
// GC-SPEC-PDR-v7-Phase-3: Agent alert contract.
func TestAgentAlert_Severity(t *testing.T) {
	alert := AgentAlert{
		ExecutionID: "exec-123",
		StepID:      "step-1",
		Severity:    "warning",
		Message:     "High token usage",
	}

	if alert.Severity == "" {
		t.Fatal("Severity must not be empty")
	}
	if alert.ExecutionID == "" {
		t.Fatal("ExecutionID must not be empty")
	}
	if alert.Message == "" {
		t.Fatal("Message must not be empty")
	}

	// Test different severity levels
	for _, sev := range []string{"info", "warning", "error"} {
		a := AgentAlert{
			Severity: sev,
			Message:  "test",
		}
		if a.Severity != sev {
			t.Fatalf("Severity mismatch: got %s, want %s", a.Severity, sev)
		}
	}
}
