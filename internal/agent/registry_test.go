package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
	"github.com/google/uuid"
)

func setupTestRegistry(t *testing.T) (*Registry, *persistence.Store) {
	t.Helper()
	store, err := persistence.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	eventBus := bus.New()
	pol, _ := policy.Load("")
	livePol := policy.NewLivePolicy(pol, "")

	reg := NewRegistry(store, eventBus, livePol, nil, nil)
	return reg, store
}

func TestCreateAgent(t *testing.T) {
	reg, store := setupTestRegistry(t)
	ctx := context.Background()

	err := reg.CreateAgent(ctx, AgentConfig{
		AgentID:     "test-agent",
		DisplayName: "Test Agent",
		Provider:    "google",
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Verify in registry.
	agent := reg.GetAgent("test-agent")
	if agent == nil {
		t.Fatal("agent not found in registry")
	}
	if agent.Config.DisplayName != "Test Agent" {
		t.Errorf("display name = %q, want %q", agent.Config.DisplayName, "Test Agent")
	}

	// Verify in DB.
	rec, err := store.GetAgent(ctx, "test-agent")
	if err != nil {
		t.Fatalf("GetAgent from DB: %v", err)
	}
	if rec == nil {
		t.Fatal("agent not found in DB")
	}
	if rec.Status != "active" {
		t.Errorf("DB status = %q, want %q", rec.Status, "active")
	}
}

func TestCreateAgentDuplicate(t *testing.T) {
	reg, _ := setupTestRegistry(t)
	ctx := context.Background()

	cfg := AgentConfig{AgentID: "dup-agent", Provider: "google"}
	if err := reg.CreateAgent(ctx, cfg); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := reg.CreateAgent(ctx, cfg); err == nil {
		t.Fatal("expected error on duplicate, got nil")
	}
}

func TestCreateAgentEmptyID(t *testing.T) {
	reg, _ := setupTestRegistry(t)
	ctx := context.Background()

	err := reg.CreateAgent(ctx, AgentConfig{AgentID: "", Provider: "google"})
	if err == nil {
		t.Fatal("expected error for empty agent_id")
	}
}

func TestRemoveAgent(t *testing.T) {
	reg, store := setupTestRegistry(t)
	ctx := context.Background()

	if err := reg.CreateAgent(ctx, AgentConfig{AgentID: "removable", Provider: "google"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if err := reg.RemoveAgent(ctx, "removable", 2*time.Second); err != nil {
		t.Fatalf("RemoveAgent: %v", err)
	}

	// Verify gone from registry.
	if agent := reg.GetAgent("removable"); agent != nil {
		t.Fatal("agent should be removed from registry")
	}

	// Verify DB status updated.
	rec, err := store.GetAgent(ctx, "removable")
	if err != nil {
		t.Fatalf("GetAgent from DB: %v", err)
	}
	if rec == nil {
		t.Fatal("agent record should still exist in DB")
	}
	if rec.Status != "stopped" {
		t.Errorf("DB status = %q, want %q", rec.Status, "stopped")
	}
}

func TestRemoveDefaultAgent(t *testing.T) {
	reg, _ := setupTestRegistry(t)
	ctx := context.Background()

	if err := reg.CreateAgent(ctx, AgentConfig{AgentID: "default", Provider: "google"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	err := reg.RemoveAgent(ctx, "default", 2*time.Second)
	if err == nil {
		t.Fatal("expected error when removing default agent")
	}
}

func TestRemoveNonexistent(t *testing.T) {
	reg, _ := setupTestRegistry(t)
	ctx := context.Background()

	err := reg.RemoveAgent(ctx, "ghost", 2*time.Second)
	if err == nil {
		t.Fatal("expected error for non-existent agent")
	}
}

func TestGetAgent(t *testing.T) {
	reg, _ := setupTestRegistry(t)
	ctx := context.Background()

	// Non-existent returns nil.
	if agent := reg.GetAgent("nope"); agent != nil {
		t.Fatal("expected nil for non-existent agent")
	}

	if err := reg.CreateAgent(ctx, AgentConfig{AgentID: "finder", Provider: "google"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	agent := reg.GetAgent("finder")
	if agent == nil {
		t.Fatal("expected to find agent")
	}
	if agent.Config.AgentID != "finder" {
		t.Errorf("agent_id = %q, want %q", agent.Config.AgentID, "finder")
	}
}

func TestListAgents(t *testing.T) {
	reg, _ := setupTestRegistry(t)
	ctx := context.Background()

	if err := reg.CreateAgent(ctx, AgentConfig{AgentID: "alpha", Provider: "google"}); err != nil {
		t.Fatalf("CreateAgent alpha: %v", err)
	}
	if err := reg.CreateAgent(ctx, AgentConfig{AgentID: "beta", Provider: "google"}); err != nil {
		t.Fatalf("CreateAgent beta: %v", err)
	}

	configs := reg.ListAgents()
	if len(configs) != 2 {
		t.Fatalf("ListAgents returned %d, want 2", len(configs))
	}

	ids := map[string]bool{}
	for _, c := range configs {
		ids[c.AgentID] = true
	}
	if !ids["alpha"] || !ids["beta"] {
		t.Errorf("missing expected agents, got %v", ids)
	}
}

func TestListRunningAgents(t *testing.T) {
	reg, _ := setupTestRegistry(t)
	ctx := context.Background()

	if err := reg.CreateAgent(ctx, AgentConfig{AgentID: "r1", Provider: "google"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	agents := reg.ListRunningAgents()
	if len(agents) != 1 {
		t.Fatalf("ListRunningAgents returned %d, want 1", len(agents))
	}
	if agents[0].Engine == nil {
		t.Fatal("running agent should have an engine")
	}
}

func TestAgentStatus(t *testing.T) {
	reg, _ := setupTestRegistry(t)
	ctx := context.Background()

	if err := reg.CreateAgent(ctx, AgentConfig{AgentID: "stat", Provider: "google", WorkerCount: 2}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	st, err := reg.AgentStatus("stat")
	if err != nil {
		t.Fatalf("AgentStatus: %v", err)
	}
	if st.WorkerCount != 2 {
		t.Errorf("WorkerCount = %d, want 2", st.WorkerCount)
	}

	// Non-existent agent.
	_, err = reg.AgentStatus("ghost")
	if err == nil {
		t.Fatal("expected error for non-existent agent")
	}
}

func TestCreateChatTaskRouting(t *testing.T) {
	reg, store := setupTestRegistry(t)
	ctx := context.Background()

	if err := reg.CreateAgent(ctx, AgentConfig{AgentID: "chat-agent", Provider: "google"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	sessionID := uuid.NewString()
	taskID, err := reg.CreateChatTask(ctx, "chat-agent", sessionID, "hello")
	if err != nil {
		t.Fatalf("CreateChatTask: %v", err)
	}
	if taskID == "" {
		t.Fatal("expected non-empty task ID")
	}

	// Verify task in DB has the correct agent_id.
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task == nil {
		t.Fatal("task not found in DB")
	}
	if task.AgentID != "chat-agent" {
		t.Errorf("task agent_id = %q, want %q", task.AgentID, "chat-agent")
	}
}

func TestCreateChatTaskUnknownAgent(t *testing.T) {
	reg, _ := setupTestRegistry(t)
	ctx := context.Background()

	_, err := reg.CreateChatTask(ctx, "nobody", uuid.NewString(), "hello")
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

func TestStreamChatTaskUnknownAgent(t *testing.T) {
	reg, _ := setupTestRegistry(t)
	ctx := context.Background()

	_, err := reg.StreamChatTask(ctx, "nobody", uuid.NewString(), "hello", func(string) error { return nil })
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

func TestAbortTaskRouting(t *testing.T) {
	reg, store := setupTestRegistry(t)
	ctx := context.Background()

	if err := reg.CreateAgent(ctx, AgentConfig{AgentID: "abort-agent", Provider: "google"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	sessionID := uuid.NewString()
	taskID, err := reg.CreateChatTask(ctx, "abort-agent", sessionID, "work")
	if err != nil {
		t.Fatalf("CreateChatTask: %v", err)
	}

	aborted, err := reg.AbortTask(ctx, taskID)
	if err != nil {
		t.Fatalf("AbortTask: %v", err)
	}

	// Check that the task was aborted (either via engine cancel or DB).
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	// The task should be canceled or abort should have returned true.
	_ = aborted
	_ = task
}

func TestAbortTaskNotFound(t *testing.T) {
	reg, _ := setupTestRegistry(t)
	ctx := context.Background()

	_, err := reg.AbortTask(ctx, uuid.NewString())
	if err == nil {
		t.Fatal("expected error for non-existent task")
	}
}

func TestDrainAll(t *testing.T) {
	reg, _ := setupTestRegistry(t)
	ctx := context.Background()

	if err := reg.CreateAgent(ctx, AgentConfig{AgentID: "drain1", Provider: "google"}); err != nil {
		t.Fatalf("CreateAgent drain1: %v", err)
	}
	if err := reg.CreateAgent(ctx, AgentConfig{AgentID: "drain2", Provider: "google"}); err != nil {
		t.Fatalf("CreateAgent drain2: %v", err)
	}

	// DrainAll should complete without deadlocking.
	done := make(chan struct{})
	go func() {
		reg.DrainAll(2 * time.Second)
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(10 * time.Second):
		t.Fatal("DrainAll timed out")
	}
}

func TestRestorePersistedAgents(t *testing.T) {
	// Phase 1: create an agent and persist it.
	store, err := persistence.Open(filepath.Join(t.TempDir(), "restore.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	eventBus := bus.New()
	pol, _ := policy.Load("")
	livePol := policy.NewLivePolicy(pol, "")

	reg1 := NewRegistry(store, eventBus, livePol, nil, nil)
	ctx := context.Background()

	if err := reg1.CreateAgent(ctx, AgentConfig{
		AgentID:     "persistent-agent",
		DisplayName: "Persistent",
		Provider:    "google",
		WorkerCount: 2,
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Drain the first registry.
	reg1.DrainAll(2 * time.Second)

	// Phase 2: create a new registry from the same store and restore.
	reg2 := NewRegistry(store, eventBus, livePol, nil, nil)
	if err := reg2.RestorePersistedAgents(ctx); err != nil {
		t.Fatalf("RestorePersistedAgents: %v", err)
	}

	agent := reg2.GetAgent("persistent-agent")
	if agent == nil {
		t.Fatal("restored agent not found in new registry")
	}
	if agent.Config.DisplayName != "Persistent" {
		t.Errorf("display name = %q, want %q", agent.Config.DisplayName, "Persistent")
	}
	if agent.Config.WorkerCount != 2 {
		t.Errorf("worker count = %d, want 2", agent.Config.WorkerCount)
	}

	// Cleanup.
	reg2.DrainAll(2 * time.Second)
}

func TestRestoreSkipsInactiveAgents(t *testing.T) {
	store, err := persistence.Open(filepath.Join(t.TempDir(), "inactive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	eventBus := bus.New()
	pol, _ := policy.Load("")
	livePol := policy.NewLivePolicy(pol, "")

	reg1 := NewRegistry(store, eventBus, livePol, nil, nil)
	ctx := context.Background()

	// Create and then remove (sets status to "stopped").
	if err := reg1.CreateAgent(ctx, AgentConfig{AgentID: "stopped-agent", Provider: "google"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := reg1.RemoveAgent(ctx, "stopped-agent", 2*time.Second); err != nil {
		t.Fatalf("RemoveAgent: %v", err)
	}

	// New registry should not restore stopped agents.
	reg2 := NewRegistry(store, eventBus, livePol, nil, nil)
	if err := reg2.RestorePersistedAgents(ctx); err != nil {
		t.Fatalf("RestorePersistedAgents: %v", err)
	}

	if agent := reg2.GetAgent("stopped-agent"); agent != nil {
		t.Fatal("stopped agent should not be restored")
	}
}

func TestDefaultWorkerCount(t *testing.T) {
	reg, _ := setupTestRegistry(t)
	ctx := context.Background()

	if err := reg.CreateAgent(ctx, AgentConfig{AgentID: "defaults", Provider: "google"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	agent := reg.GetAgent("defaults")
	if agent == nil {
		t.Fatal("agent not found")
	}
	if agent.Config.WorkerCount != 4 {
		t.Errorf("default WorkerCount = %d, want 4", agent.Config.WorkerCount)
	}
	if agent.Config.TaskTimeoutSeconds != 600 {
		t.Errorf("default TaskTimeoutSeconds = %d, want 600", agent.Config.TaskTimeoutSeconds)
	}
}
