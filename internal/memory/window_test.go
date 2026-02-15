package memory

import "testing"

func TestBuildWindow(t *testing.T) {
	cfg := DefaultWindowConfig()

	tests := []struct {
		name          string
		messages      []WindowMessage
		summary       string
		cfg           WindowConfig
		expectMsgCnt  int
		expectSummary bool
		expectTrunc   int
	}{
		{
			name:          "empty messages",
			messages:      []WindowMessage{},
			summary:       "",
			cfg:           cfg,
			expectMsgCnt:  0,
			expectSummary: false,
			expectTrunc:   0,
		},
		{
			name: "all fit in budget",
			messages: []WindowMessage{
				{Role: "user", Content: "hi", Tokens: 1},
				{Role: "assistant", Content: "hello", Tokens: 2},
			},
			summary:       "",
			cfg:           cfg,
			expectMsgCnt:  2,
			expectSummary: false,
			expectTrunc:   0,
		},
		{
			name: "messages exceed MaxMessages",
			messages: func() []WindowMessage {
				msgs := make([]WindowMessage, 60)
				for i := range msgs {
					msgs[i] = WindowMessage{Role: "user", Content: "msg", Tokens: 1}
				}
				return msgs
			}(),
			summary:       "",
			cfg:           cfg,
			expectMsgCnt:  50, // MaxMessages
			expectSummary: false,
			expectTrunc:   10,
		},
		{
			name: "with summary prefix",
			messages: []WindowMessage{
				{Role: "user", Content: "test", Tokens: 2},
			},
			summary:       "previous conversation summary",
			cfg:           cfg,
			expectMsgCnt:  1,
			expectSummary: true,
			expectTrunc:   0,
		},
		{
			name: "single large message fits",
			messages: []WindowMessage{
				{Role: "user", Content: "a", Tokens: 100},
			},
			summary:       "",
			cfg:           cfg,
			expectMsgCnt:  1,
			expectSummary: false,
			expectTrunc:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildWindow(tt.messages, tt.summary, tt.cfg)
			if len(result.Messages) != tt.expectMsgCnt {
				t.Errorf("expected %d messages, got %d", tt.expectMsgCnt, len(result.Messages))
			}
			if tt.expectSummary && result.Summary == "" {
				t.Errorf("expected summary, got empty")
			}
			if result.TruncatedCount != tt.expectTrunc {
				t.Errorf("expected truncated %d, got %d", tt.expectTrunc, result.TruncatedCount)
			}
		})
	}
}
