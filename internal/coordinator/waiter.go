package coordinator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/persistence"
)

// TaskResult holds the outcome of a completed task.
// GC-SPEC-PDR-v4-Phase-2: Event-driven task completion tracking.
type TaskResult struct {
	TaskID           string
	Status           string
	Output           string
	PromptTokens     int
	CompletionTokens int
	CostUSD          float64
	DurationMs       int64
	Error            string
}

// Waiter tracks task completion via bus events with polling fallback.
// GC-SPEC-PDR-v4-Phase-2: Event-driven task completion tracking.
// Note: Currently uses polling as fallback. Full event integration in Phase 3+.
type Waiter struct {
	eventBus *bus.Bus // Optional: can be nil for polling-only mode
	store    *persistence.Store
}

// NewWaiter creates a task completion waiter.
// eventBus can be nil to operate in polling-only mode.
func NewWaiter(eventBus *bus.Bus, store *persistence.Store) *Waiter {
	return &Waiter{eventBus: eventBus, store: store}
}

// WaitForTask blocks until the given task reaches a terminal state or the context expires.
// Uses event subscription with fallback to polling for robustness.
func (w *Waiter) WaitForTask(ctx context.Context, taskID string, timeout time.Duration) (*TaskResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 1. Subscribe FIRST to avoid missing events between the DB check and the wait loop.
	var sub *bus.Subscription
	if w.eventBus != nil {
		sub = w.eventBus.Subscribe("task.")
		defer w.eventBus.Unsubscribe(sub)
	}

	// 2. Check if already terminal (handles cases where task finished before we subscribed).
	result, err := w.checkTerminal(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if result != nil {
		return result, nil
	}

	// 3. Wait for events or poll (fallback).
	// We use a slower ticker (1s) to reduce DB load, relying on events for low latency.
	tickerInterval := 1 * time.Second
	if w.eventBus == nil {
		tickerInterval = 100 * time.Millisecond // fast polling if no bus
	}
	ticker := time.NewTicker(tickerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout waiting for task %s: %w", taskID, ctx.Err())

		case <-ticker.C:
			// Poll for task completion (fallback)
			result, err := w.checkTerminal(ctx, taskID)
			if err != nil {
				return nil, err
			}
			if result != nil {
				return result, nil
			}

		case event, ok := <-func() <-chan bus.Event {
			if sub == nil {
				return nil
			}
			return sub.Ch()
		}():
			if !ok {
				// Subscription closed unexpectedly, fall back to polling loop
				sub = nil
				continue
			}
			// Check if event is relevant to our task
			if isEventForTask(event, taskID) {
				// We got a relevant event, check DB to be sure and get full result
				result, err := w.checkTerminal(ctx, taskID)
				if err != nil {
					return nil, err
				}
				if result != nil {
					return result, nil
				}
			}
		}
	}
}

func isEventForTask(event bus.Event, taskID string) bool {
	// Check struct-based events
	switch e := event.Payload.(type) {
	case bus.TaskStateChangedEvent:
		return e.TaskID == taskID
	case bus.TaskMetricsEvent:
		return e.TaskID == taskID
	case bus.TaskTokensEvent:
		return e.TaskID == taskID
	}

	// Check legacy/map-based events
	if eventMap, ok := event.Payload.(map[string]interface{}); ok {
		if id, ok := eventMap["task_id"].(string); ok {
			return id == taskID
		}
	}
	return false
}

// WaitForAll waits for multiple tasks to complete. Returns results for all tasks.
// If any task fails, the others still complete (no early abort).
func (w *Waiter) WaitForAll(ctx context.Context, taskIDs []string, timeout time.Duration) (map[string]*TaskResult, error) {
	results := make(map[string]*TaskResult)
	var mu sync.Mutex
	var wg sync.WaitGroup
	errCh := make(chan error, len(taskIDs))

	for _, id := range taskIDs {
		wg.Add(1)
		go func(taskID string) {
			defer wg.Done()
			result, err := w.WaitForTask(ctx, taskID, timeout)
			if err != nil {
				errCh <- fmt.Errorf("task %s: %w", taskID, err)
				return
			}
			mu.Lock()
			results[taskID] = result
			mu.Unlock()
		}(id)
	}

	wg.Wait()
	close(errCh)

	// Collect errors
	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return results, fmt.Errorf("%d tasks failed: %v", len(errs), errs[0])
	}
	return results, nil
}

// checkTerminal checks if a task is in a terminal state and returns its result.
// Returns (nil, nil) if the task is still in progress.
func (w *Waiter) checkTerminal(ctx context.Context, taskID string) (*TaskResult, error) {
	task, err := w.store.GetTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("get task %s: %w", taskID, err)
	}
	if task == nil {
		return nil, fmt.Errorf("task %s not found", taskID)
	}

	if !isTerminalStatus(task.Status) {
		return nil, nil
	}

	return &TaskResult{
		TaskID:           task.ID,
		Status:           string(task.Status),
		Output:           task.Result,
		PromptTokens:     0, // TODO: populate from task metrics
		CompletionTokens: 0, // TODO: populate from task metrics
		CostUSD:          0, // TODO: populate from task metrics
		DurationMs:       0, // TODO: calculate from timestamps
		Error:            task.Error,
	}, nil
}

func isTerminalStatus(status persistence.TaskStatus) bool {
	switch status {
	case persistence.TaskStatusSucceeded,
		persistence.TaskStatusFailed,
		persistence.TaskStatusCanceled,
		persistence.TaskStatusDeadLetter:
		return true
	}
	return false
}

// extractTaskIDFromEvent extracts the task ID from a bus event.
// Events are published with map[string]interface{} payloads containing "task_id".
func extractTaskIDFromEvent(event bus.Event) string {
	if eventMap, ok := event.Payload.(map[string]interface{}); ok {
		if taskID, ok := eventMap["task_id"].(string); ok {
			return taskID
		}
	}
	return ""
}
