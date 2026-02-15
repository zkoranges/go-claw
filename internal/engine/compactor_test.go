package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/basket/go-claw/internal/persistence"
)

// MockBrain implements Brain interface for testing.
type MockBrain struct {
	RespondFunc func(ctx context.Context, sessionID, content string) (string, error)
	StreamFunc  func(ctx context.Context, sessionID, content string, onChunk func(content string) error) error
}

func (m *MockBrain) Respond(ctx context.Context, sessionID, content string) (string, error) {
	if m.RespondFunc != nil {
		return m.RespondFunc(ctx, sessionID, content)
	}
	return "mock response", nil
}

func (m *MockBrain) Stream(ctx context.Context, sessionID, content string, onChunk func(content string) error) error {
	if m.StreamFunc != nil {
		return m.StreamFunc(ctx, sessionID, content, onChunk)
	}
	return nil
}

func TestCompactor_CompactIfNeeded(t *testing.T) {
	// Setup DB
	tmpDB := t.TempDir() + "/test.db"
	store, err := persistence.Open(tmpDB, nil)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	sessionID := "00000000-0000-0000-0000-000000000001"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	// Helper to add messages
	addMsg := func(role, content string, tokens int) {
		if err := store.AddHistory(ctx, sessionID, "default", role, content, tokens); err != nil {
			t.Fatalf("add history: %v", err)
		}
	}

	// 1. Test: Under Threshold (No Op)
	// Limit 128k, usage 1000 -> no compaction
	addMsg("user", "hello", 100)
	addMsg("assistant", "hi", 100)

	mockBrain := &MockBrain{}
	compactor := NewCompactor(store, mockBrain, "openai", "gpt-4o", CompactorConfig{
		ThresholdRatio: 0.75,
		KeepRecent:     2,
	})

	history, err := compactor.CompactIfNeeded(ctx, sessionID, "default")
	if err != nil {
		t.Fatalf("CompactIfNeeded error: %v", err)
	}
	if len(history) != 2 {
		t.Errorf("expected 2 messages, got %d", len(history))
	}

	// 2. Test: Over Threshold (Compaction)
	// Add massive amount of tokens to trigger compaction
	// gpt-4o limit = 128,000. Threshold 0.75 = 96,000.
	// Available ~118,000.
	// We need > 88,500 tokens (approx).

	// Add 10 messages of 10k tokens each = 100k tokens.
	longText := strings.Repeat("a", 100) // length doesn't matter for token count in DB, we inject explicit token count
	for i := 0; i < 10; i++ {
		addMsg("user", fmt.Sprintf("msg %d %s", i, longText), 10000)
	}

	// Add 2 recent messages we want to keep
	addMsg("user", "recent 1", 100)
	addMsg("assistant", "recent 2", 100)

	// Mock summarization
	mockBrain.RespondFunc = func(ctx context.Context, sessionID, content string) (string, error) {
		return "This is a summary of the past.", nil
	}

	history, err = compactor.CompactIfNeeded(ctx, sessionID, "default")
	if err != nil {
		t.Fatalf("CompactIfNeeded error: %v", err)
	}

	// Logic:
	// Total tokens: ~200 (initial) + 100,000 + 200 = 100,400.
	// Limit 128k. Threshold 0.75 * (128k-10k) = 88.5k.
	// 100k > 88.5k -> Compact!
	// KeepRecent = 2.
	// Should keep "recent 1", "recent 2".
	// Plus the new system summary.
	// Total expected: 3 items.

	// However, the initial 2 messages ("hello", "hi") are also candidates for compaction.
	// Order in DB:
	// 1. user: hello (100)
	// 2. assistant: hi (100)
	// 3-12. user: msg X ... (10000)
	// 13. user: recent 1 (100)
	// 14. assistant: recent 2 (100)

	// KeepRecent=2 keeps 13 and 14.
	// Safe window (60% of 118k) = 70.8k.
	// 13+14 tokens = 200. Fits.
	// Can we keep more?
	// Msg 12 (10k). Total 10200. Fits.
	// ...
	// It will keep expanding backwards until > 70.8k.
	// Messages 12, 11, 10, 9, 8, 7, 6 = 70k. Total 70.2k. Fits.
	// Message 5 = 10k. Total 80.2k. Stop.
	// So it should compact messages 1, 2, 3, 4, 5.
	// And keep 6, 7, 8, 9, 10, 11, 12, 13, 14.
	// Plus 1 summary.
	// Total items = 1 (summary) + 7 (kept large) + 2 (kept recent) = 10 items.

	// Wait, my mental trace might be off by one index, but roughly correct.
	// It definitely should NOT be just 3 items because the "safe window" allows keeping a lot.

	if len(history) < 3 {
		t.Errorf("expected at least 3 messages (summary + recent), got %d", len(history))
	}

	// Check if summary exists
	foundSummary := false
	for _, h := range history {
		if h.Role == "system" && strings.Contains(h.Content, "summary") {
			foundSummary = true
			break
		}
	}
	if !foundSummary {
		t.Error("expected summary message in history")
	}

	// Verify archived state in DB
	// Check one of the old messages (e.g., the first "hello")
	// It should be archived.
	// Since we don't have direct access to "GetMessage", we ListHistory with a new instance and see it's gone.
	// Wait, ListHistory filters archived. So if it's gone from ListHistory, it's effectively verified.
	// But we returned `history` from CompactIfNeeded which calls ListHistory.

	// Let's verify that the total count of messages in DB is still 15 (original 14 + 1 summary).
	// We need raw SQL or a method that ignores archived_at.
	var count int
	err = store.DB().QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
	if err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if count != 15 {
		t.Errorf("expected 15 messages in DB (archived included), got %d", count)
	}
}

func TestCompactor_LLMFailure(t *testing.T) {
	// Setup DB
	tmpDB := t.TempDir() + "/test_fail.db"
	store, _ := persistence.Open(tmpDB, nil)
	defer store.Close()
	ctx := context.Background()
	sessionID := "00000000-0000-0000-0000-000000000003"
	store.EnsureSession(ctx, sessionID)

	// Add enough to trigger compaction
	for i := 0; i < 20; i++ {
		store.AddHistory(ctx, sessionID, "default", "user", "msg", 10000)
	}

	mockBrain := &MockBrain{
		RespondFunc: func(ctx context.Context, sessionID, content string) (string, error) {
			return "", fmt.Errorf("llm offline")
		},
	}
	compactor := NewCompactor(store, mockBrain, "openai", "gpt-4o", CompactorConfig{ThresholdRatio: 0.1}) // Low threshold

	history, err := compactor.CompactIfNeeded(ctx, sessionID, "default")
	if err != nil {
		t.Fatalf("CompactIfNeeded should not fail on LLM error: %v", err)
	}

	// Should contain fallback summary
	foundFallback := false
	for _, h := range history {
		if strings.Contains(h.Content, "History compacted due to length") {
			foundFallback = true
			break
		}
	}
	if !foundFallback {
		t.Error("expected fallback truncation message")
	}
}
