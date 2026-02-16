package persistence_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/persistence"
)

// Phase 2: Async Delegation Persistence Tests (PDR v7)

func TestDelegation_Create(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()

	d := &persistence.Delegation{
		ParentAgent: "agent1",
		ChildAgent:  "agent2",
		Prompt:      "process data",
		Status:      "queued",
		CreatedAt:   time.Now(),
	}

	err := store.CreateDelegation(ctx, d)
	if err != nil {
		t.Fatalf("CreateDelegation: %v", err)
	}

	if d.ID == "" {
		t.Fatal("expected non-empty ID after create")
	}

	// Verify it was stored
	retrieved, err := store.GetDelegation(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetDelegation after create: %v", err)
	}
	if retrieved.ParentAgent != "agent1" {
		t.Fatalf("expected parent_agent=agent1, got %q", retrieved.ParentAgent)
	}
	if retrieved.Status != "queued" {
		t.Fatalf("expected status=queued, got %q", retrieved.Status)
	}
}

func TestDelegation_GetByID(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()

	d := &persistence.Delegation{
		ID:          "test-delegation-1",
		ParentAgent: "agent1",
		ChildAgent:  "agent2",
		Prompt:      "test prompt",
		Status:      "running",
		CreatedAt:   time.Now(),
	}

	if err := store.CreateDelegation(ctx, d); err != nil {
		t.Fatalf("CreateDelegation: %v", err)
	}

	retrieved, err := store.GetDelegation(ctx, "test-delegation-1")
	if err != nil {
		t.Fatalf("GetDelegation: %v", err)
	}

	if retrieved.ID != "test-delegation-1" {
		t.Fatalf("expected id=test-delegation-1, got %q", retrieved.ID)
	}
	if retrieved.ParentAgent != "agent1" {
		t.Fatalf("expected parent_agent=agent1, got %q", retrieved.ParentAgent)
	}
	if retrieved.ChildAgent != "agent2" {
		t.Fatalf("expected child_agent=agent2, got %q", retrieved.ChildAgent)
	}
}

func TestDelegation_Complete(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()

	d := &persistence.Delegation{
		ID:          "test-complete-1",
		ParentAgent: "agent1",
		ChildAgent:  "agent2",
		Prompt:      "test",
		Status:      "running",
		CreatedAt:   time.Now(),
	}

	if err := store.CreateDelegation(ctx, d); err != nil {
		t.Fatalf("CreateDelegation: %v", err)
	}

	if err := store.CompleteDelegation(ctx, "test-complete-1", "success result"); err != nil {
		t.Fatalf("CompleteDelegation: %v", err)
	}

	retrieved, err := store.GetDelegation(ctx, "test-complete-1")
	if err != nil {
		t.Fatalf("GetDelegation after complete: %v", err)
	}

	if retrieved.Status != "completed" {
		t.Fatalf("expected status=completed, got %q", retrieved.Status)
	}
	if retrieved.Result == nil || *retrieved.Result != "success result" {
		t.Fatalf("expected result=success result, got %v", retrieved.Result)
	}
	if retrieved.CompletedAt == nil {
		t.Fatal("expected CompletedAt to be set")
	}
}

func TestDelegation_Fail(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()

	d := &persistence.Delegation{
		ID:          "test-fail-1",
		ParentAgent: "agent1",
		ChildAgent:  "agent2",
		Prompt:      "test",
		Status:      "running",
		CreatedAt:   time.Now(),
	}

	if err := store.CreateDelegation(ctx, d); err != nil {
		t.Fatalf("CreateDelegation: %v", err)
	}

	if err := store.FailDelegation(ctx, "test-fail-1", "network error"); err != nil {
		t.Fatalf("FailDelegation: %v", err)
	}

	retrieved, err := store.GetDelegation(ctx, "test-fail-1")
	if err != nil {
		t.Fatalf("GetDelegation after fail: %v", err)
	}

	if retrieved.Status != "failed" {
		t.Fatalf("expected status=failed, got %q", retrieved.Status)
	}
	if retrieved.ErrorMsg == nil || *retrieved.ErrorMsg != "network error" {
		t.Fatalf("expected error_msg=network error, got %v", retrieved.ErrorMsg)
	}
	if retrieved.CompletedAt == nil {
		t.Fatal("expected CompletedAt to be set on failure")
	}
}

