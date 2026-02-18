package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/shared"
	"github.com/firebase/genkit/go/ai"
)

// Brain Delegation Injection Tests (Phase 2)

func openBrainTestStore(t *testing.T) *persistence.Store {
	t.Helper()
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func registerBrainTestAgent(t *testing.T, store *persistence.Store, agentID string) {
	t.Helper()
	if err := store.CreateAgent(context.Background(), persistence.AgentRecord{
		AgentID:     agentID,
		DisplayName: agentID,
		Status:      "active",
	}); err != nil {
		t.Fatalf("create agent %q: %v", agentID, err)
	}
}

// injectPendingDelegations returns system messages for pending delegations.
func TestBrain_InjectPendingDelegations(t *testing.T) {
	store := openBrainTestStore(t)
	ctx := shared.WithAgentID(context.Background(), "agent-delegator")
	registerBrainTestAgent(t, store, "agent-delegator")
	registerBrainTestAgent(t, store, "agent-worker")

	// Create a delegation
	d := &persistence.Delegation{
		ID:          "deleg-1",
		ParentAgent: "agent-delegator",
		ChildAgent:  "agent-worker",
		Prompt:      "process this",
		CreatedAt:   time.Now(),
	}
	if err := store.CreateDelegation(ctx, d); err != nil {
		t.Fatalf("CreateDelegation: %v", err)
	}

	// Complete the delegation with a result
	if err := store.CompleteDelegation(ctx, "deleg-1", "processed data"); err != nil {
		t.Fatalf("CompleteDelegation: %v", err)
	}

	// Create a brain with the store
	brain := &GenkitBrain{store: store}

	// Inject pending delegations
	messages, err := brain.injectPendingDelegations(ctx)
	if err != nil {
		t.Fatalf("injectPendingDelegations: %v", err)
	}

	// Should have 1 message
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}

	// Message should be a system message with the result
	if messages[0].Role != ai.RoleSystem {
		t.Fatalf("expected role=system, got %q", messages[0].Role)
	}
	if len(messages[0].Content) == 0 {
		t.Fatal("expected message content")
	}
	text := messages[0].Content[0].Text
	if !contains(text, "processed data") {
		t.Fatalf("expected message to contain result, got: %s", text)
	}
}

// Delegation with error is injected as error message.
func TestBrain_DelegationInjection_Failed(t *testing.T) {
	store := openBrainTestStore(t)
	ctx := shared.WithAgentID(context.Background(), "agent-delegator")
	registerBrainTestAgent(t, store, "agent-delegator")
	registerBrainTestAgent(t, store, "agent-worker")

	// Create a delegation
	d := &persistence.Delegation{
		ID:          "deleg-failed-1",
		ParentAgent: "agent-delegator",
		ChildAgent:  "agent-worker",
		Prompt:      "process this",
		CreatedAt:   time.Now(),
	}
	if err := store.CreateDelegation(ctx, d); err != nil {
		t.Fatalf("CreateDelegation: %v", err)
	}

	// Fail the delegation with an error message
	if err := store.FailDelegation(ctx, "deleg-failed-1", "something went wrong"); err != nil {
		t.Fatalf("FailDelegation: %v", err)
	}

	brain := &GenkitBrain{store: store}
	messages, err := brain.injectPendingDelegations(ctx)
	if err != nil {
		t.Fatalf("injectPendingDelegations: %v", err)
	}

	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}

	// Error message should mention the error
	if len(messages[0].Content) == 0 {
		t.Fatal("expected message content")
	}
	text := messages[0].Content[0].Text
	if !contains(text, "something went wrong") {
		t.Fatalf("expected message to contain error, got: %s", text)
	}
}

// Delegations are marked as injected after injection.
func TestBrain_DelegationInjection_MarksInjected(t *testing.T) {
	store := openBrainTestStore(t)
	ctx := shared.WithAgentID(context.Background(), "agent-delegator")
	registerBrainTestAgent(t, store, "agent-delegator")
	registerBrainTestAgent(t, store, "agent-worker")

	// Create a completed delegation
	d := &persistence.Delegation{
		ID:          "deleg-mark-1",
		ParentAgent: "agent-delegator",
		ChildAgent:  "agent-worker",
		Prompt:      "process this",
		Status:      "completed",
		Result:      strPtr("done"),
		CreatedAt:   time.Now(),
	}
	if err := store.CreateDelegation(ctx, d); err != nil {
		t.Fatalf("CreateDelegation: %v", err)
	}

	brain := &GenkitBrain{store: store}
	_, err := brain.injectPendingDelegations(ctx)
	if err != nil {
		t.Fatalf("injectPendingDelegations: %v", err)
	}

	// Verify delegation is marked as injected
	retrieved, err := store.GetDelegation(ctx, "deleg-mark-1")
	if err != nil {
		t.Fatalf("GetDelegation: %v", err)
	}

	if !retrieved.Injected {
		t.Fatal("expected delegation to be marked as Injected")
	}
}

// Empty pending list returns no messages.
func TestBrain_DelegationInjection_Empty(t *testing.T) {
	store := openBrainTestStore(t)
	ctx := shared.WithAgentID(context.Background(), "agent-receiver")
	registerBrainTestAgent(t, store, "agent-receiver")

	brain := &GenkitBrain{store: store}
	messages, err := brain.injectPendingDelegations(ctx)
	if err != nil {
		t.Fatalf("injectPendingDelegations: %v", err)
	}

	if len(messages) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(messages))
	}
}

// Helper functions
func strPtr(s string) *string {
	return &s
}

func TestBrain_RegisterMCPTools(t *testing.T) {
	t.Skip("RegisterMCPTools registers allowed tools per-agent in Phase 1")
}

func TestBrain_MCPToolDiscovery(t *testing.T) {
	t.Skip("auto-discovery calls tools/list in Phase 1")
}
