package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
	"github.com/basket/go-claw/internal/shared"
	"github.com/basket/go-claw/internal/tokenutil"
)

type Config struct {
	WorkerCount   int
	PollInterval  time.Duration
	TaskTimeout   time.Duration
	MaxQueueDepth int // GC-SPEC-QUE-008: 0 = unlimited
	Bus           *bus.Bus
	AgentID       string // if set, workers only claim tasks for this agent
}

type Processor interface {
	Process(ctx context.Context, task persistence.Task) (string, error)
}

type EchoProcessor struct {
	Brain Brain
}

type chatTaskPayload struct {
	Content string `json:"content"`
}

type chatResultPayload struct {
	Reply string `json:"reply"`
}

func (p EchoProcessor) Process(ctx context.Context, task persistence.Task) (string, error) {
	var payload chatTaskPayload
	if err := json.Unmarshal([]byte(task.Payload), &payload); err != nil {
		return "", fmt.Errorf("decode payload: %w", err)
	}
	if p.Brain == nil {
		return "", fmt.Errorf("brain not initialized")
	}
	reply, err := p.Brain.Respond(ctx, task.SessionID, payload.Content)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(chatResultPayload{Reply: reply})
	if err != nil {
		return "", fmt.Errorf("encode result: %w", err)
	}
	return string(out), nil
}

type Status struct {
	AgentID     string `json:"agent_id,omitempty"`
	WorkerCount int    `json:"worker_count"`
	ActiveTasks int32  `json:"active_tasks"`
	LastError   string `json:"last_error,omitempty"`
}

type Engine struct {
	store   *persistence.Store
	proc    Processor
	policy  policy.Checker // GC-SPEC-SEC-003: policy version pinning
	config  Config
	bus     *bus.Bus
	agentID string

	once sync.Once
	wg   sync.WaitGroup

	cancelMu sync.RWMutex
	cancels  map[string]context.CancelFunc

	activeTasks atomic.Int32
	lastError   atomic.Pointer[string]
}

func New(store *persistence.Store, proc Processor, cfg Config, pol ...policy.Checker) *Engine {
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 4
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 100 * time.Millisecond
	}
	if cfg.TaskTimeout <= 0 {
		cfg.TaskTimeout = 10 * time.Minute
	}
	if proc == nil {
		proc = EchoProcessor{}
	}
	var checker policy.Checker
	if len(pol) > 0 && pol[0] != nil {
		checker = pol[0]
	}
	return &Engine{
		store:   store,
		proc:    proc,
		policy:  checker,
		config:  cfg,
		bus:     cfg.Bus,
		agentID: cfg.AgentID,
		cancels: map[string]context.CancelFunc{},
	}
}

func (e *Engine) Start(ctx context.Context) {
	e.once.Do(func() {
		n, recErr := e.store.RecoverRunningTasks(ctx)
		if recErr != nil {
			slog.Error("task recovery failed", "error", recErr)
		} else if n > 0 {
			slog.Info("recovered stale tasks on startup", "count", n)
		}
		for i := 0; i < e.config.WorkerCount; i++ {
			e.wg.Add(1)
			go func() {
				defer e.wg.Done()
				e.worker(ctx)
			}()
		}
	})
}

func (e *Engine) Wait() {
	e.wg.Wait()
}

// Drain gracefully stops the engine: waits for active tasks to finish within
// the given timeout, then marks any remaining in-flight tasks for retry on
// next startup (GC-SPEC-RUN-003).
func (e *Engine) Drain(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		slog.Info("engine drained cleanly")
	case <-time.After(timeout):
		slog.Warn("engine drain timeout; marking in-flight tasks for recovery", "timeout", timeout)
		// In-flight tasks that have active leases will be recovered on next startup
		// via RecoverRunningTasks, so no explicit action needed here.
	}
}

