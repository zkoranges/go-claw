package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basket/go-claw/internal/config"
)

func TestLoad_FromGoclawHome(t *testing.T) {
	// [SPEC: SPEC-CONFIG-DIR-1] [PDR: V-20, V-21]
	home := filepath.Join(t.TempDir(), "home")
	ic := filepath.Join(home, ".goclaw")
	if err := os.MkdirAll(ic, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ic, "config.yaml"), []byte("worker_count: 3\ntask_timeout_seconds: 120\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ic, "SOUL.md"), []byte("soul"), 0o644); err != nil {
		t.Fatalf("write soul: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ic, "AGENTS.md"), []byte("agents"), 0o644); err != nil {
		t.Fatalf("write agents: %v", err)
	}

	t.Setenv("HOME", home)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.WorkerCount != 3 {
		t.Fatalf("expected worker_count=3 got %d", cfg.WorkerCount)
	}
	if cfg.SOUL != "soul" {
		t.Fatalf("unexpected soul contents: %q", cfg.SOUL)
	}
	if cfg.AGENTS != "agents" {
		t.Fatalf("unexpected agents contents: %q", cfg.AGENTS)
	}
}

func TestLoad_GeminiEnvOverrides(t *testing.T) {
	// Verify GEMINI_API_KEY and GEMINI_MODEL env overrides.
	home := filepath.Join(t.TempDir(), "home")
	ic := filepath.Join(home, ".goclaw")
	if err := os.MkdirAll(ic, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ic, "config.yaml"), []byte("gemini_model: gemini-1.5-pro\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("GEMINI_API_KEY", "test-key-123")
	t.Setenv("GEMINI_MODEL", "gemini-2.5-flash")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.GeminiAPIKey != "test-key-123" {
		t.Fatalf("expected GEMINI_API_KEY=test-key-123, got %q", cfg.GeminiAPIKey)
	}
	if cfg.GeminiModel != "gemini-2.5-flash" {
		t.Fatalf("expected GEMINI_MODEL=gemini-2.5-flash, got %q", cfg.GeminiModel)
	}
}

func TestLoad_NeedsGenesisWhenNoConfig(t *testing.T) {
	// Config missing → NeedsGenesis=true.
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.NeedsGenesis {
		t.Fatalf("expected NeedsGenesis=true when config.yaml missing")
	}
}

func TestLoad_DefaultsApplied(t *testing.T) {
	// Verify normalize fills in defaults.
	home := filepath.Join(t.TempDir(), "home")
	ic := filepath.Join(home, ".goclaw")
	if err := os.MkdirAll(ic, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ic, "config.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("HOME", home)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.LLMProvider != "google" {
		t.Fatalf("expected default llm_provider=google, got %q", cfg.LLMProvider)
	}
	// Default should be the first model from BuiltinModels
	expectedDefault := config.BuiltinModels["google"][0].ID
	if cfg.GeminiModel != expectedDefault {
		t.Fatalf("expected default gemini_model=%s, got %q", expectedDefault, cfg.GeminiModel)
	}
	if cfg.BindAddr != "127.0.0.1:18789" {
		t.Fatalf("expected default bind_addr=127.0.0.1:18789, got %q", cfg.BindAddr)
	}
}

func TestLoad_EnvOverridesConfig(t *testing.T) {
	// [SPEC: SPEC-CONFIG-DIR-1] [PDR: V-21]
	home := filepath.Join(t.TempDir(), "home")
	ic := filepath.Join(home, ".goclaw")
	if err := os.MkdirAll(ic, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ic, "config.yaml"), []byte("worker_count: 2\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("GOCLAW_WORKER_COUNT", "9")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.WorkerCount != 9 {
		t.Fatalf("expected env override worker_count=9 got %d", cfg.WorkerCount)
	}
}

func TestAPIKey_EnvOverridesYAML(t *testing.T) {
	cfg := config.Config{
		APIKeys: map[string]string{"brave_search": "yaml-key"},
	}
	// Without env var, should return YAML value.
	if got := cfg.APIKey("brave_search"); got != "yaml-key" {
		t.Fatalf("expected yaml-key, got %q", got)
	}

	// With env var, should override.
	t.Setenv("BRAVE_API_KEY", "env-key")
	if got := cfg.APIKey("brave_search"); got != "env-key" {
		t.Fatalf("expected env-key, got %q", got)
	}
}

func TestAPIKey_Empty(t *testing.T) {
	cfg := config.Config{}
	if got := cfg.APIKey("brave_search"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := cfg.APIKey("nonexistent"); got != "" {
		t.Fatalf("expected empty for unknown key, got %q", got)
	}
}

func TestAPIKey_BraveEnvOverride(t *testing.T) {
	// BRAVE_API_KEY env should populate api_keys map during Load.
	home := filepath.Join(t.TempDir(), "home")
	ic := filepath.Join(home, ".goclaw")
	if err := os.MkdirAll(ic, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ic, "config.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("BRAVE_API_KEY", "from-env")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.APIKeys["brave_search"] != "from-env" {
		t.Fatalf("expected api_keys[brave_search]=from-env, got %q", cfg.APIKeys["brave_search"])
	}
}

func TestSetAPIKey_WritesConfig(t *testing.T) {
	homeDir := t.TempDir()
	// Write initial config.
	configPath := config.ConfigPath(homeDir)
	if err := os.WriteFile(configPath, []byte("worker_count: 4\n"), 0o644); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	// Set an API key.
	if err := config.SetAPIKey(homeDir, "brave_search", "test-key-123"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}

	// Read back and verify.
	t.Setenv("HOME", filepath.Dir(homeDir)) // Not used, but just in case.
	t.Setenv("GOCLAW_HOME", homeDir)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if cfg.APIKeys["brave_search"] != "test-key-123" {
		t.Fatalf("expected brave_search=test-key-123, got %q", cfg.APIKeys["brave_search"])
	}
	// Verify original settings preserved.
	if cfg.WorkerCount != 4 {
		t.Fatalf("expected worker_count=4 preserved, got %d", cfg.WorkerCount)
	}
}

func TestSetAPIKey_CreatesNewConfig(t *testing.T) {
	homeDir := t.TempDir()
	// No existing config.yaml.
	if err := config.SetAPIKey(homeDir, "brave_search", "new-key"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}

	data, err := os.ReadFile(config.ConfigPath(homeDir))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "brave_search") {
		t.Fatalf("expected brave_search in config, got: %s", string(data))
	}
}

func TestLoad_APIKeysFromYAML(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	ic := filepath.Join(home, ".goclaw")
	if err := os.MkdirAll(ic, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	yamlContent := "api_keys:\n  brave_search: yaml-brave-key\n  other_key: other-value\n"
	if err := os.WriteFile(filepath.Join(ic, "config.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("HOME", home)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.APIKeys["brave_search"] != "yaml-brave-key" {
		t.Fatalf("expected brave_search=yaml-brave-key, got %q", cfg.APIKeys["brave_search"])
	}
	if cfg.APIKeys["other_key"] != "other-value" {
		t.Fatalf("expected other_key=other-value, got %q", cfg.APIKeys["other_key"])
	}
}

func TestAPIKey_PerplexityEnvOverride(t *testing.T) {
	cfg := config.Config{
		APIKeys: map[string]string{"perplexity_search": "yaml-key"},
	}
	if got := cfg.APIKey("perplexity_search"); got != "yaml-key" {
		t.Fatalf("expected yaml-key, got %q", got)
	}
	t.Setenv("PERPLEXITY_API_KEY", "env-pplx-key")
	if got := cfg.APIKey("perplexity_search"); got != "env-pplx-key" {
		t.Fatalf("expected env-pplx-key, got %q", got)
	}
}

func TestLoad_PerplexityEnvPopulatesAPIKeys(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	ic := filepath.Join(home, ".goclaw")
	if err := os.MkdirAll(ic, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ic, "config.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PERPLEXITY_API_KEY", "pplx-from-env")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.APIKeys["perplexity_search"] != "pplx-from-env" {
		t.Fatalf("expected api_keys[perplexity_search]=pplx-from-env, got %q", cfg.APIKeys["perplexity_search"])
	}
}

func TestLLMProviderAPIKey_OpenRouter(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "or-test-key-123")
	cfg := config.Config{}
	got := cfg.LLMProviderAPIKey("openrouter")
	if got != "or-test-key-123" {
		t.Fatalf("LLMProviderAPIKey(openrouter) = %q, want %q", got, "or-test-key-123")
	}
}

func TestProviderAPIKey_OpenRouter(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "or-prov-key-456")
	cfg := config.Config{}
	got := cfg.ProviderAPIKey("openrouter")
	if got != "or-prov-key-456" {
		t.Fatalf("ProviderAPIKey(openrouter) = %q, want %q", got, "or-prov-key-456")
	}
}

func TestResolveLLMConfig_OpenRouter(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "or-resolve-key")
	cfg := config.Config{}
	cfg.LLM.Provider = "openrouter"
	cfg.LLM.OpenAIModel = "anthropic/claude-sonnet-4-5-20250929"
	provider, model, apiKey := cfg.ResolveLLMConfig()
	if provider != "openrouter" {
		t.Fatalf("provider = %q, want openrouter", provider)
	}
	if model != "anthropic/claude-sonnet-4-5-20250929" {
		t.Fatalf("model = %q, want anthropic/claude-sonnet-4-5-20250929", model)
	}
	if apiKey != "or-resolve-key" {
		t.Fatalf("apiKey = %q, want or-resolve-key", apiKey)
	}
}

func TestLoad_OpenRouterEnvPopulatesAPIKeys(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	ic := filepath.Join(home, ".goclaw")
	if err := os.MkdirAll(ic, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ic, "config.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("OPENROUTER_API_KEY", "or-from-env")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.APIKeys["openrouter"] != "or-from-env" {
		t.Fatalf("expected api_keys[openrouter]=or-from-env, got %q", cfg.APIKeys["openrouter"])
	}
}

func TestResolveLLMConfig_Ollama(t *testing.T) {
	cfg := config.Config{}
	cfg.LLM.Provider = "ollama"
	cfg.LLM.OpenAIModel = "llama3.1:8b"
	provider, model, apiKey := cfg.ResolveLLMConfig()
	if provider != "ollama" {
		t.Fatalf("provider = %q, want ollama", provider)
	}
	if model != "llama3.1:8b" {
		t.Fatalf("model = %q, want llama3.1:8b", model)
	}
	if apiKey != "ollama" {
		t.Fatalf("apiKey = %q, want 'ollama' (placeholder)", apiKey)
	}
}

func TestLLMProviderAPIKey_Ollama(t *testing.T) {
	cfg := config.Config{}
	got := cfg.LLMProviderAPIKey("ollama")
	if got != "ollama" {
		t.Fatalf("LLMProviderAPIKey(ollama) = %q, want 'ollama'", got)
	}
}

func TestProviderAPIKey_Ollama(t *testing.T) {
	cfg := config.Config{}
	got := cfg.ProviderAPIKey("ollama")
	if got != "ollama" {
		t.Fatalf("ProviderAPIKey(ollama) = %q, want 'ollama'", got)
	}
}

func TestNormalizeProviderName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"gemini", "google"},
		{"googleai", "google"},
		{"google", "google"},
		{"anthropic", "anthropic"},
		{"openai", "openai"},
		{"ollama", "ollama"},
		{"openrouter", "openrouter"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := config.NormalizeProviderName(tt.input); got != tt.want {
			t.Errorf("NormalizeProviderName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLoad_PreferredSearchFromYAML(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	ic := filepath.Join(home, ".goclaw")
	if err := os.MkdirAll(ic, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	yamlContent := "preferred_search: perplexity_search\n"
	if err := os.WriteFile(filepath.Join(ic, "config.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("HOME", home)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.PreferredSearch != "perplexity_search" {
		t.Fatalf("expected preferred_search=perplexity_search, got %q", cfg.PreferredSearch)
	}
}
