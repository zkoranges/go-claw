package memory

// EstimateTokens returns an approximate token count for a string.
// Uses the ~4 characters per token heuristic (accurate within ~10% for English).
// This is a commonly-used approximation; exact counting deferred to v0.4.
func EstimateTokens(text string) int {
	return (len(text) + 3) / 4 // round up: (len + 3) / 4
}
