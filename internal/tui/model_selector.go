package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/basket/go-claw/internal/config"
)

// providerDef describes a provider entry in the selector.
type providerDef struct {
	ID    string
	Label string
}

// modelDef is a local alias for config.ModelDef for backward compatibility.
type modelDef = config.ModelDef

var builtinProviders = []providerDef{
	{ID: "google", Label: "Google Gemini"},
	{ID: "anthropic", Label: "Anthropic"},
	{ID: "openai", Label: "OpenAI"},
	{ID: "openrouter", Label: "OpenRouter (100+ models)"},
}

type selectorStep int

const (
	stepSelectProvider selectorStep = iota
	stepSelectModel
)

type selectorModel struct {
	step     selectorStep
	cursor   int
	quitting bool
	done     bool
	embedded bool

	providers []providerEntry
	allModels map[string][]modelDef // merged built-in + user models
	models    []modelDef            // models for currently selected provider

	selectedProvider string
	selectedModel    string

	currentProvider string
	currentModel    string
}

type providerEntry struct {
	providerDef
	Configured bool
}

func buildSelectorData(cfg *config.Config) ([]providerEntry, map[string][]modelDef) {
	// Start with built-in providers.
	seen := make(map[string]bool)
	var providers []providerEntry
	for _, bp := range builtinProviders {
		seen[bp.ID] = true
		hasKey := cfg.ProviderAPIKey(bp.ID) != ""
		providers = append(providers, providerEntry{
			providerDef: bp,
			Configured:  hasKey,
		})
	}

	// Add user-defined custom providers.
	if cfg.Providers != nil {
		for id, pc := range cfg.Providers {
			if seen[id] {
				continue
			}
			hasKey := pc.APIKey != ""
			providers = append(providers, providerEntry{
				providerDef: providerDef{ID: id, Label: id},
				Configured:  hasKey,
			})
		}
	}

	// Build merged model lists.
	models := make(map[string][]modelDef)
	for id, builtins := range config.BuiltinModels {
		models[id] = append([]modelDef{}, builtins...)
	}
	if cfg.Providers != nil {
		for id, pc := range cfg.Providers {
			existing := make(map[string]bool)
			for _, m := range models[id] {
				existing[m.ID] = true
			}
			for _, userModel := range pc.Models {
				if !existing[userModel] {
					models[id] = append(models[id], modelDef{ID: userModel})
				}
			}
		}
	}

	return providers, models
}

func (m selectorModel) Init() tea.Cmd {
	return nil
}

func (m selectorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		key := msg.String()
		switch key {
		case "ctrl+c":
			m.quitting = true
			if m.embedded {
				return m, nil
			}
			return m, tea.Quit
		case "esc":
			return m.handleBack()
		case "enter", "ctrl+m", "ctrl+j":
			return m.handleEnter()
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			m.cursor = m.clampCursor(m.cursor + 1)
		}
	}
	return m, nil
}

func (m selectorModel) handleBack() (tea.Model, tea.Cmd) {
	if m.step == stepSelectProvider {
		m.quitting = true
		if m.embedded {
			return m, nil
		}
		return m, tea.Quit
	}
	m.step = stepSelectProvider
	m.cursor = 0
	// Restore cursor to previously selected provider.
	for i, p := range m.providers {
		if p.ID == m.selectedProvider {
			m.cursor = i
			break
		}
	}
	return m, nil
}

func (m selectorModel) handleEnter() (tea.Model, tea.Cmd) {
	switch m.step {
	case stepSelectProvider:
		m.selectedProvider = m.providers[m.cursor].ID
		m.models = m.buildModelList(m.selectedProvider)
		m.step = stepSelectModel
		m.cursor = 0
	case stepSelectModel:
		m.selectedModel = m.models[m.cursor].ID
		m.done = true
		if m.embedded {
			return m, nil
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m selectorModel) clampCursor(n int) int {
	max := 0
	switch m.step {
	case stepSelectProvider:
		max = len(m.providers) - 1
	case stepSelectModel:
		max = len(m.models) - 1
	}
	if n > max {
		return max
	}
	return n
}

func (m selectorModel) buildModelList(providerID string) []modelDef {
	if models, ok := m.allModels[providerID]; ok {
		return models
	}
	return nil
}

func (m selectorModel) View() string {
	if m.quitting {
		return ""
	}
	if m.done {
		return ""
	}

	var b strings.Builder

	switch m.step {
	case stepSelectProvider:
		b.WriteString("\n  Select a provider:\n\n")
		for i, p := range m.providers {
			cursor := "  "
			if i == m.cursor {
				cursor = "> "
			}
			badge := "  (no API key)"
			if p.Configured {
				badge = "  \u2713 configured"
			}
			b.WriteString(fmt.Sprintf("  %s%-22s%s\n", cursor, p.Label, badge))
		}
		b.WriteString("\n  [\u2191\u2193] Navigate  [Enter] Select  [Esc] Cancel\n")

	case stepSelectModel:
		label := m.selectedProvider
		for _, p := range m.providers {
			if p.ID == m.selectedProvider {
				label = p.Label
				break
			}
		}
		b.WriteString(fmt.Sprintf("\n  %s \u2014 select a model:\n\n", label))
		for i, md := range m.models {
			cursor := "  "
			if i == m.cursor {
				cursor = "> "
			}
			active := " "
			if md.ID == m.currentModel && m.selectedProvider == m.currentProvider {
				active = "*"
			}
			desc := ""
			if md.Desc != "" {
				desc = "  " + md.Desc
			}
			b.WriteString(fmt.Sprintf("  %s%s %-34s%s\n", cursor, active, md.ID, desc))
		}
		b.WriteString("\n  [\u2191\u2193] Navigate  [Enter] Select  [Esc] Back\n")
	}

	return b.String()
}

// RunModelSelector launches the interactive provider/model selector.
// Returns the selected provider and model, or an error if cancelled.
func RunModelSelector(cfg *config.Config) (provider, model string, err error) {
	providers, mergedModels := buildSelectorData(cfg)

	sm := selectorModel{
		step:            stepSelectProvider,
		providers:       providers,
		allModels:       mergedModels,
		currentProvider: cfg.LLMProvider,
		currentModel:    cfg.GeminiModel,
	}

	p := tea.NewProgram(sm)
	finalModel, err := p.Run()
	if err != nil {
		return "", "", err
	}

	final, ok := finalModel.(selectorModel)
	if !ok || final.quitting || !final.done {
		return "", "", fmt.Errorf("cancelled")
	}
	return final.selectedProvider, final.selectedModel, nil
}
