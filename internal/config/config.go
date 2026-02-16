package config

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ModelDef describes a model entry in the built-in models list.
type ModelDef struct {
	ID   string
	Desc string
}

// BuiltinModels maps provider IDs to their built-in model lists.
// This is the single source of truth for available models across all packages.
var BuiltinModels = map[string][]ModelDef{
	"google": {
		{"gemini-3-pro-preview", "Most capable, advanced reasoning"},
		{"gemini-3-flash-preview", "Balanced speed + frontier intelligence"},
		{"gemini-2.5-pro", "Strong reasoning, complex STEM tasks"},
		{"gemini-2.5-flash", "Fast, cost-effective"},
		{"gemini-2.5-flash-lite", "Ultra-fast, lowest cost"},
	},
	"anthropic": {
		{"claude-opus-4-6", "Most capable"},
		{"claude-sonnet-4-5-20250929", "Balanced performance"},
		{"claude-haiku-4-5-20251001", "Fast, cost-effective"},
	},
	"openai": {
		{"o3", "Advanced reasoning"},
		{"o4-mini", "Fast reasoning"},
		{"gpt-4o", "Versatile, multimodal"},
		{"gpt-4o-mini", "Fast, cost-effective"},
	},
	"openrouter": {
		{"anthropic/claude-sonnet-4-5-20250929", "Claude Sonnet (via OpenRouter)"},
		{"openai/gpt-4o", "GPT-4o (via OpenRouter)"},
		{"meta-llama/llama-3.1-70b-instruct", "Llama 3.1 70B"},
		{"mistralai/mistral-large-latest", "Mistral Large"},
	},
}

// ProviderConfig holds per-provider settings for multi-provider LLM support.
type ProviderConfig struct {
	APIKey  string   `yaml:"api_key"`
	BaseURL string   `yaml:"base_url"` // custom endpoint (e.g. OpenRouter)
	Models  []string `yaml:"models"`   // user-added models (merged with built-ins)
}

// LLMProviderConfig holds configuration for all LLM providers.
type LLMProviderConfig struct {
	// Provider names the active LLM provider: "google", "anthropic", "openai", "openai_compatible".
	Provider string `yaml:"provider"`

	// GoogleAI-specific config.
	GeminiModel string `yaml:"gemini_model"`

	// Anthropic-specific config.
	AnthropicModel string `yaml:"anthropic_model"`

	// OpenAI-specific config.
	OpenAIModel string `yaml:"openai_model"`

	// OpenAICompatible config.
	OpenAICompatibleProvider string `yaml:"openai_compatible_provider"` // provider name for model prefix
	OpenAICompatibleBaseURL  string `yaml:"openai_compatible_base_url"` // e.g. https://api.openai.com/v1

	// Failover config: ordered list of provider names to try when the primary fails.
	FallbackProviders []string `yaml:"fallback_providers"`

	// FailoverThreshold is the number of consecutive failures before a provider's
	// circuit breaker trips. Default 5.
	FailoverThreshold int `yaml:"failover_threshold"`

	// FailoverCooldownSeconds is the duration (in seconds) before a tripped circuit
	// breaker resets and the provider is retried. Default 300 (5 minutes).
	FailoverCooldownSeconds int `yaml:"failover_cooldown_seconds"`
}

type SkillsConfig struct {
	ProjectDir string   `yaml:"project_dir"`
	ExtraDirs  []string `yaml:"extra_dirs"`
	LegacyMode bool     `yaml:"legacy_mode"`
}

type ShellConfig struct {
	Sandbox        bool   `yaml:"sandbox"`
	SandboxImage   string `yaml:"sandbox_image"`
	SandboxMemory  int64  `yaml:"sandbox_memory_mb"`
	SandboxNetwork string `yaml:"sandbox_network"`
}

type ToolsConfig struct {
	Shell ShellConfig `yaml:"shell"`
}

type TelegramConfig struct {
	Token      string  `yaml:"token"`
	AllowedIDs []int64 `yaml:"allowed_ids"`
	Enabled    bool    `yaml:"enabled"`
}

