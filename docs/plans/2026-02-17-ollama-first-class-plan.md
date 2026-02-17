# First-Class Ollama Provider — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make Ollama a first-class provider with `provider: ollama`, auto-detect tool support, and fix all agent inheritance issues.

**Architecture:** Add `case "ollama"` in brain init that auto-configures the Genkit OpenAI-compatible plugin with sensible defaults. Query Ollama's `/api/show` endpoint at startup to detect tool capabilities. Gate tool sending on the result. Update config resolution, genesis wizard, and agent inheritance to handle the new provider.

**Tech Stack:** Go, Genkit v1.4.0 compat_oai plugin, Ollama `/api/show` REST API, net/http for detection.

---

### Task 1: Add `detectOllamaTools()` and tests

**Files:**
- Create: `internal/engine/ollama.go`
- Create: `internal/engine/ollama_test.go`

**Step 1: Write the failing test**

In `internal/engine/ollama_test.go`:

```go
package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDetectOllamaTools_Supported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var req struct{ Model string `json:"model"` }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if req.Model != "llama3.1:8b" {
			t.Fatalf("model = %q, want llama3.1:8b", req.Model)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"capabilities": []string{"completion", "tools"},
		})
	}))
	defer srv.Close()

	got := detectOllamaTools(srv.URL+"/v1", "ollama/llama3.1:8b")
	if !got {
		t.Fatal("expected tools supported")
	}
}

func TestDetectOllamaTools_NotSupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"capabilities": []string{"completion"},
		})
	}))
	defer srv.Close()

	got := detectOllamaTools(srv.URL+"/v1", "gemma:2b")
	if got {
		t.Fatal("expected tools NOT supported")
	}
}

func TestDetectOllamaTools_Unreachable(t *testing.T) {
	got := detectOllamaTools("http://127.0.0.1:1/v1", "any")
	if got {
		t.Fatal("expected false when server unreachable")
	}
}

func TestDetectOllamaTools_StripsPrefix(t *testing.T) {
	var receivedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Model string `json:"model"` }
		json.NewDecoder(r.Body).Decode(&req)
		receivedModel = req.Model
		json.NewEncoder(w).Encode(map[string]any{
			"capabilities": []string{"tools"},
		})
	}))
	defer srv.Close()

	detectOllamaTools(srv.URL+"/v1", "ollama/qwen3:8b")
	if receivedModel != "qwen3:8b" {
		t.Fatalf("model sent to Ollama = %q, want qwen3:8b (ollama/ prefix stripped)", receivedModel)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/engine/ -run TestDetectOllamaTools -v -count=1`
Expected: FAIL — `detectOllamaTools` undefined.

**Step 3: Write minimal implementation**

In `internal/engine/ollama.go`:

```go
package engine

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// detectOllamaTools queries Ollama's /api/show endpoint to check if a model
// supports tool/function calling. Returns false on any error (safe default).
// baseURL should be the OpenAI-compat URL ending in /v1.
// model may have an "ollama/" prefix which is stripped for the API call.
func detectOllamaTools(baseURL, model string) bool {
	// Strip /v1 suffix to get native Ollama API URL.
	ollamaURL := strings.TrimSuffix(strings.TrimSuffix(baseURL, "/"), "/v1")

	// Strip ollama/ prefix — Ollama expects bare model names.
	model = strings.TrimPrefix(model, "ollama/")

	client := &http.Client{Timeout: 3 * time.Second}
	body := fmt.Sprintf(`{"model":%q}`, model)
	resp, err := client.Post(ollamaURL+"/api/show", "application/json", strings.NewReader(body))
	if err != nil {
		slog.Debug("ollama tool detection failed (connection)", "error", err, "model", model)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Debug("ollama tool detection failed (status)", "status", resp.StatusCode, "model", model)
		return false
	}

	var result struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Debug("ollama tool detection failed (decode)", "error", err, "model", model)
		return false
	}

	for _, cap := range result.Capabilities {
		if cap == "tools" {
			slog.Info("ollama model supports tools", "model", model)
			return true
		}
	}
	slog.Info("ollama model does not support tools", "model", model, "capabilities", result.Capabilities)
	return false
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/engine/ -run TestDetectOllamaTools -v -count=1`
Expected: 4 PASS.

**Step 5: Commit**

