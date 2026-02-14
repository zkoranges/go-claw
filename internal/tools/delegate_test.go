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
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
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
	}, store, pol)

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
	}, store, nil)

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
	}, store, pol)

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
		{"empty target", DelegateTaskInput{Prompt: "hi", SessionID: delegateTestSession}, "target_agent must be non-empty"},
		{"empty prompt", DelegateTaskInput{TargetAgent: "b", SessionID: delegateTestSession}, "prompt must be non-empty"},
		{"empty session", DelegateTaskInput{TargetAgent: "b", Prompt: "hi"}, "session_id must be non-empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := delegateTask(context.Background(), &tt.input, store, pol)
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
	}, store, pol)

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
	}, store, pol)

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
	}, store, pol)

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
