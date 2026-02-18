package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type ModalState int

const (
	ModalClosed ModalState = iota
	ModalOpen
)

const modalFieldCount = 5 // ID, Model, Soul, Checkbox, Button

type AgentModal struct {
	state        ModalState
	focusIndex   int
	idField      string
	soulField    string
	modelOptions []string
	modelIndex   int
	saveToConfig bool
	err          string
}

func NewAgentModal(modelOptions []string) AgentModal {
	if len(modelOptions) == 0 {
		modelOptions = []string{"default"}
	}
	return AgentModal{state: ModalClosed, modelOptions: modelOptions, saveToConfig: true}
}

func (m *AgentModal) Open() {
	m.state = ModalOpen
	m.focusIndex = 0
	m.idField = ""
	m.soulField = ""
	m.modelIndex = 0
	m.saveToConfig = true
	m.err = ""
}

func (m *AgentModal) Close()           { m.state = ModalClosed }
func (m AgentModal) IsOpen() bool      { return m.state == ModalOpen }
func (m AgentModal) FocusIndex() int   { return m.focusIndex }
func (m AgentModal) IDField() string   { return m.idField }
func (m AgentModal) SoulField() string { return m.soulField }
func (m AgentModal) Err() string       { return m.err }

type AgentCreatedMsg struct {
	ID, Model, Soul string
	SaveToConfig    bool
}

type ModalCancelledMsg struct{}

func (m *AgentModal) Update(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.Close()
		return func() tea.Msg { return ModalCancelledMsg{} }
	case "tab", "down":
		m.focusIndex = (m.focusIndex + 1) % modalFieldCount
		return nil
	case "shift+tab", "up":
		m.focusIndex = (m.focusIndex + modalFieldCount - 1) % modalFieldCount
		return nil
	case "enter":
		switch m.focusIndex {
		case 3:
			m.saveToConfig = !m.saveToConfig
			return nil
		case 4:
			return m.submit()
		default:
			m.focusIndex = (m.focusIndex + 1) % modalFieldCount
			return nil
		}
	case "left", "right":
		if m.focusIndex == 1 {
			if msg.String() == "left" {
				m.modelIndex = (m.modelIndex - 1 + len(m.modelOptions)) % len(m.modelOptions)
			} else {
				m.modelIndex = (m.modelIndex + 1) % len(m.modelOptions)
			}
			return nil
		}
	case "backspace":
		switch m.focusIndex {
		case 0:
			if len(m.idField) > 0 {
				m.idField = m.idField[:len(m.idField)-1]
			}
		case 2:
			if len(m.soulField) > 0 {
				m.soulField = m.soulField[:len(m.soulField)-1]
			}
		}
		return nil
	default:
		switch m.focusIndex {
		case 0:
			for _, r := range msg.String() {
				if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
					m.idField += string(r)
				}
			}
		case 2:
			m.soulField += msg.String()
		}
	}
	return nil
}

func (m *AgentModal) submit() tea.Cmd {
	id := strings.TrimSpace(m.idField)
	if id == "" {
		m.err = "Agent ID is required"
		return nil
	}
	model := m.modelOptions[m.modelIndex]
	soul := strings.TrimSpace(m.soulField)
	m.Close()
	return func() tea.Msg {
		return AgentCreatedMsg{ID: id, Model: model, Soul: soul, SaveToConfig: m.saveToConfig}
	}
}

func (m AgentModal) View() string {
	if !m.IsOpen() {
		return ""
	}

	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).Padding(1, 2).Width(54)
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("62"))
	focus := lipgloss.NewStyle().Foreground(lipgloss.Color("86"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	errS := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

	mk := func(idx int) string {
		if m.focusIndex == idx {
			return focus.Render("▸ ")
		}
		return "  "
	}

	var b strings.Builder
	b.WriteString(title.Render("Create New Agent") + "\n\n")
	b.WriteString(mk(0) + "ID:    [ " + m.idField + " ]\n")
	b.WriteString(mk(1) + "Model: [ ◀ " + m.modelOptions[m.modelIndex] + " ▶ ]\n")
	soulPreview := m.soulField
	if len(soulPreview) > 35 {
		soulPreview = soulPreview[:35] + "..."
	}
	b.WriteString(mk(2) + "Soul:  [ " + soulPreview + " ]\n\n")
	check := "[ ]"
	if m.saveToConfig {
		check = "[x]"
	}
	b.WriteString(mk(3) + check + " Save to config.yaml\n\n")
	btn := "[ Create ]"
	if m.focusIndex == 4 {
		btn = focus.Render("[ Create ]")
	}
	b.WriteString("  " + btn + dim.Render("  (Esc to cancel)") + "\n")
	if m.err != "" {
		b.WriteString("\n" + errS.Render("  ⚠ "+m.err))
	}
	return border.Render(b.String())
}
