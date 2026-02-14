package engine_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/engine"
	"github.com/basket/go-claw/internal/persistence"
)

func openStoreForEngineTest(t *testing.T) *persistence.Store {
	t.Helper()
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func waitForTaskStatus(t *testing.T, store *persistence.Store, taskID string, want persistence.TaskStatus, timeout time.Duration) *persistence.Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := store.GetTask(context.Background(), taskID)
		if err == nil && task.Status == want {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, _ := store.GetTask(context.Background(), taskID)
	t.Fatalf("timed out waiting for task %s status %s, got %#v", taskID, want, task)
	return nil
}

type countingProcessor struct {
	sleep       time.Duration
	active      atomic.Int32
	maxObserved atomic.Int32
}

func (p *countingProcessor) Process(ctx context.Context, task persistence.Task) (string, error) {
	cur := p.active.Add(1)
	defer p.active.Add(-1)

	for {
		prev := p.maxObserved.Load()
		if cur <= prev || p.maxObserved.CompareAndSwap(prev, cur) {
			break
		}
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(p.sleep):
		return `{"reply":"ok"}`, nil
	}
}

func TestEngine_BoundedConcurrency(t *testing.T) {
	// [SPEC: SPEC-ORCH-POOL-1] [PDR: V-12]
	store := openStoreForEngineTest(t)
	ctx := context.Background()
	sessionID := "fd045cca-4f9f-4d02-b4e7-0524f618ad6a"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	for i := 0; i < 16; i++ {
		if _, err := store.CreateTask(ctx, sessionID, `{"content":"x"}`); err != nil {
			t.Fatalf("create task: %v", err)
		}
	}

	proc := &countingProcessor{sleep: 60 * time.Millisecond}
	eng := engine.New(store, proc, engine.Config{
		WorkerCount:  2,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pending, running, err := store.TaskCounts(context.Background())
		if err != nil {
			t.Fatalf("task counts: %v", err)
		}
		if pending == 0 && running == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if got := proc.maxObserved.Load(); got > 2 {
		t.Fatalf("max concurrent workers exceeded limit: got %d want <= 2", got)
	}
}

type blockingProcessor struct{}

func (blockingProcessor) Process(ctx context.Context, task persistence.Task) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func TestEngine_AbortCancelsRunningTask(t *testing.T) {
	// [SPEC: SPEC-ORCH-TIMEOUT-1, SPEC-ORCH-POOL-1] [PDR: V-17]
	store := openStoreForEngineTest(t)
	ctx := context.Background()
	sessionID := "9198e152-1dfa-40a2-bd1b-8b8ecee60d50"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	taskID, err := store.CreateTask(ctx, sessionID, `{"content":"block"}`)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	eng := engine.New(store, blockingProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  5 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	_ = waitForTaskStatus(t, store, taskID, persistence.TaskStatusRunning, 2*time.Second)

	ok, err := eng.AbortTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("abort task: %v", err)
	}
	if !ok {
		t.Fatalf("expected abort to return true")
	}

	task := waitForTaskStatus(t, store, taskID, persistence.TaskStatusCanceled, 2*time.Second)
	if task.Status != persistence.TaskStatusCanceled {
		t.Fatalf("expected canceled status, got %s", task.Status)
	}
	if task.Error != "aborted" {
		t.Fatalf("expected aborted error marker, got %q", task.Error)
	}
}

func TestEngine_TaskTimeoutEnforced(t *testing.T) {
	// [SPEC: SPEC-ORCH-TIMEOUT-1] [PDR: V-17]
	store := openStoreForEngineTest(t)
	ctx := context.Background()
	sessionID := "847e3f95-aea4-4f1a-a98e-18a7fd04f682"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	taskID, err := store.CreateTask(ctx, sessionID, `{"content":"timeout"}`)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	eng := engine.New(store, blockingProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  120 * time.Millisecond,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	deadline := time.Now().Add(4 * time.Second)
	var task *persistence.Task
	for time.Now().Before(deadline) {
		tk, err := store.GetTask(context.Background(), taskID)
		if err == nil && tk.Attempt >= 1 &&
			(tk.Status == persistence.TaskStatusQueued || tk.Status == persistence.TaskStatusDeadLetter) {
			task = tk
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if task == nil {
		task, _ = store.GetTask(context.Background(), taskID)
		t.Fatalf("timed out waiting for timeout retry/dead-letter, got %#v", task)
	}
	if task.Error == "" {
		t.Fatalf("expected timeout error")
	}
	if task.Attempt < 1 {
		t.Fatalf("expected at least one failed attempt for timeout task")
	}
}

type failingProcessor struct {
	err error
}

func (p failingProcessor) Process(ctx context.Context, task persistence.Task) (string, error) {
	if p.err != nil {
		return "", p.err
	}
	return "", errors.New("processor failure")
}

func waitForTaskStatusAny(t *testing.T, store *persistence.Store, taskID string, wants []persistence.TaskStatus, timeout time.Duration) *persistence.Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := store.GetTask(context.Background(), taskID)
		if err == nil {
			for _, w := range wants {
				if task.Status == w {
					return task
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, _ := store.GetTask(context.Background(), taskID)
	t.Fatalf("timed out waiting for task %s status in %v, got %#v", taskID, wants, task)
	return nil
}

func TestEngine_FailureRetriesThenDeadLetter(t *testing.T) {
	store := openStoreForEngineTest(t)
	ctx := context.Background()
	sessionID := "7ef12fe1-1c6f-4976-ae72-68fd1fc3f7f6"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	taskID, err := store.CreateTask(ctx, sessionID, `{"content":"retry"}`)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	eng := engine.New(store, failingProcessor{err: errors.New("boom")}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  200 * time.Millisecond,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	task := waitForTaskStatusAny(t, store, taskID, []persistence.TaskStatus{persistence.TaskStatusDeadLetter}, 10*time.Second)
	if task.Status != persistence.TaskStatusDeadLetter {
		t.Fatalf("expected DEAD_LETTER, got %s", task.Status)
	}
	if task.Attempt < 3 {
		t.Fatalf("expected at least 3 attempts before dead letter, got %d", task.Attempt)
	}
	if task.LastErrorCode == "" {
		t.Fatalf("expected last_error_code to be set")
	}
}

func TestEngine_BackpressureRejectsWhenQueueFull(t *testing.T) {
	// GC-SPEC-QUE-008: Saturation MUST apply backpressure at intake.
	store := openStoreForEngineTest(t)
	ctx := context.Background()
	sessionID := "fd045cca-4f9f-4d02-b4e7-111111111111"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	eng := engine.New(store, &countingProcessor{sleep: 1 * time.Second}, engine.Config{
		WorkerCount:   2,
		PollInterval:  50 * time.Millisecond,
		TaskTimeout:   10 * time.Second,
		MaxQueueDepth: 3,
	})

	// Fill the queue to the max (don't start the engine so tasks stay QUEUED).
	for i := 0; i < 3; i++ {
		_, err := eng.CreateChatTask(ctx, sessionID, "msg")
		if err != nil {
			t.Fatalf("expected task %d to be created, got: %v", i, err)
		}
	}

	// The 4th task should be rejected.
	_, err := eng.CreateChatTask(ctx, sessionID, "overflow")
	if err == nil {
		t.Fatal("expected backpressure error, got nil")
	}
	if !errors.Is(err, engine.ErrQueueSaturated) {
		t.Fatalf("expected ErrQueueSaturated, got: %v", err)
	}
}
