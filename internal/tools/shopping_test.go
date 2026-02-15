package tools

import (
	"testing"
)

func TestExtractComparisonProducts(t *testing.T) {
	tests := []struct {
		prompt string
		wantA  string
		wantB  string
	}{
		{
			prompt: "compare price of RTX 5090 vs RTX 4090",
			wantA:  "RTX 5090",
			wantB:  "RTX 4090",
		},
		{
			prompt: "iPhone 15 versus Galaxy S24 price",
			wantA:  "iPhone 15",
			wantB:  "Galaxy",
		},
		{
			prompt: "compare price of GTX 1080 and GTX 3090",
			wantA:  "GTX 1080",
			wantB:  "GTX 3090",
		},
		{
			prompt: "no products here",
			wantA:  "",
			wantB:  "",
		},
	}
	for _, tt := range tests {
		gotA, gotB := ExtractComparisonProducts(tt.prompt)
		if gotA != tt.wantA || gotB != tt.wantB {
			t.Errorf("ExtractComparisonProducts(%q) = (%q, %q), want (%q, %q)",
				tt.prompt, gotA, gotB, tt.wantA, tt.wantB)
		}
	}
}

func TestFindDollarNumbers(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"The RTX 5090 costs $1,999.99 and the RTX 4090 is $1,599", 2},
		{"No prices here", 0},
		{"$100 and $200 and $300", 3},
		{"$0.99", 1},
	}
	for _, tt := range tests {
		got := FindDollarNumbers(tt.input)
		if len(got) != tt.want {
			t.Errorf("FindDollarNumbers(%q) returned %d results, want %d: %v",
				tt.input, len(got), tt.want, got)
		}
	}
}

func TestFirstPriceNear(t *testing.T) {
	text := "RTX 5090 is available for $1,999.99\nRTX 4090 costs $1,599.00\nSome other line"
	tests := []struct {
		anchor string
		want   string
	}{
		{"RTX 5090", "$1,999.99"},
		{"RTX 4090", "$1,599.00"},
		{"RTX 3080", ""},
	}
	for _, tt := range tests {
		got := FirstPriceNear(text, tt.anchor)
		if got != tt.want {
			t.Errorf("FirstPriceNear(_, %q) = %q, want %q", tt.anchor, got, tt.want)
		}
	}
}

func TestComparePrices_PolicyDenied(t *testing.T) {
	// With no policy, price comparison should be denied.
	reg := &Registry{
		Policy: nil,
	}
	input := PriceComparisonInput{
		Prompt:    "compare price of RTX 5090 vs RTX 4090",
		SessionID: "test-session",
	}
	_, err := comparePrices(nil, input, reg)
	if err == nil {
		t.Fatal("expected error when policy is nil, got nil")
	}
	if !contains(err.Error(), "policy denied") {
		t.Errorf("expected policy denied error, got: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
