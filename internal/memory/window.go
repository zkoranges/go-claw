package memory

// WindowConfig controls sliding window behavior for conversation context.
type WindowConfig struct {
	MaxMessages    int // max messages to keep in window (default: 50)
	MaxTokens      int // max total tokens for messages (default: 8000)
	SummaryBudget  int // tokens reserved for summary (default: 500)
	ReservedTokens int // tokens reserved for system + soul + pins + memories (default: 2000)
}

// DefaultWindowConfig returns sensible defaults for a typical conversation window.
func DefaultWindowConfig() WindowConfig {
	return WindowConfig{
		MaxMessages:    50,
		MaxTokens:      8000,
		SummaryBudget:  500,
		ReservedTokens: 2000,
	}
}

// WindowMessage represents a single message for windowing calculations.
type WindowMessage struct {
	Role    string
	Content string
	Tokens  int
}

// WindowResult is the output of BuildWindow: what messages fit + optional summary.
type WindowResult struct {
	Summary        string           // compressed older messages (may be empty)
	Messages       []WindowMessage  // recent messages that fit in budget
	TotalTokens    int              // estimated tokens used
	TruncatedCount int              // number of messages that were dropped/summarized
}

// BuildWindow selects messages that fit within the context window.
// Takes all messages (oldest first), returns fitting subset + optional summary.
func BuildWindow(messages []WindowMessage, summary string, cfg WindowConfig) WindowResult {
	if len(messages) == 0 {
		return WindowResult{Summary: summary, Messages: []WindowMessage{}, TotalTokens: 0}
	}

	// Calculate available token budget for messages
	availableBudget := cfg.MaxTokens - cfg.ReservedTokens - cfg.SummaryBudget
	if availableBudget < 100 {
		availableBudget = 100 // minimum safety buffer
	}

	// Walk messages from newest to oldest, collecting those that fit
	var selectedMsgs []WindowMessage
	totalMsgTokens := 0
	summaryTokens := len(summary) / 4

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if len(selectedMsgs) >= cfg.MaxMessages {
			break // max message limit reached
		}
		if totalMsgTokens+msg.Tokens+summaryTokens > availableBudget {
			break // token limit reached
		}
		selectedMsgs = append(selectedMsgs, msg)
		totalMsgTokens += msg.Tokens
	}

	// Reverse to get oldest-first order
	for i := 0; i < len(selectedMsgs)/2; i++ {
		j := len(selectedMsgs) - 1 - i
		selectedMsgs[i], selectedMsgs[j] = selectedMsgs[j], selectedMsgs[i]
	}

	truncated := len(messages) - len(selectedMsgs)
	return WindowResult{
		Summary:        summary,
		Messages:       selectedMsgs,
		TotalTokens:    totalMsgTokens + summaryTokens,
		TruncatedCount: truncated,
	}
}
