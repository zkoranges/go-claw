package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func specialKey(k string) tea.KeyMsg {
	// Map special key names to Bubbletea key types
	switch k {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
}

func keyMsg(k string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

func TestAgentModal_OpenClose(t *testing.T) {
	m := NewAgentModal([]string{"a", "b"})
	if m.IsOpen() {
		t.Fatal("should start closed")
	}
	m.Open()
	if !m.IsOpen() {
		t.Fatal("should be open")
	}
	m.Close()
	if m.IsOpen() {
		t.Fatal("should be closed")
	}
}

func TestAgentModal_OpenResets(t *testing.T) {
	m := NewAgentModal([]string{"a"})
	m.Open()
	m.idField = "old"
	m.soulField = "old"
	m.focusIndex = 3
	m.Open()
	if m.idField != "" || m.soulField != "" || m.focusIndex != 0 {
		t.Fatal("Open() should reset state")
	}
}

func TestAgentModal_EscCancels(t *testing.T) {
	m := NewAgentModal([]string{"a"})
	m.Open()
	cmd := m.Update(specialKey("esc"))
	if m.IsOpen() {
		t.Fatal("Esc should close")
	}
	if cmd == nil {
		t.Fatal("should return ModalCancelledMsg cmd")
	}
	if _, ok := cmd().(ModalCancelledMsg); !ok {
		t.Fatal("cmd should produce ModalCancelledMsg")
	}
}

func TestAgentModal_TabCycles(t *testing.T) {
	m := NewAgentModal([]string{"a"})
	m.Open()
	for i := 0; i < modalFieldCount; i++ {
		if m.FocusIndex() != i {
			t.Fatalf("step %d: expected focus %d, got %d", i, i, m.FocusIndex())
		}
		m.Update(specialKey("tab"))
	}
	if m.FocusIndex() != 0 {
		t.Fatal("should wrap to 0")
	}
}

func TestAgentModal_TypeID(t *testing.T) {
	m := NewAgentModal([]string{"a"})
	m.Open()
	m.Update(keyMsg("m"))
	m.Update(keyMsg("y"))
	m.Update(keyMsg("-"))
	m.Update(keyMsg("a"))
	if m.IDField() != "my-a" {
		t.Fatalf("got %q", m.IDField())
	}
}

func TestAgentModal_IDRejectsInvalid(t *testing.T) {
	m := NewAgentModal([]string{"a"})
	m.Open()
	m.Update(keyMsg("a"))
	m.Update(keyMsg("!"))
	m.Update(keyMsg("B")) // uppercase rejected
	m.Update(keyMsg("c"))
	if m.IDField() != "ac" {
		t.Fatalf("got %q", m.IDField())
	}
}

func TestAgentModal_SubmitEmptyID(t *testing.T) {
	m := NewAgentModal([]string{"a"})
	m.Open()
	for i := 0; i < 4; i++ {
		m.Update(specialKey("tab"))
	}
	cmd := m.Update(specialKey("enter"))
	if cmd != nil {
		t.Fatal("empty ID should not produce cmd")
	}
	if m.Err() == "" {
		t.Fatal("should set error")
	}
	if !m.IsOpen() {
		t.Fatal("should stay open on error")
	}
}

func TestAgentModal_SubmitValid(t *testing.T) {
	m := NewAgentModal([]string{"model-a", "model-b"})
	m.Open()
	m.Update(keyMsg("t"))
	m.Update(keyMsg("e"))
	m.Update(keyMsg("s"))
	m.Update(keyMsg("t"))
	for i := 0; i < 4; i++ {
		m.Update(specialKey("tab"))
	}
	cmd := m.Update(specialKey("enter"))
	if cmd == nil {
		t.Fatal("valid submit should produce cmd")
	}
	if m.IsOpen() {
		t.Fatal("should close on valid submit")
	}
	msg, ok := cmd().(AgentCreatedMsg)
	if !ok {
		t.Fatal("should produce AgentCreatedMsg")
	}
	if msg.ID != "test" {
		t.Fatalf("ID: got %q", msg.ID)
	}
	if msg.Model != "model-a" {
		t.Fatalf("Model: got %q", msg.Model)
	}
	if !msg.SaveToConfig {
		t.Fatal("SaveToConfig should default true")
	}
}

func TestAgentModal_NilModelOptions(t *testing.T) {
	m := NewAgentModal(nil)
	if len(m.modelOptions) != 1 || m.modelOptions[0] != "default" {
		t.Fatal("nil options should default to ['default']")
	}
}

func TestAgentModal_ViewClosed(t *testing.T) {
	m := NewAgentModal([]string{"a"})
	if m.View() != "" {
		t.Fatal("closed modal should render empty")
	}
}

func TestAgentModal_Backspace(t *testing.T) {
	m := NewAgentModal([]string{"model"})
	m.Open()
	m.idField = "test"
	m.Update(specialKey("backspace"))
	if m.IDField() != "tes" {
		t.Fatalf("backspace failed: got %q", m.IDField())
	}
}

func TestAgentModal_CheckboxToggle(t *testing.T) {
	m := NewAgentModal([]string{"model"})
	m.Open()
	for i := 0; i < 3; i++ { // Tab to checkbox (field 3)
		m.Update(specialKey("tab"))
	}
	m.Update(specialKey("enter"))
	if m.saveToConfig { // Should have toggled
		t.Fatal("checkbox toggle failed")
	}
}

func TestAgentModal_ModelRotation(t *testing.T) {
	m := NewAgentModal([]string{"model-a", "model-b", "model-c"})
	m.Open()
	m.Update(specialKey("tab")) // Move to model field (field 1)

	// Test right arrow
	m.Update(specialKey("right"))
	if m.modelIndex != 1 {
		t.Fatalf("right arrow: expected 1, got %d", m.modelIndex)
	}

	// Test left arrow
	m.Update(specialKey("left"))
	if m.modelIndex != 0 {
		t.Fatalf("left arrow: expected 0, got %d", m.modelIndex)
	}

	// Test wraparound
	m.Update(specialKey("left"))
	if m.modelIndex != 2 {
		t.Fatalf("left wraparound: expected 2, got %d", m.modelIndex)
	}
}

func TestAgentModal_ModelCount(t *testing.T) {
	tests := []struct {
		name    string
		options []string
		count   int
	}{
		{"empty", []string{}, 1},
		{"single", []string{"a"}, 1},
		{"multiple", []string{"a", "b", "c"}, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewAgentModal(tt.options)
			if len(m.modelOptions) != tt.count {
				t.Fatalf("expected %d, got %d", tt.count, len(m.modelOptions))
			}
		})
	}
}

func TestAgentModal_SoulField(t *testing.T) {
	m := NewAgentModal([]string{"model"})
	m.Open()
	// Tab to soul field (field 2)
	m.Update(specialKey("tab"))
	m.Update(specialKey("tab"))

	soul := "You are helpful"
	for _, r := range soul {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	if m.SoulField() != soul {
		t.Fatalf("expected %q, got %q", soul, m.SoulField())
	}
}

func TestAgentModal_SaveToConfigDefault(t *testing.T) {
	m := NewAgentModal([]string{"model"})
	if !m.saveToConfig {
		t.Fatal("saveToConfig should default to true")
	}
}

func BenchmarkAgentModal_Update(b *testing.B) {
	m := NewAgentModal([]string{"model-a", "model-b"})
	m.Open()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Update(keyMsg("a"))
	}
}

func TestAgentModal_IDLength(t *testing.T) {
	m := NewAgentModal([]string{"model"})
	m.Open()

	// Type a long ID
	longID := "my-very-long-agent-id-with-many-characters"
	for _, r := range longID {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	if m.IDField() != longID {
		t.Fatalf("long ID failed: expected %q, got %q", longID, m.IDField())
	}
}

func TestAgentModal_EnterOnOtherFields(t *testing.T) {
	m := NewAgentModal([]string{"model"})
	m.Open()

	// Tab to soul field (field 2)
	m.Update(specialKey("tab"))
	m.Update(specialKey("tab"))

	// Enter on non-submit field should tab to next
	initial := m.FocusIndex()
	m.Update(specialKey("enter"))
	if m.FocusIndex() == initial {
		t.Fatal("enter on non-button should tab to next field")
	}
}
