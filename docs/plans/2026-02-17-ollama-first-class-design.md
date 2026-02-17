# First-Class Ollama Provider Support

**Date**: 2026-02-17
**Status**: Approved

## Problem

Ollama is hidden behind `openai_compatible` requiring 4+ config fields. Agents don't inherit Ollama settings. Tools crash non-tool-capable models. Stream() has no error recovery.

## Design

### 1. New `"ollama"` provider case in brain.go

Add `case "ollama"` to `NewGenkitBrain()` switch. Auto-configure the `compat_oai.OpenAICompatible` Genkit plugin with:
- Base URL defaults to `http://localhost:11434/v1`
- API key defaults to `"ollama"` placeholder (Ollama ignores it)
- Provider name: `"ollama"` (for Genkit dynamic model resolution)

### 2. Tool capability auto-detection

At brain init, query Ollama's `POST /api/show` with the model name. The response includes a `capabilities` array — check for `"tools"`.

```go
func detectOllamaTools(baseURL, model string) bool
```

- Strip `/v1` suffix from base URL to get Ollama's native API URL
- Strip `ollama/` prefix from model name (Ollama expects bare names)
- 3-second timeout, return false on any error (safe default)
- Store result as `toolsSupported bool` on GenkitBrain
- Gate tool sending in `Respond()` and `Stream()`:
  ```go
  if b.toolsSupported && len(b.tools.Tools) > 0 { ... }
  ```

### 3. Simplified config

New format (2 fields):
```yaml
llm:
  provider: ollama
  openai_model: llama3:latest
```

Optional fields:
- `openai_compatible_base_url`: Override Ollama URL (default: `http://localhost:11434/v1`)

Old format still works via existing `openai_compatible` path.

### 4. Model name handling

`modelNameForProvider("ollama", "llama3:latest")` returns `"ollama/llama3:latest"` for Genkit resolution. If already prefixed, returns as-is.

### 5. Config resolution

- `ResolveLLMConfig()`: Handle `provider: ollama`, resolve model from `openai_model`
- `LLMProviderAPIKey("ollama")`: Return "ollama" placeholder (no real key needed)
- `envAPIKeyForProvider("ollama")`: No env var needed, return ""

### 6. Stream() fallback

Add retry-without-tools matching `Respond()` behavior for edge cases.

### 7. Genesis wizard

Generate `provider: ollama` directly instead of `openai_compatible` + extra fields.

### 8. Agent inheritance

WIP fix already in place — agents inherit global LLM settings including the "ollama" provider.

## Files

| File | Change |
|------|--------|
| `internal/engine/brain.go` | `case "ollama"`, `toolsSupported`, `detectOllamaTools()`, gate tools, Stream fallback |
| `internal/config/config.go` | "ollama" in ResolveLLMConfig, LLMProviderAPIKey, BuiltinModels key |
| `internal/tui/genesis.go` | Generate `provider: ollama` config |
| `cmd/goclaw/main.go` | WIP fixes (agent inheritance, reconcile signature) |

## Backward Compatibility

- Existing `openai_compatible` + `openai_compatible_provider: ollama` unchanged
- New `provider: ollama` is a shorthand mapping to the same Genkit plugin
- Tool auto-detection only for "ollama" provider; others keep current behavior
- `toolsSupported` defaults to `true` for non-Ollama providers

## Testing

- `detectOllamaTools` with mock HTTP server (tools present, absent, timeout, error)
- `modelNameForProvider("ollama", ...)` prefix handling
- Genesis wizard flow for Ollama
- Existing brain tests pass unchanged
