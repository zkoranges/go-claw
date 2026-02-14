package config_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/config"
)

func TestWatcher_DetectsSOULFileChange(t *testing.T) {
	// [T-4] Config hot-reload integration test.
	homeDir := t.TempDir()

	// Create initial SOUL.md so the watcher has something to watch.
	soulPath := filepath.Join(homeDir, "SOUL.md")
	if err := os.WriteFile(soulPath, []byte("initial soul"), 0o644); err != nil {
		t.Fatalf("write initial soul: %v", err)
	}

	w := config.NewWatcher(homeDir, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := w.Start(ctx); err != nil {
		t.Fatalf("start watcher: %v", err)
	}

	// Instead of a fixed sleep, retry the write at short intervals until the
	// watcher produces an event. This handles any platform-specific delay in
	// filesystem notification readiness.
	deadline := time.After(3 * time.Second)
	writeTick := time.NewTicker(50 * time.Millisecond)
	defer writeTick.Stop()

	// Perform the first write immediately.
	if err := os.WriteFile(soulPath, []byte("updated soul"), 0o644); err != nil {
		t.Fatalf("write updated soul: %v", err)
	}

	for {
		select {
		case ev := <-w.Events():
			if filepath.Base(ev.Path) != "SOUL.md" {
				t.Fatalf("expected SOUL.md event, got %s", ev.Path)
			}
			return
		case <-writeTick.C:
			// Re-write the file in case the watcher was not yet ready.
			_ = os.WriteFile(soulPath, []byte("updated soul"), 0o644)
		case <-deadline:
			t.Fatalf("timed out waiting for SOUL.md change event")
		}
	}
}
