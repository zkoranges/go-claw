package config

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

type ReloadEvent struct {
	Path string
	Op   fsnotify.Op
}

type Watcher struct {
	homeDir string
	logger  *slog.Logger
	events  chan ReloadEvent
}

func NewWatcher(homeDir string, logger *slog.Logger) *Watcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Watcher{
		homeDir: homeDir,
		logger:  logger,
		events:  make(chan ReloadEvent, 16),
	}
}

func (w *Watcher) Events() <-chan ReloadEvent {
	return w.events
}

func (w *Watcher) Start(ctx context.Context) error {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	files := []string{
		filepath.Join(w.homeDir, "config.yaml"),
		filepath.Join(w.homeDir, "SOUL.md"),
		filepath.Join(w.homeDir, "AGENTS.md"),
		filepath.Join(w.homeDir, "policy.yaml"),
	}
	for _, file := range files {
		_ = fsw.Add(file)
	}

	go func() {
		defer fsw.Close()
		defer close(w.events)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-fsw.Events:
				if !ok {
					return
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
					continue
				}
				select {
				case w.events <- ReloadEvent{Path: ev.Name, Op: ev.Op}:
				default:
				}
				w.logger.Info("config file changed", "path", ev.Name, "op", ev.Op.String())
			case err, ok := <-fsw.Errors:
				if !ok {
					return
				}
				w.logger.Error("config watcher error", "error", err)
			}
		}
	}()
	return nil
}
