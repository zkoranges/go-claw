package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
	"github.com/basket/go-claw/internal/safety"
	"github.com/basket/go-claw/internal/sandbox/legacy"
	"github.com/basket/go-claw/internal/sandbox/wasm"
	"github.com/basket/go-claw/internal/skills"
	"github.com/basket/go-claw/internal/tokenutil"
	"github.com/basket/go-claw/internal/tools"
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
	"github.com/firebase/genkit/go/plugins/anthropic"
	"github.com/firebase/genkit/go/plugins/compat_oai"
	"github.com/firebase/genkit/go/plugins/googlegenai"
)

// skillCacheKey is the context key for per-turn skill deduplication cache.
type skillCacheKeyType struct{}

var skillCacheKey = skillCacheKeyType{}

// skillCache holds cached use_skill results for a single Respond/Stream turn.
type skillCache struct {
	mu    sync.Mutex
	items map[string]tools.UseSkillOutput
}

func withSkillCache(ctx context.Context) (context.Context, *skillCache) {
	sc := &skillCache{items: make(map[string]tools.UseSkillOutput)}
	return context.WithValue(ctx, skillCacheKey, sc), sc
}

func getSkillCache(ctx context.Context) *skillCache {
	if sc, ok := ctx.Value(skillCacheKey).(*skillCache); ok {
		return sc
	}
	return nil
}

// Brain is the LLM abstraction used by the runtime engine.
// It is backed by Genkit to match the architecture requirement.
type Brain interface {
	Respond(ctx context.Context, sessionID, content string) (string, error)
	Stream(ctx context.Context, sessionID, content string, onChunk func(content string) error) error
}

// BrainConfig holds configuration for the GenkitBrain.
type BrainConfig struct {
	// Provider is the LLM provider: "google", "anthropic", "openai", "openai_compatible".
	// Empty defaults to "google".
	Provider string

	// Model is the model name for the configured provider.
	Model string

	// APIKey is the API key for the LLM provider.
	APIKey string

	Soul            string
	AgentName       string
	AgentEmoji      string
	Policy          policy.Checker
	APIKeys         map[string]string
	PreferredSearch string

	// OpenAICompatible config.
	OpenAICompatibleProvider string
	OpenAICompatibleBaseURL  string
}

type skillEntry struct {
	Name        string
	Description string
	Type        string // "wasm", "legacy", "instruction"

	Source    string
	SourceDir string

	InstructionsLoaded bool
	Instructions       string
}

// GenkitBrain wraps a Genkit instance with the GoogleAI plugin for Gemini.
type GenkitBrain struct {
	g     *genkit.Genkit
	store *persistence.Store
	tools *tools.Registry
	cfg   BrainConfig
	llmOn bool

	compactor    *Compactor
	sanitizer    *safety.Sanitizer
	leakDetector *safety.LeakDetector

	soulMu sync.RWMutex // protects cfg.Soul for hot-reload

	skillMu      sync.RWMutex
	loadedSkills map[string]*skillEntry
	wasmHost     *wasm.Host
}

