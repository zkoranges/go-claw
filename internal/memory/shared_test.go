package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/persistence"
)

// mockSharedStore is a test double for SharedStore.
type mockSharedStore struct {
	memories []persistence.AgentMemory
	pins     []persistence.AgentPin
}

func (m *mockSharedStore) GetSharedMemories(ctx context.Context, targetAgentID string) ([]persistence.AgentMemory, error) {
	return m.memories, nil
}

func (m *mockSharedStore) GetSharedPinsForAgent(ctx context.Context, targetAgentID string) ([]persistence.AgentPin, error) {
	return m.pins, nil
}

func TestSharedContext_Format(t *testing.T) {
	t.Run("format_empty_shared_content", func(t *testing.T) {
		store := &mockSharedStore{}
		sc := NewSharedContext(store)

		ctx := context.Background()
		formatted, tokenCount, err := sc.Format(ctx, "test-agent")
		if err != nil {
			t.Fatalf("Format failed: %v", err)
		}

		if formatted != "" {
			t.Errorf("expected empty string for no shared content, got %q", formatted)
		}
		if tokenCount != 0 {
			t.Errorf("expected 0 tokens, got %d", tokenCount)
		}
	})

	t.Run("format_shared_memories_only", func(t *testing.T) {
		store := &mockSharedStore{
			memories: []persistence.AgentMemory{
				{
					ID:       1,
					AgentID:  "security",
					Key:      "sql-injection-risk",
					Value:    "API endpoint /users needs parametrized queries",
					Source:   "agent",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
					LastAccessed: time.Now(),
				},
			},
		}
		sc := NewSharedContext(store)

		ctx := context.Background()
		formatted, tokenCount, err := sc.Format(ctx, "coder")
		if err != nil {
			t.Fatalf("Format failed: %v", err)
		}

		if !strings.Contains(formatted, "From @security") {
			t.Errorf("formatted should contain source agent attribution")
		}
		if !strings.Contains(formatted, "sql-injection-risk") {
			t.Errorf("formatted should contain memory key")
		}
		if !strings.Contains(formatted, "API endpoint") {
			t.Errorf("formatted should contain memory value")
		}
		if !strings.Contains(formatted, "<shared_knowledge>") {
			t.Errorf("formatted should have opening tag")
		}
		if !strings.Contains(formatted, "</shared_knowledge>") {
			t.Errorf("formatted should have closing tag")
		}
		if tokenCount == 0 {
			t.Errorf("expected positive token count")
		}
	})

	t.Run("format_shared_pins_only", func(t *testing.T) {
		store := &mockSharedStore{
			pins: []persistence.AgentPin{
				{
					ID:       1,
					AgentID:  "researcher",
					PinType:  "text",
					Source:   "research-notes",
					Content:  "Key findings from literature review",
					TokenCount: 20,
					CreatedAt: time.Now(),
					LastRead: time.Now(),
				},
			},
		}
		sc := NewSharedContext(store)

		ctx := context.Background()
		formatted, tokenCount, err := sc.Format(ctx, "writer")
		if err != nil {
			t.Fatalf("Format failed: %v", err)
		}

		if !strings.Contains(formatted, "From @researcher") {
			t.Errorf("formatted should contain source agent")
		}
		if !strings.Contains(formatted, "research-notes") {
			t.Errorf("formatted should contain pin source")
		}
		if !strings.Contains(formatted, "Key findings") {
			t.Errorf("formatted should contain pin content")
		}
		if tokenCount == 0 {
			t.Errorf("expected positive token count")
		}
	})

	t.Run("format_mixed_memories_and_pins", func(t *testing.T) {
		store := &mockSharedStore{
			memories: []persistence.AgentMemory{
				{
					ID:       1,
					AgentID:  "security",
					Key:      "encryption",
					Value:    "Use AES-256 for all data at rest",
					Source:   "agent",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
					LastAccessed: time.Now(),
				},
			},
			pins: []persistence.AgentPin{
				{
					ID:       2,
					AgentID:  "security",
					PinType:  "file",
					Source:   "/policy/security.md",
					Content:  "Security policy document",
					TokenCount: 30,
					CreatedAt: time.Now(),
					LastRead: time.Now(),
				},
			},
		}
		sc := NewSharedContext(store)

		ctx := context.Background()
		formatted, tokenCount, err := sc.Format(ctx, "coder")
		if err != nil {
			t.Fatalf("Format failed: %v", err)
		}

		if !strings.Contains(formatted, "From @security") {
			t.Errorf("formatted should contain source agent once")
		}
		if !strings.Contains(formatted, "encryption") {
			t.Errorf("formatted should contain memory key")
		}
		if !strings.Contains(formatted, "security.md") {
			t.Errorf("formatted should contain pin label (extracted from path)")
		}
		if tokenCount == 0 {
			t.Errorf("expected positive token count for mixed content")
		}
	})

	t.Run("format_multiple_source_agents", func(t *testing.T) {
		store := &mockSharedStore{
			memories: []persistence.AgentMemory{
				{
					ID:       1,
					AgentID:  "security",
					Key:      "policy",
					Value:    "strict",
					Source:   "agent",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
					LastAccessed: time.Now(),
				},
				{
					ID:       2,
					AgentID:  "devops",
					Key:      "env",
					Value:    "production",
					Source:   "agent",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
					LastAccessed: time.Now(),
				},
			},
		}
		sc := NewSharedContext(store)

		ctx := context.Background()
		formatted, tokenCount, err := sc.Format(ctx, "coder")
		if err != nil {
			t.Fatalf("Format failed: %v", err)
		}

		if !strings.Contains(formatted, "From @security") {
			t.Errorf("formatted should contain security agent attribution")
		}
		if !strings.Contains(formatted, "From @devops") {
			t.Errorf("formatted should contain devops agent attribution")
		}
		if tokenCount == 0 {
			t.Errorf("expected positive token count")
		}
	})
}
