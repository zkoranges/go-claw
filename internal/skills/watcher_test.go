package skills

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// AUD-022: Tests for the skills watcher.

// TestWatcher_DebounceCoalescing verifies that multiple rapid SKILL.md writes
// produce a single coalesced event rather than one per write.
func TestWatcher_DebounceCoalescing(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "myskill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write initial SKILL.md so the watcher has something to watch.
	skillMD := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillMD, []byte("---\nname: myskill\n---\nv1\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWatcher([]string{dir}, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Perform multiple rapid writes to trigger debounce coalescing.
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(skillMD, []byte("---\nname: myskill\n---\nupdated\n"), 0o644); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for the debounce timer to fire (150ms debounce + margin).
	eventCount := 0
	timeout := time.After(1 * time.Second)
	drain := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case _, ok := <-w.Events():
			if !ok {
				break loop
			}
			eventCount++
		case <-drain:
			break loop
		case <-timeout:
			break loop
		}
	}

	if eventCount == 0 {
		t.Fatal("expected at least 1 debounced event, got 0")
	}
	if eventCount > 2 {
		t.Fatalf("expected debounce coalescing (1-2 events), got %d", eventCount)
	}
}

// TestWatcher_NonSkillFilesFiltered verifies that writing a .txt file in a
// watched directory does NOT produce an event.
func TestWatcher_NonSkillFilesFiltered(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "someskill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWatcher([]string{dir}, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait a bit for the watcher to settle.
	time.Sleep(50 * time.Millisecond)

	// Write a non-skill file.
	txtFile := filepath.Join(skillDir, "notes.txt")
	if err := os.WriteFile(txtFile, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write txt: %v", err)
	}

	// Wait long enough for a debounced event to appear if one was triggered.
	select {
	case ev := <-w.Events():
		t.Fatalf("expected no event for .txt file, got %q", ev)
	case <-time.After(400 * time.Millisecond):
		// Good: no event fired.
	}
}

// TestWatcher_ContextCancellation verifies that the watcher shuts down cleanly
// when the context is canceled, closing its events channel.
func TestWatcher_ContextCancellation(t *testing.T) {
	dir := t.TempDir()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWatcher([]string{dir}, logger)

	ctx, cancel := context.WithCancel(context.Background())

	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Cancel the context.
	cancel()

	// The events channel should close within a short period.
	select {
	case _, ok := <-w.Events():
		if ok {
			// Received an event before close; keep draining.
			for range w.Events() {
			}
		}
		// Channel closed - success.
	case <-time.After(2 * time.Second):
		t.Fatal("events channel not closed after context cancellation")
	}
}

// TestWatcher_NewSkillDirectory verifies that creating a new directory with a
// SKILL.md file triggers an event.
func TestWatcher_NewSkillDirectory(t *testing.T) {
	dir := t.TempDir()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWatcher([]string{dir}, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the watcher to stabilize.
	time.Sleep(100 * time.Millisecond)

	// Create a new skill directory with a SKILL.md.
	newSkill := filepath.Join(dir, "brand-new-skill")
	if err := os.MkdirAll(newSkill, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	skillMD := filepath.Join(newSkill, "SKILL.md")
	if err := os.WriteFile(skillMD, []byte("---\nname: brand-new-skill\n---\nInstructions.\n"), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	// Expect an event within a reasonable timeout.
	select {
	case ev := <-w.Events():
		if ev == "" {
			t.Fatal("received empty event")
		}
		// Success.
	case <-time.After(2 * time.Second):
		t.Fatal("expected event for new skill directory, got none within timeout")
	}
}
