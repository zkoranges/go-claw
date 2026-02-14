package skills

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher emits an update event when any SKILL.md-backed skill source changes.
// It is intentionally simple: it watches root dirs and their immediate child dirs.
type Watcher struct {
	dirs   []string
	logger *slog.Logger
	events chan string
}

func NewWatcher(dirs []string, logger *slog.Logger) *Watcher {
	if logger == nil {
		logger = slog.Default()
	}
	cp := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if strings.TrimSpace(d) == "" {
			continue
		}
		cp = append(cp, d)
	}
	return &Watcher{
		dirs:   cp,
		logger: logger,
		events: make(chan string, 16),
	}
}

func (w *Watcher) Events() <-chan string {
	return w.events
}

func (w *Watcher) Start(ctx context.Context) error {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("new watcher: %w", err)
	}

	addDir := func(dir string) {
		if strings.TrimSpace(dir) == "" {
			return
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			w.logger.Warn("skills watcher: abs failed", "dir", dir, "error", err)
			return
		}
		if err := fsw.Add(abs); err != nil {
			if os.IsNotExist(err) {
				return
			}
			w.logger.Warn("skills watcher: add failed", "dir", abs, "error", err)
			return
		}

		entries, err := os.ReadDir(abs)
		if err != nil {
			return
		}
		for _, ent := range entries {
			if !ent.IsDir() {
				continue
			}
			child := filepath.Join(abs, ent.Name())
			_ = fsw.Add(child)
			for _, sub := range []string{"scripts", "references", "assets"} {
				subDir := filepath.Join(child, sub)
				if fi, err := os.Stat(subDir); err == nil && fi.IsDir() {
					_ = fsw.Add(subDir)
				}
			}
		}
	}

	for _, dir := range w.dirs {
		addDir(dir)
	}

	go func() {
		defer func() {
			_ = fsw.Close()
			close(w.events)
		}()

		// Debounce bursts of events.
		var pending bool
		var timer *time.Timer
		var timerC <-chan time.Time
		flush := func() {
			if !pending {
				return
			}
			pending = false
			select {
			case w.events <- "skills":
			default:
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-fsw.Events:
				if !ok {
					return
				}
				if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
					continue
				}

				// Watch new skill directories as they appear under watched roots.
				createdDir := false
				if ev.Op&fsnotify.Create != 0 {
					if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
						createdDir = true
						_ = fsw.Add(ev.Name)
					}
				}

				// Only fire updates for skill-relevant files.
				base := filepath.Base(ev.Name)
				sep := string(filepath.Separator)
				isRelevant := base == "SKILL.md" ||
					filepath.Ext(base) == ".wasm" ||
					strings.Contains(ev.Name, sep+"scripts"+sep) ||
					strings.Contains(ev.Name, sep+"references"+sep) ||
					strings.Contains(ev.Name, sep+"assets"+sep)
				// Creating a new skill directory should trigger a reload even if we miss the
				// initial SKILL.md creation due to watcher registration races.
				if !isRelevant && createdDir {
					isRelevant = true
				}
				if !isRelevant {
					continue
				}

				pending = true
				if timer == nil {
					timer = time.NewTimer(150 * time.Millisecond)
					timerC = timer.C
				} else {
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(150 * time.Millisecond)
					timerC = timer.C
				}

			case err, ok := <-fsw.Errors:
				if !ok {
					return
				}
				w.logger.Warn("skills watcher error", "error", err)
			case <-timerC:
				flush()
				timerC = nil
			}
		}
	}()

	return nil
}
