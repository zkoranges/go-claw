package memory

import (
	"context"
	"os"
	"testing"
	"time"
)

// mockPinStore is a test double for PinStore.
type mockPinStore struct {
	pins       map[string]map[string]AgentPin // [agentID][source]pin
	lastAction string
	lastError  error
}

func (m *mockPinStore) AddPin(ctx context.Context, agentID, pinType, source, content string, shared bool) error {
	if m.pins == nil {
		m.pins = make(map[string]map[string]AgentPin)
	}
	if m.pins[agentID] == nil {
		m.pins[agentID] = make(map[string]AgentPin)
	}
	tokenCount := (len(content) + 3) / 4
	m.pins[agentID][source] = AgentPin{
		ID:        int64(len(m.pins[agentID])) + 1,
		AgentID:   agentID,
		PinType:   pinType,
		Source:    source,
		Content:   content,
		TokenCount: tokenCount,
		Shared:    shared,
		LastRead:  time.Now(),
		CreatedAt: time.Now(),
	}
	m.lastAction = "AddPin"
	return m.lastError
}

func (m *mockPinStore) UpdatePinContent(ctx context.Context, agentID, source, content, mtime string) error {
	if m.pins == nil || m.pins[agentID] == nil {
		return nil
	}
	pin := m.pins[agentID][source]
	pin.Content = content
	pin.TokenCount = (len(content) + 3) / 4
	pin.FileMtime = mtime
	pin.LastRead = time.Now()
	m.pins[agentID][source] = pin
	m.lastAction = "UpdatePinContent"
	return m.lastError
}

func (m *mockPinStore) ListPins(ctx context.Context, agentID string) ([]AgentPin, error) {
	if m.pins == nil || m.pins[agentID] == nil {
		return []AgentPin{}, nil
	}
	var pins []AgentPin
	for _, pin := range m.pins[agentID] {
		pins = append(pins, pin)
	}
	m.lastAction = "ListPins"
	return pins, m.lastError
}

func (m *mockPinStore) GetPin(ctx context.Context, agentID, source string) (AgentPin, error) {
	if m.pins == nil || m.pins[agentID] == nil {
		return AgentPin{}, nil
	}
	pin, exists := m.pins[agentID][source]
	if !exists {
		return AgentPin{}, nil
	}
	m.lastAction = "GetPin"
	return pin, m.lastError
}

func (m *mockPinStore) RemovePin(ctx context.Context, agentID, source string) error {
	if m.pins == nil || m.pins[agentID] == nil {
		return nil
	}
	delete(m.pins[agentID], source)
	m.lastAction = "RemovePin"
	return m.lastError
}

func (m *mockPinStore) GetSharedPins(ctx context.Context, targetAgentID string) ([]AgentPin, error) {
	m.lastAction = "GetSharedPins"
	return []AgentPin{}, m.lastError
}

