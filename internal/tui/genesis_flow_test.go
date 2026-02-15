package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/basket/go-claw/internal/config"
)

// TestGenesisFlow_FullWizardEndToEnd exercises every step of the genesis wizard
// from welcome through completion, verifying the final result contains expected
// provider, model, and generated config/soul content.
func TestGenesisFlow_FullWizardEndToEnd(t *testing.T) {
	m := newGenesisModel()
	m.input = "Orion"
	m.inputPos = runeLen("Orion")

	// Step 1: Name -> Role (emoji step removed)
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	assertStep(t, m, stepRole, "expected stepRole after name")

	if m.agentName != "Orion" {
		t.Fatalf("expected agentName=Orion, got %q", m.agentName)
	}

	// Step 2: Role -> Personality (select "Research Analyst", index 1)
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyDown}) // cursor 0 -> 1
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	assertStep(t, m, stepPersonality, "expected stepPersonality after role")

	if m.role != roleOptions[1].value {
		t.Fatalf("expected role=%q, got %q", roleOptions[1].value, m.role)
	}

	// Step 3: Personality -> Provider (select "Technical & Concise", index 2)
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyDown}) // 0 -> 1
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyDown}) // 1 -> 2
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	assertStep(t, m, stepProvider, "expected stepProvider after personality")

	if m.personality != personalityOptions[2].value {
		t.Fatalf("expected personality=%q, got %q", personalityOptions[2].value, m.personality)
	}

	// Step 4a: Provider -> Model (select Anthropic, index 1)
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyDown}) // 0 -> 1
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	assertStep(t, m, stepModel, "expected stepModel after provider")

	if m.provider != "anthropic" {
		t.Fatalf("expected provider=anthropic, got %q", m.provider)
	}
	if len(m.models) == 0 {
		t.Fatal("expected non-empty models list for anthropic")
	}

	// Step 4b: Model -> API Key (select first model)
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	assertStep(t, m, stepAPIKey, "expected stepAPIKey after model")

	expectedModelID := config.BuiltinModels["anthropic"][0].ID
	if m.modelID != expectedModelID {
		t.Fatalf("expected modelID=%q, got %q", expectedModelID, m.modelID)
	}

	// Step 5: API Key -> Review (type a key then press enter)
	typeString(t, &m, "sk-test-key-12345")
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	assertStep(t, m, stepReview, "expected stepReview after API key")

	if m.apiKey != "sk-test-key-12345" {
		t.Fatalf("expected apiKey=sk-test-key-12345, got %q", m.apiKey)
	}

	// Verify the review view shows all our selections.
	view := m.View()
	for _, expect := range []string{"Orion", "Research Analyst", "Anthropic", expectedModelID} {
		if !strings.Contains(view, expect) {
			t.Fatalf("review view missing %q\nview=%s", expect, view)
		}
	}

	// Step 6: Confirm -> done
	result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = result.(genesisModel)
	if !m.done {
		t.Fatal("expected done=true after review confirm")
	}
	if m.result == nil {
		t.Fatal("expected non-nil result")
	}
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd")
	}

	// Verify generated soul content.
	if !strings.Contains(m.result.Soul, "Orion") {
		t.Fatalf("soul missing agent name, got: %s", m.result.Soul)
	}
	if !strings.Contains(m.result.Soul, "Research Analyst") {
		t.Fatalf("soul missing role, got: %s", m.result.Soul)
	}

	// Verify generated config content.
	if !strings.Contains(m.result.Config, "llm_provider: anthropic") {
		t.Fatalf("config missing provider, got: %s", m.result.Config)
	}
	if !strings.Contains(m.result.Config, "gemini_model: "+expectedModelID) {
		t.Fatalf("config missing model, got: %s", m.result.Config)
	}
	if !strings.Contains(m.result.Config, "sk-test-key-12345") {
		t.Fatalf("config missing api key, got: %s", m.result.Config)
	}
}

// TestGenesisFlow_BackNavigation verifies that pressing Esc navigates backward
// through wizard steps and properly restores previous state at each step.
func TestGenesisFlow_BackNavigation(t *testing.T) {
	m := newGenesisModel()
	m.input = "Bolt"
	m.inputPos = runeLen("Bolt")

	// Name -> Role (emoji step removed)
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	assertStep(t, m, stepRole, "after name enter")

	// Select role (index 0) -> Personality
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	assertStep(t, m, stepPersonality, "after role enter")

	// Back: Personality -> Role
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEscape})
	assertStep(t, m, stepRole, "after first back")

	// Verify cursor is restored to the previously selected role (index 0).
	if m.cursor != 0 {
		t.Fatalf("expected cursor=0 on role after back, got %d", m.cursor)
	}

	// Back: Role -> Name
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEscape})
	assertStep(t, m, stepName, "after second back")

	// Verify name input is restored.
	if m.input != "Bolt" {
		t.Fatalf("expected name input restored to Bolt, got %q", m.input)
	}

	// Back from Name should stay on Name (cannot go back further).
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEscape})
	assertStep(t, m, stepName, "back from name should stay on name")

	// Verify we can re-advance after backing up.
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // -> Role
	assertStep(t, m, stepRole, "re-advance to role")
	if m.agentName != "Bolt" {
		t.Fatalf("expected agentName=Bolt after re-advance, got %q", m.agentName)
	}
}