type ChannelsConfig struct {
	Telegram TelegramConfig `yaml:"telegram"`
}

type MCPServerConfig struct {
	Name      string            `yaml:"name"`
	Command   string            `yaml:"command"`
	Args      []string          `yaml:"args"`
	Env       map[string]string `yaml:"env"`
	URL       string            `yaml:"url,omitempty"`       // SSE endpoint (v0.4)
	Transport string            `yaml:"transport,omitempty"` // "stdio" (default) or "sse" (v0.4)
	Timeout   string            `yaml:"timeout,omitempty"`   // e.g. "30s" (v0.4)
	Enabled   bool              `yaml:"enabled"`
}

// AgentMCPRef references an MCP server for per-agent use (v0.4).
// Name references a global server by name. Command/URL/Transport define an inline server.
type AgentMCPRef struct {
	Name      string            `yaml:"name"`
	Command   string            `yaml:"command,omitempty"`
	Args      []string          `yaml:"args,omitempty"`
	URL       string            `yaml:"url,omitempty"`
	Transport string            `yaml:"transport,omitempty"`
	Env       map[string]string `yaml:"env,omitempty"`
	Timeout   string            `yaml:"timeout,omitempty"`
}

type MCPConfig struct {
	Servers []MCPServerConfig `yaml:"servers"`
}

// AgentConfigEntry defines a named agent to create on startup.
type AgentConfigEntry struct {
	AgentID            string         `yaml:"agent_id"`
	DisplayName        string         `yaml:"display_name"`
	Provider           string         `yaml:"provider"`
	Model              string         `yaml:"model"`
	APIKeyEnv          string         `yaml:"api_key_env"`
	Soul               string         `yaml:"soul"`
	SoulFile           string         `yaml:"soul_file"`
	WorkerCount        int            `yaml:"worker_count"`
	TaskTimeoutSeconds int            `yaml:"task_timeout_seconds"`
	MaxQueueDepth      int            `yaml:"max_queue_depth"`
	SkillsFilter       []string       `yaml:"skills_filter"`
	PreferredSearch    string         `yaml:"preferred_search"`
	Capabilities       []string       `yaml:"capabilities,omitempty"`
	MCPServers         []AgentMCPRef  `yaml:"mcp_servers,omitempty"` // Per-agent MCP servers (v0.4)
}

type Config struct {
	HomeDir string `yaml:"-"`

	WorkerCount        int    `yaml:"worker_count"`
	TaskTimeoutSeconds int    `yaml:"task_timeout_seconds"`
	BindAddr           string `yaml:"bind_addr"`
	LogLevel           string `yaml:"log_level"`

	LLM LLMProviderConfig `yaml:"llm"`

	// Deprecated: use LLM.Provider instead.
	LLMProvider string `yaml:"llm_provider"`
	// Deprecated: use LLM.GeminiModel instead.
	GeminiModel string `yaml:"gemini_model"`
	// Deprecated: use LLMProviderAPIKey("google") instead.
	GeminiAPIKey string `yaml:"gemini_api_key"`

	// APIKeys holds centralized API keys for tools and integrations.
	// Keys: "brave_search", etc. Env vars override: BRAVE_API_KEY → api_keys["brave_search"].
	APIKeys map[string]string `yaml:"api_keys"`

	// Providers holds per-provider configuration (API keys, custom endpoints, extra models).
	Providers map[string]ProviderConfig `yaml:"providers"`

	AgentName  string `yaml:"agent_name"`
	AgentEmoji string `yaml:"agent_emoji"`

	SOUL   string `yaml:"-"`
	AGENTS string `yaml:"-"`

	// PreferredSearch names the search provider to try first (e.g. "perplexity_search").
	// Empty uses the default order: brave → perplexity → duckduckgo.
	PreferredSearch string `yaml:"preferred_search"`

	// AllowOrigins controls which Origin headers are accepted for browser WS connections (GC-SPEC-ACP-004).
	// Empty means local-only (no browser Origin required).
	AllowOrigins []string `yaml:"allow_origins"`

	// GC-SPEC-QUE-008: Maximum pending tasks before backpressure. 0 = unlimited.
	MaxQueueDepth int `yaml:"max_queue_depth"`

	// GC-SPEC-REL-005: Bounded drain timeout (seconds). 0 uses default (5s).
	DrainTimeoutSeconds int `yaml:"drain_timeout_seconds"`

	// GC-SPEC-DATA-005: Retention policy (days). 0 = no retention (keep forever).
	RetentionTaskEventsDays int `yaml:"retention_task_events_days"`
	RetentionAuditLogDays   int `yaml:"retention_audit_log_days"`
	RetentionMessagesDays   int `yaml:"retention_messages_days"`

	HeartbeatIntervalMinutes int `yaml:"heartbeat_interval_minutes"`

	// DelegationMaxHops limits delegation chain depth to prevent infinite recursion.
	// Validated: must be < WorkerCount for each agent to prevent deadlock.
	DelegationMaxHops int `yaml:"delegation_max_hops"`

	ContextLimits map[string]int `yaml:"context_limits"`

	Skills   SkillsConfig       `yaml:"skills"`
	Tools    ToolsConfig        `yaml:"tools"`
	Channels ChannelsConfig     `yaml:"channels"`
	MCP      MCPConfig          `yaml:"mcp"`
	Agents   []AgentConfigEntry `yaml:"agents"`
	Plans    []PlanConfig       `yaml:"plans"` // GC-SPEC-PDR-v4-Phase-4: Plans for workflows
	A2A      A2AConfig          `yaml:"a2a,omitempty"`

	NeedsGenesis bool `yaml:"-"`
}

