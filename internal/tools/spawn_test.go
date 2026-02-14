package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basket/go-claw/internal/persistence"
)

// spawnTestPolicy implements policy.Checker for spawn tests.
type spawnTestPolicy struct {
	allowCap map[string]bool
}

func (p spawnTestPolicy) AllowHTTPURL(string) bool { return true }
func (p spawnTestPolicy) AllowCapability(cap string) bool {
	if p.allowCap == nil {
		return false
	}
	return p.allowCap[cap]
}
func (p spawnTestPolicy) AllowPath(string) bool { return true }
func (p spawnTestPolicy) PolicyVersion() string { return "spawn-test-v1" }

// testSessionID is a valid UUID used across spawn tests.
const testSessionID = "00000000-0000-4000-8000-000000000001"

func openSpawnTestStore(t *testing.T) *persistence.Store {
	t.Helper()
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	// Ensure the test session exists (sessions table has a FK from tasks).
	if err := store.EnsureSession(context.Background(), testSessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	return store
}

// createParentTask creates a regular task that can serve as a parent for subtasks.
func createParentTask(t *testing.T, store *persistence.Store) string {
	t.Helper()
	parentID, err := store.CreateTask(context.Background(), testSessionID, "parent task payload")
	if err != nil {
		t.Fatalf("create parent task: %v", err)
	}
	return parentID
}

func TestSpawnTask_CreatesQueuedTask(t *testing.T) {
	store := openSpawnTestStore(t)
	pol := spawnTestPolicy{allowCap: map[string]bool{"tools.spawn_task": true}}

	parentID := createParentTask(t, store)

	input := &SpawnTaskInput{
		Description:  "Summarize the results",
		Payload:      "Please summarize the research findings",
		Priority:     5,
		ParentTaskID: parentID,
		SessionID:    testSessionID,
	}

	out, err := spawnTask(context.Background(), input, store, pol)
	if err != nil {
		t.Fatalf("spawnTask: %v", err)
	}

	if out.TaskID == "" {
		t.Fatal("expected non-empty task ID")
	}
	if out.Status != "QUEUED" {
		t.Fatalf("expected status QUEUED, got %q", out.Status)
	}

	// Verify the task exists in the store with correct session_id.
	task, err := store.GetTask(context.Background(), out.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.SessionID != testSessionID {
		t.Fatalf("expected session_id=%s, got %q", testSessionID, task.SessionID)
	}
	if task.Status != persistence.TaskStatusQueued {
		t.Fatalf("expected status QUEUED, got %q", task.Status)
	}
}

func TestSpawnTask_LinksToParent(t *testing.T) {
	store := openSpawnTestStore(t)
	pol := spawnTestPolicy{allowCap: map[string]bool{"tools.spawn_task": true}}

	parentID := createParentTask(t, store)

	// Spawn two subtasks linked to the parent.
	for i, desc := range []string{"subtask-1", "subtask-2"} {
		input := &SpawnTaskInput{
			Description:  desc,
			Payload:      "payload " + desc,
			Priority:     i + 1,
			ParentTaskID: parentID,
			SessionID:    testSessionID,
		}
		_, err := spawnTask(context.Background(), input, store, pol)
		if err != nil {
			t.Fatalf("spawnTask(%s): %v", desc, err)
		}
	}

	// Verify subtasks are linked to parent via GetSubtasks.
	subtasks, err := store.GetSubtasks(context.Background(), parentID)
	if err != nil {
		t.Fatalf("GetSubtasks: %v", err)
	}
	if len(subtasks) != 2 {
		t.Fatalf("expected 2 subtasks, got %d", len(subtasks))
	}

	// Verify each subtask has the correct session and status.
	for _, sub := range subtasks {
		if sub.SessionID != testSessionID {
			t.Errorf("subtask %s: expected session_id=%s, got %q", sub.ID, testSessionID, sub.SessionID)
		}
		if sub.Status != persistence.TaskStatusQueued {
			t.Errorf("subtask %s: expected status QUEUED, got %q", sub.ID, sub.Status)
		}
	}
}

func TestSpawnTask_PolicyDeny(t *testing.T) {
	store := openSpawnTestStore(t)
	// Policy that denies tools.spawn_task capability.
	pol := spawnTestPolicy{allowCap: map[string]bool{"tools.web_search": true}}

	input := &SpawnTaskInput{
		Description:  "Denied task",
		Payload:      "should not be created",
		ParentTaskID: "parent-123",
		SessionID:    testSessionID,
	}

	_, err := spawnTask(context.Background(), input, store, pol)
	if err == nil {
		t.Fatal("expected error when policy denies spawn_task capability")
	}
	if !strings.Contains(err.Error(), "tools.spawn_task") {
		t.Fatalf("expected error to mention tools.spawn_task, got: %v", err)
	}
}
