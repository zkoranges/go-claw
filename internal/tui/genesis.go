package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/basket/go-claw/internal/config"
)

// GenesisResult holds the output of the genesis wizard.
type GenesisResult struct {
	Soul   string
	Config string
}

type genesisStep int

const (
	stepName        genesisStep = iota // Name text input
	stepRole                           // Select + custom
	stepPersonality                    // Select + custom (combined tone+soul)
	stepProvider                       // Select LLM provider
	stepBaseURL                        // Text input: server URL (Ollama only)
	stepModel                          // Select model for chosen provider
	stepAPIKey                         // Text input (masked)
	stepReview                         // Summary + confirm
)

const totalDisplaySteps = 6

// displayStep returns the user-facing step number (1-6).
// stepProvider and stepModel share Step 3.
func displayStep(s genesisStep) int {
	switch s {
	case stepName:
		return 1
	case stepRole:
		return 2
	case stepPersonality:
		return 3
	case stepProvider, stepBaseURL, stepModel:
		return 4
	case stepAPIKey:
		return 5
	case stepReview:
		return 6
	default:
		return int(s) + 1
	}
}

func displayStepTitle(s genesisStep) string {
	switch s {
	case stepName:
		return "Agent Identity"
	case stepRole:
		return "Agent Role"
	case stepPersonality:
		return "Personality & Tone"
	case stepProvider, stepBaseURL, stepModel:
		return "LLM Provider"
	case stepAPIKey:
		return "API Key"
	case stepReview:
		return "Review"
	default:
		return ""
	}
}

type genesisModel struct {
	step       genesisStep
	cursor     int
	input      string // Active text input buffer
	inputPos   int    // Rune cursor position within input
	customMode bool   // True when typing custom answer
	customText string // Buffer for custom answer input
	customPos  int    // Rune cursor position within customText

	// Collected data
	agentName   string
	agentEmoji  string
	role        string
	personality string
	provider    string     // LLM provider ID (e.g. "google", "ollama")
	baseURL     string     // Server URL for Ollama (e.g. "http://localhost:11434")
	modelID     string     // Model ID (e.g. "gemini-2.5-flash")
	models      []modelDef // Models for the selected provider
	apiKey      string

	done     bool
	quitting bool
	result   *GenesisResult
}

type roleOption struct {
	emoji string
	label string
	value string
}

var roleOptions = []roleOption{
	{"", "Developer Assistant", "Developer Assistant"},
	{"", "Research Analyst", "Research Analyst"},
	{"", "Writer", "Writer"},
	{"", "General Purpose", "General Purpose assistant"},
}

type personalityOption struct {
	emoji string
	label string
	desc  string
	value string
}

var personalityOptions = []personalityOption{
	{"", "Professional & Precise", "Formal, thorough, detail-oriented", "Professional & Precise — Formal, thorough, detail-oriented"},
	{"", "Casual & Friendly", "Warm, approachable, conversational", "Casual & Friendly — Warm, approachable, conversational"},
	{"", "Technical & Concise", "Terse, efficient, code-focused", "Technical & Concise — Terse, efficient, code-focused"},
	{"", "Creative & Expressive", "Imaginative, playful, exploratory", "Creative & Expressive — Imaginative, playful, exploratory"},
}

func newGenesisModel() genesisModel {
	return genesisModel{
		step:      stepName,
		agentName: "GoClaw",
	}
}

func (m genesisModel) Init() tea.Cmd {
	return nil
}

func (m genesisModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		key := msg.String()

		// Global: quit
		if key == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}

		// Custom mode input handling
		if m.customMode {
			return m.handleCustomModeKey(key)
		}

		// Step-specific key handling
		switch m.step {
		case stepName, stepBaseURL, stepAPIKey:
			return m.handleTextInputKey(key)
		case stepRole, stepPersonality, stepProvider, stepModel:
			return m.handleSelectKey(key)
		case stepReview:
			return m.handleReviewKey(key)
		}
	}
	return m, nil
}

