package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/basket/go-claw/internal/persistence"
)

// PinStore interface for persistence operations related to pins.
type PinStore interface {
	AddPin(ctx context.Context, agentID, pinType, source, content string, shared bool) error
	UpdatePinContent(ctx context.Context, agentID, source, content, mtime string) error
	ListPins(ctx context.Context, agentID string) ([]persistence.AgentPin, error)
	GetPin(ctx context.Context, agentID, source string) (persistence.AgentPin, error)
	RemovePin(ctx context.Context, agentID, source string) error
	GetSharedPins(ctx context.Context, targetAgentID string) ([]persistence.AgentPin, error)
}

// PinManager handles adding, formatting, and live-reloading pinned context.
type PinManager struct {
	store    PinStore
	maxSize  int64 // max file size in bytes (default: 50KB)
	pollSecs int   // file change poll interval (default: 10)
	stop     chan struct{}
}

// NewPinManager creates a new pin manager with default settings.
func NewPinManager(store PinStore) *PinManager {
	return &PinManager{
		store:    store,
		maxSize:  50 * 1024, // 50KB
		pollSecs: 10,
		stop:     make(chan struct{}),
	}
}

// AddFilePin reads a file and stores its content as a pin.
func (pm *PinManager) AddFilePin(ctx context.Context, agentID, filepath string, shared bool) error {
	// Check if file exists and get its metadata
	info, err := os.Stat(filepath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file not found: %s", filepath)
		}
		return fmt.Errorf("cannot access file: %w", err)
	}

	// Check file size
	if info.Size() > pm.maxSize {
		return fmt.Errorf("file too large: %s (%d bytes, max %d bytes)", filepath, info.Size(), pm.maxSize)
	}

	// Read file content
	content, err := os.ReadFile(filepath)
	if err != nil {
		return fmt.Errorf("cannot read file: %w", err)
	}

	// Store as pin (file modification time is tracked by persistence layer)
	return pm.store.AddPin(ctx, agentID, "file", filepath, string(content), shared)
}

// AddTextPin stores arbitrary text as a pin.
func (pm *PinManager) AddTextPin(ctx context.Context, agentID, label, content string, shared bool) error {
	if label == "" {
		return fmt.Errorf("label cannot be empty")
	}
	if content == "" {
		return fmt.Errorf("content cannot be empty")
	}
	return pm.store.AddPin(ctx, agentID, "text", label, content, shared)
}

// StartFileWatcher polls pinned files for changes every N seconds.
// When a file's mtime changes, re-read and update the stored content.
func (pm *PinManager) StartFileWatcher(ctx context.Context, agentID string) {
	go func() {
		ticker := time.NewTicker(time.Duration(pm.pollSecs) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pm.refreshChangedFiles(ctx, agentID)
			case <-pm.stop:
				return
			}
		}
	}()
}

// refreshChangedFiles checks all file-type pins for the given agent and re-reads if mtime changed.
func (pm *PinManager) refreshChangedFiles(ctx context.Context, agentID string) {
	pins, err := pm.store.ListPins(ctx, agentID)
	if err != nil {
		return
	}
	for _, pin := range pins {
		if pin.PinType != "file" {
			continue
		}
		_, _ = pm.RefreshFilePin(ctx, agentID, pin.Source)
	}
}

// Stop stops the file watcher.
func (pm *PinManager) Stop() {
	close(pm.stop)
}

// FormatPins returns all pinned content formatted for the context window.
// Returns: formatted text, total token count, error
func (pm *PinManager) FormatPins(ctx context.Context, agentID string) (string, int, error) {
	pins, err := pm.store.ListPins(ctx, agentID)
	if err != nil {
		return "", 0, fmt.Errorf("failed to list pins: %w", err)
	}

	if len(pins) == 0 {
		return "", 0, nil
	}

	var sb strings.Builder
	totalTokens := 0

	sb.WriteString("<pinned_context>\n")

	for _, pin := range pins {
		// Use filename for file pins, label for text pins
		label := pin.Source
		if pin.PinType == "file" {
			label = filepath.Base(pin.Source)
		}

		sb.WriteString(fmt.Sprintf("--- %s ---\n", label))
		sb.WriteString(pin.Content)
		sb.WriteString("\n")

		totalTokens += pin.TokenCount
	}

	sb.WriteString("</pinned_context>")

	return sb.String(), totalTokens, nil
}

// RefreshFilePin re-reads a specific file pin if it has changed on disk.
// Returns true if the file was updated, false if unchanged.
func (pm *PinManager) RefreshFilePin(ctx context.Context, agentID, filePath string) (bool, error) {
	pin, err := pm.store.GetPin(ctx, agentID, filePath)
	if err != nil {
		return false, err
	}

	if pin.PinType != "file" {
		return false, fmt.Errorf("pin is not a file")
	}

	// Check if file still exists and get current mtime
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, fmt.Errorf("file no longer exists: %s", filePath)
		}
		return false, fmt.Errorf("cannot access file: %w", err)
	}

	currentMtime := info.ModTime().Format("2006-01-02 15:04:05")

	// Check if mtime changed
	if pin.FileMtime == currentMtime {
		return false, nil // File unchanged
	}

	// File changed, re-read content
	content, err := os.ReadFile(filePath)
	if err != nil {
		return false, fmt.Errorf("cannot read file: %w", err)
	}

	// Update in database
	err = pm.store.UpdatePinContent(ctx, agentID, filePath, string(content), currentMtime)
	if err != nil {
		return false, fmt.Errorf("failed to update pin: %w", err)
	}

	return true, nil
}
