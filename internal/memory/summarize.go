package memory

import (
	"context"
	"fmt"
)

// Summarizer compresses messages into a brief summary.
type Summarizer interface {
	Summarize(ctx context.Context, messages []WindowMessage) (string, error)
}

// StaticSummarizer provides a simple fallback summary without LLM.
// Used for testing or when LLM is unavailable.
type StaticSummarizer struct{}

func (s *StaticSummarizer) Summarize(ctx context.Context, messages []WindowMessage) (string, error) {
	if len(messages) == 0 {
		return "", nil
	}
	return fmt.Sprintf("[Summary of %d earlier messages]", len(messages)), nil
}

// LLMSummarizer would use the brain to create summaries.
// Deferred to later implementation - for now use StaticSummarizer.
var _ Summarizer = (*StaticSummarizer)(nil)
