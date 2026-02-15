package persistence

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPins_AddAndRetrieve(t *testing.T) {
	t.Run("add_file_pin_and_retrieve", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		filePath := "/path/to/file.go"
		content := "package main\n\nfunc main() {}"

		err = store.AddPin(ctx, "test-agent", "file", filePath, content, false)
		if err != nil {
			t.Fatalf("AddPin failed: %v", err)
		}

		pin, err := store.GetPin(ctx, "test-agent", filePath)
		if err != nil {
			t.Fatalf("GetPin failed: %v", err)
		}

		if pin.AgentID != "test-agent" {
			t.Errorf("expected agent_id=test-agent, got %s", pin.AgentID)
		}
		if pin.PinType != "file" {
			t.Errorf("expected pin_type=file, got %s", pin.PinType)
		}
		if pin.Source != filePath {
			t.Errorf("expected source=%s, got %s", filePath, pin.Source)
		}
		if pin.Content != content {
			t.Errorf("expected content to match")
		}
		if pin.Shared {
			t.Errorf("expected shared=false")
		}
	})

	t.Run("add_text_pin", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		label := "my-notes"
		content := "Important project notes"

		err = store.AddPin(ctx, "test-agent", "text", label, content, false)
		if err != nil {
			t.Fatalf("AddPin failed: %v", err)
		}

		pin, err := store.GetPin(ctx, "test-agent", label)
		if err != nil {
			t.Fatalf("GetPin failed: %v", err)
		}

		if pin.PinType != "text" {
			t.Errorf("expected pin_type=text, got %s", pin.PinType)
		}
		if pin.Content != content {
			t.Errorf("expected content to match")
		}
	})
}

func TestPins_Remove(t *testing.T) {
	t.Run("remove_pin", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		source := "/path/to/file.go"
		err = store.AddPin(ctx, "test-agent", "file", source, "content", false)
		if err != nil {
			t.Fatalf("AddPin failed: %v", err)
		}

		// Verify pin exists
		pin, err := store.GetPin(ctx, "test-agent", source)
		if err != nil {
			t.Fatalf("GetPin failed: %v", err)
		}
		if pin.Source == "" {
			t.Fatalf("pin should exist before removal")
		}

		// Remove pin
		err = store.RemovePin(ctx, "test-agent", source)
		if err != nil {
			t.Fatalf("RemovePin failed: %v", err)
		}

		// Verify pin is gone - GetPin should return an error (sql.ErrNoRows)
		pin, err = store.GetPin(ctx, "test-agent", source)
		if err == nil {
			t.Errorf("expected error when getting removed pin, but got none")
		}
		// When error occurs, pin should be empty
		if pin.Source != "" {
			t.Errorf("pin should be removed")
		}
	})
}

func TestPins_List(t *testing.T) {
	t.Run("list_pins_for_agent", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		// Add multiple pins
		err = store.AddPin(ctx, "test-agent", "file", "/path/file1.go", "content1", false)
		if err != nil {
			t.Fatalf("AddPin failed: %v", err)
		}

		err = store.AddPin(ctx, "test-agent", "text", "notes", "content2", false)
		if err != nil {
			t.Fatalf("AddPin failed: %v", err)
		}

		// List pins
		pins, err := store.ListPins(ctx, "test-agent")
		if err != nil {
			t.Fatalf("ListPins failed: %v", err)
		}

		if len(pins) != 2 {
			t.Errorf("expected 2 pins, got %d", len(pins))
		}

		// Verify both pins are present
		sources := make(map[string]bool)
		for _, pin := range pins {
			sources[pin.Source] = true
		}
		if !sources["/path/file1.go"] {
			t.Errorf("file1.go pin not found")
		}
		if !sources["notes"] {
			t.Errorf("notes pin not found")
		}
	})

	t.Run("list_pins_empty_agent", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		pins, err := store.ListPins(ctx, "nonexistent-agent")
		if err != nil {
			t.Fatalf("ListPins failed: %v", err)
		}

		if len(pins) != 0 {
			t.Errorf("expected 0 pins for nonexistent agent, got %d", len(pins))
		}
	})
}

func TestPins_Upsert(t *testing.T) {
	t.Run("duplicate_pin_updates_content", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		source := "/path/file.go"
		originalContent := "original"
		newContent := "modified"

		// Add pin
		err = store.AddPin(ctx, "test-agent", "file", source, originalContent, false)
		if err != nil {
			t.Fatalf("AddPin failed: %v", err)
		}

		// Update with same source
		err = store.AddPin(ctx, "test-agent", "file", source, newContent, false)
		if err != nil {
			t.Fatalf("AddPin (update) failed: %v", err)
		}

		// Retrieve and verify updated content
		pin, err := store.GetPin(ctx, "test-agent", source)
		if err != nil {
			t.Fatalf("GetPin failed: %v", err)
		}

		if pin.Content != newContent {
			t.Errorf("expected updated content %q, got %q", newContent, pin.Content)
		}

		// Should still have only 1 pin
		pins, err := store.ListPins(ctx, "test-agent")
		if err != nil {
			t.Fatalf("ListPins failed: %v", err)
		}
		if len(pins) != 1 {
			t.Errorf("expected 1 pin after upsert, got %d", len(pins))
		}
	})
}

