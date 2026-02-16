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

// Config controls the engine's worker pool size, polling behavior, and agent scoping.
type Config struct {
	WorkerCount   int
	PollInterval  time.Duration
	TaskTimeout   time.Duration
	MaxQueueDepth int // GC-SPEC-QUE-008: 0 = unlimited
	Bus           *bus.Bus
	AgentID       string // if set, workers only claim tasks for this agent
}

// Processor transforms a claimed task into a result string or error.
type Processor interface {
	Process(ctx context.Context, task persistence.Task) (string, error)
}

// ChatTaskRouter routes chat tasks to the appropriate agent. Implemented by
// agent.Registry to decouple packages that need to create tasks (heartbeat,
// channels) from the agent package (which would cause an import cycle).
type ChatTaskRouter interface {
	CreateChatTask(ctx context.Context, agentID, sessionID, content string) (string, error)
}

// EchoProcessor decodes chatTaskPayload JSON from the task, forwards it to a Brain,
// and wraps the reply in chatResultPayload JSON.
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
		return "", fmt.Errorf("brain respond: %w", err)
	}
	out, err := json.Marshal(chatResultPayload{Reply: reply})
	if err != nil {
		return "", fmt.Errorf("encode result: %w", err)
	}
	return string(out), nil
}

// Status is a point-in-time snapshot of an engine's worker pool, exposed via system.status.
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

	once sync.Once      // ensures Start runs exactly once
	wg   sync.WaitGroup // tracks worker goroutines for Drain

	// cancelMu protects the cancels map. Lock ordering: cancelMu is a leaf
	// lock — never hold it while acquiring another mutex or doing I/O.
	cancelMu sync.RWMutex
	cancels  map[string]context.CancelFunc // guarded by cancelMu

	activeTasks atomic.Int32           // current in-flight task count
	lastError   atomic.Pointer[string] // most recent error message
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
		slog.Info("engine drained cleanly", "agent_id", e.agentID)
	case <-time.After(timeout):
		slog.Warn("engine drain timeout; marking in-flight tasks for recovery", "timeout", timeout, "agent_id", e.agentID)
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
	// GC-SPEC-RUN-004: Propagate trace_id and run_id for this task's execution scope.
	traceID := shared.NewTraceID()
	runID := shared.NewRunID()
	ctx = shared.WithTraceID(ctx, traceID)
	ctx = shared.WithRunID(ctx, runID)
	// GC-SPEC-QUE-006: Propagate task_id so tools can build idempotency keys.
	ctx = shared.WithTaskID(ctx, task.ID)
	// Propagate agent_id so tools (send_message, read_messages) know which agent is calling.
	ctx = shared.WithAgentID(ctx, e.agentID)
	slog.Info("task processing", "task_id", task.ID, "session_id", task.SessionID, "trace_id", traceID, "run_id", runID, "agent_id", e.agentID)

	// bgCtx carries observability values (trace_id, run_id) but is not tied to
	// cancellation, so it can safely be used for store writes after the task
	// context expires.
	bgCtx := shared.WithTraceID(context.Background(), traceID)
	bgCtx = shared.WithRunID(bgCtx, runID)

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
		_, _ = e.store.AbortTask(bgCtx, task.ID)
		e.publishEvent("task.canceled", map[string]string{"task_id": task.ID, "session_id": task.SessionID})
		return
	}
	if cancelled, _ := e.store.IsCancelRequested(bgCtx, task.ID); cancelled {
		_, _ = e.store.AbortTask(bgCtx, task.ID)
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
				if cancelled, _ := e.store.IsCancelRequested(bgCtx, task.ID); cancelled {
					cancel() // Triggers context cancellation for the processor.
					return
				}
				ok, err := e.store.HeartbeatLease(bgCtx, task.ID, task.LeaseOwner)
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
			_, _ = e.store.AbortTask(bgCtx, task.ID)
			return
		}
		e.setLastError(err)
		slog.Warn("task failed", "task_id", task.ID, "session_id", task.SessionID, "trace_id", traceID, "run_id", runID, "agent_id", e.agentID, "error", err.Error())
		_, _ = e.store.HandleTaskFailure(bgCtx, task.ID, err.Error())
		e.publishEvent("task.failed", map[string]string{"task_id": task.ID, "session_id": task.SessionID})
		return
	}

	// Invariant: never write success result once context is canceled.
	if taskCtx.Err() != nil {
		if errors.Is(taskCtx.Err(), context.Canceled) {
			_, _ = e.store.AbortTask(bgCtx, task.ID)
			e.publishEvent("task.canceled", map[string]string{"task_id": task.ID, "session_id": task.SessionID})
			return
		}
		err = fmt.Errorf("skip complete after context end: %w", taskCtx.Err())
		e.setLastError(err)
		slog.Warn("task failed", "task_id", task.ID, "session_id", task.SessionID, "trace_id", traceID, "run_id", runID, "agent_id", e.agentID, "error", err.Error())
		_, _ = e.store.HandleTaskFailure(bgCtx, task.ID, err.Error())

		// Update linked delegation if this task is part of an async delegation (PDR v7 Phase 2)
		if deleg, delegErr := e.store.GetDelegationByTaskID(bgCtx, task.ID); delegErr == nil && deleg != nil {
			if delegErr := e.store.FailDelegation(bgCtx, deleg.ID, err.Error()); delegErr != nil {
				slog.Warn("failed to mark delegation as failed",
					"delegation_id", deleg.ID,
					"task_id", task.ID,
					"error", delegErr,
				)
			}
		}

		e.publishEvent("task.failed", map[string]string{"task_id": task.ID, "session_id": task.SessionID})
		return
	}

	if err := e.store.CompleteTask(bgCtx, task.ID, result); err != nil {
		e.setLastError(fmt.Errorf("complete task: %w", err))
		slog.Error("failed to complete task", "task_id", task.ID, "session_id", task.SessionID, "trace_id", traceID, "run_id", runID, "agent_id", e.agentID, "error", err)
		return
	}

	// Update linked delegation if this task is part of an async delegation (PDR v7 Phase 2)
	if deleg, err := e.store.GetDelegationByTaskID(bgCtx, task.ID); err == nil && deleg != nil {
		if err := e.store.CompleteDelegation(bgCtx, deleg.ID, result); err != nil {
			slog.Warn("failed to complete delegation",
				"delegation_id", deleg.ID,
				"task_id", task.ID,
				"error", err,
			)
		}
	}

	slog.Info("task succeeded", "task_id", task.ID, "session_id", task.SessionID, "trace_id", traceID, "run_id", runID, "agent_id", e.agentID)
	e.publishEvent("task.succeeded", map[string]string{"task_id": task.ID, "session_id": task.SessionID})

	var payload chatResultPayload
	if json.Unmarshal([]byte(result), &payload) == nil && payload.Reply != "" {
		_ = e.store.AddHistory(bgCtx, task.SessionID, e.agentID, "assistant", payload.Reply, tokenutil.EstimateTokens(payload.Reply))
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
	return e.createChatTask(ctx, e.agentID, sessionID, content)
}

