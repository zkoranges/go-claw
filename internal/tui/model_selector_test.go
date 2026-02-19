package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/basket/go-claw/internal/config"
)

func TestBuildSelectorData_BuiltinProviders(t *testing.T) {
	cfg := &config.Config{}
	providers, models := buildSelectorData(cfg)

	if len(providers) < 5 {
		t.Errorf("expected at least 5 builtin providers, got %d", len(providers))
	}

	// Verify Google is present.
	found := false
	for _, p := range providers {
		if p.ID == "google" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'google' in providers")
	}

	// Google should have models.
	googleModels := models["google"]
	if len(googleModels) == 0 {
		t.Error("expected at least 1 Google model")
	}
}

func TestBuildSelectorData_WithAPIKey(t *testing.T) {
	cfg := &config.Config{
		GeminiAPIKey: "test-key",
	}
	providers, _ := buildSelectorData(cfg)

	for _, p := range providers {
		if p.ID == "google" {
			if !p.Configured {
				t.Error("expected google to be marked as Configured when API key set")
			}
			return
		}
	}
	t.Error("google provider not found")
}

func TestBuildSelectorData_CustomProvider(t *testing.T) {
	cfg := &config.Config{
		Providers: map[string]config.ProviderConfig{
			"custom-llm": {
				APIKey: "cust-key",
				Models: []string{"custom-model-1", "custom-model-2"},
			},
		},
	}
	providers, models := buildSelectorData(cfg)

	// Should include the custom provider.
	found := false
	for _, p := range providers {
		if p.ID == "custom-llm" {
			found = true
			if !p.Configured {
				t.Error("custom provider should be Configured")
			}
			break
		}
	}
	if !found {
		t.Error("expected custom-llm in providers")
	}

	customModels := models["custom-llm"]
	if len(customModels) != 2 {
		t.Errorf("expected 2 custom models, got %d", len(customModels))
	}
}

func TestBuildSelectorData_UserModelsAppended(t *testing.T) {
	cfg := &config.Config{
		Providers: map[string]config.ProviderConfig{
			"google": {
				Models: []string{"gemini-custom-experimental"},
			},
		},
	}
	_, models := buildSelectorData(cfg)

	googleModels := models["google"]
	found := false
	for _, m := range googleModels {
		if m.ID == "gemini-custom-experimental" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected custom model appended to google models")
	}
}

func TestSelectorModel_Init(t *testing.T) {
	sm := selectorModel{}
	cmd := sm.Init()
	if cmd != nil {
		t.Error("Init() should return nil")
	}
}

func TestSelectorModel_ClampCursor(t *testing.T) {
	sm := selectorModel{
		step:      stepSelectProvider,
		providers: make([]providerEntry, 3),
	}

	if got := sm.clampCursor(0); got != 0 {
		t.Errorf("clampCursor(0) = %d, want 0", got)
	}
	if got := sm.clampCursor(2); got != 2 {
		t.Errorf("clampCursor(2) = %d, want 2", got)
	}
	if got := sm.clampCursor(5); got != 2 {
		t.Errorf("clampCursor(5) = %d, want 2 (clamped)", got)
	}
}

func TestSelectorModel_NavigateProviders(t *testing.T) {
	sm := selectorModel{
		step: stepSelectProvider,
		providers: []providerEntry{
			{providerDef: providerDef{ID: "google", Label: "Google"}},
			{providerDef: providerDef{ID: "anthropic", Label: "Anthropic"}},
		},
	}

	// Move down
	m, _ := sm.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated := m.(selectorModel)
	if updated.cursor != 1 {
		t.Errorf("cursor after down = %d, want 1", updated.cursor)
	}

	// Move up
	m, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated = m.(selectorModel)
	if updated.cursor != 0 {
		t.Errorf("cursor after up = %d, want 0", updated.cursor)
	}

	// Can't move above 0
	m, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated = m.(selectorModel)
	if updated.cursor != 0 {
		t.Errorf("cursor should stay at 0, got %d", updated.cursor)
	}
}

func TestSelectorModel_SelectProvider(t *testing.T) {
	sm := selectorModel{
		step: stepSelectProvider,
		providers: []providerEntry{
			{providerDef: providerDef{ID: "google", Label: "Google"}},
		},
		allModels: map[string][]modelDef{
			"google": {{ID: "gemini-2.5-flash", Desc: "Fast"}},
		},
	}

	m, _ := sm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := m.(selectorModel)

	if updated.step != stepSelectModel {
		t.Errorf("step = %d, want stepSelectModel", updated.step)
	}
	if updated.selectedProvider != "google" {
		t.Errorf("selectedProvider = %q, want google", updated.selectedProvider)
	}
	if len(updated.models) != 1 {
		t.Errorf("models = %d, want 1", len(updated.models))
	}
}

