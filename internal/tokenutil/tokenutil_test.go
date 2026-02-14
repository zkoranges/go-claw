package tokenutil

import "testing"

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
	}{
		{
			name:    "empty string",
			content: "",
			want:    0,
		},
		{
			name:    "single word",
			content: "hello",
			want:    1, // max(1*1.33=1, 5/4=1) = 1
		},
		{
			name:    "paragraph",
			content: "The quick brown fox jumps over the lazy dog near the river bank",
			want:    17, // 13 words * 1.33 = 17, len=63, 63/4=15 => max(17,15) = 17
		},
		{
			name:    "code snippet",
			content: `func main() { fmt.Println("hello") }`,
			want:    9, // len=37, 37/4=9; 4 words * 1.33 = 5 => max(5,9) = 9
		},
		{
			name: "CJK text",
			// CJK characters: each is typically 3 bytes in UTF-8, few whitespace-separated words.
			content: "\u4f60\u597d\u4e16\u754c\u6b22\u8fce\u5149\u4e34",
			want:    6, // 1 word * 1.33 = 1; len=24 bytes, 24/4=6 => max(1,6) = 6
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTokens(tt.content)
			if got != tt.want {
				t.Errorf("EstimateTokens(%q) = %d; want %d", tt.content, got, tt.want)
			}
		})
	}
}