func TestPins_UpdateContent(t *testing.T) {
	t.Run("update_pin_content_and_mtime", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		source := "/path/file.go"
		originalContent := "original"

		// Add pin
		err = store.AddPin(ctx, "test-agent", "file", source, originalContent, false)
		if err != nil {
			t.Fatalf("AddPin failed: %v", err)
		}

		// Update pin content with mtime
		newContent := "updated content"
		newMtime := "2026-02-15 12:00:00"

		err = store.UpdatePinContent(ctx, "test-agent", source, newContent, newMtime)
		if err != nil {
			t.Fatalf("UpdatePinContent failed: %v", err)
		}

		// Retrieve and verify
		pin, err := store.GetPin(ctx, "test-agent", source)
		if err != nil {
			t.Fatalf("GetPin failed: %v", err)
		}

		if pin.Content != newContent {
			t.Errorf("expected content %q, got %q", newContent, pin.Content)
		}
		if pin.FileMtime != newMtime {
			t.Errorf("expected mtime %q, got %q", newMtime, pin.FileMtime)
		}
	})
}

func TestPins_Shared(t *testing.T) {
	t.Run("shared_pin_visible_via_GetSharedPins", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		// Add shared pin from agent A
		err = store.AddPin(ctx, "agent-a", "text", "shared-note", "shared content", true)
		if err != nil {
			t.Fatalf("AddPin failed: %v", err)
		}

		// Get shared pins (visible to all)
		sharedPins, err := store.GetSharedPins(ctx, "agent-b")
		if err != nil {
			t.Fatalf("GetSharedPins failed: %v", err)
		}

		// Should see the shared pin from agent A
		found := false
		for _, pin := range sharedPins {
			if pin.AgentID == "agent-a" && pin.Source == "shared-note" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("shared pin not found via GetSharedPins")
		}
	})

	t.Run("non_shared_pin_not_visible", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		// Add non-shared pin from agent A
		err = store.AddPin(ctx, "agent-a", "text", "private-note", "private content", false)
		if err != nil {
			t.Fatalf("AddPin failed: %v", err)
		}

		// Get shared pins
		sharedPins, err := store.GetSharedPins(ctx, "agent-b")
		if err != nil {
			t.Fatalf("GetSharedPins failed: %v", err)
		}

		// Should NOT see the non-shared pin
		for _, pin := range sharedPins {
			if pin.AgentID == "agent-a" && pin.Source == "private-note" {
				t.Errorf("non-shared pin should not be visible")
			}
		}
	})

	t.Run("isolation_per_agent", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		// Add pins for different agents
		err = store.AddPin(ctx, "agent-a", "text", "pin", "content-a", false)
		if err != nil {
			t.Fatalf("AddPin failed: %v", err)
		}

		err = store.AddPin(ctx, "agent-b", "text", "pin", "content-b", false)
		if err != nil {
			t.Fatalf("AddPin failed: %v", err)
		}

		// Each agent should see only their own pin
		pinsA, err := store.ListPins(ctx, "agent-a")
		if err != nil {
			t.Fatalf("ListPins failed: %v", err)
		}

		pinsB, err := store.ListPins(ctx, "agent-b")
		if err != nil {
			t.Fatalf("ListPins failed: %v", err)
		}

		if len(pinsA) != 1 || pinsA[0].Content != "content-a" {
			t.Errorf("agent-a should see only their pin with content-a")
		}
		if len(pinsB) != 1 || pinsB[0].Content != "content-b" {
			t.Errorf("agent-b should see only their pin with content-b")
		}
	})

	t.Run("token_count_calculation", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		// Add pin with known content length
		content := "12345678901234567890" // 20 bytes → 5 tokens
		err = store.AddPin(ctx, "test-agent", "text", "pin", content, false)
		if err != nil {
			t.Fatalf("AddPin failed: %v", err)
		}

		pin, err := store.GetPin(ctx, "test-agent", "pin")
		if err != nil {
			t.Fatalf("GetPin failed: %v", err)
		}

		// 20 bytes / 4 = 5 tokens
		expectedTokens := (len(content) + 3) / 4
		if pin.TokenCount != expectedTokens {
			t.Errorf("expected %d tokens, got %d", expectedTokens, pin.TokenCount)
		}
	})
}

func TestPins_Timestamps(t *testing.T) {
	t.Run("pin_timestamps", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		beforeAdd := time.Now().Add(-1 * time.Second) // Allow 1 second before for rounding

		err = store.AddPin(ctx, "test-agent", "text", "pin", "content", false)
		if err != nil {
			t.Fatalf("AddPin failed: %v", err)
		}

		afterAdd := time.Now().Add(1 * time.Second) // Allow 1 second after for rounding

		pin, err := store.GetPin(ctx, "test-agent", "pin")
		if err != nil {
			t.Fatalf("GetPin failed: %v", err)
		}

		// CreatedAt should be between beforeAdd and afterAdd
		if pin.CreatedAt.Before(beforeAdd) || pin.CreatedAt.After(afterAdd) {
			t.Errorf("CreatedAt timestamp not in expected range: %v (before: %v, after: %v)", pin.CreatedAt, beforeAdd, afterAdd)
		}

		// LastRead should also be set
		if pin.LastRead.Before(beforeAdd) || pin.LastRead.After(afterAdd) {
			t.Errorf("LastRead timestamp not in expected range: %v (before: %v, after: %v)", pin.LastRead, beforeAdd, afterAdd)
		}
	})
}