func TestPinManager_AddFilePin(t *testing.T) {
	t.Run("add_file_pin_success", func(t *testing.T) {
		// Create a temporary file
		tmpFile, err := os.CreateTemp("", "test*.txt")
		if err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}
		defer os.Remove(tmpFile.Name())

		// Write test content
		testContent := "test file content"
		if _, err := tmpFile.WriteString(testContent); err != nil {
			t.Fatalf("failed to write to temp file: %v", err)
		}
		tmpFile.Close()

		// Create pin manager and add file pin
		store := &mockPinStore{}
		pm := NewPinManager(store)

		ctx := context.Background()
		err = pm.AddFilePin(ctx, "test-agent", tmpFile.Name(), false)
		if err != nil {
			t.Fatalf("AddFilePin failed: %v", err)
		}

		// Verify pin was stored
		if store.lastAction != "AddPin" {
			t.Errorf("expected AddPin action, got %s", store.lastAction)
		}

		// Verify stored content
		if pin, ok := store.pins["test-agent"][tmpFile.Name()]; !ok {
			t.Fatalf("pin not found in store")
		} else {
			if pin.PinType != "file" {
				t.Errorf("expected pin_type=file, got %s", pin.PinType)
			}
			if pin.Content != testContent {
				t.Errorf("expected content %q, got %q", testContent, pin.Content)
			}
		}
	})

	t.Run("file_not_found", func(t *testing.T) {
		store := &mockPinStore{}
		pm := NewPinManager(store)

		ctx := context.Background()
		err := pm.AddFilePin(ctx, "test-agent", "/nonexistent/file.txt", false)
		if err == nil {
			t.Fatal("expected error for nonexistent file")
		}
		if err.Error() != "file not found: /nonexistent/file.txt" {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("file_too_large", func(t *testing.T) {
		// Create a temporary file with content larger than maxSize
		tmpFile, err := os.CreateTemp("", "large*.txt")
		if err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}
		defer os.Remove(tmpFile.Name())

		// Write content larger than 50KB
		largeContent := make([]byte, 60*1024)
		for i := range largeContent {
			largeContent[i] = 'a'
		}
		if _, err := tmpFile.Write(largeContent); err != nil {
			t.Fatalf("failed to write to temp file: %v", err)
		}
		tmpFile.Close()

		store := &mockPinStore{}
		pm := NewPinManager(store)

		ctx := context.Background()
		err = pm.AddFilePin(ctx, "test-agent", tmpFile.Name(), false)
		if err == nil {
			t.Fatal("expected error for file too large")
		}
		if err.Error() != "file too large: "+tmpFile.Name()+" (61440 bytes, max 51200 bytes)" {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}

func TestPinManager_AddTextPin(t *testing.T) {
	t.Run("add_text_pin_success", func(t *testing.T) {
		store := &mockPinStore{}
		pm := NewPinManager(store)

		ctx := context.Background()
		label := "test-label"
		content := "This is test text content"

		err := pm.AddTextPin(ctx, "test-agent", label, content, false)
		if err != nil {
			t.Fatalf("AddTextPin failed: %v", err)
		}

		if store.lastAction != "AddPin" {
			t.Errorf("expected AddPin action, got %s", store.lastAction)
		}

		// Verify stored
		if pin, ok := store.pins["test-agent"][label]; !ok {
			t.Fatalf("pin not found in store")
		} else {
			if pin.PinType != "text" {
				t.Errorf("expected pin_type=text, got %s", pin.PinType)
			}
			if pin.Content != content {
				t.Errorf("expected content %q, got %q", content, pin.Content)
			}
		}
	})

	t.Run("empty_label_rejected", func(t *testing.T) {
		store := &mockPinStore{}
		pm := NewPinManager(store)

		ctx := context.Background()
		err := pm.AddTextPin(ctx, "test-agent", "", "content", false)
		if err == nil {
			t.Fatal("expected error for empty label")
		}
	})

	t.Run("empty_content_rejected", func(t *testing.T) {
		store := &mockPinStore{}
		pm := NewPinManager(store)

		ctx := context.Background()
		err := pm.AddTextPin(ctx, "test-agent", "label", "", false)
		if err == nil {
			t.Fatal("expected error for empty content")
		}
	})
}

func TestPinManager_FormatPins(t *testing.T) {
	t.Run("format_empty_pins", func(t *testing.T) {
		store := &mockPinStore{}
		pm := NewPinManager(store)

		ctx := context.Background()
		formatted, tokenCount, err := pm.FormatPins(ctx, "test-agent")
		if err != nil {
			t.Fatalf("FormatPins failed: %v", err)
		}
		if formatted != "" {
			t.Errorf("expected empty string for no pins, got %q", formatted)
		}
		if tokenCount != 0 {
			t.Errorf("expected 0 tokens, got %d", tokenCount)
		}
	})

	t.Run("format_single_pin", func(t *testing.T) {
		store := &mockPinStore{}
		pm := NewPinManager(store)

		// Add a pin
		ctx := context.Background()
		label := "test-label"
		content := "test content"
		pm.AddTextPin(ctx, "test-agent", label, content, false)

		// Format pins
		formatted, tokenCount, err := pm.FormatPins(ctx, "test-agent")
		if err != nil {
			t.Fatalf("FormatPins failed: %v", err)
		}

		// Verify formatting
		if !contains(formatted, "<pinned_context>") {
			t.Errorf("formatted text should contain <pinned_context>")
		}
		if !contains(formatted, "</pinned_context>") {
			t.Errorf("formatted text should contain </pinned_context>")
		}
		if !contains(formatted, "--- "+label+" ---") {
			t.Errorf("formatted text should contain pin label")
		}
		if !contains(formatted, content) {
			t.Errorf("formatted text should contain pin content")
		}

		// Token count should be non-zero
		if tokenCount == 0 {
			t.Errorf("expected non-zero token count")
		}
	})

	t.Run("format_multiple_pins", func(t *testing.T) {
		store := &mockPinStore{}
		pm := NewPinManager(store)

		ctx := context.Background()

		// Add multiple pins
		pm.AddTextPin(ctx, "test-agent", "pin1", "content1", false)
		pm.AddTextPin(ctx, "test-agent", "pin2", "content2 longer", false)

		formatted, tokenCount, err := pm.FormatPins(ctx, "test-agent")
		if err != nil {
			t.Fatalf("FormatPins failed: %v", err)
		}

		// Should contain both pins
		if !contains(formatted, "pin1") {
			t.Errorf("formatted text should contain pin1 label")
		}
		if !contains(formatted, "pin2") {
			t.Errorf("formatted text should contain pin2 label")
		}
		if !contains(formatted, "content1") {
			t.Errorf("formatted text should contain content1")
		}
		if !contains(formatted, "content2 longer") {
			t.Errorf("formatted text should contain content2 longer")
		}

		// Token count should be positive
		if tokenCount <= 0 {
			t.Errorf("expected positive token count, got %d", tokenCount)
		}
	})
}

func TestPinManager_RefreshFilePin(t *testing.T) {
	t.Run("refresh_unchanged_file", func(t *testing.T) {
		// Create temp file
		tmpFile, err := os.CreateTemp("", "refresh*.txt")
		if err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}
		defer os.Remove(tmpFile.Name())

		testContent := "original content"
		if _, err := tmpFile.WriteString(testContent); err != nil {
			t.Fatalf("failed to write to temp file: %v", err)
		}
		tmpFile.Close()

		store := &mockPinStore{}
		pm := NewPinManager(store)

		ctx := context.Background()

		// Add file pin
		pm.AddFilePin(ctx, "test-agent", tmpFile.Name(), false)

		// Get the mtime and set it
		info, _ := os.Stat(tmpFile.Name())
		mtime := info.ModTime().Format("2006-01-02 15:04:05")
		pin := store.pins["test-agent"][tmpFile.Name()]
		pin.FileMtime = mtime
		store.pins["test-agent"][tmpFile.Name()] = pin

		// Try to refresh (should return false because file unchanged)
		changed, err := pm.RefreshFilePin(ctx, "test-agent", tmpFile.Name())
		if err != nil {
			t.Fatalf("RefreshFilePin failed: %v", err)
		}
		if changed {
			t.Errorf("expected changed=false for unchanged file")
		}
	})

	t.Run("refresh_changed_file", func(t *testing.T) {
		// Create temp file
		tmpFile, err := os.CreateTemp("", "refresh*.txt")
		if err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}
		defer os.Remove(tmpFile.Name())

		originalContent := "original content"
		if _, err := tmpFile.WriteString(originalContent); err != nil {
			t.Fatalf("failed to write to temp file: %v", err)
		}
		tmpFile.Close()

		store := &mockPinStore{}
		pm := NewPinManager(store)

		ctx := context.Background()

		// Add file pin
		pm.AddFilePin(ctx, "test-agent", tmpFile.Name(), false)

		// Sleep to ensure mtime changes (filesystem time granularity)
		time.Sleep(100 * time.Millisecond)

		// Modify file
		if err := os.WriteFile(tmpFile.Name(), []byte("modified content"), 0600); err != nil {
			t.Fatalf("failed to modify file: %v", err)
		}

		// Sleep to ensure mtime is detected
		time.Sleep(100 * time.Millisecond)

		// Try to refresh (should return true because file changed)
		changed, err := pm.RefreshFilePin(ctx, "test-agent", tmpFile.Name())
		if err != nil {
			t.Fatalf("RefreshFilePin failed: %v", err)
		}
		if !changed {
			t.Errorf("expected changed=true for modified file")
		}

		// Verify content was updated
		pin := store.pins["test-agent"][tmpFile.Name()]
		if pin.Content != "modified content" {
			t.Errorf("expected updated content, got %q", pin.Content)
		}
	})
}

// Helper function
func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) >= len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || stringContains(s, substr)))
}

func stringContains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
