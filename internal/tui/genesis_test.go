package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestGenesis_WriteFilesAndGreeting(t *testing.T) {
	home := t.TempDir()
	result := &GenesisResult{
		Soul: `# Soul — Atlas

## Identity
You are Atlas, a Developer Assistant running on GoClaw.

## Personality
Communicate in a casual & friendly style. Be warm, approachable, conversational.
`,
		Config: "agent_name: Atlas\nworker_count: 4\n",
	}

	if err := WriteGenesisFiles(home, result); err != nil {
		t.Fatalf("write genesis files: %v", err)
	}

	soulPath := filepath.Join(home, "SOUL.md")
	if _, err := os.Stat(soulPath); err != nil {
		t.Fatalf("SOUL.md missing: %v", err)
	}
	cfgPath := filepath.Join(home, "config.yaml")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config.yaml missing: %v", err)
	}
	policyPath := filepath.Join(home, "policy.yaml")
	if _, err := os.Stat(policyPath); err != nil {
		t.Fatalf("policy.yaml missing: %v", err)
	}

	greeting := GreetingFromSoul(result.Soul)
	if !strings.Contains(greeting, "Atlas") {
		t.Fatalf("expected greeting to contain agent name, got %q", greeting)
	}
	if !strings.Contains(strings.ToLower(greeting), "hey") {
		t.Fatalf("expected casual greeting, got %q", greeting)
	}
}

func TestGenesis_WizardStepsAndView(t *testing.T) {
	if len(roleOptions) < 1 || len(personalityOptions) < 1 {
		t.Fatalf("wizard question options missing: role=%d personality=%d", len(roleOptions), len(personalityOptions))
	}

	m := newGenesisModel()
	m.input = m.agentName

	// Step 1: Name
	view := m.View()
	if !strings.Contains(view, "Agent Identity") {
		t.Fatalf("expected Agent Identity in step 1 view, got: %s", view)
	}
	if !strings.Contains(view, "name") {
		t.Fatalf("expected name prompt in step 1 view, got: %s", view)
	}

	// Step 2: Role
	m.step = stepRole
	view = strings.ToLower(m.View())
	if !strings.Contains(view, "role") {
		t.Fatalf("expected role question in wizard view, got: %s", view)
	}
	if !strings.Contains(m.View(), "Write your own...") {
		t.Fatalf("expected 'Write your own...' option in role view")
	}

	// Step 3: Personality
	m.step = stepPersonality
	view = strings.ToLower(m.View())
	if !strings.Contains(view, "personality") {
		t.Fatalf("expected personality question in wizard view, got: %s", view)
	}
	if !strings.Contains(m.View(), "Write your own...") {
		t.Fatalf("expected 'Write your own...' option in personality view")
	}
}

func TestGenesis_NameToEmojiToRole(t *testing.T) {
	m := newGenesisModel()
	m.input = "Atlas"

	// Enter name → advance to role (skipped emoji step)
	m, _ = enterStep(m)
	if m.step != stepRole {
		t.Fatalf("expected stepRole, got %d", m.step)
	}
	if m.agentName != "Atlas" {
		t.Fatalf("expected agentName=Atlas, got %q", m.agentName)
	}
}

func TestGenesis_BackNavigationPreservesSelections(t *testing.T) {
	m := newGenesisModel()
	m.input = "Atlas"

	// Name → Role (emoji step removed)
	m, _ = enterStep(m)

	// Select second role option, advance to Personality
	m.cursor = 1
	m, _ = enterStep(m)
	if m.step != stepPersonality {
		t.Fatalf("expected stepPersonality, got %d", m.step)
	}
	if m.role != roleOptions[1].value {
		t.Fatalf("expected role %q, got %q", roleOptions[1].value, m.role)
	}

	// Go back to Role — cursor should restore to 1
	m, _ = escStep(m)
	if m.step != stepRole {
		t.Fatalf("expected stepRole after back, got %d", m.step)
	}
	if m.cursor != 1 {
		t.Fatalf("expected cursor=1 (restored), got %d", m.cursor)
	}

	// Back to Name
	m, _ = escStep(m)
	if m.step != stepName {
		t.Fatalf("expected stepName after back, got %d", m.step)
	}
	if m.input != "Atlas" {
		t.Fatalf("expected name input restored to Atlas, got %q", m.input)
	}

	// Back from Name does nothing (first step)
	m, _ = escStep(m)
	if m.step != stepName {
		t.Fatalf("expected to stay on stepName, got %d", m.step)
	}
}

