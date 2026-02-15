package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/shared"
)

func openTestStore(t *testing.T) *persistence.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := persistence.Open(filepath.Join(dir, "test.db"), nil)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestCheckDedup_NoStore(t *testing.T) {
	ctx := context.Background()
	deduped, err := checkDedup(ctx, nil, "exec", ShellInput{Command: "echo hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deduped {
		t.Fatal("should not be deduped when store is nil")
	}
}

func TestCheckDedup_NoTaskID(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background() // no task ID in context
	deduped, err := checkDedup(ctx, store, "exec", ShellInput{Command: "echo hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deduped {
		t.Fatal("should not be deduped when no task ID in context")
	}
}

func TestCheckDedup_FirstCallNotDeduped(t *testing.T) {
	store := openTestStore(t)
	ctx := shared.WithTaskID(context.Background(), "task-001")
	deduped, err := checkDedup(ctx, store, "exec", ShellInput{Command: "echo hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deduped {
		t.Fatal("first call should not be deduped")
	}
}

func TestRegisterAndCheckDedup(t *testing.T) {
	store := openTestStore(t)
	ctx := shared.WithTaskID(context.Background(), "task-002")
	input := ShellInput{Command: "echo hi"}

	// First call: not deduped.
	deduped, err := checkDedup(ctx, store, "exec", input)
	if err != nil {
		t.Fatalf("check 1: %v", err)
	}
	if deduped {
		t.Fatal("first check should not be deduped")
	}

	// Register success.
	registerDedup(ctx, store, "exec", input)

	// Second call: should be deduped.
	deduped, err = checkDedup(ctx, store, "exec", input)
	if err != nil {
		t.Fatalf("check 2: %v", err)
	}
	if !deduped {
		t.Fatal("second check should be deduped after register")
	}
}

func TestDedup_DifferentInputNotDeduped(t *testing.T) {
	store := openTestStore(t)
	ctx := shared.WithTaskID(context.Background(), "task-003")

	// Register "echo hi".
	registerDedup(ctx, store, "exec", ShellInput{Command: "echo hi"})

	// Check "echo bye" — different input, should NOT be deduped.
	deduped, err := checkDedup(ctx, store, "exec", ShellInput{Command: "echo bye"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deduped {
		t.Fatal("different input should not be deduped")
	}
}

func TestDedup_DifferentTaskNotDeduped(t *testing.T) {
	store := openTestStore(t)
	input := ShellInput{Command: "echo hi"}

	// Register for task-A.
	ctxA := shared.WithTaskID(context.Background(), "task-A")
	registerDedup(ctxA, store, "exec", input)

	// Check for task-B — different task, should NOT be deduped.
	ctxB := shared.WithTaskID(context.Background(), "task-B")
	deduped, err := checkDedup(ctxB, store, "exec", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deduped {
		t.Fatal("different task should not be deduped")
	}
}

func TestBuildIdempotencyKey_Stable(t *testing.T) {
	input := ShellInput{Command: "echo hi", WorkingDir: "/tmp"}
	key1, hash1 := buildIdempotencyKey("task-1", "exec", input)
	key2, hash2 := buildIdempotencyKey("task-1", "exec", input)
	if key1 != key2 {
		t.Fatalf("keys should be stable: %q != %q", key1, key2)
	}
	if hash1 != hash2 {
		t.Fatalf("hashes should be stable: %q != %q", hash1, hash2)
	}
}

func TestDedup_WriteFileEndToEnd(t *testing.T) {
	store := openTestStore(t)
	ctx := shared.WithTaskID(context.Background(), "task-wf-001")
	input := WriteFileInput{Path: "/tmp/test-dedup.txt", Content: "hello"}

	// First call: not deduped.
	deduped, err := checkDedup(ctx, store, "write_file", input)
	if err != nil {
		t.Fatalf("check 1: %v", err)
	}
	if deduped {
		t.Fatal("first write should not be deduped")
	}

	// Simulate success and register.
	registerDedup(ctx, store, "write_file", input)

	// Retry: should be deduped.
	deduped, err = checkDedup(ctx, store, "write_file", input)
	if err != nil {
		t.Fatalf("check 2: %v", err)
	}
	if !deduped {
		t.Fatal("retry write should be deduped")
	}

	// Cleanup test file if it was created.
	_ = os.Remove("/tmp/test-dedup.txt")
}

func TestDedup_SendMessageEndToEnd(t *testing.T) {
	store := openTestStore(t)
	ctx := shared.WithTaskID(context.Background(), "task-msg-001")
	input := SendMessageInput{ToAgent: "agentB", Content: "hello"}

	deduped, _ := checkDedup(ctx, store, "send_message", input)
	if deduped {
		t.Fatal("first send should not be deduped")
	}

	registerDedup(ctx, store, "send_message", input)

	deduped, _ = checkDedup(ctx, store, "send_message", input)
	if !deduped {
		t.Fatal("retry send should be deduped")
	}
}