// A2AConfig controls A2A agent card exposure.
// GC-SPEC-PDR-v7-Phase-4: A2A protocol configuration.
type A2AConfig struct {
	Enabled *bool `yaml:"enabled,omitempty"` // pointer to distinguish unset (default true) from false
}

// PlanConfig defines a named workflow in config.yaml.
// GC-SPEC-PDR-v4-Phase-4: Plan system configuration.
type PlanConfig struct {
	Name  string           `yaml:"name"`
	Steps []PlanStepConfig `yaml:"steps"`
}

// PlanStepConfig defines a step within a plan.
type PlanStepConfig struct {
	ID        string   `yaml:"id"`
	AgentID   string   `yaml:"agent_id"`
	Prompt    string   `yaml:"prompt"`
	DependsOn []string `yaml:"depends_on"`
}

// APIKey returns the value for the named API key, checking env overrides first.
// Env mapping: "brave_search" → BRAVE_API_KEY.
func (c Config) APIKey(name string) string {
	envMap := map[string]string{
		"brave_search":      "BRAVE_API_KEY",
		"perplexity_search": "PERPLEXITY_API_KEY",
	}
	if envVar, ok := envMap[name]; ok {
		if v := os.Getenv(envVar); v != "" {
			return v
		}
	}
	if c.APIKeys != nil {
		return c.APIKeys[name]
	}
	return ""
}

// LLMProviderAPIKey returns the API key for the specified LLM provider.
// Env vars take precedence: ANTHROPIC_API_KEY, OPENAI_API_KEY, GOOGLE_API_KEY.
func (c Config) LLMProviderAPIKey(provider string) string {
	envMap := map[string]string{
		"google":     "GOOGLE_API_KEY",
		"anthropic":  "ANTHROPIC_API_KEY",
		"openai":     "OPENAI_API_KEY",
		"openrouter": "OPENROUTER_API_KEY",
	}
	if envVar, ok := envMap[provider]; ok {
		if v := os.Getenv(envVar); v != "" {
			return v
		}
	}
	if c.Providers != nil {
		if p, ok := c.Providers[provider]; ok && p.APIKey != "" {
			return p.APIKey
		}
	}
	// Fallback to deprecated GeminiAPIKey for google
	if provider == "google" && c.GeminiAPIKey != "" {
		return c.GeminiAPIKey
	}
	return ""
}

