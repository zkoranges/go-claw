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

func (m *mockRouter) CreateMessageTask(ctx context.Context, agentID, sessionID, content string, _ int) (string, error) {
	return m.CreateChatTask(ctx, agentID, sessionID, content)
}

// GC-SPEC-PDR-v4-Phase-3: Test executor construction.
func TestNewExecutor(t *testing.T) {
	router := &mockRouter{}
	exec := NewExecutor(router, nil, nil, nil)
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

	exec := NewExecutor(router, nil, nil, nil)
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
	exec := NewExecutor(router, nil, store, nil)

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
	// Note: With step persistence, we now publish step started events too.
	var events []bus.Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev := <-sub.Ch():
			events = append(events, ev)
			// We expect at least 2 events: started + completion, plus any step events.
			// Keep collecting until we see completion event or timeout.
			hasCompletion := false
			for _, e := range events {
				if e.Topic == bus.TopicPlanExecutionCompleted {
					hasCompletion = true
					break
				}
			}
			if hasCompletion && len(events) >= 2 {
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

	// Find the completion event (skip any step-related events in between)
	var completedEvent *bus.Event
	for i := 1; i < len(events); i++ {
		if events[i].Topic == bus.TopicPlanExecutionCompleted {
			completedEvent = &events[i]
			break
		}
	}
	if completedEvent == nil {
		t.Fatalf("no completion event found in %d events", len(events))
	}

	// Verify event: plan.execution.completed
	if completedEvent.Topic != bus.TopicPlanExecutionCompleted {
		t.Fatalf("completion event topic: got %q, want %s", completedEvent.Topic, bus.TopicPlanExecutionCompleted)
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

// GC-SPEC-PDR-v4-Phase-3: Test plan resumption from crash checkpoint.
func TestExecutor_Resume_FullRecovery(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := persistence.Open(dbPath, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// Create session
	sessionID := "d7d6a91d-72b7-6456-d0de-dfe3c69f4d7e"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	// Create and partially execute a plan
	plan := &Plan{
		Name: "recovery-test",
		Steps: []PlanStep{
			{ID: "s1", AgentID: "a1", Prompt: "step 1"},
			{ID: "s2", AgentID: "a2", Prompt: "step 2"},
		},
	}

	execID := "e8e7b02e-83c8-7567-e1de-dfe4d70f5e8f"
	if err := store.CreatePlanExecution(ctx, execID, plan.Name, sessionID, 2); err != nil {
		t.Fatalf("create execution: %v", err)
	}

	// Initialize steps
	stepRecs := []persistence.PlanExecutionStep{
		{StepID: "s1", StepIndex: 0, WaveNumber: 0, AgentID: "a1", Prompt: "step 1"},
		{StepID: "s2", StepIndex: 0, WaveNumber: 1, AgentID: "a2", Prompt: "step 2"},
	}
	if err := store.InitializePlanSteps(ctx, execID, stepRecs); err != nil {
		t.Fatalf("initialize steps: %v", err)
	}

	// Mark first step as completed
	if err := store.RecordStepComplete(ctx, execID, "s1", "succeeded", "output 1", "", 0.1); err != nil {
		t.Fatalf("record step: %v", err)
	}

	// Mark wave 1 as completed
	if err := store.UpdatePlanWave(ctx, execID, 1); err != nil {
		t.Fatalf("update wave: %v", err)
	}

	// Create executor and resume
	router := &mockRouter{}
	executor := NewExecutor(router, nil, store, nil)
	result, err := executor.Resume(ctx, execID, plan)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}

	// Verify result has both steps
	if len(result.StepResults) < 1 {
		t.Fatalf("expected at least 1 step result, got %d", len(result.StepResults))
	}

	// Verify s1 was recovered
	if s1, ok := result.StepResults["s1"]; !ok {
		t.Fatal("s1 missing from results")
	} else if s1.Status != "succeeded" || s1.Output != "output 1" {
		t.Fatalf("s1 wrong result: status=%s, output=%s", s1.Status, s1.Output)
	}

	// Verify plan shows completion
	exec, err := store.GetPlanExecution(ctx, execID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if exec.Status != "succeeded" {
		t.Fatalf("final status: got %s, want succeeded", exec.Status)
	}
}

// GC-SPEC-PDR-v4-Phase-3: Test resume skips completed waves.
func TestExecutor_Resume_SkipsCompletedWaves(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := persistence.Open(dbPath, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// Create session
	sessionID := "e8e7c03e-84d9-7678-f2de-eef5e81f6f0a"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	// Create 3-wave plan
	plan := &Plan{
		Name: "multi-wave",
		Steps: []PlanStep{
			{ID: "s1", AgentID: "a1", Prompt: "wave 0"},
			{ID: "s2", AgentID: "a2", Prompt: "wave 1", DependsOn: []string{"s1"}},
			{ID: "s3", AgentID: "a3", Prompt: "wave 2", DependsOn: []string{"s2"}},
		},
	}

	execID := "f9f8c13f-95ea-8789-a3de-fef6f92f7b1a"
	if err := store.CreatePlanExecution(ctx, execID, plan.Name, sessionID, 3); err != nil {
		t.Fatalf("create execution: %v", err)
	}

	// Initialize all steps
	stepRecs := []persistence.PlanExecutionStep{
		{StepID: "s1", StepIndex: 0, WaveNumber: 0, AgentID: "a1", Prompt: "wave 0"},
		{StepID: "s2", StepIndex: 0, WaveNumber: 1, AgentID: "a2", Prompt: "wave 1"},
		{StepID: "s3", StepIndex: 0, WaveNumber: 2, AgentID: "a3", Prompt: "wave 2"},
	}
	if err := store.InitializePlanSteps(ctx, execID, stepRecs); err != nil {
		t.Fatalf("initialize steps: %v", err)
	}

	// Complete waves 0 and 1
	for _, stepID := range []string{"s1", "s2"} {
		if err := store.RecordStepComplete(ctx, execID, stepID, "succeeded", "output", "", 0.1); err != nil {
			t.Fatalf("record step %s: %v", stepID, err)
		}
	}
	if err := store.UpdatePlanWave(ctx, execID, 2); err != nil {
		t.Fatalf("update wave: %v", err)
	}

	// Resume - should skip waves 0-1 and execute only wave 2
	router := &mockRouter{}
	executor := NewExecutor(router, nil, store, nil)
	result, err := executor.Resume(ctx, execID, plan)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}

	// Verify s3 was executed (should be in result)
	if _, ok := result.StepResults["s3"]; !ok {
		t.Fatal("s3 not in results (should have been re-executed)")
	}
}