// NewGenkitBrain initializes Genkit with the configured LLM provider.
// Supports: google (Gemini), anthropic (Claude), openai (GPT), openai_compatible.
func NewGenkitBrain(ctx context.Context, store *persistence.Store, cfg BrainConfig) *GenkitBrain {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" {
		provider = "google"
	}

	modelID := strings.TrimSpace(cfg.Model)
	if modelID == "" {
		modelID = defaultModelForProvider(provider)
	}

	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		apiKey = envAPIKeyForProvider(provider)
	}

	var g *genkit.Genkit
	llmOn := false

	switch provider {
	case "anthropic":
		if apiKey != "" {
			anthropicPlugin := &anthropic.Anthropic{
				APIKey:  apiKey,
				BaseURL: os.Getenv("ANTHROPIC_BASE_URL"),
			}
			g = genkit.Init(ctx, genkit.WithPlugins(anthropicPlugin))
			llmOn = true
			slog.Info("genkit brain initialized", "provider", "anthropic", "model", modelID)
		} else {
			g = genkit.Init(ctx)
			slog.Warn("Anthropic API key missing; using deterministic fallback")
		}

	case "openai":
		if apiKey != "" {
			openaiPlugin := &compat_oai.OpenAICompatible{
				Provider: "openai",
				APIKey:   apiKey,
				BaseURL:  os.Getenv("OPENAI_BASE_URL"),
			}
			g = genkit.Init(ctx, genkit.WithPlugins(openaiPlugin))
			llmOn = true
			slog.Info("genkit brain initialized", "provider", "openai", "model", modelID)
		} else {
			g = genkit.Init(ctx)
			slog.Warn("OpenAI API key missing; using deterministic fallback")
		}

	case "openai_compatible":
		if apiKey != "" {
			openaiCompatPlugin := &compat_oai.OpenAICompatible{
				Provider: cfg.OpenAICompatibleProvider,
				APIKey:   apiKey,
				BaseURL:  cfg.OpenAICompatibleBaseURL,
			}
			g = genkit.Init(ctx, genkit.WithPlugins(openaiCompatPlugin))
			llmOn = true
			slog.Info("genkit brain initialized", "provider", "openai_compatible", "model", modelID)
		} else {
			g = genkit.Init(ctx)
			slog.Warn("OpenAI compatible API key missing; using deterministic fallback")
		}

	case "openrouter":
		if apiKey != "" {
			openrouterPlugin := &compat_oai.OpenAICompatible{
				Provider: "openrouter",
				APIKey:   apiKey,
				BaseURL:  "https://openrouter.ai/api/v1",
			}
			g = genkit.Init(ctx, genkit.WithPlugins(openrouterPlugin))
			llmOn = true
			slog.Info("genkit brain initialized", "provider", "openrouter", "model", modelID)
		} else {
			g = genkit.Init(ctx)
			slog.Warn("OpenRouter API key missing; using deterministic fallback")
		}

	case "google", "":
		if apiKey != "" {
			_ = os.Setenv("GEMINI_API_KEY", apiKey)
			g = genkit.Init(ctx,
				genkit.WithPlugins(&googlegenai.GoogleAI{}),
				genkit.WithDefaultModel("googleai/"+modelID),
			)
			llmOn = true
			slog.Info("genkit brain initialized", "provider", "google", "model", "googleai/"+modelID)
		} else {
			g = genkit.Init(ctx)
			slog.Warn("Google API key missing; using deterministic fallback")
		}

	default:
		g = genkit.Init(ctx)
		slog.Warn("unknown LLM provider, using deterministic fallback", "provider", provider)
	}

	toolRegistry := tools.NewRegistry(cfg.Policy, cfg.APIKeys, cfg.PreferredSearch, store)
	toolRegistry.RegisterAll(g)

	// Create the brain struct so closures below can capture it.
	brain := &GenkitBrain{
		g:     g,
		store: store,
		tools: toolRegistry,
		cfg:   cfg,
		llmOn: llmOn,

		sanitizer:    safety.NewSanitizer(),
		leakDetector: safety.NewLeakDetector(),

		loadedSkills: map[string]*skillEntry{},
	}

	// Initialize compactor.
	brain.compactor = NewCompactor(store, brain, provider, modelID, CompactorConfig{
		ThresholdRatio: 0.75,
		KeepRecent:     10,
	})

	// Register the use_skill tool: lets the LLM invoke a registered skill by name.
	useSkillTool := genkit.DefineTool(g, "use_skill",
		"Invoke a registered skill by name. Returns the skill's instructions and activation status. Use this when the user asks to use a specific skill.",
		func(ctx *ai.ToolContext, input tools.UseSkillInput) (tools.UseSkillOutput, error) {
			// Policy check: require skill.inject capability.
			if brain.cfg.Policy == nil || !brain.cfg.Policy.AllowCapability("skill.inject") {
				return tools.UseSkillOutput{}, fmt.Errorf("policy denied capability %q", "skill.inject")
			}

			name := strings.TrimSpace(input.SkillName)
			if name == "" {
				return tools.UseSkillOutput{}, fmt.Errorf("skill_name must be non-empty")
			}
			key := strings.ToLower(name)

			// Per-turn deduplication: return cached result if skill was already
			// injected during this Respond/Stream call.
			if sc := getSkillCache(ctx); sc != nil {
				sc.mu.Lock()
				if cached, ok := sc.items[key]; ok {
					sc.mu.Unlock()
					return cached, nil
				}
				sc.mu.Unlock()
			}

			brain.skillMu.RLock()
			entry := brain.loadedSkills[key]
			brain.skillMu.RUnlock()

			if entry == nil {
				return tools.UseSkillOutput{}, fmt.Errorf("skill not found: %s", name)
			}

			// Load instructions on demand.
			if err := brain.ensureSkillInstructionsLoaded(ctx, key); err != nil {
				return tools.UseSkillOutput{}, fmt.Errorf("load skill instructions: %w", err)
			}

			brain.skillMu.RLock()
			entry = brain.loadedSkills[key]
			brain.skillMu.RUnlock()

			result := tools.UseSkillOutput{
				SkillName:    entry.Name,
				Output:       "activated",
				Instructions: entry.Instructions,
			}

			// Cache the result for this turn.
			if sc := getSkillCache(ctx); sc != nil {
				sc.mu.Lock()
				sc.items[key] = result
				sc.mu.Unlock()
			}

			return result, nil
		},
	)
	toolRegistry.Tools = append(toolRegistry.Tools, useSkillTool)

	var availableProviders []string
	for _, p := range toolRegistry.Providers {
		if p.Available() {
			availableProviders = append(availableProviders, p.Name())
		}
	}
	slog.Info("brain tools registered", "tools", len(toolRegistry.Tools), "search_providers", availableProviders)
	if len(availableProviders) == 1 && availableProviders[0] == "duckduckgo" {
		slog.Info("set api_keys.brave_search or api_keys.perplexity_search in config.yaml for better search results")
	}

	return brain
}

func defaultModelForProvider(provider string) string {
	switch provider {
	case "anthropic":
		return "claude-3-5-sonnet-20241022"
	case "openai":
		return "gpt-4o-mini"
	case "openai_compatible":
		return "gpt-4o-mini"
	case "openrouter":
		return "anthropic/claude-sonnet-4-5-20250929"
	case "google", "":
		return "gemini-2.5-flash"
	default:
		return "gemini-2.5-flash"
	}
}