func (m genesisModel) handleTextInputKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter", "ctrl+m", "ctrl+j":
		return m.handleEnter()
	case "esc":
		return m.handleBack()
	case "left":
		if m.inputPos > 0 {
			m.inputPos--
		}
	case "right":
		if m.inputPos < runeLen(m.input) {
			m.inputPos++
		}
	case "home", "ctrl+a":
		m.inputPos = 0
	case "end", "ctrl+e":
		m.inputPos = runeLen(m.input)
	case "backspace":
		if m.inputPos > 0 {
			m.input = runeDeleteAt(m.input, m.inputPos)
			m.inputPos--
		}
	case "alt+backspace":
		m.input, m.inputPos = deleteWordAt(m.input, m.inputPos)
	case "tab", "shift+tab":
		// ignore
	default:
		m.input = runeInsertAt(m.input, m.inputPos, key)
		m.inputPos += runeLen(key)
	}
	return m, nil
}

func (m genesisModel) handleSelectKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter", "ctrl+m", "ctrl+j":
		return m.handleEnter()
	case "esc":
		return m.handleBack()
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		m.cursor = m.clampCursor(m.cursor + 1)
	}
	return m, nil
}

func (m genesisModel) handleReviewKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter", "ctrl+m", "ctrl+j":
		return m.handleEnter()
	case "esc":
		return m.handleBack()
	}
	return m, nil
}

func (m genesisModel) handleCustomModeKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.customMode = false
		m.customText = ""
		m.customPos = 0
		return m, nil
	case "enter", "ctrl+m", "ctrl+j":
		text := strings.TrimSpace(m.customText)
		if text == "" {
			return m, nil
		}
		switch m.step {
		case stepRole:
			m.role = text
		case stepPersonality:
			m.personality = text
		}
		m.customMode = false
		m.customText = ""
		m.customPos = 0
		m.step++
		m.cursor = 0
		m.input = ""
		m.inputPos = 0
		return m, nil
	case "left":
		if m.customPos > 0 {
			m.customPos--
		}
	case "right":
		if m.customPos < runeLen(m.customText) {
			m.customPos++
		}
	case "home", "ctrl+a":
		m.customPos = 0
	case "end", "ctrl+e":
		m.customPos = runeLen(m.customText)
	case "backspace":
		if m.customPos > 0 {
			m.customText = runeDeleteAt(m.customText, m.customPos)
			m.customPos--
		}
	case "alt+backspace":
		m.customText, m.customPos = deleteWordAt(m.customText, m.customPos)
	case "tab", "shift+tab":
		// ignore
	default:
		m.customText = runeInsertAt(m.customText, m.customPos, key)
		m.customPos += runeLen(key)
	}
	return m, nil
}

func (m genesisModel) handleBack() (tea.Model, tea.Cmd) {
	if m.step <= stepName {
		return m, nil
	}
	m.step--

	// Skip steps that don't apply to this provider.
	if m.step == stepBaseURL && m.provider != "ollama" {
		m.step--
	}
	if m.step == stepAPIKey && m.provider == "ollama" {
		m.step--
	}

	// Restore state for the step we're going back to.
	switch m.step {
	case stepName:
		m.input = m.agentName
		m.inputPos = runeLen(m.agentName)
		m.cursor = 0
	case stepRole:
		m.cursor = m.findRoleCursor()
		m.customMode = false
	case stepPersonality:
		m.cursor = m.findPersonalityCursor()
		m.customMode = false
	case stepProvider:
		m.cursor = m.findProviderCursor()
	case stepBaseURL:
		m.input = m.baseURL
		if m.input == "" {
			m.input = "http://localhost:11434"
		}
		m.inputPos = runeLen(m.input)
	case stepModel:
		if m.provider == "ollama" {
			m.models = m.ollamaModels()
		} else {
			m.models = modelsForProvider(m.provider)
		}
		m.cursor = m.findModelCursor()
	case stepAPIKey:
		m.input = m.apiKey
		m.inputPos = runeLen(m.apiKey)
	}
	return m, nil
}

