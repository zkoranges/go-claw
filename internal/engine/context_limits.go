package engine

import (
	"strings"
)

// ReservedTokens returns tokens to reserve for system prompt + tool schemas + response.
// Typically ~4K for system, ~2K for tools, ~4K for response = ~10K.
const reservedTokens = 10_000

var contextLimitOverrides map[string]int

// SetContextLimitOverrides sets config-driven context limit overrides.
func SetContextLimitOverrides(m map[string]int) {
	contextLimitOverrides = m
}

// ContextLimitForModel returns the token limit for a given provider+model.
// Falls back to conservative defaults when model is unknown.
func ContextLimitForModel(provider, model string) int {
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.ToLower(strings.TrimSpace(model))

	// Check overrides first
	if contextLimitOverrides != nil {
		key := provider + "/" + model
		if v, ok := contextLimitOverrides[key]; ok {
			return v
		}
		// Try model-only key
		if v, ok := contextLimitOverrides[model]; ok {
			return v
		}
	}

	// Exact model matches
	switch model {
	case "gemini-2.5-flash", "gemini-2.5-pro", "gemini-1.5-flash", "gemini-1.5-pro":
		return 1_048_576
	case "claude-3-5-sonnet-20241022", "claude-3-5-haiku-20241022", "claude-3-opus-20240229":
		return 200_000
	case "gpt-4o", "gpt-4o-mini":
		return 128_000
	case "o1", "o3-mini":
		return 128_000 // Conservative
	case "llama-3.1-70b-versatile":
		return 131_072
	case "mistral-large-latest":
		return 128_000
	}

	// Prefix matches
	if strings.HasPrefix(model, "gemini-") {
		return 1_048_576
	}
	if strings.HasPrefix(model, "claude-") {
		return 200_000
	}
	if strings.HasPrefix(model, "gpt-4") {
		return 128_000
	}

	// Provider defaults
	switch provider {
	case "google":
		return 1_048_576
	case "anthropic":
		return 200_000
	case "openai":
		return 128_000
	case "openrouter":
		// OpenRouter varies wildly, but 128k is a safe baseline for modern models
		return 128_000
	}

	// Ultimate fallback
	return 128_000
}
