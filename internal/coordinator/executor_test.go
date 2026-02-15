package coordinator

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/persistence"
)

// Mock router for testing.
type mockRouter struct {
	tasks map[string]string // taskID -> content
}

func (m *mockRouter) CreateChatTask(ctx context.Context, agentID, sessionID, content string) (string, error) {
	taskID := "task-" + agentID
	if m.tasks == nil {
		m.tasks = make(map[string]string)
	}
	m.tasks[taskID] = content
	return taskID, nil
}

// GC-SPEC-PDR-v4-Phase-3: Test executor construction.
func TestNewExecutor(t *testing.T) {
	router := &mockRouter{}
	exec := NewExecutor(router, nil, nil)
	if exec == nil {
		t.Fatal("expected non-nil executor")
	}
	if exec.taskRouter != router {
		t.Fatal("expected router to be set")
	}
}

// GC-SPEC-PDR-v4-Phase-3: Test prompt template substitution.
func TestResolvePrompt(t *testing.T) {
	result := &ExecutionResult{
		StepResults: map[string]StepResult{
			"research": {Output: "The sun is a star."},
		},
	}
	got := resolvePrompt("Based on: {research.output}", result)
	want := "Based on: The sun is a star."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// GC-SPEC-PDR-v4-Phase-3: Test prompt with no references.
func TestResolvePrompt_NoMatch(t *testing.T) {
	result := &ExecutionResult{StepResults: make(map[string]StepResult)}
	got := resolvePrompt("No references here", result)
	if got != "No references here" {
		t.Fatalf("prompt should be unchanged, got %q", got)
	}
}

// GC-SPEC-PDR-v4-Phase-3: Test plan validation - empty plan.
func TestValidate_EmptyPlan(t *testing.T) {
	p := Plan{Steps: nil}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for empty plan")
	}
}

// GC-SPEC-PDR-v4-Phase-3: Test plan validation - duplicate IDs.
func TestValidate_DuplicateID(t *testing.T) {
	p := Plan{Steps: []PlanStep{
		{ID: "a", AgentID: "x", Prompt: "1"},
		{ID: "a", AgentID: "y", Prompt: "2"},
	}}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for duplicate ID")
	}
}

// GC-SPEC-PDR-v4-Phase-3: Test plan validation - missing dependency.
func TestValidate_MissingDependency(t *testing.T) {
	p := Plan{Steps: []PlanStep{
		{ID: "a", AgentID: "x", Prompt: "1", DependsOn: []string{"nonexistent"}},
	}}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for missing dependency")
	}
}

// GC-SPEC-PDR-v4-Phase-3: Test topological sort.
func TestTopoSort_LinearDependencies(t *testing.T) {
	steps := []PlanStep{
		{ID: "a", AgentID: "x", Prompt: "1"},
		{ID: "b", AgentID: "y", Prompt: "2", DependsOn: []string{"a"}},
		{ID: "c", AgentID: "z", Prompt: "3", DependsOn: []string{"b"}},
	}
	waves, err := topoSort(steps)
	if err != nil {
		t.Fatalf("topoSort failed: %v", err)
	}
	if len(waves) != 3 {
		t.Fatalf("expected 3 waves, got %d", len(waves))
	}
	if waves[0][0].ID != "a" {
		t.Fatalf("wave 0 should be a, got %s", waves[0][0].ID)
	}
	if waves[1][0].ID != "b" {
		t.Fatalf("wave 1 should be b, got %s", waves[1][0].ID)
	}
	if waves[2][0].ID != "c" {
		t.Fatalf("wave 2 should be c, got %s", waves[2][0].ID)
	}
}

// GC-SPEC-PDR-v4-Phase-3: Test topological sort with parallel steps.
func TestTopoSort_ParallelWave(t *testing.T) {
	steps := []PlanStep{
		{ID: "a", AgentID: "x", Prompt: "1"},
		{ID: "b", AgentID: "y", Prompt: "2"},
		{ID: "c", AgentID: "z", Prompt: "3", DependsOn: []string{"a", "b"}},
	}
	waves, err := topoSort(steps)
	if err != nil {
		t.Fatalf("topoSort failed: %v", err)
	}
	if len(waves) != 2 {
		t.Fatalf("expected 2 waves, got %d", len(waves))
	}
	if len(waves[0]) != 2 {
		t.Fatalf("wave 0 should have 2 parallel steps, got %d", len(waves[0]))
	}
	if len(waves[1]) != 1 {
		t.Fatalf("wave 1 should have 1 step, got %d", len(waves[1]))
	}
}