func (e *Engine) worker(ctx context.Context) {
	ticker := time.NewTicker(e.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if _, err := e.store.RequeueExpiredLeases(ctx); err != nil {
			e.setLastError(fmt.Errorf("requeue expired leases: %w", err))
		}
		// GC-SPEC-QUE-007: Age queued tasks to prevent session starvation.
		if _, err := e.store.AgeQueuedPriorities(ctx, 30*time.Second, 10); err != nil {
			e.setLastError(fmt.Errorf("age queued priorities: %w", err))
		}

		var task *persistence.Task
		var err error
		if e.agentID != "" {
			task, err = e.store.ClaimNextPendingTaskForAgent(ctx, e.agentID)
		} else {
			task, err = e.store.ClaimNextPendingTask(ctx)
		}
		if err != nil {
			e.setLastError(err)
		}
		if err != nil || task == nil {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				continue
			}
		}
		// GC-SPEC-SEC-003: Pin policy version at task attempt start.
		policyVer := ""
		if e.policy != nil {
			policyVer = e.policy.PolicyVersion()
		}
		if err := e.store.StartTaskRun(ctx, task.ID, task.LeaseOwner, policyVer); err != nil {
			e.setLastError(fmt.Errorf("start task run: %w", err))
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				continue
			}
		}
		task.Status = persistence.TaskStatusRunning
		e.handleTask(ctx, *task)
	}
}

func (e *Engine) handleTask(ctx context.Context, task persistence.Task) {
	// GC-SPEC-RUN-004: Propagate trace_id for this task's execution scope.
	traceID := shared.NewTraceID()
	ctx = shared.WithTraceID(ctx, traceID)
	slog.Info("task processing", "task_id", task.ID, "session_id", task.SessionID, "trace_id", traceID)

	taskCtx, cancel := context.WithTimeout(ctx, e.config.TaskTimeout)
	e.activeTasks.Add(1)
	defer e.activeTasks.Add(-1)

	e.cancelMu.Lock()
	e.cancels[task.ID] = cancel
	e.cancelMu.Unlock()

	defer func() {
		cancel()
		e.cancelMu.Lock()
		delete(e.cancels, task.ID)
		e.cancelMu.Unlock()
	}()

	// Observe cancellation before processing boundary (GC-SPEC-STM-005).
	if taskCtx.Err() != nil {
		_, _ = e.store.AbortTask(context.Background(), task.ID)
		e.publishEvent("task.canceled", map[string]string{"task_id": task.ID, "session_id": task.SessionID})
		return
	}
	if cancelled, _ := e.store.IsCancelRequested(context.Background(), task.ID); cancelled {
		_, _ = e.store.AbortTask(context.Background(), task.ID)
		e.publishEvent("task.canceled", map[string]string{"task_id": task.ID, "session_id": task.SessionID})
		return
	}

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-taskCtx.Done():
				return
			case <-ticker.C:
				// Check cooperative cancel flag during heartbeat (GC-SPEC-STM-005).
				if cancelled, _ := e.store.IsCancelRequested(context.Background(), task.ID); cancelled {
					cancel() // Triggers context cancellation for the processor.
					return
				}
				ok, err := e.store.HeartbeatLease(context.Background(), task.ID, task.LeaseOwner)
				if err != nil {
					e.setLastError(fmt.Errorf("lease heartbeat: %w", err))
					continue
				}
				if !ok {
					e.setLastError(fmt.Errorf("lease heartbeat rejected for task %s", task.ID))
				}
			}
		}
	}()

	result, err := e.proc.Process(taskCtx, task)
	if err != nil {
		if errors.Is(taskCtx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("task timeout exceeded: %w", taskCtx.Err())
		} else if errors.Is(taskCtx.Err(), context.Canceled) {
			_, _ = e.store.AbortTask(context.Background(), task.ID)
			return
		}
		e.setLastError(err)
		_, _ = e.store.HandleTaskFailure(context.Background(), task.ID, err.Error())
		e.publishEvent("task.failed", map[string]string{"task_id": task.ID, "session_id": task.SessionID})
		return
	}

	// Invariant: never write success result once context is canceled.
	if taskCtx.Err() != nil {
		if errors.Is(taskCtx.Err(), context.Canceled) {
			_, _ = e.store.AbortTask(context.Background(), task.ID)
			e.publishEvent("task.canceled", map[string]string{"task_id": task.ID, "session_id": task.SessionID})
			return
		}
		err = fmt.Errorf("skip complete after context end: %w", taskCtx.Err())
		e.setLastError(err)
		_, _ = e.store.HandleTaskFailure(context.Background(), task.ID, err.Error())
		e.publishEvent("task.failed", map[string]string{"task_id": task.ID, "session_id": task.SessionID})
		return
	}

	if err := e.store.CompleteTask(context.Background(), task.ID, result); err != nil {
		e.setLastError(err)
		return
	}
	e.publishEvent("task.succeeded", map[string]string{"task_id": task.ID, "session_id": task.SessionID})

	var payload chatResultPayload
	if json.Unmarshal([]byte(result), &payload) == nil && payload.Reply != "" {
		_ = e.store.AddHistory(context.Background(), task.SessionID, "assistant", payload.Reply, tokenutil.EstimateTokens(payload.Reply))
	}
}

