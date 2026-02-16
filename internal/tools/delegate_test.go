package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/shared"
)

// delegateTestPolicy implements policy.Checker for delegate tests.
type delegateTestPolicy struct {
	allowCap map[string]bool
}

func (p delegateTestPolicy) AllowHTTPURL(string) bool { return true }
func (p delegateTestPolicy) AllowCapability(cap string) bool {
	if p.allowCap == nil {
		return false
	}
	return p.allowCap[cap]
}
func (p delegateTestPolicy) AllowPath(string) bool { return true }
func (p delegateTestPolicy) PolicyVersion() string { return "delegate-test-v1" }

const delegateTestSession = "00000000-0000-4000-8000-000000000002"

func openDelegateTestStore(t *testing.T) *persistence.Store {
	t.Helper()
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.EnsureSession(context.Background(), delegateTestSession); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	return store
}

func registerTestAgent(t *testing.T, store *persistence.Store, agentID string) {
	t.Helper()
	if err := store.CreateAgent(context.Background(), persistence.AgentRecord{
		AgentID:     agentID,
		DisplayName: agentID,
		Status:      "active",
	}); err != nil {
		t.Fatalf("create agent %q: %v", agentID, err)
	}
}

// GC-SPEC-SEC-006: Policy deny is audited for delegate_task.
func TestDelegateTask_PolicyDeny(t *testing.T) {
	store := openDelegateTestStore(t)
	pol := delegateTestPolicy{allowCap: map[string]bool{}} // no capabilities

	_, err := delegateTask(context.Background(), &DelegateTaskInput{
		TargetAgent: "agent-b",
		Prompt:      "hello",
		SessionID:   delegateTestSession,
	}, store, pol, 2)

	if err == nil || !strings.Contains(err.Error(), "policy denied") {
		t.Fatalf("expected policy denial, got err=%v", err)
	}
}

// GC-SPEC-SEC-006: Policy deny with nil policy.
func TestDelegateTask_NilPolicyDeny(t *testing.T) {
	store := openDelegateTestStore(t)

	_, err := delegateTask(context.Background(), &DelegateTaskInput{
		TargetAgent: "agent-b",
		Prompt:      "hello",
		SessionID:   delegateTestSession,
	}, store, nil, 3)

	if err == nil || !strings.Contains(err.Error(), "policy denied") {
		t.Fatalf("expected policy denial with nil policy, got err=%v", err)
	}
}

// Self-delegation must be prevented (would deadlock).
func TestDelegateTask_SelfDelegationBlocked(t *testing.T) {
	store := openDelegateTestStore(t)
	pol := delegateTestPolicy{allowCap: map[string]bool{capDelegateTask: true}}
	registerTestAgent(t, store, "agent-a")

	ctx := shared.WithAgentID(context.Background(), "agent-a")
	_, err := delegateTask(ctx, &DelegateTaskInput{
		TargetAgent: "agent-a",
		Prompt:      "think harder",
		SessionID:   delegateTestSession,
	}, store, pol, 2)

	if err == nil || !strings.Contains(err.Error(), "cannot delegate to yourself") {
		t.Fatalf("expected self-delegation rejection, got err=%v", err)
	}
}

// Empty target/prompt/session must be rejected.
func TestDelegateTask_InputValidation(t *testing.T) {
	store := openDelegateTestStore(t)
	pol := delegateTestPolicy{allowCap: map[string]bool{capDelegateTask: true}}

	tests := []struct {
		name  string
		input DelegateTaskInput
		want  string
	}{
		{"empty target and capability", DelegateTaskInput{Prompt: "hi", SessionID: delegateTestSession}, "either target_agent or capability must be provided"},
		{"empty prompt", DelegateTaskInput{TargetAgent: "b", SessionID: delegateTestSession}, "prompt must be non-empty"},
		{"empty session", DelegateTaskInput{TargetAgent: "b", Prompt: "hi"}, "session_id must be non-empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := delegateTask(context.Background(), &tt.input, store, pol, 3)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q, got err=%v", tt.want, err)
			}
		})
	}
}

// Non-existent target agent must be rejected.
func TestDelegateTask_TargetNotFound(t *testing.T) {
	store := openDelegateTestStore(t)
	pol := delegateTestPolicy{allowCap: map[string]bool{capDelegateTask: true}}

	_, err := delegateTask(context.Background(), &DelegateTaskInput{
		TargetAgent: "nonexistent",
		Prompt:      "hello",
		SessionID:   delegateTestSession,
	}, store, pol, 2)

	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected target not found error, got err=%v", err)
	}
}