// CreateChatTaskForAgent creates a chat task scoped to a specific agent.
func (e *Engine) CreateChatTaskForAgent(ctx context.Context, agentID, sessionID, content string) (string, error) {
	return e.createChatTask(ctx, agentID, sessionID, content)
}

func (e *Engine) createChatTask(ctx context.Context, agentID, sessionID, content string) (string, error) {
	// GC-SPEC-QUE-008: Apply backpressure at intake when queue is saturated.
	if e.config.MaxQueueDepth > 0 {
		var depth int
		var err error
		if agentID != "" {
			depth, err = e.store.QueueDepthForAgent(ctx, agentID)
		} else {
			depth, err = e.store.QueueDepth(ctx)
		}
		if err != nil {
			return "", fmt.Errorf("check queue depth: %w", err)
		}
		if depth >= e.config.MaxQueueDepth {
			slog.Warn("queue backpressure applied", "depth", depth, "max", e.config.MaxQueueDepth)
			return "", ErrQueueSaturated
		}
	}
	if err := e.store.EnsureSession(ctx, sessionID); err != nil {
		return "", fmt.Errorf("create chat task: ensure session: %w", err)
	}
	if err := e.store.AddHistory(ctx, sessionID, agentID, "user", content, tokenutil.EstimateTokens(content)); err != nil {
		return "", fmt.Errorf("create chat task: add history: %w", err)
	}
	payload, err := json.Marshal(chatTaskPayload{Content: content})
	if err != nil {
		return "", fmt.Errorf("create chat task: encode payload: %w", err)
	}
	if agentID != "" {
		return e.store.CreateTaskForAgent(ctx, agentID, sessionID, string(payload))
	}
	return e.store.CreateTask(ctx, sessionID, string(payload))
}

