package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/config"
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
	shouldExit := handleCommand(context.Background(), "/help", &cc, "sess", &buf)
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
		Topic: bus.TopicPlanExecutionStarted,
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
		Topic: bus.TopicPlanExecutionCompleted,
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

func TestNewChatModel_Defaults(t *testing.T) {
	m := newChatModel(context.Background(), ChatConfig{}, "sess-id", "TestAgent", "gemini-2.5-flash")

	if m.sessionID != "sess-id" {
		t.Errorf("sessionID = %q, want %q", m.sessionID, "sess-id")
	}
	if m.agentPrefix != "TestAgent" {
		t.Errorf("agentPrefix = %q, want %q", m.agentPrefix, "TestAgent")
	}
	if m.modelName != "gemini-2.5-flash" {
		t.Errorf("modelName = %q, want %q", m.modelName, "gemini-2.5-flash")
	}
	if m.mode != chatModeChat {
		t.Errorf("mode = %d, want chatModeChat (%d)", m.mode, chatModeChat)
	}
	if len(m.history) != 1 {
		t.Errorf("expected 1 intro entry, got %d", len(m.history))
	}
	if m.plans == nil {
		t.Error("plans tracker should not be nil")
	}
}

func TestChatModel_InputHistoryNavigation(t *testing.T) {
	m := newChatModel(context.Background(), ChatConfig{}, "sess", "test", "model")

	// Simulate sending two messages by populating input history.
	m.inputHistory = []string{"first message", "second message"}
	m.histIdx = 2

	// Press Up arrow — should show "second message".
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(chatModel)
	if string(m.input) != "second message" {
		t.Errorf("after Up: input = %q, want %q", string(m.input), "second message")
	}

	// Press Up again — should show "first message".
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(chatModel)
	if string(m.input) != "first message" {
		t.Errorf("after 2x Up: input = %q, want %q", string(m.input), "first message")
	}

	// Press Down — should go back to "second message".
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(chatModel)
	if string(m.input) != "second message" {
		t.Errorf("after Down: input = %q, want %q", string(m.input), "second message")
	}
}

func TestChatModel_CtrlC_Quits(t *testing.T) {
	m := newChatModel(context.Background(), ChatConfig{}, "sess", "test", "model")

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("expected a quit command from Ctrl+C")
	}
}

func TestChatModel_EmptyEnter_NoOp(t *testing.T) {
	m := newChatModel(context.Background(), ChatConfig{}, "sess", "test", "model")
	m.input = nil
	m.cursor = 0

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(chatModel)
	if cmd != nil {
		t.Error("expected nil cmd for empty enter")
	}
	// History should not grow.
	if len(m.inputHistory) != 0 {
		t.Errorf("expected empty input history, got %d", len(m.inputHistory))
	}
}

func TestChatModel_SlashQuit_Exits(t *testing.T) {
	m := newChatModel(context.Background(), ChatConfig{}, "sess", "test", "model")
	m.input = []rune("/quit")
	m.cursor = 5

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected quit command from /quit")
	}
}

func TestChatModel_SlashModelEntersSelector(t *testing.T) {
	m := newChatModel(context.Background(), ChatConfig{
		Cfg: &config.Config{},
	}, "sess", "test", "model")
	m.input = []rune("/model")
	m.cursor = 6

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(chatModel)
	if um.mode != chatModeModelSelector {
		t.Errorf("expected chatModeModelSelector, got %d", um.mode)
	}
}

func TestChatModel_ViewInChatMode(t *testing.T) {
	m := newChatModel(context.Background(), ChatConfig{}, "sess", "TestBot", "model")
	m.width = 80
	m.height = 24

	view := m.View()
	if view == "" {
		t.Error("view should not be empty")
	}
}

func TestChatModel_UnknownCommand(t *testing.T) {
	m := newChatModel(context.Background(), ChatConfig{}, "sess", "test", "model")
	m.input = []rune("/nonexistent")
	m.cursor = 12

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(chatModel)

	// Should show error in history.
	lastEntry := um.history[len(um.history)-1]
	if !strings.Contains(lastEntry.text, "Unknown command") {
		t.Errorf("expected unknown command error, got: %q", lastEntry.text)
	}
}

func TestChatModel_ThinkingBlocksInput(t *testing.T) {
	m := newChatModel(context.Background(), ChatConfig{}, "sess", "test", "model")
	m.thinking = true
	m.input = []rune("test message")
	m.cursor = 12

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(chatModel)

	// When thinking, enter should be a no-op.
	if cmd != nil {
		t.Error("expected nil cmd when thinking")
	}
}

func TestChatModel_CtrlN_OpensModal(t *testing.T) {
	m := newChatModel(context.Background(), ChatConfig{}, "sess", "test", "model")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	um := updated.(chatModel)

	if !um.agentModal.IsOpen() {
		t.Error("expected agent modal to be open after Ctrl+N")
	}
}

func TestDeleteWordLeft_EmptyInput(t *testing.T) {
	out, cur := deleteWordLeft(nil, 0)
	if len(out) != 0 || cur != 0 {
		t.Errorf("expected empty result, got out=%q cur=%d", string(out), cur)
	}
}

func TestDeleteWordLeft_CursorAtStart(t *testing.T) {
	in := []rune("hello")
	out, cur := deleteWordLeft(in, 0)
	if string(out) != "hello" || cur != 0 {
		t.Errorf("got out=%q cur=%d, want out='hello' cur=0", string(out), cur)
	}
}

func TestDeleteWordLeft_SingleWord(t *testing.T) {
	in := []rune("hello")
	out, cur := deleteWordLeft(in, 5)
	if string(out) != "" || cur != 0 {
		t.Errorf("got out=%q cur=%d, want out='' cur=0", string(out), cur)
	}
}

func TestDeleteWordLeft_MidWord(t *testing.T) {
	in := []rune("hello world")
	out, cur := deleteWordLeft(in, 8) // cursor after "wo" in "world"
	// Deletes "wo" (word chars before cursor), leaves "hello rld".
	if string(out) != "hello rld" || cur != 6 {
		t.Errorf("got out=%q cur=%d, want out='hello rld' cur=6", string(out), cur)
	}
}