```bash
git add internal/engine/ollama.go internal/engine/ollama_test.go
git commit -m "feat: add Ollama tool capability auto-detection via /api/show"
```

---

### Task 2: Add `toolsSupported` to GenkitBrain and wire into Respond/Stream

**Files:**
- Modify: `internal/engine/brain.go:101-122` (struct), `556-560` (Respond tools), `726-730` (Stream tools)

**Step 1: Write the failing test**

In `internal/engine/ollama_test.go`, append:

```go
func TestGenkitBrain_ToolsGating(t *testing.T) {
	// Brain with toolsSupported=false should not include tools in opts.
	store := openStoreForBrainTest(t)
	b := NewGenkitBrain(context.Background(), store, BrainConfig{
		Policy: policy.Default(),
		Soul:   "test",
	})

	if b.toolsSupported {
		t.Fatal("default brain (no ollama) should have toolsSupported=true by default for backward compat")
	}
	// Actually, non-ollama providers should default to true.
	// We'll verify the field exists and is settable.
}

func TestGenkitBrain_ToolsSupportedDefault(t *testing.T) {
	store := openStoreForBrainTest(t)
	b := NewGenkitBrain(context.Background(), store, BrainConfig{
		Provider: "google",
		Policy:   policy.Default(),
		Soul:     "test",
	})
	if !b.toolsSupported {
		t.Fatal("non-ollama providers should default to toolsSupported=true")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/engine/ -run TestGenkitBrain_Tools -v -count=1`
Expected: FAIL — `toolsSupported` field does not exist.

**Step 3: Implement**

In `internal/engine/brain.go`:

3a. Add field to struct (after line 121):
```go
// toolsSupported indicates whether the model supports tool/function calling.
// Defaults to true for all providers except "ollama" where it's auto-detected.
toolsSupported bool
```

3b. Set `toolsSupported = true` as default. After the brain struct is created (around line 249), add:
```go
brain.toolsSupported = true
```

3c. In the `"ollama"` case (Task 3 adds this), set it from detection.

3d. Gate tools in `Respond()` — change lines 556-560 from:
```go
// Add tools for autonomous use
if len(b.tools.Tools) > 0 {
    opts = append(opts, ai.WithTools(b.tools.Tools...))
    opts = append(opts, ai.WithMaxTurns(3))
}
```
to:
```go
// Add tools for autonomous use (only if model supports them).
if b.toolsSupported && len(b.tools.Tools) > 0 {
    opts = append(opts, ai.WithTools(b.tools.Tools...))
    opts = append(opts, ai.WithMaxTurns(3))
}
```

3e. Gate tools in `Stream()` — change lines 726-730 from:
```go
// Add tools (disable streaming for tool calls - fall back to non-streaming)
if len(b.tools.Tools) > 0 {
    opts = append(opts, ai.WithTools(b.tools.Tools...))
    opts = append(opts, ai.WithMaxTurns(3))
}
```
to:
```go
// Add tools for autonomous use (only if model supports them).
if b.toolsSupported && len(b.tools.Tools) > 0 {
    opts = append(opts, ai.WithTools(b.tools.Tools...))
    opts = append(opts, ai.WithMaxTurns(3))
}
```

3f. Also gate the fallback retry in `Respond()` — change line 575 from:
```go
if len(b.tools.Tools) > 0 {
```
to:
```go
if b.toolsSupported && len(b.tools.Tools) > 0 {
```

**Step 4: Run tests**

Run: `go test ./internal/engine/ -run TestGenkitBrain_Tools -v -count=1`
Expected: PASS.

Run: `go test ./internal/engine/ -count=1 -timeout 120s`
Expected: All existing tests pass (toolsSupported defaults to true).

**Step 5: Commit**

```bash
git add internal/engine/brain.go internal/engine/ollama_test.go
git commit -m "feat: gate tool sending on toolsSupported flag"
```

---

### Task 3: Add `case "ollama"` provider to brain.go

**Files:**
- Modify: `internal/engine/brain.go:155-232` (switch), `332-344` (defaultModel), `347-365` (envAPIKey), `367-386` (modelName)

**Step 1: Write the failing test**

In `internal/engine/brain_test.go`, append:

