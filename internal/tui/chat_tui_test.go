package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/basket/go-claw/internal/bus"
)

func TestDeleteWordLeft(t *testing.T) {
	in := []rune("hello   world")
	out, cur := deleteWordLeft(in, len(in))
	if string(out) != "hello   " {
		t.Fatalf("unexpected out: %q", string(out))
	}
	if cur != len([]rune("hello   ")) {
		t.Fatalf("unexpected cursor: %d", cur)
	}
}

func TestDeleteWordLeft_SkipsSpacesThenWord(t *testing.T) {
	in := []rune("abc   ")
	out, cur := deleteWordLeft(in, len(in))
	if string(out) != "" {
		t.Fatalf("unexpected out: %q", string(out))
	}
	if cur != 0 {
		t.Fatalf("unexpected cursor: %d", cur)
	}
}

func TestHandleCommand_HelpWritesOutput(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	shouldExit := handleCommand("/help", &cc, "sess", &buf)
	if shouldExit {
		t.Fatalf("expected shouldExit=false")
	}
	if !strings.Contains(buf.String(), "Commands:") {
		t.Fatalf("expected help output, got: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "/plans") {
		t.Fatalf("expected /plans in help output, got: %q", buf.String())
	}
}

func TestPlanTracker_HandleEvent(t *testing.T) {
	pt := &planTracker{executions: make(map[string]*PlanExecutionState)}

	// Simulate plan.execution.started event.
	pt.handleEvent(bus.Event{
		Topic: "plan.execution.started",
		Payload: map[string]interface{}{
			"execution_id": "exec-1",
			"plan_name":    "deploy",
			"total_steps":  4,
		},
	})

	pt.mu.RLock()
	if len(pt.executions) != 1 {
		t.Fatalf("expected 1 execution, got %d", len(pt.executions))
	}
	pe := pt.executions["exec-1"]
	pt.mu.RUnlock()

	if pe.PlanName != "deploy" {
		t.Errorf("expected plan name 'deploy', got %q", pe.PlanName)
	}
	if pe.Status != "running" {
		t.Errorf("expected status 'running', got %q", pe.Status)
	}
	if pe.TotalSteps != 4 {
		t.Errorf("expected 4 total steps, got %d", pe.TotalSteps)
	}

	// Simulate plan.execution.completed event.
	pt.handleEvent(bus.Event{
		Topic: "plan.execution.completed",
		Payload: map[string]interface{}{
			"execution_id": "exec-1",
			"status":       "succeeded",
		},
	})

	pt.mu.RLock()
	pe = pt.executions["exec-1"]
	pt.mu.RUnlock()

	if pe.Status != "succeeded" {
		t.Errorf("expected status 'succeeded', got %q", pe.Status)
	}
	if pe.CompletedSteps != 4 {
		t.Errorf("expected 4 completed steps, got %d", pe.CompletedSteps)
	}
}

func TestPlanTracker_Cleanup(t *testing.T) {
	pt := &planTracker{executions: make(map[string]*PlanExecutionState)}

	// Add a completed plan with old timestamp.
	pt.executions["old"] = &PlanExecutionState{
		ExecutionID: "old",
		Status:      "succeeded",
		StartedAt:   time.Now().Add(-5 * time.Second),
	}
	// Add a running plan.
	pt.executions["active"] = &PlanExecutionState{
		ExecutionID: "active",
		Status:      "running",
		StartedAt:   time.Now(),
	}

	pt.cleanup()

	pt.mu.RLock()
	defer pt.mu.RUnlock()
	if _, exists := pt.executions["old"]; exists {
		t.Error("expected old completed plan to be removed")
	}
	if _, exists := pt.executions["active"]; !exists {
		t.Error("expected active plan to remain")
	}
}

func TestPlanView_RenderEmpty(t *testing.T) {
	m := newChatModel(context.Background(), ChatConfig{}, "sess", "test", "model")
	m.mode = chatModePlanView

	view := m.renderPlanView()
	if !strings.Contains(view, "No active plans") {
		t.Errorf("expected 'No active plans' in view, got: %q", view)
	}
	if !strings.Contains(view, "Plan Executions") {
		t.Errorf("expected 'Plan Executions' header, got: %q", view)
	}
}

func TestPlanView_RenderWithExecution(t *testing.T) {
	m := newChatModel(context.Background(), ChatConfig{}, "sess", "test", "model")
	m.plans.executions["e1"] = &PlanExecutionState{
		ExecutionID:    "e1",
		PlanName:       "deploy-prod",
		Status:         "running",
		TotalSteps:     8,
		CompletedSteps: 4,
		StartedAt:      time.Now(),
	}

	view := m.renderPlanView()
	if !strings.Contains(view, "deploy-prod") {
		t.Errorf("expected plan name in view, got: %q", view)
	}
	if !strings.Contains(view, "4/8 steps") {
		t.Errorf("expected step count in view, got: %q", view)
	}
	if !strings.Contains(view, "RUNNING") {
		t.Errorf("expected RUNNING status in view, got: %q", view)
	}
	if !strings.Contains(view, "e1") {
		t.Errorf("expected execution ID in view, got: %q", view)
	}
}

func TestPlanView_SlashPlansEntersMode(t *testing.T) {
	m := newChatModel(context.Background(), ChatConfig{}, "sess", "test", "model")

	// Type "/plans" into input.
	m.input = []rune("/plans")
	m.cursor = 6

	// Send Enter to submit.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(chatModel)

	if um.mode != chatModePlanView {
		t.Errorf("expected chatModePlanView, got %d", um.mode)
	}
}

func TestPlanView_AnyKeyExits(t *testing.T) {
	m := newChatModel(context.Background(), ChatConfig{}, "sess", "test", "model")
	m.mode = chatModePlanView

	// Press any key (e.g. 'q')
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	um := updated.(chatModel)

	if um.mode != chatModeChat {
		t.Errorf("expected chatModeChat after key press in plan view, got %d", um.mode)
	}
}

func TestPlanView_ViewRendersInPlanMode(t *testing.T) {
	m := newChatModel(context.Background(), ChatConfig{}, "sess", "test", "model")
	m.mode = chatModePlanView

	view := m.View()
	if !strings.Contains(view, "Plan Executions") {
		t.Errorf("expected plan view in View() output, got: %q", view)
	}
}
