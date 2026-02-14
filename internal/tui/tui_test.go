package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestView_DisplaysDLQAndRetryMetrics(t *testing.T) {
	// GC-SPEC-TUI-002: TUI MUST display retry pressure, DLQ count.
	m := model{
		snap: Snapshot{
			DBOK:             true,
			Workers:          4,
			QueueDepth:       5,
			Active:           2,
			RetryWait:        3,
			DeadLetter:       1,
			LeaseExpiries:    0,
			PendingApprovals: 2,
			ApprovalAlert:    "action required",
			LastError:        "",
			LastEvent:        "test",
			Uptime:           10 * time.Second,
		},
	}
	view := m.View()

	for _, want := range []string{
		"Retry Wait: 3",
		"Dead Letter: 1",
		"Lease Expiries: 0",
		"Queue Depth: 5",
		"Active Tasks: 2",
		"Pending Approvals: 2",
		"action required",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("expected view to contain %q, got:\n%s", want, view)
		}
	}
}

func TestTUI_HeadlessNonTTY(t *testing.T) {
	// GC-SPEC-TUI-004: TUI MUST function in non-TTY headless mode.
	// Verify model init, update, and view work without a real terminal.
	provider := func() Snapshot {
		return Snapshot{
			DBOK:       true,
			Workers:    2,
			QueueDepth: 0,
			Active:     0,
			Uptime:     5 * time.Second,
		}
	}

	m := model{provider: provider, snap: provider()}

	// Init should return a tick command without panicking.
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected Init to return a cmd")
	}

	// Simulated key press "q" should signal quit.
	updated, quitCmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if updated == nil {
		t.Fatal("expected non-nil model after Update")
	}
	if quitCmd == nil {
		t.Fatal("expected quit command on 'q' key")
	}

	// Tick msg should update snapshot.
	m2 := model{provider: provider, snap: Snapshot{}}
	updated2, tickCmd := m2.Update(tickMsg(time.Now()))
	if tickCmd == nil {
		t.Fatal("expected tick cmd after tick message")
	}
	updatedModel := updated2.(model)
	if !updatedModel.snap.DBOK {
		t.Fatal("expected snapshot to be refreshed from provider")
	}

	// View should produce non-empty output.
	view := m.View()
	if view == "" {
		t.Fatal("expected non-empty view output in headless mode")
	}

	// Run with context cancellation should exit cleanly.
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately
	err := Run(cancelCtx, provider)
	if err != nil && err != context.Canceled {
		t.Fatalf("expected clean exit or context.Canceled, got: %v", err)
	}
}
