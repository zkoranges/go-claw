package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/engine"
	"github.com/basket/go-claw/internal/persistence"
)

func TestEngineWithAgentID(t *testing.T) {
	store := openStoreForEngineTest(t)
	ctx := context.Background()
	sessionID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	agentID := "test-agent"

	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	// Create a task scoped to the agent.
	taskID, err := store.CreateTaskForAgent(ctx, agentID, sessionID, `{"content":"hello"}`)
	if err != nil {
		t.Fatalf("create task for agent: %v", err)
	}

	proc := &countingProcessor{sleep: 20 * time.Millisecond}
	eng := engine.New(store, proc, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
		AgentID:      agentID,
	})

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	eng.Start(runCtx)

	task := waitForTaskStatus(t, store, taskID, persistence.TaskStatusSucceeded, 5*time.Second)
	if task.Status != persistence.TaskStatusSucceeded {
		t.Fatalf("expected SUCCEEDED, got %s", task.Status)
	}
}

func TestEngineWithoutAgentID(t *testing.T) {
	// Backward compatibility: engine without AgentID claims any task.
	store := openStoreForEngineTest(t)
	ctx := context.Background()
	sessionID := "b2c3d4e5-f6a7-8901-bcde-f12345678901"

	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	// Create a regular (unscoped) task.
	taskID, err := store.CreateTask(ctx, sessionID, `{"content":"generic"}`)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	proc := &countingProcessor{sleep: 20 * time.Millisecond}
	eng := engine.New(store, proc, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
		// AgentID not set — should claim any task
	})

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	eng.Start(runCtx)

	task := waitForTaskStatus(t, store, taskID, persistence.TaskStatusSucceeded, 5*time.Second)
	if task.Status != persistence.TaskStatusSucceeded {
		t.Fatalf("expected SUCCEEDED, got %s", task.Status)
	}
}

func TestCreateChatTaskForAgent(t *testing.T) {
	store := openStoreForEngineTest(t)
	ctx := context.Background()
	sessionID := "c3d4e5f6-a7b8-9012-cdef-123456789012"
	agentID := "agent-alpha"

	eng := engine.New(store, nil, engine.Config{
		WorkerCount:  1,
		PollInterval: 50 * time.Millisecond,
		TaskTimeout:  5 * time.Second,
	})

	taskID, err := eng.CreateChatTaskForAgent(ctx, agentID, sessionID, "test message")
	if err != nil {
		t.Fatalf("create chat task for agent: %v", err)
	}
	if taskID == "" {
		t.Fatal("expected non-empty task ID")
	}

	// Verify the task is stored with the correct agent_id.
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.AgentID != agentID {
		t.Fatalf("expected agent_id %q, got %q", agentID, task.AgentID)
	}
	if task.Status != persistence.TaskStatusQueued {
		t.Fatalf("expected QUEUED status, got %s", task.Status)
	}
}