func (m genesisModel) findRoleCursor() int {
	for i, opt := range roleOptions {
		if opt.value == m.role {
			return i
		}
	}
	// Custom role — put cursor on "Write your own..."
	return len(roleOptions)
}

func (m genesisModel) findProviderCursor() int {
	for i, p := range builtinProviders {
		if p.ID == m.provider {
			return i
		}
	}
	return 0
}

func (m genesisModel) findModelCursor() int {
	for i, md := range m.models {
		if md.ID == m.modelID {
			return i
		}
	}
	return 0
}

func providerEmoji(id string) string {
	switch id {
	case "google":
		return "🔷"
	case "anthropic":
		return "🟠"
	case "openai":
		return "🟢"
	case "openrouter":
		return "🔀"
	case "ollama":
		return "🦙"
	default:
		return "🔌"
	}
}

func providerLabel(id string) string {
	for _, p := range builtinProviders {
		if p.ID == id {
			return p.Label
		}
	}
	if id != "" {
		return id
	}
	return "LLM"
}

func modelsForProvider(providerID string) []modelDef {
	if models, ok := config.BuiltinModels[providerID]; ok {
		return models
	}
	return nil
}

// ollamaModels returns models discovered from the Ollama server, falling back
// to the builtin list if the server is unreachable.
func (m genesisModel) ollamaModels() []modelDef {
	if discovered := discoverOllamaModels(m.baseURL); len(discovered) > 0 {
		return discovered
	}
	return modelsForProvider("ollama")
}

// discoverOllamaModels queries an Ollama server's /api/tags endpoint to get
// the list of locally available models.
func discoverOllamaModels(baseURL string) []modelDef {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	url := strings.TrimSuffix(baseURL, "/") + "/api/tags"
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil
	}
	var models []modelDef
	for _, m := range result.Models {
		models = append(models, modelDef{ID: m.Name})
	}
	return models
}

func (m genesisModel) findPersonalityCursor() int {
	for i, opt := range personalityOptions {
		if opt.value == m.personality {
			return i
		}
	}
	return len(personalityOptions)
}

