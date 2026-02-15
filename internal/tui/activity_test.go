package tui

import (
	"testing"
	"time"
)

func TestActivityFeed_AddAndLen(t *testing.T) {
	f := NewActivityFeed()
	if f.Len() != 0 {
		t.Fatal("new feed should be empty")
	}
	f.Add(ActivityItem{ID: "1", Icon: "⏳", Message: "test", StartedAt: time.Now()})
	if f.Len() != 1 {
		t.Fatal("len should be 1")
	}
}

func TestActivityFeed_MaxItems(t *testing.T) {
	f := NewActivityFeed()
	f.maxItems = 3
	for i := 0; i < 5; i++ {
		f.Add(ActivityItem{ID: string(rune(i)), StartedAt: time.Now()})
	}
	if f.Len() != 3 {
		t.Fatalf("expected 3, got %d", f.Len())
	}
}

func TestActivityFeed_Complete(t *testing.T) {
	f := NewActivityFeed()
	f.Add(ActivityItem{ID: "t1", Icon: "⏳", StartedAt: time.Now()})
	if !f.HasActive() {
		t.Fatal("should have active")
	}
	f.Complete("t1", "✅", 0.005)
	if f.HasActive() {
		t.Fatal("should have no active")
	}
}

func TestActivityFeed_CompleteNonExistent(t *testing.T) {
	f := NewActivityFeed()
	f.Add(ActivityItem{ID: "t1", StartedAt: time.Now()})
	f.Complete("nope", "✅", 0)
	if !f.HasActive() {
		t.Fatal("original should still be active")
	}
}

func TestActivityFeed_CleanupOld(t *testing.T) {
	f := NewActivityFeed()
	past := time.Now().Add(-2 * time.Minute)
	done := past.Add(10 * time.Second)
	f.Add(ActivityItem{ID: "old", StartedAt: past, DoneAt: &done})
	f.Add(ActivityItem{ID: "active", StartedAt: time.Now()})
	removed := f.CleanupOld(30 * time.Second)
	if removed != 1 {
		t.Fatalf("removed %d", removed)
	}
	if f.Len() != 1 {
		t.Fatal("should have 1 remaining")
	}
}

func TestActivityFeed_CleanupKeepsRecent(t *testing.T) {
	f := NewActivityFeed()
	now := time.Now()
	recent := now.Add(-5 * time.Second)
	f.Add(ActivityItem{ID: "r", StartedAt: now.Add(-10 * time.Second), DoneAt: &recent})
	if f.CleanupOld(30*time.Second) != 0 {
		t.Fatal("should not remove recent")
	}
}

func TestActivityFeed_HasActiveEmpty(t *testing.T) {
	if NewActivityFeed().HasActive() {
		t.Fatal("empty feed not active")
	}
}

func TestActivityFeed_Toggle(t *testing.T) {
	f := NewActivityFeed()
	if !f.collapsed {
		t.Fatal("should start collapsed")
	}
	f.Toggle()
	if f.collapsed {
		t.Fatal("should be expanded")
	}
}

func TestActivityFeed_AutoExpand(t *testing.T) {
	f := NewActivityFeed()
	f.Add(ActivityItem{ID: "1", StartedAt: time.Now()})
	if f.collapsed {
		t.Fatal("should auto-expand on add")
	}
}

func TestActivityFeed_ViewEmpty(t *testing.T) {
	if NewActivityFeed().View() != "" {
		t.Fatal("empty view should be empty string")
	}
}

func TestActivityFeed_ViewCollapsed(t *testing.T) {
	f := NewActivityFeed()
	f.Add(ActivityItem{ID: "1", Icon: "⏳", Message: "test", StartedAt: time.Now()})
	f.Toggle() // collapse
	view := f.View()
	if view == "" {
		t.Fatal("collapsed view should show active count")
	}
}

func TestActivityFeed_ViewExpanded(t *testing.T) {
	f := NewActivityFeed()
	f.Add(ActivityItem{ID: "1", Icon: "⏳", Message: "test", StartedAt: time.Now()})
	view := f.View()
	if view == "" {
		t.Fatal("expanded view should have content")
	}
}