// publishEvent publishes a task lifecycle event on the bus if configured.
func (e *Engine) publishEvent(topic string, payload map[string]string) {
	if e.bus != nil {
		if e.agentID != "" {
			payload["agent_id"] = e.agentID
		}
		e.bus.Publish(topic, payload)
	}
}

func (e *Engine) setLastError(err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	e.lastError.Store(&msg)
}

// ErrQueueSaturated is returned when the queue exceeds MaxQueueDepth (GC-SPEC-QUE-008).
var ErrQueueSaturated = fmt.Errorf("queue saturated: backpressure applied")

func (e *Engine) CreateChatTask(ctx context.Context, sessionID, content string) (string, error) {
	// When engine is agent-scoped, delegate to agent-scoped method for correct
	// queue depth checks and task agent_id assignment.
	if e.agentID != "" {
		return e.CreateChatTaskForAgent(ctx, e.agentID, sessionID, content)
	}
	// GC-SPEC-QUE-008: Apply backpressure at intake when queue is saturated.
	if e.config.MaxQueueDepth > 0 {
		depth, err := e.store.QueueDepth(ctx)
		if err != nil {
			return "", fmt.Errorf("check queue depth: %w", err)
		}
		if depth >= e.config.MaxQueueDepth {
			slog.Warn("queue backpressure applied", "depth", depth, "max", e.config.MaxQueueDepth)
			return "", ErrQueueSaturated
		}
	}
	if err := e.store.EnsureSession(ctx, sessionID); err != nil {
		return "", err
	}
	if err := e.store.AddHistory(ctx, sessionID, "user", content, tokenutil.EstimateTokens(content)); err != nil {
		return "", err
	}
	payload, err := json.Marshal(chatTaskPayload{Content: content})
	if err != nil {
		return "", fmt.Errorf("encode task payload: %w", err)
	}
	// GC-SPEC-RUN-004: Pass trace_id from context through to task creation.
	taskID, err := e.store.CreateTask(ctx, sessionID, string(payload))
	if err != nil {
		return "", err
	}
	return taskID, nil
}

// CreateChatTaskForAgent creates a chat task scoped to a specific agent.
func (e *Engine) CreateChatTaskForAgent(ctx context.Context, agentID, sessionID, content string) (string, error) {
	// GC-SPEC-QUE-008: Apply backpressure at intake when queue is saturated.
	if e.config.MaxQueueDepth > 0 {
		depth, err := e.store.QueueDepthForAgent(ctx, agentID)
		if err != nil {
			return "", fmt.Errorf("check queue depth: %w", err)
		}
		if depth >= e.config.MaxQueueDepth {
			slog.Warn("queue backpressure applied", "depth", depth, "max", e.config.MaxQueueDepth, "agent_id", agentID)
			return "", ErrQueueSaturated
		}
	}
	if err := e.store.EnsureSession(ctx, sessionID); err != nil {
		return "", err
	}
	if err := e.store.AddHistory(ctx, sessionID, "user", content, tokenutil.EstimateTokens(content)); err != nil {
		return "", err
	}
	payload, err := json.Marshal(chatTaskPayload{Content: content})
	if err != nil {
		return "", fmt.Errorf("encode task payload: %w", err)
	}
	taskID, err := e.store.CreateTaskForAgent(ctx, agentID, sessionID, string(payload))
	if err != nil {
		return "", err
	}
	return taskID, nil
}

