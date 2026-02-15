package persistence

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestSummaries(t *testing.T) {
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
			name: "save and load summary",
			fn: func(t *testing.T) {
				agentID := "test-agent"
				if err := store.SaveSummary(ctx, agentID, "test summary", 10); err != nil {
					t.Fatalf("save: %v", err)
				}
				summary, err := store.LoadLatestSummary(ctx, agentID)
				if err != nil {
					t.Fatalf("load: %v", err)
				}
				if summary.Summary != "test summary" || summary.MsgCount != 10 {
					t.Errorf("unexpected summary: %+v", summary)
				}
			},
		},
		{
			name: "load nonexistent summary returns empty",
			fn: func(t *testing.T) {
				summary, err := store.LoadLatestSummary(ctx, "nonexistent")
				if err != nil {
					t.Fatalf("load: %v", err)
				}
				if summary.Summary != "" {
					t.Errorf("expected empty summary, got: %s", summary.Summary)
				}
			},
		},
		{
			name: "delete summary",
			fn: func(t *testing.T) {
				agentID := uuid.NewString()
				store.SaveSummary(ctx, agentID, "temp", 5)
				if err := store.DeleteAgentSummaries(ctx, agentID); err != nil {
					t.Fatalf("delete: %v", err)
				}
				summary, _ := store.LoadLatestSummary(ctx, agentID)
				if summary.Summary != "" {
					t.Errorf("expected empty after delete")
				}
			},
		},
		{
			name: "isolation per agent",
			fn: func(t *testing.T) {
				if err := store.SaveSummary(ctx, "agent-a", "summary-a", 1); err != nil {
					t.Fatalf("save A: %v", err)
				}
				if err := store.SaveSummary(ctx, "agent-b", "summary-b", 2); err != nil {
					t.Fatalf("save B: %v", err)
				}
				summaryA, _ := store.LoadLatestSummary(ctx, "agent-a")
				summaryB, _ := store.LoadLatestSummary(ctx, "agent-b")
				if summaryA.Summary != "summary-a" || summaryB.Summary != "summary-b" {
					t.Errorf("isolation failed")
				}
			},
		},
		{
			name: "overwrites previous summary",
			fn: func(t *testing.T) {
				agentID := "overwrite-test"
				store.SaveSummary(ctx, agentID, "old", 1)
				store.SaveSummary(ctx, agentID, "new", 2)
				summary, _ := store.LoadLatestSummary(ctx, agentID)
				if summary.Summary != "new" || summary.MsgCount != 2 {
					t.Errorf("overwrite failed: got %+v", summary)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}
