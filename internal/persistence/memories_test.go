package persistence

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemories(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := Open(dbPath, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{
			name: "set and get",
			fn: func(t *testing.T) {
				agentID := "test-agent"
				if err := store.SetMemory(ctx, agentID, "language", "Go", "user"); err != nil {
					t.Fatalf("SetMemory: %v", err)
				}
				mem, err := store.GetMemory(ctx, agentID, "language")
				if err != nil {
					t.Fatalf("GetMemory: %v", err)
				}
				if mem.Value != "Go" || mem.Source != "user" {
					t.Errorf("unexpected memory: %+v", mem)
				}
			},
		},
		{
			name: "set overwrites existing, resets relevance to 1.0",
			fn: func(t *testing.T) {
				agentID := uuid.NewString()
				// Set initial value
				if err := store.SetMemory(ctx, agentID, "project", "project-a", "user"); err != nil {
					t.Fatalf("first SetMemory: %v", err)
				}
				mem1, _ := store.GetMemory(ctx, agentID, "project")
				if mem1.RelevanceScore != 1.0 {
					t.Errorf("expected relevance 1.0, got %v", mem1.RelevanceScore)
				}
				// Decay the relevance
				if err := store.DecayMemories(ctx, agentID, 0.5); err != nil {
					t.Fatalf("DecayMemories: %v", err)
				}
				mem2, _ := store.GetMemory(ctx, agentID, "project")
				if mem2.RelevanceScore >= 1.0 {
					t.Errorf("expected relevance to decay, got %v", mem2.RelevanceScore)
				}
				// Update with same key
				if err := store.SetMemory(ctx, agentID, "project", "project-b", "agent"); err != nil {
					t.Fatalf("second SetMemory: %v", err)
				}
				mem3, _ := store.GetMemory(ctx, agentID, "project")
				if mem3.Value != "project-b" || mem3.RelevanceScore != 1.0 {
					t.Errorf("unexpected memory after update: %+v", mem3)
				}
			},
		},
		{
			name: "get nonexistent returns error",
			fn: func(t *testing.T) {
				_, err := store.GetMemory(ctx, "nonexistent", "missing")
				if err == nil {
					t.Error("expected error for nonexistent memory")
				}
			},
		},
		{
			name: "list all memories ordered by relevance DESC then updated_at DESC",
			fn: func(t *testing.T) {
				agentID := uuid.NewString()
				store.SetMemory(ctx, agentID, "lang", "Go", "user")
				store.SetMemory(ctx, agentID, "project", "goclaw", "user")
				store.SetMemory(ctx, agentID, "style", "concise", "user")

				mems, err := store.ListMemories(ctx, agentID)
				if err != nil {
					t.Fatalf("ListMemories: %v", err)
				}
				if len(mems) != 3 {
					t.Errorf("expected 3 memories, got %d", len(mems))
				}
				// All have same relevance (1.0), so should be ordered by updated_at DESC (most recent first)
				if mems[0].RelevanceScore < mems[1].RelevanceScore {
					t.Errorf("memories not ordered by relevance DESC")
				}
			},
		},
		{
			name: "list empty agent returns empty slice",
			fn: func(t *testing.T) {
				mems, err := store.ListMemories(ctx, "empty-agent")
				if err != nil {
					t.Fatalf("ListMemories: %v", err)
				}
				if len(mems) != 0 {
					t.Errorf("expected empty, got %d memories", len(mems))
				}
			},
		},
		{
			name: "list top N respects limit",
			fn: func(t *testing.T) {
				agentID := uuid.NewString()
				for i := 0; i < 10; i++ {
					key := "mem-" + string(rune(i))
					store.SetMemory(ctx, agentID, key, "val", "user")
				}

				topN, err := store.ListTopMemories(ctx, agentID, 3)
				if err != nil {
					t.Fatalf("ListTopMemories: %v", err)
				}
				if len(topN) != 3 {
					t.Errorf("expected 3 top memories, got %d", len(topN))
				}
			},
		},
		{
			name: "delete memory",
			fn: func(t *testing.T) {
				agentID := uuid.NewString()
				store.SetMemory(ctx, agentID, "temp", "temporary", "user")
				if err := store.DeleteMemory(ctx, agentID, "temp"); err != nil {
					t.Fatalf("DeleteMemory: %v", err)
				}
				_, err := store.GetMemory(ctx, agentID, "temp")
				if err == nil {
					t.Error("expected error after delete")
				}
			},
		},
		{
			name: "delete nonexistent is no-op",
			fn: func(t *testing.T) {
				agentID := uuid.NewString()
				if err := store.DeleteMemory(ctx, agentID, "nonexistent"); err != nil {
					t.Fatalf("DeleteMemory nonexistent: %v", err)
				}
			},
		},
		{
			name: "search by key substring",
			fn: func(t *testing.T) {
				agentID := uuid.NewString()
				store.SetMemory(ctx, agentID, "user_language", "Go", "user")
				store.SetMemory(ctx, agentID, "user_preference", "tabs", "user")
				store.SetMemory(ctx, agentID, "project_name", "goclaw", "user")

				results, err := store.SearchMemories(ctx, agentID, "user_")
				if err != nil {
					t.Fatalf("SearchMemories: %v", err)
				}
				if len(results) != 2 {
					t.Errorf("expected 2 results for 'user_', got %d", len(results))
				}
			},
		},
		{
			name: "search by value substring",
			fn: func(t *testing.T) {
				agentID := uuid.NewString()
				store.SetMemory(ctx, agentID, "lang1", "Go 1.22", "user")
				store.SetMemory(ctx, agentID, "lang2", "Python 3.11", "user")
				store.SetMemory(ctx, agentID, "style", "Go style", "user")

				results, err := store.SearchMemories(ctx, agentID, "Go")
				if err != nil {
					t.Fatalf("SearchMemories: %v", err)
				}
				if len(results) != 2 {
					t.Errorf("expected 2 results for 'Go', got %d", len(results))
				}
			},
		},
		{
			name: "search no match returns empty",
			fn: func(t *testing.T) {
				agentID := uuid.NewString()
				store.SetMemory(ctx, agentID, "language", "Go", "user")

				results, err := store.SearchMemories(ctx, agentID, "Rust")
				if err != nil {
					t.Fatalf("SearchMemories: %v", err)
				}
				if len(results) != 0 {
					t.Errorf("expected no results, got %d", len(results))
				}
			},
		},
		{
			name: "isolation per agent",
			fn: func(t *testing.T) {
				agent1 := uuid.NewString()
				agent2 := uuid.NewString()
				store.SetMemory(ctx, agent1, "key1", "agent1-value", "user")
				store.SetMemory(ctx, agent2, "key1", "agent2-value", "user")

				mem1, _ := store.GetMemory(ctx, agent1, "key1")
				mem2, _ := store.GetMemory(ctx, agent2, "key1")
				if mem1.Value != "agent1-value" || mem2.Value != "agent2-value" {
					t.Errorf("memories not isolated per agent")
				}
			},
		},
		{
			name: "touch increments access_count and updates last_accessed",
			fn: func(t *testing.T) {
				agentID := uuid.NewString()
				store.SetMemory(ctx, agentID, "key", "value", "user")
				mem1, _ := store.GetMemory(ctx, agentID, "key")
				initialCount := mem1.AccessCount
				initialAccess := mem1.LastAccessed

				// Small delay to ensure timestamp difference
				time.Sleep(100 * time.Millisecond)

				if err := store.TouchMemory(ctx, agentID, "key"); err != nil {
					t.Fatalf("TouchMemory: %v", err)
				}
				mem2, _ := store.GetMemory(ctx, agentID, "key")
				if mem2.AccessCount != initialCount+1 {
					t.Errorf("expected access_count %d, got %d", initialCount+1, mem2.AccessCount)
				}
				if mem2.LastAccessed.Before(initialAccess) {
					t.Errorf("expected last_accessed to be updated")
				}
			},
		},
		{
			name: "decay reduces all relevance scores by factor",
			fn: func(t *testing.T) {
				agentID := uuid.NewString()
				store.SetMemory(ctx, agentID, "mem1", "val1", "user")
				store.SetMemory(ctx, agentID, "mem2", "val2", "user")

				before1, _ := store.GetMemory(ctx, agentID, "mem1")
				before2, _ := store.GetMemory(ctx, agentID, "mem2")

				if err := store.DecayMemories(ctx, agentID, 0.8); err != nil {
					t.Fatalf("DecayMemories: %v", err)
				}

				after1, _ := store.GetMemory(ctx, agentID, "mem1")
				after2, _ := store.GetMemory(ctx, agentID, "mem2")

				expectedScore1 := before1.RelevanceScore * 0.8
				expectedScore2 := before2.RelevanceScore * 0.8

				if after1.RelevanceScore < expectedScore1-0.001 || after1.RelevanceScore > expectedScore1+0.001 {
					t.Errorf("expected score ~%v, got %v", expectedScore1, after1.RelevanceScore)
				}
				if after2.RelevanceScore < expectedScore2-0.001 || after2.RelevanceScore > expectedScore2+0.001 {
					t.Errorf("expected score ~%v, got %v", expectedScore2, after2.RelevanceScore)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}