func TestSelectorModel_SelectModel(t *testing.T) {
	sm := selectorModel{
		step:             stepSelectModel,
		selectedProvider: "google",
		models:           []modelDef{{ID: "gemini-2.5-pro", Desc: "Smart"}},
	}

	m, _ := sm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := m.(selectorModel)

	if !updated.done {
		t.Error("expected done=true after selecting model")
	}
	if updated.selectedModel != "gemini-2.5-pro" {
		t.Errorf("selectedModel = %q, want gemini-2.5-pro", updated.selectedModel)
	}
}

func TestSelectorModel_EscBackToProviders(t *testing.T) {
	sm := selectorModel{
		step:             stepSelectModel,
		selectedProvider: "google",
		providers: []providerEntry{
			{providerDef: providerDef{ID: "google", Label: "Google"}},
		},
	}

	m, _ := sm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := m.(selectorModel)

	if updated.step != stepSelectProvider {
		t.Errorf("step = %d, want stepSelectProvider after esc", updated.step)
	}
}

func TestSelectorModel_EscFromProvidersQuits(t *testing.T) {
	sm := selectorModel{
		step: stepSelectProvider,
	}

	m, _ := sm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated := m.(selectorModel)

	if !updated.quitting {
		t.Error("expected quitting=true after esc from provider list")
	}
}

func TestSelectorModel_CtrlCQuits(t *testing.T) {
	sm := selectorModel{
		step: stepSelectProvider,
	}

	m, _ := sm.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	updated := m.(selectorModel)

	if !updated.quitting {
		t.Error("expected quitting=true after ctrl+c")
	}
}

func TestSelectorModel_ViewProviders(t *testing.T) {
	sm := selectorModel{
		step: stepSelectProvider,
		providers: []providerEntry{
			{providerDef: providerDef{ID: "google", Label: "Google Gemini"}, Configured: true},
			{providerDef: providerDef{ID: "anthropic", Label: "Anthropic"}, Configured: false},
		},
	}

	view := sm.View()
	if view == "" {
		t.Error("expected non-empty view")
	}

	// Should contain provider labels.
	if !contains(view, "Google Gemini") {
		t.Error("expected 'Google Gemini' in view")
	}
	if !contains(view, "Anthropic") {
		t.Error("expected 'Anthropic' in view")
	}
	if !contains(view, "configured") {
		t.Error("expected 'configured' badge in view")
	}
}

func TestSelectorModel_ViewModels(t *testing.T) {
	sm := selectorModel{
		step:             stepSelectModel,
		selectedProvider: "google",
		providers: []providerEntry{
			{providerDef: providerDef{ID: "google", Label: "Google Gemini"}},
		},
		models: []modelDef{
			{ID: "gemini-2.5-flash", Desc: "Fast"},
		},
		currentModel:    "gemini-2.5-flash",
		currentProvider: "google",
	}

	view := sm.View()
	if !contains(view, "gemini-2.5-flash") {
		t.Error("expected model name in view")
	}
	if !contains(view, "Fast") {
		t.Error("expected model description in view")
	}
}

func TestSelectorModel_ViewEmptyWhenQuitting(t *testing.T) {
	sm := selectorModel{quitting: true}
	if sm.View() != "" {
		t.Error("expected empty view when quitting")
	}
}

func TestSelectorModel_ViewEmptyWhenDone(t *testing.T) {
	sm := selectorModel{done: true}
	if sm.View() != "" {
		t.Error("expected empty view when done")
	}
}

func TestSelectorModel_EmbeddedNoQuit(t *testing.T) {
	sm := selectorModel{
		step:     stepSelectProvider,
		embedded: true,
	}

	m, cmd := sm.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	updated := m.(selectorModel)

	if !updated.quitting {
		t.Error("expected quitting=true")
	}
	// Embedded mode should NOT send tea.Quit
	if cmd != nil {
		t.Error("expected nil cmd in embedded mode")
	}
}

func TestBuildModelList_UnknownProvider(t *testing.T) {
	sm := selectorModel{
		allModels: map[string][]modelDef{
			"google": {{ID: "gemini-2.5-flash"}},
		},
	}

	models := sm.buildModelList("nonexistent")
	if models != nil {
		t.Errorf("expected nil for unknown provider, got %d models", len(models))
	}
}

// contains is a simple substring check helper.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && len(substr) > 0 && searchSubstring(s, substr)
}

func searchSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
