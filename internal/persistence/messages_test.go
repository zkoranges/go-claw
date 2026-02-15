package persistence

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestPhase1_ConversationPersistence is the comprehensive test suite for Phase 1.
// Minimum 12 subtests as per PDR v6 Phase 1 requirements.
func TestPhase1_ConversationPersistence(t *testing.T) {
	// Setup test store
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "goclaw.db")
	store, err := Open(dbPath, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	ctx := context.Background()

	// Test scenarios per PDR Phase 1 Step 1.3
	tests := []struct {
		name string
		fn   func(t *testing.T, s *Store, ctx context.Context)
	}{
		{
			name: "save and load single message",
			fn: func(t *testing.T, s *Store, ctx context.Context) {
				sessionID := uuid.NewString()
				if err := s.EnsureSession(ctx, sessionID); err != nil {
					t.Fatalf("ensure session: %v", err)
				}
				if err := s.AddHistory(ctx, sessionID, "coder", "user", "hello", 1); err != nil {
					t.Fatalf("add history: %v", err)
				}
				items, err := s.LoadRecentMessages(ctx, "coder", sessionID, 10)
				if err != nil {
					t.Fatalf("load messages: %v", err)
				}
				if len(items) != 1 || items[0].Content != "hello" {
					t.Fatalf("expected 1 message with content 'hello', got %d: %v", len(items), items)
				}
			},
		},
		{
			name: "save multiple messages, verify order (oldest first)",
			fn: func(t *testing.T, s *Store, ctx context.Context) {
				sessionID := uuid.NewString()
				if err := s.EnsureSession(ctx, sessionID); err != nil {
					t.Fatalf("ensure session: %v", err)
				}
				// Add 3 messages in sequence
				for i, msg := range []string{"first", "second", "third"} {
					if err := s.AddHistory(ctx, sessionID, "researcher", "user", msg, 1); err != nil {
						t.Fatalf("add message %d: %v", i, err)
					}
				}
				items, err := s.LoadRecentMessages(ctx, "researcher", sessionID, 10)
				if err != nil {
					t.Fatalf("load messages: %v", err)
				}
				if len(items) != 3 {
					t.Fatalf("expected 3 messages, got %d", len(items))
				}
				if items[0].Content != "first" || items[1].Content != "second" || items[2].Content != "third" {
					t.Fatalf("message order incorrect: %v", items)
				}
			},
		},
		{
			name: "load with limit (save 20, load 10 — get last 10)",
			fn: func(t *testing.T, s *Store, ctx context.Context) {
				sessionID := uuid.NewString()
				if err := s.EnsureSession(ctx, sessionID); err != nil {
					t.Fatalf("ensure session: %v", err)
				}
				// Add 20 messages
				for range 20 {
					if err := s.AddHistory(ctx, sessionID, "writer", "user", "msg", 1); err != nil {
						t.Fatalf("add message: %v", err)
					}
				}
				// Load last 10
				items, err := s.LoadRecentMessages(ctx, "writer", sessionID, 10)
				if err != nil {
					t.Fatalf("load messages: %v", err)
				}
				if len(items) != 10 {
					t.Fatalf("expected 10 messages (limit), got %d", len(items))
				}
			},
		},
		{
			name: "load for empty agent returns empty slice",
			fn: func(t *testing.T, s *Store, ctx context.Context) {
				sessionID := uuid.NewString()
				if err := s.EnsureSession(ctx, sessionID); err != nil {
					t.Fatalf("ensure session: %v", err)
				}
				items, err := s.LoadRecentMessages(ctx, "nonexistent", sessionID, 10)
				if err != nil {
					t.Fatalf("load messages: %v", err)
				}
				if len(items) != 0 {
					t.Fatalf("expected empty slice for nonexistent agent, got %d messages", len(items))
				}
			},
		},
		{
			name: "load for nonexistent session returns empty slice",
			fn: func(t *testing.T, s *Store, ctx context.Context) {
				items, err := s.LoadRecentMessages(ctx, "coder", uuid.NewString(), 10)
				if err != nil {
					t.Fatalf("load messages: %v", err)
				}
				if len(items) != 0 {
					t.Fatalf("expected empty slice for nonexistent session, got %d messages", len(items))
				}
			},
		},
		{
			name: "LoadMessagesSince exists and is callable",
			fn: func(t *testing.T, s *Store, ctx context.Context) {
				sessionID := uuid.NewString()
				if err := s.EnsureSession(ctx, sessionID); err != nil {
					t.Fatalf("ensure session: %v", err)
				}
				// Just verify the method exists and works with basic data
				if err := s.AddHistory(ctx, sessionID, "coder", "user", "test", 1); err != nil {
					t.Fatalf("add message: %v", err)
				}
				// Query with a far past timestamp
				oldTime := time.Now().AddDate(-1, 0, 0)
				items, err := s.LoadMessagesSince(ctx, "coder", sessionID, oldTime)
				if err != nil {
					t.Fatalf("load messages since: %v", err)
				}
				// Should get the message since it's after the old timestamp
				if len(items) < 1 {
					t.Fatalf("expected at least 1 message, got %d", len(items))
				}
			},
		},
		{
			name: "count messages",
			fn: func(t *testing.T, s *Store, ctx context.Context) {
				sessionID := uuid.NewString()
				if err := s.EnsureSession(ctx, sessionID); err != nil {
					t.Fatalf("ensure session: %v", err)
				}
				// Add 5 messages
				for range 5 {
					if err := s.AddHistory(ctx, sessionID, "coder", "user", "msg", 1); err != nil {
						t.Fatalf("add message: %v", err)
					}
				}
				count, err := s.CountMessages(ctx, "coder", sessionID)
				if err != nil {
					t.Fatalf("count messages: %v", err)
				}
				if count != 5 {
					t.Fatalf("expected 5 messages, got %d", count)
				}
			},
		},
		{
			name: "count for empty agent returns 0",
			fn: func(t *testing.T, s *Store, ctx context.Context) {
				sessionID := uuid.NewString()
				if err := s.EnsureSession(ctx, sessionID); err != nil {
					t.Fatalf("ensure session: %v", err)
				}
				count, err := s.CountMessages(ctx, "nonexistent", sessionID)
				if err != nil {
					t.Fatalf("count messages: %v", err)
				}
				if count != 0 {
					t.Fatalf("expected 0 messages, got %d", count)
				}
			},
		},
		{
			name: "delete agent messages",
			fn: func(t *testing.T, s *Store, ctx context.Context) {
				sessionID := uuid.NewString()
				if err := s.EnsureSession(ctx, sessionID); err != nil {
					t.Fatalf("ensure session: %v", err)
				}
				// Add 3 messages
				for range 3 {
					if err := s.AddHistory(ctx, sessionID, "coder", "user", "msg", 1); err != nil {
						t.Fatalf("add message: %v", err)
					}
				}
				// Delete
				if err := s.DeleteAgentMessages(ctx, "coder", sessionID); err != nil {
					t.Fatalf("delete messages: %v", err)
				}
				// Verify empty
				items, err := s.LoadRecentMessages(ctx, "coder", sessionID, 10)
				if err != nil {
					t.Fatalf("load messages: %v", err)
				}
				if len(items) != 0 {
					t.Fatalf("expected 0 messages after delete, got %d", len(items))
				}
			},
		},
		{
			name: "delete then load returns empty",
			fn: func(t *testing.T, s *Store, ctx context.Context) {
				sessionID := uuid.NewString()
				if err := s.EnsureSession(ctx, sessionID); err != nil {
					t.Fatalf("ensure session: %v", err)
				}
				// Add message
				if err := s.AddHistory(ctx, sessionID, "coder", "user", "msg", 1); err != nil {
					t.Fatalf("add message: %v", err)
				}
				// Delete
				if err := s.DeleteAgentMessages(ctx, "coder", sessionID); err != nil {
					t.Fatalf("delete messages: %v", err)
				}
				// Load should be empty
				items, err := s.LoadRecentMessages(ctx, "coder", sessionID, 10)
				if err != nil {
					t.Fatalf("load messages: %v", err)
				}
				if len(items) != 0 {
					t.Fatalf("expected 0 messages, got %d", len(items))
				}
			},
		},
		{
			name: "save with empty content (should work)",
			fn: func(t *testing.T, s *Store, ctx context.Context) {
				sessionID := uuid.NewString()
				if err := s.EnsureSession(ctx, sessionID); err != nil {
					t.Fatalf("ensure session: %v", err)
				}
				// Add message with empty content
				if err := s.AddHistory(ctx, sessionID, "coder", "user", "", 0); err != nil {
					t.Fatalf("add empty message: %v", err)
				}
				items, err := s.LoadRecentMessages(ctx, "coder", sessionID, 10)
				if err != nil {
					t.Fatalf("load messages: %v", err)
				}
				if len(items) != 1 || items[0].Content != "" {
					t.Fatalf("expected empty message, got %v", items)
				}
			},
		},
		{
			name: "save with unicode content (emoji, CJK characters)",
			fn: func(t *testing.T, s *Store, ctx context.Context) {
				sessionID := uuid.NewString()
				if err := s.EnsureSession(ctx, sessionID); err != nil {
					t.Fatalf("ensure session: %v", err)
				}
				// Add messages with unicode
				content := "Hello 👋 世界 こんにちは 🌍"
				if err := s.AddHistory(ctx, sessionID, "coder", "user", content, 1); err != nil {
					t.Fatalf("add unicode message: %v", err)
				}
				items, err := s.LoadRecentMessages(ctx, "coder", sessionID, 10)
				if err != nil {
					t.Fatalf("load messages: %v", err)
				}
				if len(items) != 1 || items[0].Content != content {
					t.Fatalf("expected unicode content, got %v", items)
				}
			},
		},
		{
			name: "messages are isolated per agent",
			fn: func(t *testing.T, s *Store, ctx context.Context) {
				sessionID := uuid.NewString()
				if err := s.EnsureSession(ctx, sessionID); err != nil {
					t.Fatalf("ensure session: %v", err)
				}
				// Add to agent A
				if err := s.AddHistory(ctx, sessionID, "agentA", "user", "msgA", 1); err != nil {
					t.Fatalf("add to agent A: %v", err)
				}
				// Add to agent B
				if err := s.AddHistory(ctx, sessionID, "agentB", "user", "msgB", 1); err != nil {
					t.Fatalf("add to agent B: %v", err)
				}
				// Load from agent A - should only see msgA
				itemsA, err := s.LoadRecentMessages(ctx, "agentA", sessionID, 10)
				if err != nil {
					t.Fatalf("load from agent A: %v", err)
				}
				if len(itemsA) != 1 || itemsA[0].Content != "msgA" {
					t.Fatalf("agent A should only see msgA, got %v", itemsA)
				}
				// Load from agent B - should only see msgB
				itemsB, err := s.LoadRecentMessages(ctx, "agentB", sessionID, 10)
				if err != nil {
					t.Fatalf("load from agent B: %v", err)
				}
				if len(itemsB) != 1 || itemsB[0].Content != "msgB" {
					t.Fatalf("agent B should only see msgB, got %v", itemsB)
				}
			},
		},
		{
			name: "save different message roles (user, assistant, system, tool)",
			fn: func(t *testing.T, s *Store, ctx context.Context) {
				sessionID := uuid.NewString()
				if err := s.EnsureSession(ctx, sessionID); err != nil {
					t.Fatalf("ensure session: %v", err)
				}
				roles := []string{"user", "assistant", "system", "tool"}
				for _, role := range roles {
					if err := s.AddHistory(ctx, sessionID, "coder", role, "content", 1); err != nil {
						t.Fatalf("add message with role %s: %v", role, err)
					}
				}
				items, err := s.LoadRecentMessages(ctx, "coder", sessionID, 10)
				if err != nil {
					t.Fatalf("load messages: %v", err)
				}
				if len(items) != 4 {
					t.Fatalf("expected 4 messages, got %d", len(items))
				}
				for i, role := range roles {
					if items[i].Role != role {
						t.Fatalf("expected role %s at position %d, got %s", role, i, items[i].Role)
					}
				}
			},
		},
		{
			name: "token count is preserved",
			fn: func(t *testing.T, s *Store, ctx context.Context) {
				sessionID := uuid.NewString()
				if err := s.EnsureSession(ctx, sessionID); err != nil {
					t.Fatalf("ensure session: %v", err)
				}
				if err := s.AddHistory(ctx, sessionID, "coder", "user", "test", 42); err != nil {
					t.Fatalf("add message: %v", err)
				}
				items, err := s.LoadRecentMessages(ctx, "coder", sessionID, 10)
				if err != nil {
					t.Fatalf("load messages: %v", err)
				}
				if items[0].Tokens != 42 {
					t.Fatalf("expected 42 tokens, got %d", items[0].Tokens)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.fn(t, store, ctx)
		})
	}
}