func envAPIKeyForProvider(provider string) string {
	switch provider {
	case "anthropic":
		return os.Getenv("ANTHROPIC_API_KEY")
	case "openai":
		return os.Getenv("OPENAI_API_KEY")
	case "openai_compatible":
		return os.Getenv("OPENAI_API_KEY")
	case "openrouter":
		return os.Getenv("OPENROUTER_API_KEY")
	case "google", "":
		if k := os.Getenv("GEMINI_API_KEY"); k != "" {
			return k
		}
		return os.Getenv("GOOGLE_API_KEY")
	default:
		return ""
	}
}

func modelNameForProvider(provider, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		model = defaultModelForProvider(provider)
	}
	switch provider {
	case "anthropic":
		return "anthropic/" + model
	case "openai":
		return "openai/" + model
	case "openai_compatible":
		return model
	case "openrouter":
		return model // OpenRouter uses full model names like "anthropic/claude-sonnet-4-5-20250929"
	case "google", "":
		return "googleai/" + model
	default:
		return "googleai/" + model
	}
}

func (b *GenkitBrain) Genkit() *genkit.Genkit {
	return b.g
}

func (b *GenkitBrain) Registry() *tools.Registry {
	return b.tools
}

// Respond generates an LLM response for the given session and content.
// It loads session history, builds the message context, and calls Gemini
// with registered tools available for autonomous use.
func (b *GenkitBrain) Respond(ctx context.Context, sessionID, content string) (string, error) {
	// Attach per-turn skill deduplication cache to context.
	ctx, _ = withSkillCache(ctx)

	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "", fmt.Errorf("empty content")
	}

	// Safety check: detect prompt injection attempts.
	if b.sanitizer != nil {
		if err := b.sanitizer.Check(trimmed).MustAllow(); err != nil {
			slog.Warn("prompt injection blocked", "session_id", sessionID, "error", err)
			return "", fmt.Errorf("input blocked by safety filter: %w", err)
		}
	}

	// US-3 deterministic fast-path: if random skill is loaded and user asks
	// for a random number, invoke the hot-swapped module immediately.
	if b.isRandomRequest(trimmed) {
		if out, ok := b.respondWithRandomSkill(ctx); ok {
			return out, nil
		}
	}

	// US-2 deterministic multi-step research path:
	// Search -> Read -> Synthesize with task traceability in SQLite.
	if b.isPriceComparisonRequest(trimmed) {
		if out, err := b.runPriceComparisonWorkflow(ctx, sessionID, trimmed); err == nil {
			return out, nil
		} else {
			slog.Warn("price comparison workflow fallback to llm", "error", err)
		}
	}

	// Skill progressive disclosure:
	// - Match skill names against prompt
	// - If allowed by policy, load full SKILL.md instructions on activation
	// - In non-LLM mode, return a deterministic injected response for verification
	injectedSkillKey := ""
	if key := b.matchSkillForPrompt(trimmed); key != "" {
		// GC-SPEC-SKL: Instruction injection must be explicitly enabled by policy.
		if b.cfg.Policy != nil && b.cfg.Policy.AllowCapability("skill.inject") {
			if err := b.ensureSkillInstructionsLoaded(ctx, key); err != nil {
				slog.Warn("skill activation failed", "skill", key, "error", err)
			} else {
				injectedSkillKey = key
			}
		} else {
			slog.Info("skill injection denied by policy", "skill", key)
		}
	}
	if injectedSkillKey != "" && !b.llmOn {
		if entry := b.skillByName(injectedSkillKey); entry != nil && entry.InstructionsLoaded {
			return fmt.Sprintf("Skill injected: %s\n\n%s", entry.Name, entry.Instructions), nil
		}
	}

	// Build conversation history from DB (compacted if necessary)
	history, err := b.compactor.CompactIfNeeded(ctx, sessionID)
	if err != nil {
		slog.Warn("failed to load/compact session history", "session_id", sessionID, "error", err)
		// Continue without history rather than failing
	}

	// Build generate options
	opts := []ai.GenerateOption{
		ai.WithPrompt(trimmed),
	}

	// Add system prompt from SOUL.md if available (read-lock for hot-reload safety).
	b.soulMu.RLock()
	systemPrompt := strings.TrimSpace(b.cfg.Soul)
	b.soulMu.RUnlock()
	if systemPrompt == "" {
		systemPrompt = defaultSystemPrompt(b.cfg.AgentName)
	}
	if injectedSkillKey != "" {
		if entry := b.skillByName(injectedSkillKey); entry != nil && entry.InstructionsLoaded && strings.TrimSpace(entry.Instructions) != "" {
			systemPrompt = systemPrompt + "\n\n" + formatSkillInjection(entry.Name, entry.Instructions)
		}
	}
	opts = append(opts, ai.WithSystem(systemPrompt))

	// Add conversation history as messages
	appendHistory := func(o []ai.GenerateOption) []ai.GenerateOption {
		if len(history) > 0 {
			if msgs := historyToMessages(history); len(msgs) > 0 {
				o = append(o, ai.WithMessages(msgs...))
			}
		}
		return o
	}
	opts = appendHistory(opts)

	// Add tools for autonomous use
	if len(b.tools.Tools) > 0 {
		opts = append(opts, ai.WithTools(b.tools.Tools...))
		opts = append(opts, ai.WithMaxTurns(3))
	}

	if !b.llmOn {
		return "I can answer with full LLM reasoning after an API key is configured.", nil
	}

	// Build model name based on provider and prepend to options
	modelName := modelNameForProvider(strings.ToLower(b.cfg.Provider), b.cfg.Model)
	modelOpts := []ai.GenerateOption{ai.WithModelName(modelName)}
	modelOpts = append(modelOpts, opts...)

	resp, err := genkit.Generate(ctx, b.g, modelOpts...)
	if err != nil {
		slog.Error("genkit generate failed", "error", err, "session_id", sessionID)
		// If generation failed with tools, retry without tools as fallback
		if len(b.tools.Tools) > 0 {
			slog.Info("retrying without tools")
			fallbackOpts := appendHistory([]ai.GenerateOption{
				ai.WithPrompt(trimmed),
				ai.WithSystem(defaultFallbackSystemPrompt(b.cfg.AgentName)),
			})
			resp, err = genkit.Generate(ctx, b.g, fallbackOpts...)
			if err != nil {
				return "", fmt.Errorf("genkit generate (fallback): %w", err)
			}
			reply := resp.Text()
			// Scan fallback output for credential leaks (warning only).
			if b.leakDetector != nil {
				if findings := b.leakDetector.Scan(reply); len(findings) > 0 {
					slog.Warn("leak detector triggered on LLM output", "session_id", sessionID, "findings_count", len(findings))
				}
			}
			return reply, nil
		}
		return "", fmt.Errorf("genkit generate: %w", err)
	}

	reply := resp.Text()
	// Scan output for credential leaks (warning only).
	if b.leakDetector != nil {
		if findings := b.leakDetector.Scan(reply); len(findings) > 0 {
			slog.Warn("leak detector triggered on LLM output", "session_id", sessionID, "findings_count", len(findings))
		}
	}
	return reply, nil
}

