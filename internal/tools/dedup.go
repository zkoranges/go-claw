package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/shared"
)

// GC-SPEC-QUE-006: Side-effecting tool calls MUST include a stable
// idempotency key and MUST be deduplicated against prior successful side effects.
// GC-SPEC-REL-002: At-most-once side effects per idempotency key when tool reports success.

// checkDedup returns true if a prior successful call with the same tool+input
// already exists for this task. When true, the caller should skip execution
// and return a safe default. Returns false (proceed) when there is no task
// context, no store, or no prior record.
func checkDedup(ctx context.Context, store *persistence.Store, toolName string, input any) (bool, error) {
	if store == nil {
		return false, nil
	}
	taskID := shared.TaskID(ctx)
	if taskID == "" {
		return false, nil
	}
	key, reqHash := buildIdempotencyKey(taskID, toolName, input)
	return store.CheckToolCallDedup(ctx, key, reqHash)
}

// registerDedup records a successful side-effecting tool call. Call this
// AFTER the side effect succeeds. No-op if there is no task context or no store.
func registerDedup(ctx context.Context, store *persistence.Store, toolName string, input any) {
	if store == nil {
		return
	}
	taskID := shared.TaskID(ctx)
	if taskID == "" {
		return
	}
	key, reqHash := buildIdempotencyKey(taskID, toolName, input)
	_, _ = store.RegisterSuccessfulToolCall(ctx, key, toolName, reqHash, "")
}

// buildIdempotencyKey creates a stable key from task ID, tool name, and input hash.
func buildIdempotencyKey(taskID, toolName string, input any) (key, reqHash string) {
	inputBytes, _ := json.Marshal(input)
	h := sha256.Sum256(inputBytes)
	reqHash = fmt.Sprintf("%x", h[:16])
	key = fmt.Sprintf("%s:%s:%s", taskID, toolName, reqHash)
	return key, reqHash
}
