package tokenutil

import "strings"

// EstimateTokens returns a word-based token estimate.
// Splits on whitespace, multiplies by 1.33 (avg tokens/word for English).
// Uses max(wordEstimate, len/4) as floor for code/non-English.
func EstimateTokens(content string) int {
	if content == "" {
		return 0
	}
	words := len(strings.Fields(content))
	wordEstimate := int(float64(words) * 1.33)
	charEstimate := len(content) / 4
	if wordEstimate > charEstimate {
		return wordEstimate
	}
	return charEstimate
}
