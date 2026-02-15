package memory

import (
	"context"
	"testing"
)

func TestStaticSummarizer(t *testing.T) {
	tests := []struct {
		name     string
		messages []WindowMessage
	}{
		{"empty messages", []WindowMessage{}},
		{"single message", []WindowMessage{{Role: "user", Content: "hi", Tokens: 1}}},
		{"multiple messages", []WindowMessage{
			{Role: "user", Content: "a", Tokens: 1},
			{Role: "assistant", Content: "b", Tokens: 1},
			{Role: "user", Content: "c", Tokens: 1},
		}},
	}

	s := &StaticSummarizer{}
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary, err := s.Summarize(ctx, tt.messages)
			if err != nil {
				t.Fatalf("Summarize: %v", err)
			}
			if len(tt.messages) > 0 && summary == "" {
				t.Errorf("expected non-empty summary")
			}
			if len(tt.messages) == 0 && summary != "" {
				t.Errorf("expected empty summary for empty messages")
			}
		})
	}
}
