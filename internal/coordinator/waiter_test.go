package coordinator_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/coordinator"
	"github.com/basket/go-claw/internal/persistence"
)

func openTestStore(t *testing.T) *persistence.Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "goclaw.db")
	store, err := persistence.Open(dbPath, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

// GC-SPEC-PDR-v4-Phase-2: Event-driven task completion tracking.
func TestWaiterConstruction(t *testing.T) {
	store := openTestStore(t)
	b := bus.New()
	w := coordinator.NewWaiter(b, store)
	if w == nil {
		t.Fatal("expected non-nil waiter")
	}
}

// GC-SPEC-PDR-v4-Phase-2: Check terminal task returns immediately.
func TestWaitForTask_AlreadyTerminal(t *testing.T) {
	store := openTestStore(t)
	b := bus.New()
	w := coordinator.NewWaiter(b, store)
	ctx := context.Background()

	// Create and complete a task
	sessionID := "00000000-0000-0000-0000-000000000001"
	_ = store.EnsureSession(ctx, sessionID)
	taskID, _ := store.CreateTask(ctx, sessionID, "payload")

	// Claim, start, and complete
	task, _ := store.ClaimNextPendingTask(ctx)
	if task != nil {
		_ = store.StartTaskRun(ctx, task.ID, "test-owner", "1")
		_ = store.CompleteTask(ctx, task.ID, "result")
	}

	// Wait should return immediately
	result, err := w.WaitForTask(ctx, taskID, 5*time.Second)
	if err != nil {
		t.Fatalf("wait for task: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Status != string(persistence.TaskStatusSucceeded) {
		t.Fatalf("expected SUCCEEDED, got %s", result.Status)
	}
}

// GC-SPEC-PDR-v4-Phase-2: Timeout on incomplete task.
func TestWaitForTask_Timeout(t *testing.T) {
	store := openTestStore(t)
	b := bus.New()
	w := coordinator.NewWaiter(b, store)
	ctx := context.Background()

	// Create but don't complete a task
	sessionID := "00000000-0000-0000-0000-000000000002"
	_ = store.EnsureSession(ctx, sessionID)
	taskID, _ := store.CreateTask(ctx, sessionID, "payload")

	// Wait with short timeout should timeout
	result, err := w.WaitForTask(ctx, taskID, 50*time.Millisecond)
	if err == nil {
		t.Fatalf("expected error, got result: %v", result)
	}
}

// GC-SPEC-PDR-v4-Phase-2: WaitForAll processes multiple tasks.
func TestWaitForAll_Parallel(t *testing.T) {
	store := openTestStore(t)
	b := bus.New()
	w := coordinator.NewWaiter(b, store)
	ctx := context.Background()

	sessionID := "00000000-0000-0000-0000-000000000003"
	_ = store.EnsureSession(ctx, sessionID)

	// Create 2 tasks and complete them
	task1, _ := store.CreateTask(ctx, sessionID, "p1")
	task2, _ := store.CreateTask(ctx, sessionID, "p2")

	// Claim and complete both
	for i := 0; i < 2; i++ {
		t, _ := store.ClaimNextPendingTask(ctx)
		if t != nil {
			_ = store.StartTaskRun(ctx, t.ID, "owner", "1")
			_ = store.CompleteTask(ctx, t.ID, "done")
		}
	}

	// Wait for both
	results, err := w.WaitForAll(ctx, []string{task1, task2}, 5*time.Second)
	if err != nil {
		t.Fatalf("wait for all: %v", err)
	}
	if len(results) < 1 {
		t.Fatal("expected at least one result")
	}
}