// Stream generates an LLM response with streaming support.
// The onChunk callback is invoked for each streaming chunk.
func (b *GenkitBrain) Stream(ctx context.Context, sessionID, content string, onChunk func(content string) error) error {
	// Attach per-turn skill deduplication cache to context.
	ctx, _ = withSkillCache(ctx)

	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return fmt.Errorf("empty content")
	}

	// Safety check: detect prompt injection attempts (same as Respond).
	if b.sanitizer != nil {
		if err := b.sanitizer.Check(trimmed).MustAllow(); err != nil {
			slog.Warn("prompt injection blocked (stream)", "session_id", sessionID, "error", err)
			return fmt.Errorf("input blocked by safety filter: %w", err)
		}
	}

	// Skill progressive disclosure (same as Respond):
	injectedSkillKey := ""
	if key := b.matchSkillForPrompt(trimmed); key != "" {
		if b.cfg.Policy != nil && b.cfg.Policy.AllowCapability("skill.inject") {
			if err := b.ensureSkillInstructionsLoaded(ctx, key); err != nil {
				slog.Warn("skill activation failed (stream)", "skill", key, "error", err)
			} else {
				injectedSkillKey = key
			}
		} else {
			slog.Info("skill injection denied by policy (stream)", "skill", key)
		}
	}

	if !b.llmOn {
		// Non-streaming fallback
		if injectedSkillKey != "" {
			if entry := b.skillByName(injectedSkillKey); entry != nil && entry.InstructionsLoaded {
				reply := fmt.Sprintf("Skill injected: %s\n\n%s", entry.Name, entry.Instructions)
				_ = onChunk(reply)
				return nil
			}
		}
		reply := "I can answer with full LLM reasoning after an API key is configured."
		_ = onChunk(reply)
		return nil
	}

	// Build conversation history from DB (compacted if necessary)
	history, err := b.compactor.CompactIfNeeded(ctx, sessionID)
	if err != nil {
		slog.Warn("failed to load/compact session history for streaming", "session_id", sessionID, "error", err)
	}

	// Build system prompt (read-lock for hot-reload safety).
	b.soulMu.RLock()
	systemPrompt := strings.TrimSpace(b.cfg.Soul)
	b.soulMu.RUnlock()
	if systemPrompt == "" {
		systemPrompt = defaultSystemPrompt(b.cfg.AgentName)
	}
	if injectedSkillKey != "" {
		if entry := b.skillByName(injectedSkillKey); entry != nil && entry.InstructionsLoaded && strings.TrimSpace(entry.Instructions) != "" {
			systemPrompt = systemPrompt + "\n\n" + formatSkillInjection(entry.Name, entry.Instructions)
		}
	}

	// Build generate options
	opts := []ai.GenerateOption{
		ai.WithPrompt(trimmed),
		ai.WithSystem(systemPrompt),
	}

	// Add conversation history
	if len(history) > 0 {
		if msgs := historyToMessages(history); len(msgs) > 0 {
			opts = append(opts, ai.WithMessages(msgs...))
		}
	}

	// Add tools (disable streaming for tool calls - fall back to non-streaming)
	if len(b.tools.Tools) > 0 {
		opts = append(opts, ai.WithTools(b.tools.Tools...))
		opts = append(opts, ai.WithMaxTurns(3))
	}

	// Build model name
	modelName := modelNameForProvider(strings.ToLower(b.cfg.Provider), b.cfg.Model)
	modelOpts := []ai.GenerateOption{ai.WithModelName(modelName)}
	modelOpts = append(modelOpts, opts...)

	// Stream using Genkit's GenerateStream
	stream := genkit.GenerateStream(ctx, b.g, modelOpts...)

	var fullReply strings.Builder
	for streamVal, err := range stream {
		if err != nil {
			return fmt.Errorf("stream error: %w", err)
		}
		if streamVal.Chunk != nil {
			for _, part := range streamVal.Chunk.Content {
				if part.Kind == ai.PartText && part.Text != "" {
					if err := onChunk(part.Text); err != nil {
						return err
					}
					fullReply.WriteString(part.Text)
				}
			}
		}
		if streamVal.Done && streamVal.Response != nil {
			// Final response - also save to history
			reply := streamVal.Response.Text()
			if reply != "" && fullReply.Len() == 0 {
				if err := b.store.AddHistory(ctx, sessionID, "assistant", reply, tokenutil.EstimateTokens(reply)); err != nil {
					slog.Warn("failed to save streaming response to history", "error", err)
				}
			}
		}
	}

	// Save final response to history if we accumulated chunks
	if fullReply.Len() > 0 {
		// Scan for credential leaks in streaming output.
		if b.leakDetector != nil {
			if findings := b.leakDetector.Scan(fullReply.String()); len(findings) > 0 {
				slog.Warn("leak detector triggered on streaming output", "session_id", sessionID, "findings_count", len(findings))
			}
		}
		if err := b.store.AddHistory(ctx, sessionID, "assistant", fullReply.String(), tokenutil.EstimateTokens(fullReply.String())); err != nil {
			slog.Warn("failed to save streaming response to history", "error", err)
		}
	}

	return nil
}