func TestGenesis_CustomRoleInput(t *testing.T) {
	m := newGenesisModel()
	m.input = "Bot"
	m, _ = enterStep(m) // → role (emoji step removed)

	// Navigate to "Write your own..."
	m.cursor = len(roleOptions)
	m, _ = enterStep(m) // Enter custom mode

	if !m.customMode {
		t.Fatal("expected customMode=true after selecting Write your own...")
	}
	if m.step != stepRole {
		t.Fatal("expected to stay on stepRole in custom mode")
	}

	// Type custom role
	for _, ch := range "Data Scientist" {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = updated.(genesisModel)
	}
	if m.customText != "Data Scientist" {
		t.Fatalf("expected customText='Data Scientist', got %q", m.customText)
	}

	// Confirm custom role
	m, _ = enterStep(m)
	if m.customMode {
		t.Fatal("expected customMode=false after confirming")
	}
	if m.role != "Data Scientist" {
		t.Fatalf("expected role='Data Scientist', got %q", m.role)
	}
	if m.step != stepPersonality {
		t.Fatalf("expected stepPersonality after custom role, got %d", m.step)
	}
}

func TestGenesis_CustomModeEscCancels(t *testing.T) {
	m := newGenesisModel()
	m.input = "Bot"
	m, _ = enterStep(m) // → role (emoji step removed)

	m.cursor = len(roleOptions)
	m, _ = enterStep(m) // Enter custom mode

	// Type something then Esc
	for _, ch := range "test" {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = updated.(genesisModel)
	}

	m, _ = escStep(m)
	if m.customMode {
		t.Fatal("expected customMode=false after Esc")
	}
	if m.step != stepRole {
		t.Fatalf("expected to stay on stepRole after Esc, got %d", m.step)
	}
}

func TestGenesis_CustomEmptyTextIgnored(t *testing.T) {
	m := newGenesisModel()
	m.step = stepRole
	m.customMode = true
	m.customText = ""

	// Pressing Enter with empty text should not advance
	m, _ = enterStep(m)
	if m.step != stepRole {
		t.Fatalf("expected to stay on stepRole with empty custom text, got %d", m.step)
	}
}

func TestGenesis_PasteAPIKey(t *testing.T) {
	m := newGenesisModel()
	m.step = stepAPIKey

	pastedKey := "AIzaSyBvdjVycgzbHDOdGLOqqLkKg9AH9MwlW9E"
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(pastedKey)})
	m = updated.(genesisModel)

	if m.input != pastedKey {
		t.Fatalf("expected pasted key %q, got %q", pastedKey, m.input)
	}
}

func TestSanitizeAPIKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"AIzaSyBvdjVycgzbHDOdGLOqqLkKg9AH9MwlW9E", "AIzaSyBvdjVycgzbHDOdGLOqqLkKg9AH9MwlW9E"},
		{"[AIzaSyBvdjVycgzbHDOdGLOqqLkKg9AH9MwlW9E]", "AIzaSyBvdjVycgzbHDOdGLOqqLkKg9AH9MwlW9E"},
		{`"AIzaSyBvdjVycgzbHDOdGLOqqLkKg9AH9MwlW9E"`, "AIzaSyBvdjVycgzbHDOdGLOqqLkKg9AH9MwlW9E"},
		{"  AIzaSyBvdjVycgzbHDOdGLOqqLkKg9AH9MwlW9E  ", "AIzaSyBvdjVycgzbHDOdGLOqqLkKg9AH9MwlW9E"},
		{"GEMINI_API_KEY=AIzaSyBvdjVycgzbHDOdGLOqqLkKg9AH9MwlW9E", "AIzaSyBvdjVycgzbHDOdGLOqqLkKg9AH9MwlW9E"},
		{"", ""},
	}
	for _, tt := range tests {
		got := sanitizeAPIKey(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeAPIKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGenesis_SoulGeneration(t *testing.T) {
	m := genesisModel{
		agentName:   "Atlas",
		role:        "Developer Assistant",
		personality: personalityOptions[0].value, // Professional & Precise
	}

	soul := m.generateSoul()
	if !strings.Contains(soul, "# Soul — Atlas") {
		t.Fatalf("soul missing header, got: %s", soul)
	}
	if !strings.Contains(soul, "You are Atlas") {
		t.Fatalf("soul missing identity, got: %s", soul)
	}
	if !strings.Contains(soul, "Developer Assistant") {
		t.Fatalf("soul missing role, got: %s", soul)
	}
	if !strings.Contains(soul, "professional & precise") {
		t.Fatalf("soul missing personality, got: %s", soul)
	}
}

func TestGenesis_ConfigGeneration(t *testing.T) {
	m := genesisModel{
		agentName: "Atlas",
		provider:  "anthropic",
		modelID:   "claude-sonnet-4-5-20250929",
		apiKey:    "testkey123",
	}

	cfg := m.generateConfig()
	if !strings.Contains(cfg, "agent_name: Atlas") {
		t.Fatalf("config missing agent_name, got: %s", cfg)
	}
	if !strings.Contains(cfg, "llm_provider: anthropic") {
		t.Fatalf("config missing provider, got: %s", cfg)
	}
	if !strings.Contains(cfg, "gemini_model: claude-sonnet-4-5-20250929") {
		t.Fatalf("config missing model, got: %s", cfg)
	}
	if !strings.Contains(cfg, "testkey123") {
		t.Fatalf("config missing api key, got: %s", cfg)
	}
	if !strings.Contains(cfg, "Setup Wizard") {
		t.Fatalf("config missing wizard comment, got: %s", cfg)
	}
}

func TestGenesis_FullWizardFlow(t *testing.T) {
	m := newGenesisModel()
	m.input = "Atlas"

	// Step 1: Name → Role (emoji step removed)
	m, _ = enterStep(m) // → role
	if m.step != stepRole {
		t.Fatalf("expected stepRole, got %d", m.step)
	}

	// Step 2: Role (select first)
	m.cursor = 0
	m, _ = enterStep(m) // → personality
	if m.step != stepPersonality {
		t.Fatalf("expected stepPersonality, got %d", m.step)
	}

	// Step 3: Personality (select first)
	m.cursor = 0
	m, _ = enterStep(m) // → provider
	if m.step != stepProvider {
		t.Fatalf("expected stepProvider, got %d", m.step)
	}

	// Step 4a: Provider (select Google)
	m.cursor = 0
	m, _ = enterStep(m) // → model
	if m.step != stepModel {
		t.Fatalf("expected stepModel, got %d", m.step)
	}
	if m.provider != "google" {
		t.Fatalf("expected provider=google, got %q", m.provider)
	}

	// Step 4b: Model (select first)
	m.cursor = 0
	m, _ = enterStep(m) // → api key
	if m.step != stepAPIKey {
		t.Fatalf("expected stepAPIKey, got %d", m.step)
	}
	if m.modelID == "" {
		t.Fatal("expected non-empty modelID")
	}

	// Step 5: API Key (skip)
	m, _ = enterStep(m) // → review
	if m.step != stepReview {
		t.Fatalf("expected stepReview, got %d", m.step)
	}

	// Step 6: Review
	view := m.View()
	if !strings.Contains(view, "Atlas") {
		t.Fatalf("review missing agent name")
	}
	if !strings.Contains(view, "Developer Assistant") {
		t.Fatalf("review missing role")
	}
	if !strings.Contains(view, "Google Gemini") {
		t.Fatalf("review missing provider")
	}

	// Confirm
	m, cmd := enterStep(m)
	if !m.done {
		t.Fatal("expected done=true after review confirm")
	}
	if m.result == nil {
		t.Fatal("expected non-nil result")
	}
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd")
	}
}

func TestGenesis_DisplayStep(t *testing.T) {
	tests := []struct {
		step genesisStep
		want int
	}{
		{stepName, 1},
		{stepRole, 2},
		{stepPersonality, 3},
		{stepProvider, 4},
		{stepModel, 4},
		{stepAPIKey, 5},
		{stepReview, 6},
	}
	for _, tt := range tests {
		got := displayStep(tt.step)
		if got != tt.want {
			t.Errorf("displayStep(%d) = %d, want %d", tt.step, got, tt.want)
		}
	}
}

func TestGenesis_ParseNameEmoji(t *testing.T) {
	tests := []struct {
		soul     string
		wantName string
	}{
		{"# Soul — Atlas\n\n## Identity\n", "Atlas"},
		{"# Soul — GoClaw\n", "GoClaw"},
		{"# Soul — My Agent Bot\n", "My Agent Bot"},
		{"# Soul\n## Identity\n", ""},
	}
	for _, tt := range tests {
		name, emoji := parseNameEmoji(tt.soul)
		if name != tt.wantName || emoji != "" {
			t.Errorf("parseNameEmoji(%q) = (%q, %q), want (%q, %q)", tt.soul, name, emoji, tt.wantName, "")
		}
	}
}

func TestGenesis_GreetingPersonalized(t *testing.T) {
	soul := `# Soul — Atlas

## Identity
You are Atlas, a Developer Assistant running on GoClaw.

## Personality
Communicate in a professional & precise style.
`
	greeting := GreetingFromSoul(soul)
	if !strings.Contains(greeting, "Atlas") {
		t.Fatalf("greeting missing name, got: %q", greeting)
	}
	if !strings.Contains(greeting, "online") {
		t.Fatalf("greeting missing 'online', got: %q", greeting)
	}
}

func TestGenesis_DefaultNameAndEmoji(t *testing.T) {
	m := newGenesisModel()
	if m.agentName != "GoClaw" {
		t.Fatalf("expected default name GoClaw, got %q", m.agentName)
	}

	// Empty name should default to GoClaw
	m.input = ""
	m, _ = enterStep(m)
	if m.agentName != "GoClaw" {
		t.Fatalf("expected GoClaw for empty name, got %q", m.agentName)
	}
}

func TestGenesis_MaskAPIKey(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"AIzaSyBvdjVycgzbHDOdGLOqqLkKg9AH9MwlW9E", "AIza...W9E"},
		{"short", "*****"},
		{"12345678", "********"},
		{"123456789", "1234...789"},
	}
	for _, tt := range tests {
		got := maskAPIKey(tt.key)
		if got != tt.want {
			t.Errorf("maskAPIKey(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestGenesis_DeleteWordAt(t *testing.T) {
	tests := []struct {
		input   string
		pos     int
		want    string
		wantPos int
	}{
		{"hello world", 11, "hello ", 6},
		{"hello ", 6, "", 0},
		{"hello", 5, "", 0},
		{"", 0, "", 0},
		{"one two three", 13, "one two ", 8},
		{"abc def", 4, "def", 0},                // cursor after space, deletes "abc "
		{"abc  def", 5, "def", 0},               // cursor after spaces, deletes "abc  "
		{"mid cursor here", 10, "mid  here", 4}, // delete "cursor" from middle, space before remains
	}
	for _, tt := range tests {
		got, gotPos := deleteWordAt(tt.input, tt.pos)
		if got != tt.want || gotPos != tt.wantPos {
			t.Errorf("deleteWordAt(%q, %d) = (%q, %d), want (%q, %d)", tt.input, tt.pos, got, gotPos, tt.want, tt.wantPos)
		}
	}
}

func TestGenesis_AltBackspaceInTextInput(t *testing.T) {
	m := newGenesisModel()
	m.input = "My Agent Name"
	m.inputPos = runeLen(m.input)

	result, _ := m.handleTextInputKey("alt+backspace")
	m = result.(genesisModel)

	if m.input != "My Agent " {
		t.Fatalf("expected 'My Agent ' after alt+backspace, got %q", m.input)
	}
	if m.inputPos != 9 {
		t.Fatalf("expected inputPos=9, got %d", m.inputPos)
	}

	// Second alt+backspace
	result, _ = m.handleTextInputKey("alt+backspace")
	m = result.(genesisModel)
	if m.input != "My " {
		t.Fatalf("expected 'My ' after second alt+backspace, got %q", m.input)
	}
}

func TestGenesis_LeftRightNavigation(t *testing.T) {
	m := newGenesisModel()
	m.input = "hello"
	m.inputPos = 5

	// Left moves cursor back
	result, _ := m.handleTextInputKey("left")
	m = result.(genesisModel)
	if m.inputPos != 4 {
		t.Fatalf("expected inputPos=4 after left, got %d", m.inputPos)
	}

	// Right moves cursor forward
	result, _ = m.handleTextInputKey("right")
	m = result.(genesisModel)
	if m.inputPos != 5 {
		t.Fatalf("expected inputPos=5 after right, got %d", m.inputPos)
	}

	// Right at end stays put
	result, _ = m.handleTextInputKey("right")
	m = result.(genesisModel)
	if m.inputPos != 5 {
		t.Fatalf("expected inputPos=5 at end, got %d", m.inputPos)
	}

	// Left to beginning
	for i := 0; i < 5; i++ {
		result, _ = m.handleTextInputKey("left")
		m = result.(genesisModel)
	}
	if m.inputPos != 0 {
		t.Fatalf("expected inputPos=0, got %d", m.inputPos)
	}

	// Left at start stays put
	result, _ = m.handleTextInputKey("left")
	m = result.(genesisModel)
	if m.inputPos != 0 {
		t.Fatalf("expected inputPos=0 at start, got %d", m.inputPos)
	}

	// Type at position 0 inserts at beginning
	result, _ = m.handleTextInputKey("X")
	m = result.(genesisModel)
	if m.input != "Xhello" || m.inputPos != 1 {
		t.Fatalf("expected 'Xhello' pos=1, got %q pos=%d", m.input, m.inputPos)
	}

	// Backspace at position 1 deletes the X
	result, _ = m.handleTextInputKey("backspace")
	m = result.(genesisModel)
	if m.input != "hello" || m.inputPos != 0 {
		t.Fatalf("expected 'hello' pos=0, got %q pos=%d", m.input, m.inputPos)
	}
}

func TestGenesis_RenderCursor(t *testing.T) {
	tests := []struct {
		s    string
		pos  int
		want string
	}{
		{"hello", 5, "hello█"},
		{"hello", 0, "█hello"},
		{"hello", 2, "he█llo"},
		{"", 0, "█"},
	}
	for _, tt := range tests {
		got := renderCursor(tt.s, tt.pos)
		if got != tt.want {
			t.Errorf("renderCursor(%q, %d) = %q, want %q", tt.s, tt.pos, got, tt.want)
		}
	}
}

func enterStep(m genesisModel) (genesisModel, tea.Cmd) {
	result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return result.(genesisModel), cmd
}

func escStep(m genesisModel) (genesisModel, tea.Cmd) {
	result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	return result.(genesisModel), cmd
}
