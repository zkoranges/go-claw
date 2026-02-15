package coordinator

import (
	"context"
	"testing"
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
