package engine

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/basket/go-claw/internal/config"
	"github.com/basket/go-claw/internal/mcp"
	"github.com/basket/go-claw/internal/memory"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
	"github.com/basket/go-claw/internal/safety"
	"github.com/basket/go-claw/internal/sandbox/legacy"
	"github.com/basket/go-claw/internal/sandbox/wasm"
	"github.com/basket/go-claw/internal/shared"
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
	// Normalize openai_compatible to openai for BuiltinModels lookup
	if provider == "openai_compatible" {
		provider = "openai"
	}

	// Use the first model from BuiltinModels as the default.
	// If no models are defined for the provider, return empty string.
	models, ok := config.BuiltinModels[provider]
	if !ok || len(models) == 0 {
		return "" // No default available, fail gracefully
	}
	return models[0].ID // First model = most capable
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

	// Extract agent ID from context for per-agent history isolation.
	agentID := shared.AgentID(ctx)
	if agentID == "" {
		agentID = shared.DefaultAgentID
	}

	// Build conversation history from DB (compacted if necessary)
	history, err := b.compactor.CompactIfNeeded(ctx, sessionID, agentID)
	if err != nil {
		slog.Warn("failed to load/compact session history", "session_id", sessionID, "agent_id", agentID, "error", err)
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

	// Inject core memory block (top memories by relevance)
	if topMemories, err := b.store.ListTopMemories(ctx, agentID, 10); err == nil && len(topMemories) > 0 {
		// Convert to memory.KeyValue format
		var kvs []memory.KeyValue
		for _, m := range topMemories {
			kvs = append(kvs, memory.KeyValue{
				Key:            m.Key,
				Value:          m.Value,
				RelevanceScore: m.RelevanceScore,
			})
			// Touch each memory to update access stats (non-blocking)
			go func(k string) {
				_ = b.store.TouchMemory(ctx, agentID, k)
			}(m.Key)
		}
		coreBlock := memory.NewCoreMemoryBlock(kvs)
		if formatted := coreBlock.Format(); formatted != "" {
			systemPrompt = systemPrompt + "\n\n" + formatted
		}
	} else if err != nil && agentID != shared.DefaultAgentID {
		slog.Warn("failed to load core memories", "agent_id", agentID, "error", err)
	}

	// Inject pinned files and text context
	if pins, err := b.store.ListPins(ctx, agentID); err == nil && len(pins) > 0 {
		pinMgr := memory.NewPinManager(b.store)
		if formatted, _, err := pinMgr.FormatPins(ctx, agentID); err == nil && formatted != "" {
			systemPrompt = systemPrompt + "\n\n" + formatted
		}
	} else if err != nil && agentID != shared.DefaultAgentID {
		slog.Warn("failed to load pinned context", "agent_id", agentID, "error", err)
	}

	// Inject shared team knowledge (memories and pins from other agents)
	sharedCtx := memory.NewSharedContext(b.store)
	if formatted, _, err := sharedCtx.Format(ctx, agentID); err == nil && formatted != "" {
		systemPrompt = systemPrompt + "\n\n" + formatted
	} else if err != nil && agentID != shared.DefaultAgentID {
		slog.Warn("failed to load shared knowledge context", "agent_id", agentID, "error", err)
	}

	// Decay memories once per session start (factor 0.95 = 5% decay)
	// Run async to not block response
	go func() {
		_ = b.store.DecayMemories(ctx, agentID, 0.95)
	}()

	// Escape % characters to prevent fmt.Sprintf corruption in ai.WithSystem().
	systemPrompt = strings.ReplaceAll(systemPrompt, "%", "%%")
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
				ai.WithSystem(systemPrompt), // Reuse the same soul-injected prompt
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

	// Extract agent ID from context for per-agent history isolation.
	agentID := shared.AgentID(ctx)
	if agentID == "" {
		agentID = shared.DefaultAgentID
	}

	// Build conversation history from DB (compacted if necessary)
	history, err := b.compactor.CompactIfNeeded(ctx, sessionID, agentID)
	if err != nil {
		slog.Warn("failed to load/compact session history for streaming", "session_id", sessionID, "agent_id", agentID, "error", err)
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
	// Escape % characters to prevent fmt.Sprintf corruption in ai.WithSystem().
	systemPrompt = strings.ReplaceAll(systemPrompt, "%", "%%")

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
	var doneReply string
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
			doneReply = streamVal.Response.Text()
		}
	}

	// Determine final reply: prefer accumulated chunks, fall back to Done response.
	finalReply := fullReply.String()
	if finalReply == "" {
		finalReply = doneReply
	}

	if finalReply != "" {
		// Scan for credential leaks in streaming output.
		if b.leakDetector != nil {
			if findings := b.leakDetector.Scan(finalReply); len(findings) > 0 {
				slog.Warn("leak detector triggered on streaming output", "session_id", sessionID, "findings_count", len(findings))
			}
		}
		if err := b.store.AddHistory(ctx, sessionID, agentID, "assistant", finalReply, tokenutil.EstimateTokens(finalReply)); err != nil {
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

	// Phase 2: insert new entries.
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

// RegisterMCPTools discovers and registers MCP tools for a specific agent.
// Calls manager.DiscoverTools to get tools allowed by policy.
// If discovery fails, logs a warning but continues (non-fatal).
func (b *GenkitBrain) RegisterMCPTools(ctx context.Context, agentID string, manager interface{}) error {
	// Import mcp package to use Manager type
	var mgr *mcp.Manager
	switch m := manager.(type) {
	case *mcp.Manager:
		mgr = m
	default:
		slog.Warn("invalid manager type for mcp tools", "agent", agentID)
		return nil
	}

	refs := tools.RegisterMCPTools(b.g, agentID, mgr)
	slog.Info("mcp tools registered for agent", "agent", agentID, "count", len(refs))
	return nil
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

	// Check file size before reading (prevent DoS via large SKILL.md).
	skillPath := filepath.Join(sourceDir, "SKILL.md")
	stat, err := os.Stat(skillPath)
	if err != nil {
		return fmt.Errorf("stat SKILL.md: %w", err)
	}
	const maxSkillMDSize = 1 << 20 // 1 MiB (matches skills/loader.go)
	if stat.Size() > maxSkillMDSize {
		return fmt.Errorf("SKILL.md too large: %d bytes (max %d)", stat.Size(), maxSkillMDSize)
	}

	data, err := os.ReadFile(skillPath)
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
	return fmt.Sprintf("Random number: %d (via random skill fallback)", rand.Int31n(1000)), true
}