// Timeout clamping: timeout > max is capped.
func TestDelegateTask_TimeoutClamped(t *testing.T) {
	store := openDelegateTestStore(t)
	pol := delegateTestPolicy{allowCap: map[string]bool{capDelegateTask: true}}
	registerTestAgent(t, store, "agent-b")

	// Use a very short context so it cancels quickly, but set a huge timeout
	// to verify clamping doesn't cause unexpected behavior.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	out, err := delegateTask(ctx, &DelegateTaskInput{
		TargetAgent: "agent-b",
		Prompt:      "hello",
		SessionID:   delegateTestSession,
		TimeoutSec:  9999, // should be clamped to 300s
	}, store, pol, 2)

	// Task will be created but context will cancel quickly.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != "CANCELED" {
		t.Fatalf("expected CANCELED, got %q", out.Status)
	}
}

// Context cancellation aborts the child task.
func TestDelegateTask_ContextCancelAbortsChild(t *testing.T) {
	store := openDelegateTestStore(t)
	pol := delegateTestPolicy{allowCap: map[string]bool{capDelegateTask: true}}
	registerTestAgent(t, store, "agent-b")

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a brief delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	out, err := delegateTask(ctx, &DelegateTaskInput{
		TargetAgent: "agent-b",
		Prompt:      "hello",
		SessionID:   delegateTestSession,
	}, store, pol, 2)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != "CANCELED" {
		t.Fatalf("expected CANCELED, got %q", out.Status)
	}
	if out.TaskID == "" {
		t.Fatal("expected non-empty TaskID")
	}
}

// === delegateTaskAsync tests (PDR v7 Phase 2) ===

// Async delegation returns immediately with delegation ID.
func TestDelegateTaskAsync_ReturnsImmediately(t *testing.T) {
	store := openDelegateTestStore(t)
	pol := delegateTestPolicy{allowCap: map[string]bool{capDelegateTaskAsync: true}}
	registerTestAgent(t, store, "agent-b")

	start := time.Now()
	out, err := delegateTaskAsync(context.Background(), &AsyncDelegateTaskInput{
		TargetAgent: "agent-b",
		Prompt:      "hello",
		SessionID:   delegateTestSession,
	}, store, pol, 2)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.DelegationID == "" {
		t.Fatal("expected non-empty DelegationID")
	}
	if out.Status != "queued" {
		t.Fatalf("expected status=queued, got %q", out.Status)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected immediate return, took %v", elapsed)
	}
}

// Async delegation creates delegation record in store.
func TestDelegateTaskAsync_StoresDelegation(t *testing.T) {
	store := openDelegateTestStore(t)
	pol := delegateTestPolicy{allowCap: map[string]bool{capDelegateTaskAsync: true}}
	registerTestAgent(t, store, "agent-b")
	ctx := shared.WithAgentID(context.Background(), "agent-a")
	registerTestAgent(t, store, "agent-a")

	out, err := delegateTaskAsync(ctx, &AsyncDelegateTaskInput{
		TargetAgent: "agent-b",
		Prompt:      "process data",
		SessionID:   delegateTestSession,
	}, store, pol, 2)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify delegation was stored
	deleg, err := store.GetDelegation(context.Background(), out.DelegationID)
	if err != nil {
		t.Fatalf("GetDelegation: %v", err)
	}

	if deleg.ParentAgent != "agent-a" {
		t.Fatalf("expected parent_agent=agent-a, got %q", deleg.ParentAgent)
	}
	if deleg.ChildAgent != "agent-b" {
		t.Fatalf("expected child_agent=agent-b, got %q", deleg.ChildAgent)
	}
	if deleg.Status != "queued" {
		t.Fatalf("expected status=queued, got %q", deleg.Status)
	}
	if deleg.Prompt != "process data" {
		t.Fatalf("expected prompt=process data, got %q", deleg.Prompt)
	}
}