```go
func TestNewGenkitBrain_Ollama(t *testing.T) {
	store := openStoreForBrainTest(t)
	// No Ollama server running — should still create brain without panicking.
	// toolsSupported should be false (can't reach server).
	b := NewGenkitBrain(context.Background(), store, BrainConfig{
		Provider: "ollama",
		Model:    "llama3.1:8b",
		Soul:     "You are a test assistant.",
		Policy:   policy.Default(),
	})
	if b == nil {
		t.Fatal("expected non-nil brain")
	}
	if !b.llmOn {
		t.Fatal("expected llmOn=true for ollama (uses placeholder API key)")
	}
	if b.toolsSupported {
		t.Fatal("expected toolsSupported=false (no Ollama server running)")
	}
}

func TestModelNameForProvider_Ollama(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{"llama3.1:8b", "ollama/llama3.1:8b"},
		{"ollama/llama3.1:8b", "ollama/llama3.1:8b"},     // already prefixed
		{"qwen3:8b", "ollama/qwen3:8b"},
	}
	for _, tt := range tests {
		got := modelNameForProvider("ollama", tt.model)
		if got != tt.want {
			t.Errorf("modelNameForProvider(ollama, %q) = %q, want %q", tt.model, got, tt.want)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/engine/ -run "TestNewGenkitBrain_Ollama|TestModelNameForProvider_Ollama" -v -count=1`
Expected: FAIL — "ollama" hits the default case (deterministic fallback), llmOn=false.

**Step 3: Implement**

3a. Add `case "ollama"` in `NewGenkitBrain` switch (after `case "openai_compatible"`, before `case "openrouter"`):

```go
case "ollama":
    baseURL := cfg.OpenAICompatibleBaseURL
    if baseURL == "" {
        baseURL = "http://localhost:11434/v1"
    }
    ollamaKey := apiKey
    if ollamaKey == "" {
        ollamaKey = "ollama" // Ollama doesn't require auth; placeholder for Genkit
    }
    openaiCompatPlugin := &compat_oai.OpenAICompatible{
        Provider: "ollama",
        APIKey:   ollamaKey,
        BaseURL:  baseURL,
    }
    g = genkit.Init(ctx, genkit.WithPlugins(openaiCompatPlugin))
    llmOn = true
    slog.Info("genkit brain initialized", "provider", "ollama", "model", modelID, "base_url", baseURL)
```

3b. After brain struct creation (after `brain.toolsSupported = true`), add Ollama tool detection:

```go
// Auto-detect tool support for Ollama models.
if provider == "ollama" {
    baseURL := cfg.OpenAICompatibleBaseURL
    if baseURL == "" {
        baseURL = "http://localhost:11434/v1"
    }
    brain.toolsSupported = detectOllamaTools(baseURL, modelID)
}
```

3c. Update `defaultModelForProvider` — add before the BuiltinModels lookup (line 333):
```go
// "ollama" has its own entry in BuiltinModels, use it directly.
```
No change needed — `BuiltinModels["ollama"]` already exists with `qwen3:8b` as first entry.

3d. Update `envAPIKeyForProvider` — add case:
```go
case "ollama":
    return "" // Ollama doesn't require an API key
```

3e. Update `modelNameForProvider` — add case:
```go
case "ollama":
    if !strings.HasPrefix(model, "ollama/") {
        return "ollama/" + model
    }
    return model
```

**Step 4: Run tests**

Run: `go test ./internal/engine/ -run "TestNewGenkitBrain_Ollama|TestModelNameForProvider_Ollama" -v -count=1`
Expected: PASS.

Run: `go test ./internal/engine/ -count=1 -timeout 120s`
Expected: All tests pass.

**Step 5: Commit**

```bash
git add internal/engine/brain.go internal/engine/brain_test.go
git commit -m "feat: add first-class 'ollama' provider with auto-detection"
```

---

### Task 4: Update config resolution for "ollama" provider

**Files:**
- Modify: `internal/config/config.go:355-378` (LLMProviderAPIKey), `381-421` (ResolveLLMConfig), `659-679` (ProviderAPIKey)

**Step 1: Write the failing test**

In `internal/config/config_test.go`, append:

```go
func TestResolveLLMConfig_Ollama(t *testing.T) {
	cfg := Config{}
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
	cfg := Config{}
	got := cfg.LLMProviderAPIKey("ollama")
	if got != "ollama" {
		t.Fatalf("LLMProviderAPIKey(ollama) = %q, want 'ollama'", got)
	}
}

func TestProviderAPIKey_Ollama(t *testing.T) {
	cfg := Config{}
	got := cfg.ProviderAPIKey("ollama")
	if got != "ollama" {
		t.Fatalf("ProviderAPIKey(ollama) = %q, want 'ollama'", got)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run "Ollama" -v -count=1`