func TestDelegation_PendingQuery(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()

	// Create multiple delegations in different states
	done := "done"
	errMsg := "error"
	d1 := &persistence.Delegation{
		ID:          "pending-1",
		ParentAgent: "agent1",
		ChildAgent:  "agent2",
		Prompt:      "test1",
		Status:      "completed",
		Result:      &done,
		CreatedAt:   time.Now(),
		Injected:    false,
	}
	d2 := &persistence.Delegation{
		ID:          "pending-2",
		ParentAgent: "agent1",
		ChildAgent:  "agent2",
		Prompt:      "test2",
		Status:      "failed",
		ErrorMsg:    &errMsg,
		CreatedAt:   time.Now().Add(time.Second),
		Injected:    false,
	}
	d3 := &persistence.Delegation{
		ID:          "pending-3",
		ParentAgent: "agent1",
		ChildAgent:  "agent2",
		Prompt:      "test3",
		Status:      "completed",
		Result:      &done,
		CreatedAt:   time.Now().Add(2 * time.Second),
		Injected:    true, // Already injected - should NOT appear
	}
	d4 := &persistence.Delegation{
		ID:          "pending-4",
		ParentAgent: "agent3", // Different agent - should NOT appear
		ChildAgent:  "agent2",
		Prompt:      "test4",
		Status:      "completed",
		Result:      &done,
		CreatedAt:   time.Now().Add(3 * time.Second),
		Injected:    false,
	}

	for _, d := range []*persistence.Delegation{d1, d2, d3, d4} {
		if err := store.CreateDelegation(ctx, d); err != nil {
			t.Fatalf("CreateDelegation %s: %v", d.ID, err)
		}
	}

	// Mark d3 as already injected so it doesn't appear in pending
	if err := store.MarkDelegationInjected(ctx, "pending-3"); err != nil {
		t.Fatalf("MarkDelegationInjected: %v", err)
	}

	pending, err := store.PendingDelegationsForAgent(ctx, "agent1")
	if err != nil {
		t.Fatalf("PendingDelegationsForAgent: %v", err)
	}

	if len(pending) != 2 {
		t.Fatalf("expected 2 pending delegations for agent1, got %d", len(pending))
	}

	// Should be ordered by created_at ASC
	if pending[0].ID != "pending-1" {
		t.Fatalf("expected first pending to be pending-1, got %s", pending[0].ID)
	}
	if pending[1].ID != "pending-2" {
		t.Fatalf("expected second pending to be pending-2, got %s", pending[1].ID)
	}
}

func TestDelegation_MarkInjected(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()

	done := "done"
	d := &persistence.Delegation{
		ID:          "inject-test-1",
		ParentAgent: "agent1",
		ChildAgent:  "agent2",
		Prompt:      "test",
		Status:      "completed",
		Result:      &done,
		CreatedAt:   time.Now(),
		Injected:    false,
	}

	if err := store.CreateDelegation(ctx, d); err != nil {
		t.Fatalf("CreateDelegation: %v", err)
	}

	// Should appear in pending before injection
	pending, err := store.PendingDelegationsForAgent(ctx, "agent1")
	if err != nil {
		t.Fatalf("PendingDelegationsForAgent before: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending before injection, got %d", len(pending))
	}

	// Mark as injected
	if err := store.MarkDelegationInjected(ctx, "inject-test-1"); err != nil {
		t.Fatalf("MarkDelegationInjected: %v", err)
	}

	// Should NOT appear in pending after injection
	pending, err = store.PendingDelegationsForAgent(ctx, "agent1")
	if err != nil {
		t.Fatalf("PendingDelegationsForAgent after: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected 0 pending after injection, got %d", len(pending))
	}
}

