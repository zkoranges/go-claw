package persistence_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/persistence"
)

func TestSaveLoopCheckpoint(t *testing.T) {
	store, _ := openTestStore(t)

	cp := &persistence.LoopCheckpoint{
		LoopID:      "loop-001",
		TaskID:      "task-001",
		AgentID:     "agent-001",
		CurrentStep: 5,
		MaxSteps:    25,
		TokensUsed:  1000,
		MaxTokens:   100000,
		StartedAt:   time.Now().Truncate(time.Second),
		MaxDuration: 30 * time.Minute,
		Status:      "running",
		Messages:    "[]",
	}

	err := store.SaveLoopCheckpoint(cp)
	if err != nil {
		t.Fatalf("SaveLoopCheckpoint: %v", err)
	}

	// Verify by loading
	loaded, err := store.LoadLoopCheckpoint("task-001")
	if err != nil {
		t.Fatalf("LoadLoopCheckpoint after save: %v", err)
	}
	if loaded.LoopID != "loop-001" {
		t.Fatalf("expected loop_id=loop-001, got %q", loaded.LoopID)
	}
	if loaded.CurrentStep != 5 {
		t.Fatalf("expected current_step=5, got %d", loaded.CurrentStep)
	}
	if loaded.Status != "running" {
		t.Fatalf("expected status=running, got %q", loaded.Status)
	}
}

func TestLoadLoopCheckpoint(t *testing.T) {
	store, _ := openTestStore(t)

	cp := &persistence.LoopCheckpoint{
		LoopID:      "loop-load-001",
		TaskID:      "task-load-001",
		AgentID:     "agent-load-001",
		CurrentStep: 3,
		MaxSteps:    10,
		TokensUsed:  500,
		MaxTokens:   50000,
		StartedAt:   time.Now().Truncate(time.Second),
		MaxDuration: 15 * time.Minute,
		Status:      "running",
		Messages:    `[{"role":"user","content":"hello"}]`,
	}

	if err := store.SaveLoopCheckpoint(cp); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := store.LoadLoopCheckpoint("task-load-001")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if loaded.AgentID != "agent-load-001" {
		t.Fatalf("expected agent_id=agent-load-001, got %q", loaded.AgentID)
	}
	if loaded.MaxSteps != 10 {
		t.Fatalf("expected max_steps=10, got %d", loaded.MaxSteps)
	}
	if loaded.TokensUsed != 500 {
		t.Fatalf("expected tokens_used=500, got %d", loaded.TokensUsed)
	}
	if loaded.MaxDuration != 15*time.Minute {
		t.Fatalf("expected max_duration=15m, got %v", loaded.MaxDuration)
	}
	if loaded.Messages != `[{"role":"user","content":"hello"}]` {
		t.Fatalf("expected messages JSON, got %q", loaded.Messages)
	}
}