// Async delegation creates task in store.
func TestDelegateTaskAsync_CreatesTask(t *testing.T) {
	store := openDelegateTestStore(t)
	pol := delegateTestPolicy{allowCap: map[string]bool{capDelegateTaskAsync: true}}
	registerTestAgent(t, store, "agent-b")

	out, err := delegateTaskAsync(context.Background(), &AsyncDelegateTaskInput{
		TargetAgent: "agent-b",
		Prompt:      "hello",
		SessionID:   delegateTestSession,
	}, store, pol, 2)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify delegation has task_id set
	deleg, err := store.GetDelegation(context.Background(), out.DelegationID)
	if err != nil {
		t.Fatalf("GetDelegation: %v", err)
	}

	if deleg.TaskID == "" {
		t.Fatal("expected delegation to have task_id")
	}

	// Verify task exists and is queued for agent-b
	task, err := store.GetTask(context.Background(), deleg.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	if task.AgentID != "agent-b" {
		t.Fatalf("expected task AgentID=agent-b, got %q", task.AgentID)
	}
	if task.Status != "QUEUED" {
		t.Fatalf("expected task Status=QUEUED, got %q", task.Status)
	}
}

// Async delegation policy validation.
func TestDelegateTaskAsync_PolicyDeny(t *testing.T) {
	store := openDelegateTestStore(t)
	pol := delegateTestPolicy{allowCap: map[string]bool{}} // no capability
	registerTestAgent(t, store, "agent-b")

	_, err := delegateTaskAsync(context.Background(), &AsyncDelegateTaskInput{
		TargetAgent: "agent-b",
		Prompt:      "hello",
		SessionID:   delegateTestSession,
	}, store, pol, 2)

	if err == nil || !strings.Contains(err.Error(), "policy denied") {
		t.Fatalf("expected policy denial, got err=%v", err)
	}
}

// Async delegation rejects self-delegation.
func TestDelegateTaskAsync_SelfDelegationBlocked(t *testing.T) {
	store := openDelegateTestStore(t)
	pol := delegateTestPolicy{allowCap: map[string]bool{capDelegateTaskAsync: true}}
	registerTestAgent(t, store, "agent-a")

	ctx := shared.WithAgentID(context.Background(), "agent-a")
	_, err := delegateTaskAsync(ctx, &AsyncDelegateTaskInput{
		TargetAgent: "agent-a",
		Prompt:      "hello",
		SessionID:   delegateTestSession,
	}, store, pol, 2)

	if err == nil || !strings.Contains(err.Error(), "cannot delegate to yourself") {
		t.Fatalf("expected self-delegation rejection, got err=%v", err)
	}
}

// Async delegation enforces hop limit.
func TestDelegateTaskAsync_HopLimit(t *testing.T) {
	store := openDelegateTestStore(t)
	pol := delegateTestPolicy{allowCap: map[string]bool{capDelegateTaskAsync: true}}
	registerTestAgent(t, store, "agent-b")

	// Set hop count to max (will reject)
	ctx := shared.WithDelegationHop(context.Background(), 2)
	_, err := delegateTaskAsync(ctx, &AsyncDelegateTaskInput{
		TargetAgent: "agent-b",
		Prompt:      "hello",
		SessionID:   delegateTestSession,
	}, store, pol, 2)

	if err == nil || !strings.Contains(err.Error(), "max delegation depth exceeded") {
		t.Fatalf("expected hop limit exceeded, got err=%v", err)
	}
}

// Async delegation input validation.
func TestDelegateTaskAsync_InputValidation(t *testing.T) {
	store := openDelegateTestStore(t)
	pol := delegateTestPolicy{allowCap: map[string]bool{capDelegateTaskAsync: true}}

	tests := []struct {
		name  string
		input AsyncDelegateTaskInput
		want  string
	}{
		{"empty target", AsyncDelegateTaskInput{Prompt: "hi", SessionID: delegateTestSession}, "target_agent must be provided"},
		{"empty prompt", AsyncDelegateTaskInput{TargetAgent: "b", SessionID: delegateTestSession}, "prompt must be non-empty"},
		{"empty session", AsyncDelegateTaskInput{TargetAgent: "b", Prompt: "hi"}, "session_id must be non-empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := delegateTaskAsync(context.Background(), &tt.input, store, pol, 3)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q, got err=%v", tt.want, err)
			}
		})
	}
}

// Async delegation links delegation to task via task_id.
func TestDelegateTaskAsync_DelegationTaskLink(t *testing.T) {
	store := openDelegateTestStore(t)
	pol := delegateTestPolicy{allowCap: map[string]bool{capDelegateTaskAsync: true}}
	registerTestAgent(t, store, "agent-b")

	out, err := delegateTaskAsync(context.Background(), &AsyncDelegateTaskInput{
		TargetAgent: "agent-b",
		Prompt:      "hello",
		SessionID:   delegateTestSession,
	}, store, pol, 2)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Get delegation and verify task_id is set
	deleg, err := store.GetDelegation(context.Background(), out.DelegationID)
	if err != nil {
		t.Fatalf("GetDelegation: %v", err)
	}

	// Verify we can retrieve by task_id
	byTask, err := store.GetDelegationByTaskID(context.Background(), deleg.TaskID)
	if err != nil {
		t.Fatalf("GetDelegationByTaskID: %v", err)
	}

	if byTask.ID != out.DelegationID {
		t.Fatalf("expected delegation ID %s, got %s", out.DelegationID, byTask.ID)
	}
}