func TestDelegation_GetByTaskID(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()

	taskID := "task-xyz"
	d := &persistence.Delegation{
		ID:          "deleg-task-link",
		TaskID:      taskID,
		ParentAgent: "agent1",
		ChildAgent:  "agent2",
		Prompt:      "test",
		Status:      "queued",
		CreatedAt:   time.Now(),
	}

	if err := store.CreateDelegation(ctx, d); err != nil {
		t.Fatalf("CreateDelegation: %v", err)
	}

	// Should find by task ID
	retrieved, err := store.GetDelegationByTaskID(ctx, taskID)
	if err != nil {
		t.Fatalf("GetDelegationByTaskID: %v", err)
	}

	if retrieved.ID != "deleg-task-link" {
		t.Fatalf("expected id=deleg-task-link, got %s", retrieved.ID)
	}
	if retrieved.TaskID != taskID {
		t.Fatalf("expected task_id=%s, got %s", taskID, retrieved.TaskID)
	}
}

func TestSchema_MigrationV13(t *testing.T) {
	store, _ := openTestStore(t)
	db := store.DB()

	// Verify delegations table exists
	var tableName string
	err := db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='delegations'",
	).Scan(&tableName)
	if err == sql.ErrNoRows {
		t.Fatal("delegations table not found")
	}
	if err != nil {
		t.Fatalf("query delegations table: %v", err)
	}

	if tableName != "delegations" {
		t.Fatalf("expected table name 'delegations', got %q", tableName)
	}

	// Verify required columns exist
	requiredCols := []string{
		"id", "task_id", "parent_agent", "child_agent", "prompt",
		"status", "result", "error_msg", "created_at", "completed_at", "injected",
	}

	rows, err := db.Query("PRAGMA table_info(delegations)")
	if err != nil {
		t.Fatalf("query delegations columns: %v", err)
	}
	defer rows.Close()

	found := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name string
		var typeName string
		var notnull int
		var dfltValue interface{}
		var pk int
		if err := rows.Scan(&cid, &name, &typeName, &notnull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan column info: %v", err)
		}
		found[name] = true
	}

	for _, col := range requiredCols {
		if !found[col] {
			t.Fatalf("column %q not found in delegations table", col)
		}
	}

	// Verify indexes exist
	indexes := []string{
		"idx_deleg_parent_pending",
		"idx_deleg_task",
	}

	for _, idx := range indexes {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='index' AND name=?",
			idx,
		).Scan(&name)

		if err == sql.ErrNoRows {
			t.Fatalf("index %q not found", idx)
		}
		if err != nil {
			t.Fatalf("query index %q: %v", idx, err)
		}
	}
}

func TestDelegation_SurvivesCrash(t *testing.T) {
	// Create a store, add a delegation, close it
	dir := t.TempDir()
	dbPath := dir + "/test.db"

	store, err := persistence.Open(dbPath, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	ctx := context.Background()
	d := &persistence.Delegation{
		ID:          "crash-test-1",
		ParentAgent: "agent1",
		ChildAgent:  "agent2",
		Prompt:      "will survive crash",
		Status:      "running",
		CreatedAt:   time.Now(),
	}

	if err := store.CreateDelegation(ctx, d); err != nil {
		t.Fatalf("CreateDelegation: %v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// Reopen store and verify delegation is still there
	store2, err := persistence.Open(dbPath, nil)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer store2.Close()

	retrieved, err := store2.GetDelegation(ctx, "crash-test-1")
	if err != nil {
		t.Fatalf("GetDelegation after crash: %v", err)
	}

	if retrieved.ID != "crash-test-1" {
		t.Fatalf("expected id=crash-test-1, got %s", retrieved.ID)
	}
	if retrieved.Status != "running" {
		t.Fatalf("expected status=running, got %q", retrieved.Status)
	}
}
