package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/basket/go-claw/internal/persistence"
)

const HeartbeatSessionID = "00000000-0000-0000-0000-000000000000" // Reserved session ID for system tasks

// HeartbeatManager manages periodic system checks.
type HeartbeatManager struct {
	router   ChatTaskRouter
	store    *persistence.Store
	homeDir  string
	interval time.Duration
	logger   *slog.Logger
}

// NewHeartbeatManager creates a new HeartbeatManager.
func NewHeartbeatManager(router ChatTaskRouter, store *persistence.Store, homeDir string, intervalMinutes int, logger *slog.Logger) *HeartbeatManager {
	if intervalMinutes <= 0 {
		intervalMinutes = 30
	}
	return &HeartbeatManager{
		router:   router,
		store:    store,
		homeDir:  homeDir,
		interval: time.Duration(intervalMinutes) * time.Minute,
		logger:   logger,
	}
}

// Start begins the heartbeat loop in a background goroutine.
func (h *HeartbeatManager) Start(ctx context.Context) {
	h.logger.Info("starting heartbeat manager", "interval", h.interval)

	// Ensure session exists
	if err := h.store.EnsureSession(ctx, HeartbeatSessionID); err != nil {
		h.logger.Error("failed to ensure heartbeat session", "error", err)
		return
	}

	go func() {
		ticker := time.NewTicker(h.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := h.runOnce(ctx); err != nil {
					h.logger.Error("heartbeat failed", "error", err)
				}
			}
		}
	}()
}

func (h *HeartbeatManager) runOnce(ctx context.Context) error {
	path := filepath.Join(h.homeDir, "workspace", "HEARTBEAT.md")

	// If file doesn't exist, we skip.
	// Users must create it to enable the check logic.
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read HEARTBEAT.md: %w", err)
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil
	}

	h.logger.Info("running heartbeat task")

	prompt := fmt.Sprintf("Periodic System Review.\n\nPlease review the current system status against the following heartbeat checklist:\n\n%s\n\nIf you find any issues, report them. If everything is normal, confirm the system status is healthy.", content)

	taskID, err := h.router.CreateChatTask(ctx, "default", HeartbeatSessionID, prompt)
	if err != nil {
		return fmt.Errorf("create heartbeat task: %w", err)
	}

	h.logger.Info("heartbeat task scheduled", "task_id", taskID)

	// Poll for task completion to capture results.
	if h.store != nil {
		go h.awaitResult(ctx, taskID)
	}
	return nil
}

func (h *HeartbeatManager) awaitResult(ctx context.Context, taskID string) {
	timeout := 5 * time.Minute
	pollInterval := 5 * time.Second
	deadline := time.After(timeout)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			h.logger.Warn("heartbeat task timed out waiting for result", "task_id", taskID)
			h.writeResult(taskID, "TIMEOUT: task did not complete within 5 minutes")
			return
		case <-ticker.C:
			task, err := h.store.GetTask(ctx, taskID)
			if err != nil {
				continue
			}
			switch task.Status {
			case persistence.TaskStatusSucceeded:
				h.writeResult(taskID, task.Result)
				return
			case persistence.TaskStatusFailed, persistence.TaskStatusDeadLetter:
				h.logger.Warn("heartbeat task failed", "task_id", taskID, "error", task.Error)
				h.writeResult(taskID, fmt.Sprintf("FAILED: %s", task.Error))
				return
			}
		}
	}
}

func (h *HeartbeatManager) writeResult(taskID, result string) {
	path := filepath.Join(h.homeDir, "workspace", "HEARTBEAT_RESULTS.md")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		h.logger.Error("failed to open heartbeat results file", "error", err)
		return
	}
	defer f.Close()

	entry := fmt.Sprintf("\n## %s - Task %s\n\n%s\n", time.Now().UTC().Format(time.RFC3339), taskID, result)
	if _, err := f.WriteString(entry); err != nil {
		h.logger.Error("failed to write heartbeat result", "error", err)
	}
}
