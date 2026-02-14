// Package pricing provides per-model cost estimation for token usage.
package pricing

// ModelPricing holds per-million-token costs in USD.
type ModelPricing struct {
	PromptPer1M     float64
	CompletionPer1M float64
}

// Known model pricing as of Feb 2026. Add new models as needed.
var knownModels = map[string]ModelPricing{
	// Gemini
	"gemini-2.0-flash-exp":  {0.0, 0.0},
	"gemini-1.5-pro":        {1.25, 5.00},
	"gemini-2.5-flash":      {0.075, 0.30},
	"gemini-2.5-flash-lite": {0.0, 0.0},
	// Anthropic
	"claude-3-7-sonnet":     {3.00, 15.00},
	"claude-sonnet-4-5":     {3.00, 15.00},
	// OpenAI
	"gpt-4o":                {2.50, 10.00},
	"gpt-4o-mini":           {0.15, 0.60},
}

// EstimateCost returns the estimated USD cost for the given token counts.
// Returns 0.0 for unknown models (safe default).
func EstimateCost(model string, promptTokens, completionTokens int) float64 {
	p, ok := knownModels[model]
	if !ok {
		return 0.0
	}
	return (float64(promptTokens)/1_000_000)*p.PromptPer1M +
		(float64(completionTokens)/1_000_000)*p.CompletionPer1M
}