// ResolveLLMConfig returns the effective LLM configuration, handling deprecated fields.
func (c Config) ResolveLLMConfig() (provider, model, apiKey string) {
	// Resolve provider
	if c.LLM.Provider != "" {
		provider = c.LLM.Provider
	} else if c.LLMProvider != "" {
		provider = c.LLMProvider
	} else {
		provider = "google"
	}

	// Resolve model
	switch provider {
	case "anthropic":
		if c.LLM.AnthropicModel != "" {
			model = c.LLM.AnthropicModel
		}
	case "openai":
		if c.LLM.OpenAIModel != "" {
			model = c.LLM.OpenAIModel
		}
	case "openai_compatible":
		if c.LLM.OpenAIModel != "" {
			model = c.LLM.OpenAIModel
		}
	case "openrouter":
		if c.LLM.OpenAIModel != "" {
			model = c.LLM.OpenAIModel
		}
	case "google":
		if c.LLM.GeminiModel != "" {
			model = c.LLM.GeminiModel
		} else if c.GeminiModel != "" {
			model = c.GeminiModel
		}
	}

	// Resolve API key
	apiKey = c.LLMProviderAPIKey(provider)

	return provider, model, apiKey
}

// ConfigPath returns the path to config.yaml within the given home directory.
func ConfigPath(homeDir string) string {
	return filepath.Join(homeDir, "config.yaml")
}

// loadRawConfig reads config.yaml into a generic map, returning an empty map if the file doesn't exist.
func loadRawConfig(path string) (map[string]interface{}, error) {
	raw := make(map[string]interface{})
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read config.yaml: %w", err)
	}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse config.yaml: %w", err)
		}
	}
	return raw, nil
}

// saveRawConfig marshals and writes a generic map back to config.yaml.
func saveRawConfig(path string, raw map[string]interface{}) error {
	out, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshal config.yaml: %w", err)
	}
	return os.WriteFile(path, out, 0o644)
}

// SetModel updates the LLM provider and model in config.yaml, preserving other settings.
func SetModel(homeDir, provider, model string) error {
	configPath := ConfigPath(homeDir)
	raw, err := loadRawConfig(configPath)
	if err != nil {
		return err
	}
	raw["llm_provider"] = provider
	raw["gemini_model"] = model
	return saveRawConfig(configPath, raw)
}

// SetAPIKey updates a single API key in config.yaml, preserving other settings.
func SetAPIKey(homeDir, name, value string) error {
	configPath := ConfigPath(homeDir)
	raw, err := loadRawConfig(configPath)
	if err != nil {
		return err
	}
	apiKeys, _ := raw["api_keys"].(map[string]interface{})
	if apiKeys == nil {
		apiKeys = make(map[string]interface{})
	}
	apiKeys[name] = value
	raw["api_keys"] = apiKeys
	return saveRawConfig(configPath, raw)
}

// AppendAgent adds an agent to config.yaml.
// WARNING: Round-trips through yaml.Marshal — strips comments, may reorder fields.
// TODO(v0.3): preserve user formatting via YAML-aware append.
func AppendAgent(configPath string, agent AgentConfigEntry) error {
	cfg := Config{}
	data, err := os.ReadFile(configPath)
	if err == nil && len(data) > 0 {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("parse config: %w", err)
		}
	}

	// Check for duplicate ID
	for _, existing := range cfg.Agents {
		if existing.AgentID == agent.AgentID {
			return fmt.Errorf("agent @%s already exists — use a different ID or remove the existing agent first", agent.AgentID)
		}
	}

	cfg.Agents = append(cfg.Agents, agent)
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(configPath, out, 0o644)
}

// Fingerprint returns a stable hash of the active config (GC-SPEC-CFG-005).
func (c Config) Fingerprint() string {
	h := fnv.New64a()
	fmt.Fprintf(h, "workers=%d|timeout=%d|bind=%s|log=%s|model=%s|origins=%v",
		c.WorkerCount, c.TaskTimeoutSeconds, c.BindAddr, c.LogLevel, c.GeminiModel, c.AllowOrigins)
	return fmt.Sprintf("cfg-%x", h.Sum64())
}