func defaultSystemPrompt(agentName string) string {
	name := strings.TrimSpace(agentName)
	if name == "" {
		name = "GoClaw"
	}
	// Keep this prompt stable and tool-forward; don't hardcode the app name when a custom agent name is set.
	return fmt.Sprintf(
		"You are %s, an autonomous AI agent. Act decisively: when the user asks you to search, read, or look something up, use your tools immediately — never ask for confirmation first. If a search returns no results, retry with different keywords automatically. Provide accurate, well-sourced answers and cite your sources.",
		name,
	)
}

func defaultFallbackSystemPrompt(agentName string) string {
	name := strings.TrimSpace(agentName)
	if name == "" {
		name = "GoClaw"
	}
	return fmt.Sprintf(
		"You are %s, an autonomous AI agent. Answer the following question using your training knowledge. Be helpful and accurate.",
		name,
	)
}

// historyToMessages converts persistence history items to Genkit messages.
func historyToMessages(items []persistence.HistoryItem) []*ai.Message {
	var msgs []*ai.Message
	for _, item := range items {
		var role ai.Role
		switch item.Role {
		case "user":
			role = ai.RoleUser
		case "assistant":
			role = ai.RoleModel
		case "system":
			role = ai.RoleSystem
		case "tool":
			role = ai.RoleTool
		default:
			continue
		}
		msgs = append(msgs, &ai.Message{
			Role:    role,
			Content: []*ai.Part{ai.NewTextPart(item.Content)},
		})
	}
	return msgs
}

// Providers returns the ordered list of search providers from the tool registry.
func (b *GenkitBrain) Providers() []tools.SearchProvider {
	return b.tools.Providers
}

// UpdateSystemPrompt updates the SOUL content used as system prompt.
// Thread-safe for concurrent access from hot-reload and Respond/Stream.
func (b *GenkitBrain) UpdateSystemPrompt(soul string) {
	b.soulMu.Lock()
	defer b.soulMu.Unlock()
	b.cfg.Soul = soul
}

func (b *GenkitBrain) SetWASMHost(host *wasm.Host) {
	b.skillMu.Lock()
	defer b.skillMu.Unlock()
	b.wasmHost = host
}

func (b *GenkitBrain) RegisterSkill(name string) {
	b.skillMu.Lock()
	defer b.skillMu.Unlock()
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return
	}
	if _, ok := b.loadedSkills[key]; ok {
		return
	}
	b.loadedSkills[key] = &skillEntry{
		Name:        name,
		Description: "WASM skill",
		Type:        "wasm",
	}
}