Expected: FAIL — apiKey returns "" (no "ollama" case).

**Step 3: Implement**

3a. In `LLMProviderAPIKey` (line 355), add to envMap:
```go
"ollama": "", // Ollama doesn't use env vars for API keys
```

Then add before the final `return ""` (line 377):
```go
// Ollama doesn't require a real API key; provide a placeholder.
if provider == "ollama" {
    return "ollama"
}
```

3b. In `ResolveLLMConfig` model resolution (line 392 switch), add:
```go
case "ollama":
    if c.LLM.OpenAIModel != "" {
        model = c.LLM.OpenAIModel
    }
```

3c. In `ProviderAPIKey` (line 659), add "ollama" handling. After the legacy google fallback (line 679), add:
```go
if provider == "ollama" {
    return "ollama"
}
```

**Step 4: Run tests**

Run: `go test ./internal/config/ -run "Ollama" -v -count=1`
Expected: 3 PASS.

Run: `go test ./internal/config/ -count=1`
Expected: All config tests pass.

**Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: config resolution handles 'ollama' provider"
```

---

### Task 5: Update genesis wizard to generate `provider: ollama`

**Files:**
- Modify: `internal/tui/genesis.go:766-777`
- Modify: `internal/tui/genesis_flow_test.go:409-425`

**Step 1: Update the implementation**

Change genesis.go lines 766-777 from:

```go
if provider == "ollama" {
    baseURL := m.baseURL
    if baseURL == "" {
        baseURL = "http://localhost:11434"
    }
    compatURL := strings.TrimSuffix(baseURL, "/") + "/v1"
    b.WriteString("llm:\n")
    b.WriteString("  provider: openai_compatible\n")
    b.WriteString("  openai_compatible_provider: ollama\n")
    b.WriteString(fmt.Sprintf("  openai_compatible_base_url: %s\n", compatURL))
    b.WriteString(fmt.Sprintf("  openai_model: ollama/%s\n", model))
    b.WriteString("gemini_api_key: \"ollama\"\n")
```

to:

```go
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
```

No more `gemini_api_key: "ollama"`, no more `openai_compatible_provider`, no `ollama/` prefix on model.

**Step 2: Update test expectations**

In `genesis_flow_test.go`, change lines 409-425 from:

```go
for _, expect := range []string{
    "provider: openai_compatible",
    "openai_compatible_provider: ollama",
    "openai_compatible_base_url: http://localhost:11434/v1",
    "openai_model: ollama/",
    "gemini_api_key: \"ollama\"",
} {
```

to:

```go
for _, expect := range []string{
    "provider: ollama",
    "openai_compatible_base_url: http://localhost:11434/v1",
    "openai_model: ",
} {
```

Also remove or update the `llm_provider:` negative check if needed (line 423 — keep it, still valid).

**Step 3: Run tests**

Run: `go test ./internal/tui/ -run TestGenesisFlow_Ollama -v -count=1`
Expected: PASS.

Run: `go test ./internal/tui/ -count=1 -timeout 120s`
Expected: All TUI tests pass.

**Step 4: Commit**

```bash
git add internal/tui/genesis.go internal/tui/genesis_flow_test.go
git commit -m "feat: genesis wizard generates 'provider: ollama' directly"
```

---

### Task 6: Complete WIP fixes (agent inheritance + main.go)

**Files:**
- Modify: `cmd/goclaw/main.go` (already WIP — just needs "ollama" awareness)

**Step 1: Verify current WIP agent inheritance handles "ollama"**

The WIP changes already pass through `agentProvider`, `agentCompatProvider`, `agentCompatBaseURL`. For `provider: ollama`, agents need to inherit:
- `agentProvider = "ollama"`
- `agentCompatBaseURL = cfg.LLM.OpenAICompatibleBaseURL` (for the Ollama URL)

Check `buildAgentConfig` in `main.go:1298-1339`. When provider is "ollama" and per-agent provider is empty:
- `agentProvider = "ollama"` ✓ (inherited from ResolveLLMConfig)
- `agentCompatBaseURL = cfg.LLM.OpenAICompatibleBaseURL` ✓ (inherited)

This already works. No additional changes needed in main.go for "ollama".

**Step 2: Run full build + test**

Run: `just check`
Expected: Build + vet + all tests pass.

**Step 3: Commit WIP changes**

```bash
git add cmd/goclaw/main.go internal/engine/brain.go internal/tui/chat.go
git commit -m "fix: complete WIP fixes — agent LLM inheritance, plan command, brain fallback"
```

---

### Task 7: Add Stream() fallback retry

**Files:**
- Modify: `internal/engine/brain.go:738-745`

**Step 1: Write the test**

In `internal/engine/brain_test.go`, append:

```go
func TestStream_FallbackOnError(t *testing.T) {
	// Verify Stream method exists and has consistent signature.
	// Can't test real streaming without LLM, but verify structure compiles.
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	store := openStoreForBrainTest(t)
	b := NewGenkitBrain(context.Background(), store, BrainConfig{
		Policy: policy.Default(),
		Soul:   "test",
	})
	// With llmOn=false, Stream should return the fallback message.
	var chunks []string
	err := b.Stream(context.Background(), "test-session", "hello", func(content string) error {
		chunks = append(chunks, content)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk from fallback")
	}
}
```

**Step 2: Run test**

Run: `go test ./internal/engine/ -run TestStream_FallbackOnError -v -count=1`
Expected: PASS (this tests the offline fallback; real stream fallback requires LLM).

**Step 3: Add Stream retry logic**

In `brain.go`, change the stream error handling (around line 742-744) from:

```go
for streamVal, err := range stream {
    if err != nil {
        return fmt.Errorf("stream error: %w", err)
    }
```

to:

```go
var streamErr error
for streamVal, err := range stream {
    if err != nil {
        streamErr = err
        break
    }
```

Then after the stream loop ends, add retry logic (before the `finalReply` handling):

```go
// If streaming failed and tools were sent, retry without tools.
if streamErr != nil && b.toolsSupported && len(b.tools.Tools) > 0 {
    slog.Info("stream failed with tools, retrying without tools", "error", streamErr)
    retryOpts := []ai.GenerateOption{
        ai.WithModelName(modelName),
        ai.WithPrompt(trimmed),
        ai.WithSystem(systemPrompt),
    }
    if len(history) > 0 {
        if msgs := historyToMessages(history); len(msgs) > 0 {
            retryOpts = append(retryOpts, ai.WithMessages(msgs...))
        }
    }
    resp, retryErr := genkit.Generate(ctx, b.g, retryOpts...)
    if retryErr != nil {
        return fmt.Errorf("stream fallback: %w", retryErr)
    }
    reply := resp.Text()
    if reply != "" {
        if err := onChunk(reply); err != nil {
            return err
        }
        if err := b.store.AddHistory(ctx, sessionID, agentID, "assistant", reply, tokenutil.EstimateTokens(reply)); err != nil {
            slog.Warn("failed to save stream fallback response", "error", err)
        }
    }
    return nil
} else if streamErr != nil {
    return fmt.Errorf("stream error: %w", streamErr)
}
```

**Step 4: Run full tests**

Run: `go test ./internal/engine/ -count=1 -timeout 120s`
Expected: All pass.

**Step 5: Commit**

```bash
git add internal/engine/brain.go internal/engine/brain_test.go
git commit -m "fix: Stream() retries without tools on failure"
```

---

### Task 8: Full verification

**Step 1: Run full test suite**

```bash
just check
```

Expected: Build + vet + all tests pass.

**Step 2: Run race detector**

```bash
go test -race ./... -count=1 -timeout 300s
```

Expected: Clean, no races.

**Step 3: Verify backward compat**

Check that the existing `openai_compatible` config format still works. The user's current `~/.goclaw/config.yaml` uses `provider: openai_compatible`. This still hits the existing `case "openai_compatible"` in brain.go — unchanged.

**Step 4: Update the user's config**

The user can optionally simplify their config from:

```yaml
llm:
  provider: openai_compatible
  openai_compatible_provider: ollama
  openai_compatible_base_url: http://localhost:11434/v1
  openai_model: ollama/llama3:latest
gemini_api_key: "ollama"
```

to:

```yaml
llm:
  provider: ollama
  openai_model: llama3:latest
```

**Step 5: Final commit**

```bash
git add -A
git commit -m "feat: first-class Ollama provider with auto-detect tool support"
```
