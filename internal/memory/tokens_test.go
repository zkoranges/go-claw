package memory

import "testing"

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		minToken int
		maxToken int
	}{
		{"empty string", "", 0, 1},
		{"short text", "hi", 0, 2},
		{"medium text", "The quick brown fox", 3, 6},
		{"unicode emoji", "Hello 👋 世界", 4, 6},
		{"long text", "abbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", 25, 27},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTokens(tt.text)
			if got < tt.minToken || got > tt.maxToken {
				t.Errorf("EstimateTokens(%q) = %d, want between %d and %d", tt.text, got, tt.minToken, tt.maxToken)
			}
		})
	}
}
