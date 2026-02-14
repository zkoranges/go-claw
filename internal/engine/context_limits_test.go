package engine

import "testing"

func TestContextLimitOverrides(t *testing.T) {
	// Set overrides
	SetContextLimitOverrides(map[string]int{
		"google/gemini-2.5-flash": 500_000,
		"my-custom-model":         42_000,
	})
	defer SetContextLimitOverrides(nil) // cleanup

	// Full provider/model key
	if got := ContextLimitForModel("google", "gemini-2.5-flash"); got != 500_000 {
		t.Errorf("override google/gemini-2.5-flash = %d; want 500000", got)
	}

	// Model-only key
	if got := ContextLimitForModel("anything", "my-custom-model"); got != 42_000 {
		t.Errorf("override my-custom-model = %d; want 42000", got)
	}

	// Non-overridden model falls through to defaults
	if got := ContextLimitForModel("anthropic", "claude-3-5-sonnet-20241022"); got != 200_000 {
		t.Errorf("non-overridden claude = %d; want 200000", got)
	}
}

func TestContextLimitForModel(t *testing.T) {
	tests := []struct {
		provider string
		model    string
		want     int
	}{
		{"google", "gemini-2.5-flash", 1_048_576},
		{"google", "gemini-1.5-pro", 1_048_576},
		{"google", "unknown-gemini", 1_048_576},
		{"google", "", 1_048_576},

		{"anthropic", "claude-3-5-sonnet-20241022", 200_000},
		{"anthropic", "claude-3-opus", 200_000},
		{"anthropic", "", 200_000},

		{"openai", "gpt-4o", 128_000},
		{"openai", "gpt-4o-mini", 128_000},
		{"openai", "gpt-3.5-turbo", 128_000}, // Defaults to provider
		{"openai", "", 128_000},

		{"openrouter", "mistral-large-latest", 128_000},
		{"", "gemini-2.5-flash", 1_048_576}, // Matches model name
		{"", "unknown-model", 128_000},      // Ultimate fallback
	}

	for _, tt := range tests {
		got := ContextLimitForModel(tt.provider, tt.model)
		if got != tt.want {
			t.Errorf("ContextLimitForModel(%q, %q) = %d; want %d", tt.provider, tt.model, got, tt.want)
		}
	}
}
