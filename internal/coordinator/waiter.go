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

// Waiter tracks task completion via bus events instead of polling.
// GC-SPEC-PDR-v4-Phase-2: Event-driven task completion tracking.
type Waiter struct {
	eventBus *bus.Bus
	store    *persistence.Store
}

// NewWaiter creates a task completion waiter.
func NewWaiter(eventBus *bus.Bus, store *persistence.Store) *Waiter {
	return &Waiter{eventBus: eventBus, store: store}
}

// WaitForTask blocks until the given task reaches a terminal state or the context expires.
// Uses bus event subscription — does not poll.
func (w *Waiter) WaitForTask(ctx context.Context, taskID string, timeout time.Duration) (*TaskResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Subscribe to task completion events
	sub := w.eventBus.Subscribe("task.")
	defer w.eventBus.Unsubscribe(sub)

	// Check if already terminal before waiting (race condition guard)
	result, err := w.checkTerminal(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if result != nil {
		return result, nil
	}

	// Wait for bus event or timeout
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout waiting for task %s: %w", taskID, ctx.Err())

		case event := <-sub.Ch():
			// Check if this event is for our task
			// Event payload should have task_id field if it matches our task
			if extractTaskIDFromEvent(event) != taskID {
				continue
			}

			// Check if task is now terminal
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