// StreamChatTask handles streaming chat directly without going through the task queue.
// It returns a task ID and an error channel for streaming results.
func (e *Engine) StreamChatTask(ctx context.Context, sessionID, content string, onChunk func(content string) error) (string, error) {
	// When engine is agent-scoped, delegate to agent-scoped method for correct
	// queue depth checks and task agent_id assignment.
	if e.agentID != "" {
		return e.StreamChatTaskForAgent(ctx, e.agentID, sessionID, content, onChunk)
	}
	if e.config.MaxQueueDepth > 0 {
		depth, err := e.store.QueueDepth(ctx)
		if err != nil {
			return "", fmt.Errorf("check queue depth: %w", err)
		}
		if depth >= e.config.MaxQueueDepth {
			slog.Warn("queue backpressure applied", "depth", depth, "max", e.config.MaxQueueDepth)
			return "", ErrQueueSaturated
		}
	}
	if err := e.store.EnsureSession(ctx, sessionID); err != nil {
		return "", err
	}
	if err := e.store.AddHistory(ctx, sessionID, "user", content, tokenutil.EstimateTokens(content)); err != nil {
		return "", err
	}

	payload, err := json.Marshal(chatTaskPayload{Content: content})
	if err != nil {
		return "", fmt.Errorf("encode task payload: %w", err)
	}
	taskID, err := e.store.CreateTask(ctx, sessionID, string(payload))
	if err != nil {
		return "", err
	}

	// Run streaming in background and return immediately with task ID
	go func() {
		taskCtx, cancel := context.WithTimeout(ctx, e.config.TaskTimeout)
		defer cancel()

		// Use EchoProcessor's Brain for streaming
		if e.proc == nil {
			slog.Error("processor not initialized for streaming")
			return
		}

		// Type assert to get the Brain
		var brain Brain
		switch p := e.proc.(type) {
		case EchoProcessor:
			brain = p.Brain
		}

		if brain == nil {
			slog.Error("brain not available for streaming")
			return
		}

		// Stream the response - history is saved inside Stream()
		if err := brain.Stream(taskCtx, sessionID, content, onChunk); err != nil {
			slog.Error("streaming failed", "error", err)
			_, _ = e.store.HandleTaskFailure(taskCtx, taskID, err.Error())
			return
		}

		// Mark task as completed
		_ = e.store.CompleteTask(taskCtx, taskID, `{"reply": "streamed"}`)
	}()

	return taskID, nil
}

// StreamChatTaskForAgent handles streaming chat scoped to a specific agent.
func (e *Engine) StreamChatTaskForAgent(ctx context.Context, agentID, sessionID, content string, onChunk func(content string) error) (string, error) {
	if e.config.MaxQueueDepth > 0 {
		depth, err := e.store.QueueDepthForAgent(ctx, agentID)
		if err != nil {
			return "", fmt.Errorf("check queue depth: %w", err)
		}
		if depth >= e.config.MaxQueueDepth {
			slog.Warn("queue backpressure applied", "depth", depth, "max", e.config.MaxQueueDepth, "agent_id", agentID)
			return "", ErrQueueSaturated
		}
	}
	if err := e.store.EnsureSession(ctx, sessionID); err != nil {
		return "", err
	}
	if err := e.store.AddHistory(ctx, sessionID, "user", content, tokenutil.EstimateTokens(content)); err != nil {
		return "", err
	}

	payload, err := json.Marshal(chatTaskPayload{Content: content})
	if err != nil {
		return "", fmt.Errorf("encode task payload: %w", err)
	}
	taskID, err := e.store.CreateTaskForAgent(ctx, agentID, sessionID, string(payload))
	if err != nil {
		return "", err
	}

	go func() {
		taskCtx, cancel := context.WithTimeout(ctx, e.config.TaskTimeout)
		defer cancel()

		var brain Brain
		switch p := e.proc.(type) {
		case EchoProcessor:
			brain = p.Brain
		}

		if brain == nil {
			slog.Error("brain not available for streaming")
			return
		}

		if err := brain.Stream(taskCtx, sessionID, content, onChunk); err != nil {
			slog.Error("streaming failed", "error", err)
			_, _ = e.store.HandleTaskFailure(taskCtx, taskID, err.Error())
			return
		}

		_ = e.store.CompleteTask(taskCtx, taskID, `{"reply": "streamed"}`)
	}()

	return taskID, nil
}

func (e *Engine) AbortTask(ctx context.Context, taskID string) (bool, error) {
	e.cancelMu.RLock()
	cancel, ok := e.cancels[taskID]
	e.cancelMu.RUnlock()
	if ok {
		cancel()
	}
	aborted, err := e.store.AbortTask(ctx, taskID)
	if err != nil {
		e.setLastError(err)
		return false, err
	}
	return aborted || ok, nil
}

// Bus returns the event bus, or nil if not configured.
func (e *Engine) Bus() *bus.Bus {
	return e.bus
}

func (e *Engine) Status() Status {
	status := Status{
		AgentID:     e.agentID,
		WorkerCount: e.config.WorkerCount,
		ActiveTasks: e.activeTasks.Load(),
	}
	if ptr := e.lastError.Load(); ptr != nil {
		status.LastError = *ptr
	}
	return status
}