func defaultConfig() Config {
	return Config{
		WorkerCount:              16,
		TaskTimeoutSeconds:       int((10 * time.Minute).Seconds()),
		BindAddr:                 "127.0.0.1:18789",
		LogLevel:                 "info",
		MaxQueueDepth:            100,
		DrainTimeoutSeconds:      5,
		RetentionTaskEventsDays:  90,
		RetentionAuditLogDays:    365,
		RetentionMessagesDays:    90,
		HeartbeatIntervalMinutes: 30,
		Skills: SkillsConfig{
			ProjectDir: "./skills",
			ExtraDirs:  nil, LegacyMode: false,
		},
	}
}

func HomeDir() string {
	if override := os.Getenv("GOCLAW_HOME"); override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return filepath.Join(home, ".goclaw")
}

func Load() (Config, error) {
	cfg := defaultConfig()
	cfg.HomeDir = HomeDir()

	if err := os.MkdirAll(cfg.HomeDir, 0o755); err != nil {
		return cfg, fmt.Errorf("create goclaw home: %w", err)
	}

	configPath := filepath.Join(cfg.HomeDir, "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.NeedsGenesis = true
		} else {
			return cfg, fmt.Errorf("read config.yaml: %w", err)
		}
	} else if len(data) > 0 {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("parse config.yaml: %w", err)
		}
	}

	applyEnvOverrides(&cfg)
	loadTextFiles(&cfg)
	normalize(&cfg)
	if err := validateDelegation(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func normalize(cfg *Config) {
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 4
	}
	if cfg.TaskTimeoutSeconds <= 0 {
		cfg.TaskTimeoutSeconds = int((10 * time.Minute).Seconds())
	}
	if cfg.BindAddr == "" {
		cfg.BindAddr = "127.0.0.1:18789"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.LLMProvider == "" {
		cfg.LLMProvider = "google"
	}
	// Normalize legacy provider name.
	if cfg.LLMProvider == "gemini" {
		cfg.LLMProvider = "google"
	}
	if cfg.GeminiModel == "" {
		// Use first model from BuiltinModels as default
		if models, ok := BuiltinModels["google"]; ok && len(models) > 0 {
			cfg.GeminiModel = models[0].ID
		} else {
			cfg.GeminiModel = "gemini-2.5-flash" // Emergency fallback (should never happen)
		}
	}
	if strings.TrimSpace(cfg.Skills.ProjectDir) == "" {
		cfg.Skills.ProjectDir = "./skills"
	}

	// Backward compat: copy gemini_api_key into providers.gemini.api_key if not set.
	if cfg.GeminiAPIKey != "" {
		if cfg.Providers == nil {
			cfg.Providers = make(map[string]ProviderConfig)
		}
		p := cfg.Providers["google"]
		if p.APIKey == "" {
			p.APIKey = cfg.GeminiAPIKey
			cfg.Providers["google"] = p
		}
	}

	// Populate with starter agents on first run if no agents are configured
	if len(cfg.Agents) == 0 {
		cfg.Agents = StarterAgents()
	}
}

// validateDelegation ensures delegation configuration prevents deadlock.
// Deadlock occurs if all workers are blocked waiting for delegated tasks with no free workers to run them.
// Solution: DelegationMaxHops must be <= (WorkerCount - 1) to guarantee at least 1 worker always free.
// Equivalently: DelegationMaxHops < WorkerCount when DelegationMaxHops is strictly less.
func validateDelegation(cfg *Config) error {
	// Set default if not configured
	if cfg.DelegationMaxHops == 0 {
		cfg.DelegationMaxHops = 2 // Default: max 2 hops (safe for 3+ workers)
	}

	// Validate per-agent worker count
	for _, agent := range cfg.Agents {
		agentWorkers := agent.WorkerCount
		if agentWorkers == 0 {
			agentWorkers = cfg.WorkerCount
		}
		if cfg.DelegationMaxHops > agentWorkers-1 {
			return fmt.Errorf("delegation_max_hops (%d) must be <= worker_count-1 (%d) for agent %s to prevent deadlock",
				cfg.DelegationMaxHops, agentWorkers-1, agent.AgentID)
		}
	}

	// Check default agent worker count
	defaultWorkers := cfg.WorkerCount
	if cfg.DelegationMaxHops > defaultWorkers-1 {
		return fmt.Errorf("delegation_max_hops (%d) must be <= default worker_count-1 (%d) to prevent deadlock",
			cfg.DelegationMaxHops, defaultWorkers-1)
	}

	return nil
}

// ProviderAPIKey returns the API key for the given provider, checking env overrides first.
func (c Config) ProviderAPIKey(provider string) string {
	envMap := map[string]string{
		"google":     "GEMINI_API_KEY",
		"anthropic":  "ANTHROPIC_API_KEY",
		"openai":     "OPENAI_API_KEY",
		"openrouter": "OPENROUTER_API_KEY",
	}
	if envVar, ok := envMap[provider]; ok {
		if v := os.Getenv(envVar); v != "" {
			return v
		}
	}
	if c.Providers != nil {
		if p, ok := c.Providers[provider]; ok {
			return p.APIKey
		}
	}
	// Legacy fallback for gemini.
	if provider == "google" {
		return c.GeminiAPIKey
	}
	return ""
}

func applyEnvOverrides(cfg *Config) {
	if raw := os.Getenv("GOCLAW_WORKER_COUNT"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			cfg.WorkerCount = v
		}
	}
	if raw := os.Getenv("GOCLAW_TASK_TIMEOUT_SECONDS"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			cfg.TaskTimeoutSeconds = v
		}
	}
	if raw := os.Getenv("GOCLAW_BIND_ADDR"); raw != "" {
		cfg.BindAddr = raw
	}
	if raw := os.Getenv("GOCLAW_LOG_LEVEL"); raw != "" {
		cfg.LogLevel = raw
	}
	if raw := os.Getenv("GOCLAW_DRAIN_TIMEOUT_SECONDS"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			cfg.DrainTimeoutSeconds = v
		}
	}
	if raw := os.Getenv("GOCLAW_HEARTBEAT_INTERVAL_MINUTES"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			cfg.HeartbeatIntervalMinutes = v
		}
	}
	if raw := os.Getenv("GEMINI_API_KEY"); raw != "" {
		cfg.GeminiAPIKey = raw
	}
	if raw := os.Getenv("GEMINI_MODEL"); raw != "" {
		cfg.GeminiModel = raw
	}
	if raw := os.Getenv("GOCLAW_AGENT_NAME"); raw != "" {
		cfg.AgentName = raw
	}
	if raw := os.Getenv("GOCLAW_AGENT_EMOJI"); raw != "" {
		cfg.AgentEmoji = raw
	}
	if raw := os.Getenv("BRAVE_API_KEY"); raw != "" {
		if cfg.APIKeys == nil {
			cfg.APIKeys = make(map[string]string)
		}
		cfg.APIKeys["brave_search"] = raw
	}
	if raw := os.Getenv("PERPLEXITY_API_KEY"); raw != "" {
		if cfg.APIKeys == nil {
			cfg.APIKeys = make(map[string]string)
		}
		cfg.APIKeys["perplexity_search"] = raw
	}
	if raw := os.Getenv("OPENROUTER_API_KEY"); raw != "" {
		if cfg.APIKeys == nil {
			cfg.APIKeys = make(map[string]string)
		}
		cfg.APIKeys["openrouter"] = raw
	}
	if raw := os.Getenv("TELEGRAM_TOKEN"); raw != "" {
		cfg.Channels.Telegram.Token = raw
	}
}

func loadTextFiles(cfg *Config) {
	soulPath := filepath.Join(cfg.HomeDir, "SOUL.md")
	if b, err := os.ReadFile(soulPath); err == nil {
		cfg.SOUL = string(b)
	}

	agentsPath := filepath.Join(cfg.HomeDir, "AGENTS.md")
	if b, err := os.ReadFile(agentsPath); err == nil {
		cfg.AGENTS = string(b)
	}
}