// TestGenesisFlow_CustomRoleInput verifies the custom role entry flow where
// the user selects "Write your own..." and types a free-text role description.
func TestGenesisFlow_CustomRoleInput(t *testing.T) {
	m := newGenesisModel()
	m.input = "Nova"
	m.inputPos = runeLen("Nova")

	// Name -> Role (emoji step removed)
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // -> role

	assertStep(t, m, stepRole, "should be at role step")

	// Navigate to "Write your own..." (last option, after all roleOptions).
	for i := 0; i < len(roleOptions); i++ {
		m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.cursor != len(roleOptions) {
		t.Fatalf("expected cursor=%d (Write your own), got %d", len(roleOptions), m.cursor)
	}

	// Press Enter to activate custom mode.
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.customMode {
		t.Fatal("expected customMode=true after selecting 'Write your own...'")
	}
	if m.step != stepRole {
		t.Fatalf("expected to stay on stepRole in custom mode, got %d", m.step)
	}

	// Type a custom role.
	customRole := "AI Safety Researcher"
	typeString(t, &m, customRole)

	if m.customText != customRole {
		t.Fatalf("expected customText=%q, got %q", customRole, m.customText)
	}

	// Confirm custom role.
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.customMode {
		t.Fatal("expected customMode=false after confirming custom role")
	}
	if m.role != customRole {
		t.Fatalf("expected role=%q, got %q", customRole, m.role)
	}
	assertStep(t, m, stepPersonality, "should advance to personality after custom role")

	// Verify the custom role also works with the personality custom entry.
	for i := 0; i < len(personalityOptions); i++ {
		m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // Enter custom mode
	if !m.customMode {
		t.Fatal("expected personality customMode=true")
	}

	customPersonality := "Deeply analytical and methodical"
	typeString(t, &m, customPersonality)
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.personality != customPersonality {
		t.Fatalf("expected personality=%q, got %q", customPersonality, m.personality)
	}
	assertStep(t, m, stepProvider, "should advance to provider after custom personality")
}

// TestGenesisFlow_CustomModeEscRetainsStep verifies that pressing Esc in custom
// mode cancels the custom input without advancing or going back a step.
func TestGenesisFlow_CustomModeEscRetainsStep(t *testing.T) {
	m := newGenesisModel()
	m.step = stepRole
	m.cursor = len(roleOptions) // "Write your own..."

	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // Enter custom mode
	if !m.customMode {
		t.Fatal("expected customMode=true")
	}

	typeString(t, &m, "partial input")

	// Esc should cancel custom mode but stay on role step.
	m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyEscape})
	if m.customMode {
		t.Fatal("expected customMode=false after Esc")
	}
	assertStep(t, m, stepRole, "should stay on role step after Esc from custom")
	if m.customText != "" {
		t.Fatalf("expected customText cleared after Esc, got %q", m.customText)
	}
}

// TestGenesisFlow_CtrlCQuits verifies that Ctrl+C sets quitting=true and
// returns a tea.Quit command from any step.
func TestGenesisFlow_CtrlCQuits(t *testing.T) {
	steps := []genesisStep{stepName, stepRole, stepProvider, stepReview}
	for _, step := range steps {
		m := newGenesisModel()
		m.step = step
		if step == stepProvider || step == stepModel {
			m.models = modelsForProvider("google")
		}

		result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		updated := result.(genesisModel)
		if !updated.quitting {
			t.Fatalf("expected quitting=true at step %d", step)
		}
		if cmd == nil {
			t.Fatalf("expected tea.Quit cmd at step %d", step)
		}
	}
}

// TestGenesisFlow_UpDownNavigation verifies cursor movement with up/down keys
// in selection steps, including clamping at boundaries.
func TestGenesisFlow_UpDownNavigation(t *testing.T) {
	m := newGenesisModel()
	m.step = stepRole
	m.cursor = 0

	// Down to the last option (roleOptions + "Write your own...")
	maxCursor := len(roleOptions) // "Write your own..." adds one more
	for i := 0; i < maxCursor+5; i++ {
		m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.cursor != maxCursor {
		t.Fatalf("expected cursor clamped to %d, got %d", maxCursor, m.cursor)
	}

	// Up back to 0.
	for i := 0; i < maxCursor+5; i++ {
		m = mustUpdate(t, m, tea.KeyMsg{Type: tea.KeyUp})
	}
	if m.cursor != 0 {
		t.Fatalf("expected cursor=0 after many ups, got %d", m.cursor)
	}
}

// --- test helpers ---

func mustUpdate(t *testing.T, m genesisModel, msg tea.Msg) genesisModel {
	t.Helper()
	result, _ := m.Update(msg)
	gm, ok := result.(genesisModel)
	if !ok {
		t.Fatalf("Update returned non-genesisModel: %T", result)
	}
	return gm
}

func assertStep(t *testing.T, m genesisModel, want genesisStep, context string) {
	t.Helper()
	if m.step != want {
		t.Fatalf("%s: expected step %d, got %d", context, want, m.step)
	}
}

func typeString(t *testing.T, m *genesisModel, s string) {
	t.Helper()
	for _, ch := range s {
		result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		*m = result.(genesisModel)
	}
}