func TestLoadLoopCheckpoint_NotFound(t *testing.T) {
	store, _ := openTestStore(t)

	_, err := store.LoadLoopCheckpoint("nonexistent-task")
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestSaveLoopCheckpoint_Upsert(t *testing.T) {
	store, _ := openTestStore(t)

	cp := &persistence.LoopCheckpoint{
		LoopID:      "loop-upsert-001",
		TaskID:      "task-upsert-001",
		AgentID:     "agent-upsert-001",
		CurrentStep: 1,
		MaxSteps:    25,
		TokensUsed:  100,
		MaxTokens:   100000,
		StartedAt:   time.Now().Truncate(time.Second),
		MaxDuration: 30 * time.Minute,
		Status:      "running",
		Messages:    "[]",
	}

	if err := store.SaveLoopCheckpoint(cp); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// Update the checkpoint
	cp.CurrentStep = 5
	cp.TokensUsed = 2000
	cp.Status = "running"

	if err := store.SaveLoopCheckpoint(cp); err != nil {
		t.Fatalf("second save (upsert): %v", err)
	}

	// Verify updated values
	loaded, err := store.LoadLoopCheckpoint("task-upsert-001")
	if err != nil {
		t.Fatalf("load after upsert: %v", err)
	}
	if loaded.CurrentStep != 5 {
		t.Fatalf("expected current_step=5 after upsert, got %d", loaded.CurrentStep)
	}
	if loaded.TokensUsed != 2000 {
		t.Fatalf("expected tokens_used=2000 after upsert, got %d", loaded.TokensUsed)
	}
}

func TestCleanupCompletedLoops(t *testing.T) {
	store, _ := openTestStore(t)

	// Save a completed checkpoint
	cp := &persistence.LoopCheckpoint{
		LoopID:      "loop-cleanup-001",
		TaskID:      "task-cleanup-001",
		AgentID:     "agent-cleanup-001",
		CurrentStep: 10,
		MaxSteps:    10,
		TokensUsed:  5000,
		MaxTokens:   100000,
		StartedAt:   time.Now().Truncate(time.Second),
		MaxDuration: 30 * time.Minute,
		Status:      "completed",
		Messages:    "[]",
	}

	if err := store.SaveLoopCheckpoint(cp); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Also save a running checkpoint that should NOT be cleaned up
	cpRunning := &persistence.LoopCheckpoint{
		LoopID:      "loop-cleanup-002",
		TaskID:      "task-cleanup-002",
		AgentID:     "agent-cleanup-002",
		CurrentStep: 3,
		MaxSteps:    10,
		TokensUsed:  500,
		MaxTokens:   100000,
		StartedAt:   time.Now().Truncate(time.Second),
		MaxDuration: 30 * time.Minute,
		Status:      "running",
		Messages:    "[]",
	}

	if err := store.SaveLoopCheckpoint(cpRunning); err != nil {
		t.Fatalf("save running: %v", err)
	}

	// Backdate the completed checkpoint's updated_at so cleanup can find it.
	// SQLite datetime resolution is seconds, so we must move it far enough back.
	db := store.DB()
	_, dbErr := db.Exec(`UPDATE loop_checkpoints SET updated_at = datetime('now', '-120 seconds') WHERE loop_id = ?`, "loop-cleanup-001")
	if dbErr != nil {
		t.Fatalf("backdate updated_at: %v", dbErr)
	}

	// Cleanup anything older than 60 seconds
	n, err := store.CleanupCompletedLoops(60 * time.Second)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 cleaned up, got %d", n)
	}

	// Running checkpoint should still exist
	loaded, err := store.LoadLoopCheckpoint("task-cleanup-002")
	if err != nil {
		t.Fatalf("running checkpoint should still exist: %v", err)
	}
	if loaded.LoopID != "loop-cleanup-002" {
		t.Fatalf("expected loop-cleanup-002, got %q", loaded.LoopID)
	}
}

func TestLoadLoopCheckpoint_OnlyRunning(t *testing.T) {
	store, _ := openTestStore(t)

	// Save a completed checkpoint - should NOT be returned by LoadLoopCheckpoint
	cp := &persistence.LoopCheckpoint{
		LoopID:      "loop-status-001",
		TaskID:      "task-status-001",
		AgentID:     "agent-status-001",
		CurrentStep: 10,
		MaxSteps:    10,
		TokensUsed:  5000,
		MaxTokens:   100000,
		StartedAt:   time.Now().Truncate(time.Second),
		MaxDuration: 30 * time.Minute,
		Status:      "completed",
		Messages:    "[]",
	}

	if err := store.SaveLoopCheckpoint(cp); err != nil {
		t.Fatalf("save completed: %v", err)
	}

	// LoadLoopCheckpoint only returns running checkpoints
	_, err := store.LoadLoopCheckpoint("task-status-001")
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows for completed checkpoint, got %v", err)
	}

	// Save a running checkpoint for the same task (different loop_id)
	cpRunning := &persistence.LoopCheckpoint{
		LoopID:      "loop-status-002",
		TaskID:      "task-status-001",
		AgentID:     "agent-status-001",
		CurrentStep: 2,
		MaxSteps:    10,
		TokensUsed:  200,
		MaxTokens:   100000,
		StartedAt:   time.Now().Truncate(time.Second),
		MaxDuration: 30 * time.Minute,
		Status:      "running",
		Messages:    "[]",
	}

	if err := store.SaveLoopCheckpoint(cpRunning); err != nil {
		t.Fatalf("save running: %v", err)
	}

	// Now should find the running checkpoint
	loaded, err := store.LoadLoopCheckpoint("task-status-001")
	if err != nil {
		t.Fatalf("expected to find running checkpoint, got: %v", err)
	}
	if loaded.LoopID != "loop-status-002" {
		t.Fatalf("expected loop-status-002, got %q", loaded.LoopID)
	}
	if loaded.Status != "running" {
		t.Fatalf("expected status=running, got %q", loaded.Status)
	}
}