// GC-SPEC-PDR-v4-Phase-3: Test topological sort with cycle detection.
func TestTopoSort_CycleDetected(t *testing.T) {
	steps := []PlanStep{
		{ID: "a", AgentID: "x", Prompt: "1", DependsOn: []string{"b"}},
		{ID: "b", AgentID: "y", Prompt: "2", DependsOn: []string{"a"}},
	}
	_, err := topoSort(steps)
	if err == nil {
		t.Fatal("expected error for cycle")
	}
}

// GC-SPEC-PDR-v4-Phase-3: Test execution with no waiter (test mode).
func TestExecute_TestMode(t *testing.T) {
	router := &mockRouter{}
	plan := &Plan{
		Name: "test-plan",
		Steps: []PlanStep{
			{ID: "step1", AgentID: "agent-a", Prompt: "Do something"},
		},
	}

	exec := NewExecutor(router, nil, nil)
	result, err := exec.Execute(context.Background(), plan, "test-session")
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.ExecutionID == "" {
		t.Fatal("expected non-empty execution ID")
	}
	if stepResult, ok := result.StepResults["step1"]; !ok {
		t.Fatal("expected step1 result")
	} else if stepResult.Status != "RUNNING" {
		t.Fatalf("expected RUNNING status in test mode, got %s", stepResult.Status)
	}
}

// TestExecutor_Events verifies that plan execution publishes events to the event bus.
// Events flow through Store.CreatePlanExecution and Store.CompletePlanExecution.
// GC-SPEC-PDR-v4-Phase-4: Plan execution event integration test.
func TestExecutor_Events(t *testing.T) {
	// Create event bus and subscribe to plan events.
	eventBus := bus.New()
	sub := eventBus.Subscribe("plan.")
	defer eventBus.Unsubscribe(sub)

	// Create a real store (with bus) so CreatePlanExecution/CompletePlanExecution publish events.
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"), eventBus)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Simple 1-step plan.
	plan := &Plan{
		Name: "event-test-plan",
		Steps: []PlanStep{
			{ID: "step1", AgentID: "default", Prompt: "echo test"},
		},
	}

	// Executor with mock router and no waiter (test mode).
	router := &mockRouter{}
	exec := NewExecutor(router, nil, store)

	// Ensure the session exists (plan_executions has FK on sessions).
	sessionID := "7ced61c5-923f-41c2-ac40-d2137193a676"
	if err := store.EnsureSession(context.Background(), sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	result, err := exec.Execute(context.Background(), plan, sessionID)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if result.ExecutionID == "" {
		t.Fatal("expected non-empty execution ID")
	}

	// Collect events from subscription within timeout.
	var events []bus.Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev := <-sub.Ch():
			events = append(events, ev)
			// We expect exactly 2 events: started + completed.
			if len(events) >= 2 {
				goto verify
			}
		case <-timeout:
			goto verify
		}
	}

verify:
	if len(events) < 2 {
		t.Fatalf("expected at least 2 plan events, got %d", len(events))
	}

	// Verify event 1: plan.execution.started
	startedEvent := events[0]
	if startedEvent.Topic != bus.TopicPlanExecutionStarted {
		t.Fatalf("event 0 topic: got %q, want %s", startedEvent.Topic, bus.TopicPlanExecutionStarted)
	}
	startedPayload, ok := startedEvent.Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("event 0 payload type: got %T, want map[string]interface{}", startedEvent.Payload)
	}
	if startedPayload["execution_id"] == nil || startedPayload["execution_id"].(string) == "" {
		t.Fatal("started event missing execution_id")
	}
	if startedPayload["plan_name"] != "event-test-plan" {
		t.Fatalf("started event plan_name: got %v, want event-test-plan", startedPayload["plan_name"])
	}
	if startedPayload["total_steps"] != 1 {
		t.Fatalf("started event total_steps: got %v, want 1", startedPayload["total_steps"])
	}

	// Verify event 2: plan.execution.completed
	completedEvent := events[1]
	if completedEvent.Topic != bus.TopicPlanExecutionCompleted {
		t.Fatalf("event 1 topic: got %q, want %s", completedEvent.Topic, bus.TopicPlanExecutionCompleted)
	}
	completedPayload, ok := completedEvent.Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("event 1 payload type: got %T, want map[string]interface{}", completedEvent.Payload)
	}
	if completedPayload["execution_id"] != startedPayload["execution_id"] {
		t.Fatalf("completed execution_id %v != started execution_id %v",
			completedPayload["execution_id"], startedPayload["execution_id"])
	}
	if completedPayload["status"] != "succeeded" {
		t.Fatalf("completed status: got %v, want succeeded", completedPayload["status"])
	}
}