func (m genesisModel) handleEnter() (tea.Model, tea.Cmd) {
	switch m.step {
	case stepName:
		name := strings.TrimSpace(m.input)
		if name == "" {
			name = "GoClaw"
		}
		m.agentName = name
		m.step = stepRole
		m.cursor = 0
		m.input = ""
		m.inputPos = 0

	case stepRole:
		// Last option is "Write your own..."
		if m.cursor >= len(roleOptions) {
			m.customMode = true
			m.customText = ""
			m.customPos = 0
			return m, nil
		}
		m.role = roleOptions[m.cursor].value
		m.step = stepPersonality
		m.cursor = 0

	case stepPersonality:
		if m.cursor >= len(personalityOptions) {
			m.customMode = true
			m.customText = ""
			m.customPos = 0
			return m, nil
		}
		m.personality = personalityOptions[m.cursor].value
		m.step = stepProvider
		m.cursor = 0

	case stepProvider:
		m.provider = builtinProviders[m.cursor].ID
		if m.provider == "ollama" {
			m.step = stepBaseURL
			m.input = m.baseURL
			if m.input == "" {
				m.input = "http://localhost:11434"
			}
			m.inputPos = runeLen(m.input)
		} else {
			m.models = modelsForProvider(m.provider)
			m.step = stepModel
			m.cursor = 0
		}

	case stepBaseURL:
		url := strings.TrimSpace(m.input)
		if url == "" {
			url = "http://localhost:11434"
		}
		m.baseURL = strings.TrimSuffix(url, "/")
		m.models = m.ollamaModels()
		m.step = stepModel
		m.cursor = 0

	case stepModel:
		m.modelID = m.models[m.cursor].ID
		if m.provider == "ollama" {
			// Ollama doesn't need an API key; skip to review.
			m.apiKey = "ollama"
			m.step = stepReview
		} else {
			m.step = stepAPIKey
			m.input = ""
			m.inputPos = 0
		}

	case stepAPIKey:
		m.apiKey = sanitizeAPIKey(m.input)
		m.step = stepReview

	case stepReview:
		m.done = true
		m.result = &GenesisResult{
			Soul:   m.generateSoul(),
			Config: m.generateConfig(),
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m genesisModel) clampCursor(n int) int {
	max := 0
	switch m.step {
	case stepRole:
		max = len(roleOptions) // +1 for "Write your own..."
	case stepPersonality:
		max = len(personalityOptions) // +1 for "Write your own..."
	case stepProvider:
		max = len(builtinProviders) - 1
	case stepModel:
		max = len(m.models) - 1
	}
	if max < 0 {
		max = 0
	}
	if n > max {
		return max
	}
	return n
}

func (m genesisModel) View() string {
	if m.quitting {
		return "  Setup cancelled.\n"
	}
	if m.done {
		return m.viewCompletion()
	}

	var b strings.Builder

	b.WriteString("\n  ✨ GoClaw Setup Wizard\n")
	b.WriteString("  ════════════════════════════════════════\n\n")

	stepNum := displayStep(m.step)
	title := displayStepTitle(m.step)
	b.WriteString(fmt.Sprintf("  📋 Step %d/%d — %s\n\n", stepNum, totalDisplaySteps, title))

	switch m.step {
	case stepName:
		b.WriteString("  What's your agent's name?\n\n")
		b.WriteString(fmt.Sprintf("  > %s\n", renderCursor(m.input, m.inputPos)))

	case stepRole:
		if m.customMode {
			b.WriteString("  Describe your agent's role:\n\n")
			b.WriteString(fmt.Sprintf("  > %s\n", renderCursor(m.customText, m.customPos)))
			b.WriteString("\n  [Enter] Confirm  [Esc] Cancel\n")
			return b.String()
		}
		b.WriteString("  What role should your agent play?\n\n")
		for i, opt := range roleOptions {
			cursor := "  "
			if i == m.cursor {
				cursor = "> "
			}
			b.WriteString(fmt.Sprintf("  %s%s  %s\n", cursor, opt.emoji, opt.label))
		}
		// "Write your own..." option
		cursor := "  "
		if m.cursor == len(roleOptions) {
			cursor = "> "
		}
		b.WriteString(fmt.Sprintf("  %s✏️  Write your own...\n", cursor))

	case stepPersonality:
		if m.customMode {
			b.WriteString("  Describe your agent's personality and tone:\n\n")
			b.WriteString(fmt.Sprintf("  > %s\n", renderCursor(m.customText, m.customPos)))
			b.WriteString("\n  [Enter] Confirm  [Esc] Cancel\n")
			return b.String()
		}
		b.WriteString("  What personality and communication tone?\n\n")
		for i, opt := range personalityOptions {
			cursor := "  "
			if i == m.cursor {
				cursor = "> "
			}
			b.WriteString(fmt.Sprintf("  %s%s  %s — %s\n", cursor, opt.emoji, opt.label, opt.desc))
		}
		cursor := "  "
		if m.cursor == len(personalityOptions) {
			cursor = "> "
		}
		b.WriteString(fmt.Sprintf("  %s✏️  Write your own...\n", cursor))

	case stepProvider:
		b.WriteString("  Which LLM provider do you want to use?\n\n")
		for i, p := range builtinProviders {
			cursor := "  "
			if i == m.cursor {
				cursor = "> "
			}
			b.WriteString(fmt.Sprintf("  %s%s  %s\n", cursor, providerEmoji(p.ID), p.Label))
		}

	case stepBaseURL:
		b.WriteString("  Enter your Ollama server URL:\n\n")
		b.WriteString(fmt.Sprintf("  > %s\n", renderCursor(m.input, m.inputPos)))
		b.WriteString("\n  Press Enter for default (http://localhost:11434)\n")

	case stepModel:
		provLabel := m.provider
		for _, p := range builtinProviders {
			if p.ID == m.provider {
				provLabel = p.Label
				break
			}
		}
		b.WriteString(fmt.Sprintf("  %s %s — pick a model:\n\n", providerEmoji(m.provider), provLabel))
		for i, md := range m.models {
			cursor := "  "
			if i == m.cursor {
				cursor = "> "
			}
			desc := ""
			if md.Desc != "" {
				desc = "  — " + md.Desc
			}
			b.WriteString(fmt.Sprintf("  %s%-30s%s\n", cursor, md.ID, desc))
		}

	case stepAPIKey:
		b.WriteString(fmt.Sprintf("  🔑 Enter your %s API key (or press Enter to skip):\n\n", providerLabel(m.provider)))
		masked := strings.Repeat("*", runeLen(m.input))
		b.WriteString(fmt.Sprintf("  > %s\n", renderCursor(masked, m.inputPos)))
		b.WriteString("\n  Paste with Cmd+V. Key is stored in config.yaml.\n")

	case stepReview:
		b.WriteString(fmt.Sprintf("  %s\n", m.agentName))
		b.WriteString(fmt.Sprintf("  Role: %s\n", m.role))
		b.WriteString(fmt.Sprintf("  Tone: %s\n", m.personalityDisplay()))
		b.WriteString(fmt.Sprintf("  Model: %s / %s\n", providerLabel(m.provider), m.modelID))
		apiDisplay := "(not set)"
		if m.apiKey != "" {
			apiDisplay = maskAPIKey(m.apiKey)
		}
		b.WriteString(fmt.Sprintf("  🔑  %s\n", apiDisplay))
		b.WriteString("\n  Press Enter to create your agent!\n")
	}

	b.WriteString("\n  ")
	if m.step > stepName {
		b.WriteString("[Esc] Back  ")
	}
	if m.step == stepReview {
		b.WriteString("[Enter] Create  [Ctrl+C] Quit\n")
	} else {
		b.WriteString("[Enter] Continue  [Ctrl+C] Quit\n")
	}

	return b.String()
}

func (m genesisModel) personalityDisplay() string {
	// Show short label for predefined options.
	for _, opt := range personalityOptions {
		if opt.value == m.personality {
			return opt.label
		}
	}
	// Custom personality: truncate if long.
	if len(m.personality) > 40 {
		return m.personality[:37] + "..."
	}
	return m.personality
}

func maskAPIKey(key string) string {
	if len(key) <= 8 {
		return strings.Repeat("*", len(key))
	}
	return key[:4] + "..." + key[len(key)-3:]
}

func (m genesisModel) viewCompletion() string {
	var b strings.Builder
	b.WriteString("\n  Setup complete!\n\n")
	b.WriteString("  Created:\n")
	b.WriteString("    SOUL.md (agent identity)\n")
	b.WriteString("    config.yaml (settings)\n")
	b.WriteString("    policy.yaml (permissions)\n\n")
	b.WriteString(fmt.Sprintf("  %s is ready. Starting GoClaw...\n\n", m.agentName))
	return b.String()
}

func (m genesisModel) generateSoul() string {
	return fmt.Sprintf(`# Soul — %s

## Identity
You are %s, a %s running on GoClaw.

## Personality
%s

## Created
%s
`, m.agentName, m.agentName, m.role,
		m.personalitySoulText(),
		time.Now().Format(time.RFC3339))
}

func (m genesisModel) personalitySoulText() string {
	// Map predefined options to descriptive soul text.
	for _, opt := range personalityOptions {
		if opt.value == m.personality {
			return fmt.Sprintf("Communicate in a %s style. Be %s.", strings.ToLower(opt.label), strings.ToLower(opt.desc))
		}
	}
	// Custom personality used as-is.
	return m.personality
}

func (m genesisModel) generateConfig() string {
	provider := m.provider
	if provider == "" {
		provider = "google"
	}
	model := m.modelID
	if model == "" {
		// Should never happen (wizard forces selection), but defensive fallback
		if models, ok := config.BuiltinModels["google"]; ok && len(models) > 0 {
			model = models[0].ID
		} else {
			model = "gemini-2.5-flash" // Emergency fallback
		}
	}

	var b strings.Builder
	b.WriteString("# GoClaw Configuration\n")
	b.WriteString("# Generated by the Setup Wizard\n\n")
	b.WriteString(fmt.Sprintf("agent_name: %s\n", m.agentName))

	if provider == "ollama" {
		baseURL := m.baseURL
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		compatURL := strings.TrimSuffix(baseURL, "/") + "/v1"
		b.WriteString("llm:\n")
		b.WriteString("  provider: ollama\n")
		b.WriteString(fmt.Sprintf("  openai_compatible_base_url: %s\n", compatURL))
		b.WriteString(fmt.Sprintf("  openai_model: %s\n", model))
	} else {
		b.WriteString(fmt.Sprintf("llm_provider: %s\n", provider))
		b.WriteString(fmt.Sprintf("gemini_model: %s\n", model))
		if m.apiKey != "" {
			b.WriteString(fmt.Sprintf("gemini_api_key: \"%s\"\n", m.apiKey))
		}
	}

	b.WriteString("worker_count: 4\n")
	b.WriteString("task_timeout_seconds: 600\n")
	b.WriteString("bind_addr: 127.0.0.1:18789\n")
	b.WriteString("log_level: info\n")

	// Add starter agents
	b.WriteString("\nagents:\n")
	for _, agent := range config.StarterAgents() {
		b.WriteString(fmt.Sprintf("  - agent_id: %s\n", agent.AgentID))
		b.WriteString(fmt.Sprintf("    display_name: %s\n", agent.DisplayName))
		b.WriteString(fmt.Sprintf("    soul: |\n"))
		for _, line := range strings.Split(agent.Soul, "\n") {
			b.WriteString(fmt.Sprintf("      %s\n", line))
		}
	}

	return b.String()
}

// runeLen returns the number of runes in s.
func runeLen(s string) int {
	return len([]rune(s))
}

// renderCursor inserts a block cursor (█) at rune position pos within s.
func renderCursor(s string, pos int) string {
	runes := []rune(s)
	if pos >= len(runes) {
		return s + "█"
	}
	return string(runes[:pos]) + "█" + string(runes[pos:])
}

// runeInsertAt inserts text at rune position pos within s.
func runeInsertAt(s string, pos int, text string) string {
	runes := []rune(s)
	if pos >= len(runes) {
		return s + text
	}
	return string(runes[:pos]) + text + string(runes[pos:])
}

// runeDeleteAt deletes the rune before position pos, returning the new string.
func runeDeleteAt(s string, pos int) string {
	runes := []rune(s)
	if pos <= 0 || pos > len(runes) {
		return s
	}
	return string(runes[:pos-1]) + string(runes[pos:])
}

// deleteWordAt deletes the word before rune position pos, returning new string and position.
func deleteWordAt(s string, pos int) (string, int) {
	runes := []rune(s)
	if pos <= 0 {
		return s, 0
	}
	// Skip spaces immediately before cursor.
	i := pos
	for i > 0 && runes[i-1] == ' ' {
		i--
	}
	// Delete back to the previous space.
	for i > 0 && runes[i-1] != ' ' {
		i--
	}
	result := string(runes[:i]) + string(runes[pos:])
	return result, i
}

// sanitizeAPIKey strips surrounding brackets, quotes, whitespace, and the
// "GEMINI_API_KEY=" prefix that users might accidentally paste.
func sanitizeAPIKey(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, "[]\"'`")
	s = strings.TrimSpace(s)
	// Handle pasting "GEMINI_API_KEY=..." from .env
	if i := strings.Index(s, "="); i >= 0 && !strings.ContainsAny(s[:i], " \t") {
		s = s[i+1:]
	}
	return strings.TrimSpace(s)
}

// RunGenesis runs the genesis wizard TUI and returns the generated files.
func RunGenesis(ctx context.Context) (*GenesisResult, error) {
	defer bestEffortResetTTY()

	m := newGenesisModel()
	m.input = m.agentName // Pre-fill name input
	m.inputPos = runeLen(m.agentName)
	p := tea.NewProgram(m)

	done := make(chan error, 1)
	var finalModel tea.Model
	go func() {
		var err error
		finalModel, err = p.Run()
		done <- err
	}()

	select {
	case <-ctx.Done():
		p.Quit()
		return nil, ctx.Err()
	case err := <-done:
		if err != nil {
			return nil, err
		}
	}

	gm, ok := finalModel.(genesisModel)
	if !ok || gm.quitting || gm.result == nil {
		return nil, fmt.Errorf("wizard cancelled")
	}
	return gm.result, nil
}

func WriteGenesisFiles(homeDir string, result *GenesisResult) error {
	if result == nil {
		return fmt.Errorf("nil genesis result")
	}
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(homeDir, "SOUL.md"), []byte(result.Soul), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(homeDir, "config.yaml"), []byte(result.Config), 0o644); err != nil {
		return err
	}
	// Create default policy.yaml with baseline capabilities.
	policyPath := filepath.Join(homeDir, "policy.yaml")
	if _, err := os.Stat(policyPath); os.IsNotExist(err) {
		if err := os.WriteFile(policyPath, []byte(DefaultPolicyYAML()), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// DefaultPolicyYAML returns the baseline policy for a new installation.
func DefaultPolicyYAML() string {
	return `# GoClaw Policy — default-deny with baseline ACP access
# See SPEC GC-SPEC-SEC-001: all capabilities denied unless listed here.

allow_capabilities:
  - acp.read
  - acp.mutate
  - tools.web_search
  - tools.read_url

# Domains the agent may reach for web_search and read_url tools.
# Suffix matching: "duckduckgo.com" allows "html.duckduckgo.com".
allow_domains:
  - duckduckgo.com
  - search.brave.com
  - api.search.brave.com
  - api.perplexity.ai
  - perplexity.ai
  - wikipedia.org
  - github.com
  - stackoverflow.com

allow_loopback: false
`
}

func GreetingFromSoul(soul string) string {
	// Try to extract agent name from "# Soul — Name" header.
	name, _ := parseNameEmoji(soul)

	lower := strings.ToLower(soul)
	var greeting string
	switch {
	case strings.Contains(lower, "casual") || strings.Contains(lower, "friendly"):
		greeting = "Hey there! Ready when you are."
	case strings.Contains(lower, "technical") || strings.Contains(lower, "concise"):
		greeting = "System online. Awaiting your first task."
	case strings.Contains(lower, "creative") || strings.Contains(lower, "expressive"):
		greeting = "Let's build something amazing!"
	default:
		greeting = "Hello. Ready to assist."
	}

	if name != "" {
		return fmt.Sprintf("%s is online! %s", name, greeting)
	}
	return greeting
}

// parseNameEmoji extracts name from "# Soul — Name" header line.
// The emoji return value is always empty (emojis removed from onboarding).
func parseNameEmoji(soul string) (name, emoji string) {
	for _, line := range strings.Split(soul, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "# Soul") {
			continue
		}
		idx := strings.Index(line, "—")
		if idx < 0 {
			idx = strings.Index(line, "-")
		}
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(line[idx+len("—"):])
		if rest == "" {
			// try single dash
			rest = strings.TrimSpace(line[idx+1:])
		}
		if rest != "" {
			return rest, ""
		}
	}
	return "", ""
}
