package config

import "os"

// AvailableModels returns models based on configured API keys.
func AvailableModels() []string {
	var models []string
	if os.Getenv("GEMINI_API_KEY") != "" {
		models = append(models, "gemini-2.5-pro", "gemini-2.5-flash")
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		models = append(models, "claude-sonnet-4-5", "claude-haiku-4-5")
	}
	if os.Getenv("OPENAI_API_KEY") != "" {
		models = append(models, "gpt-4o", "gpt-4o-mini")
	}
	if os.Getenv("OPENROUTER_API_KEY") != "" {
		models = append(models, "openrouter/auto")
	}
	if len(models) == 0 {
		models = []string{"default"}
	}
	return models
}
