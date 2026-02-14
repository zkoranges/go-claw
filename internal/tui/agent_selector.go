package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// agentSelectorModel is a single-step Bubbletea model for selecting an agent.
type agentSelectorModel struct {
	agents  []AgentInfo
	cursor  int
	current string // current agent ID (marked with *)
	done    bool
	quit    bool

	selectedID string
}

func newAgentSelector(agents []AgentInfo, currentID string) agentSelectorModel {
	return agentSelectorModel{
		agents:  agents,
		current: currentID,
	}
}

func (m agentSelectorModel) Init() tea.Cmd {
	return nil
}

func (m agentSelectorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.quit = true
			return m, nil
		case "enter", "ctrl+m", "ctrl+j":
			if len(m.agents) > 0 {
				m.selectedID = m.agents[m.cursor].ID
				m.done = true
			}
			return m, nil
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.agents)-1 {
				m.cursor++
			}
		}
	}
	return m, nil
}

func (m agentSelectorModel) View() string {
	if m.quit || m.done {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n  Select an agent:\n\n")

	for i, info := range m.agents {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		marker := " "
		if info.ID == m.current {
			marker = "*"
		}

		label := info.ID
		if info.DisplayName != "" && info.DisplayName != info.ID {
			label = fmt.Sprintf("%s (%s)", info.ID, info.DisplayName)
		}
		if info.Emoji != "" {
			label = fmt.Sprintf("%s %s", info.Emoji, label)
		}

		model := info.Model
		if model == "" {
			model = "default"
		}

		b.WriteString(fmt.Sprintf("  %s%s %-30s %s\n", cursor, marker, label, model))
	}

	b.WriteString("\n  [Up/Down] Navigate  [Enter] Select  [Esc] Cancel\n")
	return b.String()
}
