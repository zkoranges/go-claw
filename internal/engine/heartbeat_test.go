package engine

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
)

func TestHeartbeatManager_RunOnce(t *testing.T) {
	// Setup temp home
	homeDir := t.TempDir()
	workspaceDir := filepath.Join(homeDir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	// Create HEARTBEAT.md
	hbContent := "- [ ] Check system health"
	if err := os.WriteFile(filepath.Join(workspaceDir, "HEARTBEAT.md"), []byte(hbContent), 0o644); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}

	// Setup DB
	dbPath := filepath.Join(homeDir, "test.db")
	store, err := persistence.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// Setup Engine (Mock proc)
	mockProc := &MockProcessor{}
	eng := New(store, mockProc, Config{WorkerCount: 1}, policy.Default())

	// Create manager
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := NewHeartbeatManager(eng, store, homeDir, 1, logger)

	// Ensure session (normally done in Start, but we call runOnce directly)
	if err := store.EnsureSession(context.Background(), HeartbeatSessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	// Run Once
	ctx := context.Background()
	if err := mgr.runOnce(ctx); err != nil {
		t.Fatalf("runOnce failed: %v", err)
	}

	// Verify task created
	pending, _, err := store.TaskCounts(ctx)
	if err != nil {
		t.Fatalf("task counts: %v", err)
	}
	if pending != 1 {
		t.Errorf("expected 1 pending task, got %d", pending)
	}

	// Verify content
	task, err := store.ClaimNextPendingTask(ctx)
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if task == nil {
		t.Fatal("expected task to be claimable")
	}

	if !strings.Contains(task.Payload, hbContent) {
		t.Errorf("task payload missing heartbeat content: %s", task.Payload)
	}
}

// MockProcessor for testing
type MockProcessor struct{}

func (m *MockProcessor) Process(ctx context.Context, task persistence.Task) (string, error) {
	return "processed", nil
}
