package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewAgentSelector(t *testing.T) {
	agents := []AgentInfo{
		{ID: "default", DisplayName: "Default"},
		{ID: "coder", DisplayName: "Coder"},
	}
	as := newAgentSelector(agents, "default")

	if len(as.agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(as.agents))
	}
	if as.current != "default" {
		t.Errorf("current = %q, want default", as.current)
	}
	if as.cursor != 0 {
		t.Error("expected cursor at 0")
	}
}

func TestAgentSelector_Init(t *testing.T) {
	as := newAgentSelector(nil, "")
	cmd := as.Init()
	if cmd != nil {
		t.Error("Init() should return nil")
	}
}

func TestAgentSelector_NavigateDown(t *testing.T) {
	agents := []AgentInfo{
		{ID: "default"}, {ID: "coder"}, {ID: "writer"},
	}
	as := newAgentSelector(agents, "default")

	m, _ := as.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated := m.(agentSelectorModel)
	if updated.cursor != 1 {
		t.Errorf("cursor after down = %d, want 1", updated.cursor)
	}

	m, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = m.(agentSelectorModel)
	if updated.cursor != 2 {
		t.Errorf("cursor after second down = %d, want 2", updated.cursor)
	}

	// Can't go past last
	m, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = m.(agentSelectorModel)
	if updated.cursor != 2 {
		t.Errorf("cursor should stay at 2, got %d", updated.cursor)
	}
}

func TestAgentSelector_NavigateUp(t *testing.T) {
	agents := []AgentInfo{
		{ID: "default"}, {ID: "coder"},
	}
	as := newAgentSelector(agents, "default")

	// Can't go above 0
	m, _ := as.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated := m.(agentSelectorModel)
	if updated.cursor != 0 {
		t.Errorf("cursor should stay at 0, got %d", updated.cursor)
	}
}

func TestAgentSelector_SelectEnter(t *testing.T) {
	agents := []AgentInfo{
		{ID: "default"}, {ID: "coder"},
	}
	as := newAgentSelector(agents, "default")

	// Move to coder
	m, _ := as.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := m.(agentSelectorModel)

	if !updated.done {
		t.Error("expected done=true after enter")
	}
	if updated.selectedID != "coder" {
		t.Errorf("selectedID = %q, want coder", updated.selectedID)
	}
}

func TestAgentSelector_EscQuits(t *testing.T) {
	agents := []AgentInfo{{ID: "default"}}
	as := newAgentSelector(agents, "default")

	m, _ := as.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := m.(agentSelectorModel)

	if !updated.quit {
		t.Error("expected quit=true after esc")
	}
	if updated.done {
		t.Error("expected done=false after esc (cancel)")
	}
}

func TestAgentSelector_CtrlCQuits(t *testing.T) {
	agents := []AgentInfo{{ID: "default"}}
	as := newAgentSelector(agents, "default")

	m, _ := as.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	updated := m.(agentSelectorModel)

	if !updated.quit {
		t.Error("expected quit=true after ctrl+c")
	}
}

func TestAgentSelector_EnterEmptyList(t *testing.T) {
	as := newAgentSelector(nil, "")

	m, _ := as.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := m.(agentSelectorModel)

	if updated.done {
		t.Error("should not be done on empty list")
	}
	if updated.selectedID != "" {
		t.Error("should not select anything on empty list")
	}
}

func TestAgentSelector_ViewShowsAgents(t *testing.T) {
	agents := []AgentInfo{
		{ID: "default", DisplayName: "Default", Model: "gemini-2.5-flash"},
		{ID: "coder", DisplayName: "Coder", Emoji: "💻", Model: "gemini-2.5-pro"},
	}
	as := newAgentSelector(agents, "default")

	view := as.View()
	if !strings.Contains(view, "Select an agent") {
		t.Error("expected 'Select an agent' header")
	}
	if !strings.Contains(view, "default") {
		t.Error("expected 'default' in view")
	}
	if !strings.Contains(view, "coder") {
		t.Error("expected 'coder' in view")
	}
	if !strings.Contains(view, "*") {
		t.Error("expected current marker '*' in view")
	}
}

func TestAgentSelector_ViewEmptyOnQuit(t *testing.T) {
	as := agentSelectorModel{quit: true}
	if as.View() != "" {
		t.Error("expected empty view on quit")
	}
}

func TestAgentSelector_ViewEmptyOnDone(t *testing.T) {
	as := agentSelectorModel{done: true}
	if as.View() != "" {
		t.Error("expected empty view on done")
	}
}

func TestAgentSelector_ViewDefaultName(t *testing.T) {
	agents := []AgentInfo{
		{ID: "agent1", DisplayName: ""},                  // empty name -> "-"
		{ID: "agent2", DisplayName: "agent2"},            // same as ID -> "-"
		{ID: "agent3", DisplayName: "Agent Three"},       // custom name
		{ID: "agent4", DisplayName: "", Model: ""},       // empty model -> "default"
		{ID: "agent5", DisplayName: "Five", Emoji: "✨"}, // with emoji
	}
	as := newAgentSelector(agents, "")

	view := as.View()
	if !strings.Contains(view, "Agent Three") {
		t.Error("expected 'Agent Three' in view")
	}
}

func TestAgentSelector_KKeyDown(t *testing.T) {
	agents := []AgentInfo{{ID: "a"}, {ID: "b"}}
	as := newAgentSelector(agents, "a")
	as.cursor = 1

	m, _ := as.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	updated := m.(agentSelectorModel)
	if updated.cursor != 0 {
		t.Errorf("cursor after 'k' = %d, want 0", updated.cursor)
	}
}

func TestAgentSelector_JKeyDown(t *testing.T) {
	agents := []AgentInfo{{ID: "a"}, {ID: "b"}}
	as := newAgentSelector(agents, "a")

	m, _ := as.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	updated := m.(agentSelectorModel)
	if updated.cursor != 1 {
		t.Errorf("cursor after 'j' = %d, want 1", updated.cursor)
	}
}