// RegisterLoadedSkills registers eligible skills as metadata-only entries.
// Full SKILL.md instructions are loaded on-demand when a skill activates.
func (b *GenkitBrain) RegisterLoadedSkills(items []skills.LoadedSkill) {
	b.skillMu.Lock()
	defer b.skillMu.Unlock()

	for _, ls := range items {
		if !ls.Eligible {
			continue
		}
		// Directory name is treated as the canonical skill name (TODO Phase 2.2).
		name := filepath.Base(strings.TrimSpace(ls.SourceDir))
		if name == "." || name == "" {
			name = strings.TrimSpace(ls.Skill.Name)
		}
		key := strings.ToLower(name)
		if key == "" {
			continue
		}
		typ := "instruction"
		if strings.TrimSpace(ls.Skill.Script) != "" {
			typ = "legacy"
		}
		b.loadedSkills[key] = &skillEntry{
			Name:        name,
			Description: strings.TrimSpace(ls.Skill.Description),
			Type:        typ,
			Source:      strings.TrimSpace(ls.Source),
			SourceDir:   strings.TrimSpace(ls.SourceDir),
			// Metadata-only at startup:
			InstructionsLoaded: false,
			Instructions:       "",
		}
	}
}

// ReplaceLoadedSkills atomically replaces the non-WASM skill catalog
// (instruction/legacy), preserving any registered WASM modules.
// The write lock is held for the entire operation to prevent a transient
// window where the skill catalog is empty.
func (b *GenkitBrain) ReplaceLoadedSkills(items []skills.LoadedSkill) {
	b.skillMu.Lock()
	defer b.skillMu.Unlock()

	// Phase 1: delete existing instruction/legacy entries.
	for key, entry := range b.loadedSkills {
		if entry == nil {
			continue
		}
		if entry.Type == "instruction" || entry.Type == "legacy" {
			delete(b.loadedSkills, key)
		}
	}

	// Phase 2: insert new entries (inlined from RegisterLoadedSkills).
	for _, ls := range items {
		if !ls.Eligible {
			continue
		}
		name := filepath.Base(strings.TrimSpace(ls.SourceDir))
		if name == "." || name == "" {
			name = strings.TrimSpace(ls.Skill.Name)
		}
		key := strings.ToLower(name)
		if key == "" {
			continue
		}
		typ := "instruction"
		if strings.TrimSpace(ls.Skill.Script) != "" {
			typ = "legacy"
		}
		b.loadedSkills[key] = &skillEntry{
			Name:               name,
			Description:        strings.TrimSpace(ls.Skill.Description),
			Type:               typ,
			Source:             strings.TrimSpace(ls.Source),
			SourceDir:          strings.TrimSpace(ls.SourceDir),
			InstructionsLoaded: false,
			Instructions:       "",
		}
	}
}

func (b *GenkitBrain) skillByName(name string) *skillEntry {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return nil
	}
	b.skillMu.RLock()
	defer b.skillMu.RUnlock()
	return b.loadedSkills[key]
}

// isWordBoundary reports whether the byte at position i in s is a word boundary
// (i.e., not a letter or digit). Positions before the start or after the end of
// the string are treated as boundaries.
func isWordBoundary(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return true
	}
	ch := s[i]
	if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' {
		return false
	}
	return true
}

// skillMatchesPrompt checks whether the skill key appears in the lowercased prompt
// with word boundaries on both sides.
func skillMatchesPrompt(lower, key string) bool {
	start := 0
	for {
		idx := strings.Index(lower[start:], key)
		if idx < 0 {
			return false
		}
		absIdx := start + idx
		endIdx := absIdx + len(key)
		if isWordBoundary(lower, absIdx-1) && isWordBoundary(lower, endIdx) {
			return true
		}
		start = absIdx + 1
	}
}

// minAutoMatchLen is the minimum skill name length for automatic prompt matching.
// Shorter names (e.g. "go", "ls") are only activated via the use_skill tool.
const minAutoMatchLen = 3

func (b *GenkitBrain) matchSkillForPrompt(prompt string) string {
	lower := strings.ToLower(prompt)

	b.skillMu.RLock()
	defer b.skillMu.RUnlock()

	best := ""
	for key, entry := range b.loadedSkills {
		if entry == nil {
			continue
		}
		if entry.Type != "instruction" && entry.Type != "legacy" {
			continue
		}
		if key == "" {
			continue
		}
		if len(key) < minAutoMatchLen {
			continue
		}
		if skillMatchesPrompt(lower, key) {
			if len(key) > len(best) || (len(key) == len(best) && key < best) {
				best = key
			}
		}
	}
	return best
}

func (b *GenkitBrain) ensureSkillInstructionsLoaded(ctx context.Context, key string) error {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return fmt.Errorf("empty skill key")
	}

	b.skillMu.RLock()
	entry := b.loadedSkills[key]
	if entry == nil {
		b.skillMu.RUnlock()
		return fmt.Errorf("skill not found: %s", key)
	}
	if entry.InstructionsLoaded {
		b.skillMu.RUnlock()
		return nil
	}
	sourceDir := strings.TrimSpace(entry.SourceDir)
	b.skillMu.RUnlock()
	if sourceDir == "" {
		return fmt.Errorf("skill %s missing SourceDir", key)
	}

	data, err := os.ReadFile(filepath.Join(sourceDir, "SKILL.md"))
	if err != nil {
		return fmt.Errorf("read SKILL.md: %w", err)
	}
	parsed, err := legacy.ParseSkillMD(data)
	if err != nil {
		return fmt.Errorf("parse SKILL.md: %w", err)
	}
	instructions := strings.TrimSpace(parsed.Instructions)
	if instructions == "" {
		instructions = strings.TrimSpace(parsed.Script)
	}
	instructions = expandSkillFileReferences(sourceDir, instructions)

	b.skillMu.Lock()
	// Skill could have been removed/reloaded while IO happened.
	entry = b.loadedSkills[key]
	if entry != nil {
		entry.Instructions = instructions
		entry.InstructionsLoaded = true
	}
	b.skillMu.Unlock()
	return nil
}

