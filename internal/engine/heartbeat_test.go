package engine

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockChatTaskRouter implements ChatTaskRouter for testing.
type mockChatTaskRouter struct {
	lastAgentID   string
	lastSessionID string
	lastContent   string
	taskID        string
	err           error
}

func (m *mockChatTaskRouter) CreateChatTask(_ context.Context, agentID, sessionID, content string) (string, error) {
	m.lastAgentID = agentID
	m.lastSessionID = sessionID
	m.lastContent = content
	return m.taskID, m.err
}

func (m *mockChatTaskRouter) CreateMessageTask(_ context.Context, agentID, sessionID, content string, _ int) (string, error) {
	m.lastAgentID = agentID
	m.lastSessionID = sessionID
	m.lastContent = content
	return m.taskID, m.err
}

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

	// Setup mock router
	router := &mockChatTaskRouter{taskID: "test-task-id"}

	// Create manager
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := NewHeartbeatManager(router, nil, homeDir, 1, logger)

	// Run Once
	ctx := context.Background()
	if err := mgr.runOnce(ctx); err != nil {
		t.Fatalf("runOnce failed: %v", err)
	}

	// Verify router was called with correct args
	if router.lastAgentID != "default" {
		t.Errorf("expected agent_id 'default', got %q", router.lastAgentID)
	}
	if router.lastSessionID != HeartbeatSessionID {
		t.Errorf("expected session_id %q, got %q", HeartbeatSessionID, router.lastSessionID)
	}
	if !strings.Contains(router.lastContent, hbContent) {
		t.Errorf("expected content to contain heartbeat checklist, got %q", router.lastContent)
	}
}

func TestHeartbeatManager_RunOnce_NoFile(t *testing.T) {
	homeDir := t.TempDir()
	// workspace/HEARTBEAT.md does NOT exist

	router := &mockChatTaskRouter{taskID: "should-not-be-called"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := NewHeartbeatManager(router, nil, homeDir, 1, logger)

	if err := mgr.runOnce(context.Background()); err != nil {
		t.Fatalf("runOnce with missing file should succeed silently, got: %v", err)
	}
	if router.lastAgentID != "" {
		t.Error("router should not have been called when HEARTBEAT.md is missing")
	}
}
