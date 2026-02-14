package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type Snapshot struct {
	DBOK             bool
	Workers          int
	QueueDepth       int
	Active           int32
	RetryWait        int    // GC-SPEC-TUI-002: Tasks in RETRY_WAIT state.
	DeadLetter       int    // GC-SPEC-TUI-002: Tasks in DEAD_LETTER state.
	LeaseExpiries    int    // GC-SPEC-TUI-002: Expired leases (stale claims).
	PendingApprovals int    // GC-SPEC-TUI-003: Approval broker requests.
	ApprovalAlert    string // GC-SPEC-TUI-003: Summary of pending approvals.
	LastError        string
	LastEvent        string
	Uptime           time.Duration
}

type StatusProvider func() Snapshot

type model struct {
	provider StatusProvider
	snap     Snapshot
}

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(1*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Init() tea.Cmd {
	return tickCmd()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	case tickMsg:
		m.snap = m.provider()
		return m, tickCmd()
	}
	return m, nil
}

func (m model) View() string {
	lastErr := m.snap.LastError
	if lastErr == "" {
		lastErr = "(none)"
	}
	lastEvent := m.snap.LastEvent
	if lastEvent == "" {
		lastEvent = "(none)"
	}
	approvalLine := fmt.Sprintf("Pending Approvals: %d", m.snap.PendingApprovals)
	if m.snap.ApprovalAlert != "" {
		approvalLine += " (" + m.snap.ApprovalAlert + ")"
	}
	return fmt.Sprintf(
		"GoClaw Status\n\nDB OK: %t\nWorkers: %d\nQueue Depth: %d\nActive Tasks: %d\nRetry Wait: %d\nDead Letter: %d\nLease Expiries: %d\n%s\nUptime: %s\nLast Error: %s\nLast Event: %s\n\nPress q to quit.\n",
		m.snap.DBOK,
		m.snap.Workers,
		m.snap.QueueDepth,
		m.snap.Active,
		m.snap.RetryWait,
		m.snap.DeadLetter,
		m.snap.LeaseExpiries,
		approvalLine,
		m.snap.Uptime.Truncate(time.Second),
		lastErr,
		lastEvent,
	)
}

func Run(ctx context.Context, provider StatusProvider) error {
	defer bestEffortResetTTY()

	m := model{provider: provider, snap: provider()}
	p := tea.NewProgram(m)

	done := make(chan error, 1)
	go func() {
		_, err := p.Run()
		done <- err
	}()

	select {
	case <-ctx.Done():
		p.Quit()
		return ctx.Err()
	case err := <-done:
		return err
	}
}