func TestPublishEventIncludesAgentID(t *testing.T) {
	store := openStoreForEngineTest(t)
	ctx := context.Background()
	sessionID := "d4e5f6a7-b8c9-0123-defa-234567890123"
	agentID := "agent-beta"

	b := bus.New()
	sub := b.Subscribe("task.")

	proc := &countingProcessor{sleep: 20 * time.Millisecond}
	eng := engine.New(store, proc, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
		Bus:          b,
		AgentID:      agentID,
	})

	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	taskID, err := store.CreateTaskForAgent(ctx, agentID, sessionID, `{"content":"event-test"}`)
	if err != nil {
		t.Fatalf("create task for agent: %v", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	eng.Start(runCtx)

	// Wait for task to succeed.
	waitForTaskStatus(t, store, taskID, persistence.TaskStatusSucceeded, 5*time.Second)

	// Drain events and check for agent_id in payload.
	found := false
	deadline := time.After(2 * time.Second)
	for !found {
		select {
		case ev := <-sub.Ch():
			payload, ok := ev.Payload.(map[string]string)
			if !ok {
				continue
			}
			if payload["agent_id"] == agentID {
				found = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for event with agent_id")
			return
		}
	}
}

func TestEngineStatus_IncludesAgentID(t *testing.T) {
	store := openStoreForEngineTest(t)
	agentID := "status-agent"

	eng := engine.New(store, nil, engine.Config{
		WorkerCount: 2,
		AgentID:     agentID,
	})

	status := eng.Status()
	if status.AgentID != agentID {
		t.Fatalf("expected agent_id %q in status, got %q", agentID, status.AgentID)
	}
	if status.WorkerCount != 2 {
		t.Fatalf("expected worker_count 2, got %d", status.WorkerCount)
	}
}

func TestEngineStatus_EmptyAgentID(t *testing.T) {
	store := openStoreForEngineTest(t)

	eng := engine.New(store, nil, engine.Config{
		WorkerCount: 1,
	})

	status := eng.Status()
	if status.AgentID != "" {
		t.Fatalf("expected empty agent_id in status, got %q", status.AgentID)
	}
}

func TestTwoEnginesDifferentAgents(t *testing.T) {
	// Two engines with different AgentIDs sharing the same store should only
	// claim tasks scoped to their own agent.
	store := openStoreForEngineTest(t)
	ctx := context.Background()
	sessionID := "f6a7b8c9-d0e1-2345-fabc-456789012345"

	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	// Create one task for each agent.
	taskA, err := store.CreateTaskForAgent(ctx, "agent-A", sessionID, `{"content":"for A"}`)
	if err != nil {
		t.Fatalf("create task for agent-A: %v", err)
	}
	taskB, err := store.CreateTaskForAgent(ctx, "agent-B", sessionID, `{"content":"for B"}`)
	if err != nil {
		t.Fatalf("create task for agent-B: %v", err)
	}

	procA := &countingProcessor{sleep: 20 * time.Millisecond}
	procB := &countingProcessor{sleep: 20 * time.Millisecond}

	engA := engine.New(store, procA, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
		AgentID:      "agent-A",
	})
	engB := engine.New(store, procB, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
		AgentID:      "agent-B",
	})

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	engA.Start(runCtx)
	engB.Start(runCtx)

	// Both tasks should succeed — each engine claims only its own.
	waitForTaskStatus(t, store, taskA, persistence.TaskStatusSucceeded, 5*time.Second)
	waitForTaskStatus(t, store, taskB, persistence.TaskStatusSucceeded, 5*time.Second)

	// Verify each engine only processed one task.
	if got := procA.maxObserved.Load(); got != 1 {
		t.Fatalf("agent-A: expected max 1 concurrent task, got %d", got)
	}
	if got := procB.maxObserved.Load(); got != 1 {
		t.Fatalf("agent-B: expected max 1 concurrent task, got %d", got)
	}
}

func TestCreateChatTaskForAgent_Backpressure(t *testing.T) {
	store := openStoreForEngineTest(t)
	ctx := context.Background()
	sessionID := "e5f6a7b8-c9d0-1234-efab-345678901234"
	agentID := "agent-bp"

	eng := engine.New(store, nil, engine.Config{
		WorkerCount:   1,
		PollInterval:  50 * time.Millisecond,
		TaskTimeout:   5 * time.Second,
		MaxQueueDepth: 2,
	})

	// Fill the agent queue to the max.
	for i := 0; i < 2; i++ {
		_, err := eng.CreateChatTaskForAgent(ctx, agentID, sessionID, "msg")
		if err != nil {
			t.Fatalf("expected task %d to be created, got: %v", i, err)
		}
	}

	// The 3rd task should be rejected with backpressure.
	_, err := eng.CreateChatTaskForAgent(ctx, agentID, sessionID, "overflow")
	if err == nil {
		t.Fatal("expected backpressure error, got nil")
	}
	if err != engine.ErrQueueSaturated {
		t.Fatalf("expected ErrQueueSaturated, got: %v", err)
	}
}