// StreamChatTask handles streaming chat directly without going through the task queue.
// It returns a task ID and an error channel for streaming results.
func (e *Engine) StreamChatTask(ctx context.Context, sessionID, content string, onChunk func(content string) error) (string, error) {
	return e.streamChatTask(ctx, e.agentID, sessionID, content, onChunk)
}

// StreamChatTaskForAgent handles streaming chat scoped to a specific agent.
func (e *Engine) StreamChatTaskForAgent(ctx context.Context, agentID, sessionID, content string, onChunk func(content string) error) (string, error) {
	return e.streamChatTask(ctx, agentID, sessionID, content, onChunk)
}

func (e *Engine) streamChatTask(ctx context.Context, agentID, sessionID, content string, onChunk func(content string) error) (string, error) {
	if e.config.MaxQueueDepth > 0 {
		var depth int
		var err error
		if agentID != "" {
			depth, err = e.store.QueueDepthForAgent(ctx, agentID)
		} else {
			depth, err = e.store.QueueDepth(ctx)
		}
		if err != nil {
			return "", fmt.Errorf("check queue depth: %w", err)
		}
		if depth >= e.config.MaxQueueDepth {
			slog.Warn("queue backpressure applied", "depth", depth, "max", e.config.MaxQueueDepth)
			return "", ErrQueueSaturated
		}
	}
	if err := e.store.EnsureSession(ctx, sessionID); err != nil {
		return "", fmt.Errorf("stream chat task: ensure session: %w", err)
	}
	if err := e.store.AddHistory(ctx, sessionID, agentID, "user", content, tokenutil.EstimateTokens(content)); err != nil {
		return "", fmt.Errorf("stream chat task: add history: %w", err)
	}

	payload, err := json.Marshal(chatTaskPayload{Content: content})
	if err != nil {
		return "", fmt.Errorf("stream chat task: encode payload: %w", err)
	}

	var taskID string
	if agentID != "" {
		taskID, err = e.store.CreateTaskForAgent(ctx, agentID, sessionID, string(payload))
	} else {
		taskID, err = e.store.CreateTask(ctx, sessionID, string(payload))
	}
	if err != nil {
		return "", fmt.Errorf("stream chat task: create task: %w", err)
	}

	// Run streaming synchronously so that the caller (HTTP/WS handler) blocks
	// until all chunks have been delivered. Previously this ran in a goroutine,
	// which caused the OpenAI SSE handler to send [DONE] before any chunks.
	e.wg.Add(1)
	defer e.wg.Done()

	taskCtx, cancel := context.WithTimeout(ctx, e.config.TaskTimeout)
	defer cancel()

	if e.proc == nil {
		_, _ = e.store.HandleTaskFailure(context.Background(), taskID, "processor not initialized for streaming")
		return taskID, fmt.Errorf("processor not initialized for streaming")
	}

	var brain Brain
	switch p := e.proc.(type) {
	case EchoProcessor:
		brain = p.Brain
	}

	if brain == nil {
		_, _ = e.store.HandleTaskFailure(context.Background(), taskID, "brain not available for streaming")
		return taskID, fmt.Errorf("brain not available for streaming")
	}

	if err := brain.Stream(taskCtx, sessionID, content, onChunk); err != nil {
		slog.Error("streaming failed", "error", err)
		_, _ = e.store.HandleTaskFailure(context.Background(), taskID, err.Error())
		return taskID, nil // task failure recorded; return taskID so caller can check status
	}

	_ = e.store.CompleteTask(context.Background(), taskID, `{"reply": "streamed"}`)
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
		e.setLastError(fmt.Errorf("abort task: %w", err))
		return false, fmt.Errorf("abort task: %w", err)
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