func formatSkillInjection(name string, instructions string) string {
	name = strings.TrimSpace(name)
	instructions = strings.TrimSpace(instructions)
	if name == "" || instructions == "" {
		return ""
	}
	return fmt.Sprintf("# Skill: %s\n\n%s", name, instructions)
}

var skillFileRefRE = regexp.MustCompile(`(?m)(scripts|references|assets)/[A-Za-z0-9._\\-\\/]+`)

func expandSkillFileReferences(skillDir string, instructions string) string {
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return ""
	}

	refs := skillFileRefRE.FindAllString(instructions, -1)
	if len(refs) == 0 {
		return instructions
	}

	seen := make(map[string]struct{}, len(refs))
	var unique []string
	for _, r := range refs {
		r = filepath.Clean(r)
		if r == "." || r == "" {
			continue
		}
		if strings.HasPrefix(r, ".."+string(filepath.Separator)) || r == ".." || filepath.IsAbs(r) {
			continue
		}
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		unique = append(unique, r)
	}
	if len(unique) == 0 {
		return instructions
	}

	baseAbs, err := filepath.Abs(skillDir)
	if err != nil {
		return instructions
	}

	const maxBytesPerFile = 64 << 10 // 64 KB per file
	const maxBytesTotal = 256 << 10  // 256 KB aggregate limit

	var sb strings.Builder
	sb.WriteString(instructions)
	totalInlined := 0
	for _, rel := range unique {
		abs := filepath.Join(baseAbs, rel)
		abs, err := filepath.Abs(abs)
		if err != nil {
			continue
		}
		if abs != baseAbs && !strings.HasPrefix(abs, baseAbs+string(filepath.Separator)) {
			continue
		}
		if totalInlined >= maxBytesTotal {
			slog.Warn("skill file reference aggregate limit reached, skipping remaining files",
				"limit_bytes", maxBytesTotal, "skipped_file", rel)
			break
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			// Keep deterministic: reference exists in instructions but file missing.
			sb.WriteString("\n\n")
			sb.WriteString("### File: ")
			sb.WriteString(rel)
			sb.WriteString("\n\n")
			sb.WriteString("(missing)\n")
			continue
		}
		if len(data) > maxBytesPerFile {
			data = data[:maxBytesPerFile]
		}
		remaining := maxBytesTotal - totalInlined
		if len(data) > remaining {
			data = data[:remaining]
			slog.Warn("skill file reference aggregate limit reached during file inlining",
				"limit_bytes", maxBytesTotal, "truncated_file", rel)
		}
		totalInlined += len(data)
		sb.WriteString("\n\n")
		sb.WriteString("### File: ")
		sb.WriteString(rel)
		sb.WriteString("\n\n```text\n")
		sb.WriteString(string(data))
		if len(data) == 0 || data[len(data)-1] != '\n' {
			sb.WriteString("\n")
		}
		sb.WriteString("```\n")
	}
	return sb.String()
}

func (b *GenkitBrain) isRandomRequest(prompt string) bool {
	lower := strings.ToLower(prompt)
	return strings.Contains(lower, "random number") || strings.Contains(lower, "generate a random")
}

func (b *GenkitBrain) respondWithRandomSkill(ctx context.Context) (string, bool) {
	b.skillMu.RLock()
	_, hasRandom := b.loadedSkills["random"]
	host := b.wasmHost
	b.skillMu.RUnlock()

	if !hasRandom {
		return "", false
	}
	if host != nil {
		if n, err := host.InvokeModuleRandom(ctx, "random"); err == nil {
			return fmt.Sprintf("Random number: %d (via random skill)", n), true
		}
	}

	// Safe fallback if module export signature doesn't match expected ABI.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	return fmt.Sprintf("Random number: %d (via random skill fallback)", rng.Int31n(1000)), true
}

// priceCompareREs matches patterns like "X vs Y", "X versus Y", "X compared to Y".
// priceCompareRE matches structured comparison patterns like "iPhone 15 vs Galaxy S24".
var priceCompareRE = regexp.MustCompile(`(?i)\b([A-Za-z][\w-]*(?:\s+\d[\w-]*)?)\s+(?:vs\.?|versus)\s+([A-Za-z][\w-]*(?:\s+\d[\w-]*)?)\b`)

// productNameREs extracts plausible product identifiers from comparison prompts.
var productNameREs = []*regexp.Regexp{
	regexp.MustCompile(`\b([A-Za-z]{3,}\s+\d[\w-]*)\b`), // "RTX 5090", "GTX 1080"
	regexp.MustCompile(`\b([A-Za-z]+-\d[\w-]*)\b`),      // "i9-13900K"
	regexp.MustCompile(`\b(\d{4,}[A-Za-z]*)\b`),         // "4090", "5090Ti"
}

func (b *GenkitBrain) isPriceComparisonRequest(prompt string) bool {
	lower := strings.ToLower(prompt)
	hasPriceKeyword := strings.Contains(lower, "price") || strings.Contains(lower, "cost")
	hasCompareKeyword := strings.Contains(lower, "compare") || strings.Contains(lower, "vs") ||
		strings.Contains(lower, "versus") || strings.Contains(lower, "compared to")
	return hasPriceKeyword && hasCompareKeyword
}

// extractComparisonProducts extracts two product names from a comparison prompt.
// It first tries structured patterns (X vs Y, price of X and Y), then falls back
// to extracting product-like identifiers (tokens containing digits).
func extractComparisonProducts(prompt string) (string, string) {
	if matches := priceCompareRE.FindStringSubmatch(prompt); len(matches) >= 3 {
		a := strings.TrimSpace(matches[1])
		b := strings.TrimSpace(matches[2])
		if a != "" && b != "" {
			return a, b
		}
	}
	// Fallback: extract product identifiers (tokens with digits like "RTX 5090", "4090").
	// Collect compound names first (e.g. "RTX 5090"), then standalone numbers,
	// suppressing standalone tokens that are substrings of already-found compounds.
	var compounds, standalones []string
	for _, m := range productNameREs[0].FindAllString(prompt, -1) {
		compounds = append(compounds, strings.TrimSpace(m))
	}
	for _, m := range productNameREs[1].FindAllString(prompt, -1) {
		compounds = append(compounds, strings.TrimSpace(m))
	}
	for _, m := range productNameREs[2].FindAllString(prompt, -1) {
		m = strings.TrimSpace(m)
		subsumed := false
		for _, c := range compounds {
			if strings.Contains(c, m) {
				subsumed = true
				break
			}
		}
		if !subsumed {
			standalones = append(standalones, m)
		}
	}
	products := append(compounds, standalones...)
	if len(products) >= 2 {
		return products[0], products[1]
	}
	return "", ""
}

func (b *GenkitBrain) runPriceComparisonWorkflow(ctx context.Context, sessionID, prompt string) (string, error) {
	productA, productB := extractComparisonProducts(prompt)
	if productA == "" || productB == "" {
		return "", fmt.Errorf("could not extract product names from comparison request")
	}

	queryA := productA + " price"
	queryB := productB + " price"

	searchA, err := b.tools.Search(ctx, queryA)
	_, _ = b.store.RecordToolTask(ctx, sessionID, "Search", queryA, marshalSearch(searchA), err)
	if err != nil {
		return "", err
	}
	searchB, err := b.tools.Search(ctx, queryB)
	_, _ = b.store.RecordToolTask(ctx, sessionID, "Search", queryB, marshalSearch(searchB), err)
	if err != nil {
		return "", err
	}

	var readSnippets []string
	readOnce := func(results tools.SearchOutput) {
		for _, r := range results.Results {
			if strings.TrimSpace(r.URL) == "" {
				continue
			}
			readOut, readErr := b.tools.Read(ctx, r.URL)
			_, _ = b.store.RecordToolTask(ctx, sessionID, "Read", r.URL, readOut.Content, readErr)
			if readErr == nil && readOut.Content != "" {
				readSnippets = append(readSnippets, readOut.Content)
			}
			break
		}
	}
	readOnce(searchA)
	readOnce(searchB)

	combined := marshalSearch(searchA) + "\n" + marshalSearch(searchB) + "\n" + strings.Join(readSnippets, "\n")
	prices := findDollarNumbers(combined)
	quoteA := firstPriceNear(combined, productA)
	quoteB := firstPriceNear(combined, productB)

	if quoteA == "" && len(prices) > 0 {
		quoteA = prices[0]
	}
	if quoteB == "" && len(prices) > 1 {
		quoteB = prices[1]
	}

	srcA := firstURL(searchA)
	srcB := firstURL(searchB)
	if quoteA == "" || quoteB == "" {
		return "", fmt.Errorf("could not extract both prices from research results")
	}

	answer := fmt.Sprintf(
		"Based on current fetched results, %s is around %s and %s is around %s.\n"+
			"Sources: %s (%s), %s (%s).",
		productA, quoteA,
		productB, quoteB,
		productA, srcA,
		productB, srcB,
	)
	return answer, nil
}

func marshalSearch(out tools.SearchOutput) string {
	b, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(b)
}

var dollarRE = regexp.MustCompile(`\$\s?[0-9][0-9,]*(?:\.[0-9]+)?`)

func findDollarNumbers(in string) []string {
	return dollarRE.FindAllString(in, -1)
}

func firstPriceNear(in, anchor string) string {
	lines := strings.Split(in, "\n")
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), strings.ToLower(anchor)) {
			if prices := findDollarNumbers(line); len(prices) > 0 {
				return prices[0]
			}
		}
	}
	return ""
}

func firstURL(out tools.SearchOutput) string {
	for _, r := range out.Results {
		if strings.TrimSpace(r.URL) != "" {
			return r.URL
		}
	}
	return "n/a"
}
