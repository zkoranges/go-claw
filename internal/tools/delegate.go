package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/coordinator"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
	"github.com/basket/go-claw/internal/shared"
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
)

const (
	capDelegateTask      = "tools.delegate_task"
	capDelegateTaskAsync = "tools.delegate_task_async"
)

// DelegateTaskInput is the input for the delegate_task tool.
type DelegateTaskInput struct {
	// TargetAgent is the agent to delegate the task to (either this or Capability must be provided).
	TargetAgent string `json:"target_agent,omitempty"`
	// Capability routes delegation to an agent with this capability if TargetAgent is not specified.
	// GC-SPEC-PDR-v4-Phase-1: Capability-based agent routing.
	Capability string `json:"capability,omitempty"`
	// Prompt is what to ask the target agent.
	Prompt string `json:"prompt"`
	// SessionID is the session context for the delegated task.
	SessionID string `json:"session_id"`
	// TimeoutSec is the max time to wait for the result. Default 120s, max 300s.
	TimeoutSec int `json:"timeout_sec,omitempty"`
}

// DelegateTaskOutput is the output for the delegate_task tool.
type DelegateTaskOutput struct {
	// TaskID is the ID of the delegated task.
	TaskID string `json:"task_id"`
	// Status is the terminal status (SUCCEEDED or FAILED).
	Status string `json:"status"`
	// Result is the agent's response.
	Result string `json:"result,omitempty"`
	// Error is the error message if the task failed.
	Error string `json:"error,omitempty"`
}

// AsyncDelegateTaskInput is the input for the delegate_task_async tool (PDR v7 Phase 2).
type AsyncDelegateTaskInput struct {
	// TargetAgent is the agent to delegate the task to (required).
	TargetAgent string `json:"target_agent"`
	// Prompt is what to ask the target agent.
	Prompt string `json:"prompt"`
	// SessionID is the session context for the delegated task.
	SessionID string `json:"session_id"`
}

// AsyncDelegateTaskOutput is the output for the delegate_task_async tool (PDR v7 Phase 2).
type AsyncDelegateTaskOutput struct {
	// DelegationID is the ID of the delegation record.
	DelegationID string `json:"delegation_id"`
	// Status is "queued" for async delegations.
	Status string `json:"status"`
}

// chatPayload mirrors engine.chatTaskPayload for encoding delegated task payloads.
type chatPayload struct {
	Content string `json:"content"`
}

// delegateTask creates a task for the target agent and blocks until it completes.
func delegateTask(ctx context.Context, input *DelegateTaskInput, store *persistence.Store, pol policy.Checker, maxHops int) (*DelegateTaskOutput, error) {
	// Policy check.
	if pol == nil || !pol.AllowCapability(capDelegateTask) {
		pv := ""
		if pol != nil {
			pv = pol.PolicyVersion()
		}
		audit.Record("deny", capDelegateTask, "missing_capability", pv, "delegate_task")
		return nil, fmt.Errorf("policy denied capability %q", capDelegateTask)
	}

	pv := pol.PolicyVersion()
	audit.Record("allow", capDelegateTask, "capability_granted", pv, "delegate_task")

	// Validate inputs.
	targetAgent := input.TargetAgent
	if targetAgent == "" && input.Capability == "" {
		return nil, fmt.Errorf("delegate_task: either target_agent or capability must be provided")
	}
	if input.Prompt == "" {
		return nil, fmt.Errorf("delegate_task: prompt must be non-empty")
	}
	if input.SessionID == "" {
		return nil, fmt.Errorf("delegate_task: session_id must be non-empty")
	}

	// If capability is specified without a specific agent, resolve it.
	// GC-SPEC-PDR-v4-Phase-1: Capability-based agent routing.
	if targetAgent == "" && input.Capability != "" {
		// For now, find the first agent in the store with any matching pattern.
		// In future, agents could declare their capabilities explicitly.
		agents, err := store.ListAgents(ctx)
		if err != nil {
			return nil, fmt.Errorf("delegate_task: list agents for capability %q: %w", input.Capability, err)
		}
		if len(agents) == 0 {
			return nil, fmt.Errorf("delegate_task: no agents available for capability %q", input.Capability)
		}
		// Use the first agent (simplest routing for Phase 1).
		// TODO: In future phases, agents will declare capabilities explicitly.
		targetAgent = agents[0].AgentID
		slog.Debug("delegate_task: resolved capability to agent",
			"capability", input.Capability, "agent", targetAgent)
	}

	// Prevent self-delegation (would deadlock the calling agent's worker).
	callerAgent := shared.AgentID(ctx)
	if callerAgent != "" && callerAgent == targetAgent {
		return nil, fmt.Errorf("delegate_task: cannot delegate to yourself (%q)", callerAgent)
	}

	// Check delegation hop limit to prevent infinite chains.
	currentHop := shared.DelegationHop(ctx)
	if currentHop >= maxHops {
		return nil, fmt.Errorf("delegate_task: max delegation depth exceeded (%d hops)", maxHops)
	}

	// Validate target agent exists.
	agent, err := store.GetAgent(ctx, targetAgent)
	if err != nil {
		return nil, fmt.Errorf("delegate_task: check target agent: %w", err)
	}
	if agent == nil {
		return nil, fmt.Errorf("delegate_task: target agent %q not found", targetAgent)
	}

	timeout := time.Duration(input.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	if timeout > 300*time.Second {
		timeout = 300 * time.Second
	}

	// Ensure session exists before creating the task.
	if err := store.EnsureSession(ctx, input.SessionID); err != nil {
		return nil, fmt.Errorf("delegate_task: ensure session: %w", err)
	}

	// Get caller task ID early (before creating child task).
	// GC-SPEC-PDR-v4-Phase-1: Track delegation as task tree hierarchy.
	callerTaskID := shared.TaskID(ctx)

	// Wrap prompt in chatTaskPayload JSON so the engine's EchoProcessor can decode it.
	payload, err := json.Marshal(chatPayload{Content: input.Prompt})
	if err != nil {
		return nil, fmt.Errorf("delegate_task: encode payload: %w", err)
	}

	// Create the task for the target agent.
	taskID, err := store.CreateTaskForAgent(ctx, targetAgent, input.SessionID, string(payload))
	if err != nil {
		return nil, fmt.Errorf("delegate_task: create task: %w", err)
	}

	// Publish delegation started event.
	if store.Bus() != nil {
		store.Bus().Publish(bus.TopicDelegationStarted, map[string]interface{}{
			"parent_task_id": callerTaskID,
			"child_task_id":  taskID,
			"target_agent":   targetAgent,
			"hop_count":      currentHop,
		})
	}

	// Set parent-child relationship for task tree.
	if callerTaskID != "" {
		// Best-effort: don't fail the delegation if this fails
		if err := store.SetParentTask(ctx, taskID, callerTaskID); err != nil {
			slog.Warn("delegate_task: failed to set parent task",
				"child_task_id", taskID,
				"parent_task_id", callerTaskID,
				"error", err,
			)
		}
	}

	slog.Info("delegate_task: task created, waiting for result",
		"task_id", taskID,
		"target_agent", targetAgent,
		"timeout", timeout,
	)

	// Wait for completion using the coordinator's Waiter.
	// GC-SPEC-PDR-v4-Phase-2: Event-driven task completion tracking.
	// GC-SPEC-PDR-v4-Phase-5: Publish delegation events for TUI visibility.
	waiter := coordinator.NewWaiter(nil, store) // nil bus means polling-only mode
	// Pass incremented hop count to child task context
	childCtx := shared.WithDelegationHop(ctx, currentHop+1)
	result, err := waiter.WaitForTask(childCtx, taskID, timeout)
	if err != nil {
		// Abort the child task on error so it doesn't run orphaned.
		if _, abortErr := store.AbortTask(context.Background(), taskID); abortErr != nil {
			slog.Warn("delegate_task: failed to abort child task on error",
				"task_id", taskID, "error", abortErr)
		}

		// Check if the context was canceled or deadline exceeded.
		ctxErr := ctx.Err()
		if ctxErr == context.Canceled || ctxErr == context.DeadlineExceeded {
			return &DelegateTaskOutput{
				TaskID: taskID,
				Status: "CANCELED",
				Error:  ctxErr.Error(),
			}, nil
		}

		// For other errors, return the error.
		return nil, err
	}

	// Publish delegation completed event.
	if store.Bus() != nil {
		store.Bus().Publish(bus.TopicDelegationCompleted, map[string]interface{}{
			"child_task_id": taskID,
			"status":        result.Status,
		})
	}

	return &DelegateTaskOutput{
		TaskID: result.TaskID,
		Status: result.Status,
		Result: result.Output,
		Error:  result.Error,
	}, nil
}

// delegateTaskAsync creates a delegation record and task without blocking (PDR v7 Phase 2).
func delegateTaskAsync(ctx context.Context, input *AsyncDelegateTaskInput, store *persistence.Store, pol policy.Checker, maxHops int) (*AsyncDelegateTaskOutput, error) {
	// Policy check.
	if pol == nil || !pol.AllowCapability(capDelegateTaskAsync) {
		pv := ""
		if pol != nil {
			pv = pol.PolicyVersion()
		}
		audit.Record("deny", capDelegateTaskAsync, "missing_capability", pv, "delegate_task_async")
		return nil, fmt.Errorf("policy denied capability %q", capDelegateTaskAsync)
	}

	pv := pol.PolicyVersion()
	audit.Record("allow", capDelegateTaskAsync, "capability_granted", pv, "delegate_task_async")

	// Validate inputs.
	if input.TargetAgent == "" {
		return nil, fmt.Errorf("delegate_task_async: target_agent must be provided")
	}
	if input.Prompt == "" {
		return nil, fmt.Errorf("delegate_task_async: prompt must be non-empty")
	}
	if input.SessionID == "" {
		return nil, fmt.Errorf("delegate_task_async: session_id must be non-empty")
	}

	// Prevent self-delegation (would deadlock).
	callerAgent := shared.AgentID(ctx)
	if callerAgent != "" && callerAgent == input.TargetAgent {
		return nil, fmt.Errorf("delegate_task_async: cannot delegate to yourself (%q)", callerAgent)
	}

	// Check delegation hop limit to prevent infinite chains.
	currentHop := shared.DelegationHop(ctx)
	if currentHop >= maxHops {
		return nil, fmt.Errorf("delegate_task_async: max delegation depth exceeded (%d hops)", maxHops)
	}

	// Validate target agent exists.
	agent, err := store.GetAgent(ctx, input.TargetAgent)
	if err != nil {
		return nil, fmt.Errorf("delegate_task_async: check target agent: %w", err)
	}
	if agent == nil {
		return nil, fmt.Errorf("delegate_task_async: target agent %q not found", input.TargetAgent)
	}

	// Ensure session exists before creating the task.
	if err := store.EnsureSession(ctx, input.SessionID); err != nil {
		return nil, fmt.Errorf("delegate_task_async: ensure session: %w", err)
	}

	// Wrap prompt in chatTaskPayload JSON so the engine's EchoProcessor can decode it.
	payload, err := json.Marshal(chatPayload{Content: input.Prompt})
	if err != nil {
		return nil, fmt.Errorf("delegate_task_async: encode payload: %w", err)
	}

	// Create the task for the target agent.
	taskID, err := store.CreateTaskForAgent(ctx, input.TargetAgent, input.SessionID, string(payload))
	if err != nil {
		return nil, fmt.Errorf("delegate_task_async: create task: %w", err)
	}

	// Create delegation record.
	deleg := &persistence.Delegation{
		ParentAgent: callerAgent,
		ChildAgent:  input.TargetAgent,
		Prompt:      input.Prompt,
		Status:      "queued",
		TaskID:      taskID,
		CreatedAt:   time.Now(),
	}

	if err := store.CreateDelegation(ctx, deleg); err != nil {
		return nil, fmt.Errorf("delegate_task_async: create delegation: %w", err)
	}

	// Publish delegation started event.
	if store.Bus() != nil {
		store.Bus().Publish(bus.TopicDelegationStarted, map[string]interface{}{
			"delegation_id": deleg.ID,
			"child_task_id": taskID,
			"target_agent":  input.TargetAgent,
			"hop_count":     currentHop,
		})
	}

	slog.Info("delegate_task_async: delegation queued",
		"delegation_id", deleg.ID,
		"task_id", taskID,
		"target_agent", input.TargetAgent,
	)

	return &AsyncDelegateTaskOutput{
		DelegationID: deleg.ID,
		Status:       "queued",
	}, nil
}

func registerDelegate(g *genkit.Genkit, reg *Registry) ai.ToolRef {
	return genkit.DefineTool(g, "delegate_task",
		"Delegate a task to another agent and wait for its result. The calling agent's turn pauses until the target agent completes. Requires tools.delegate_task capability.",
		func(ctx *ai.ToolContext, input DelegateTaskInput) (DelegateTaskOutput, error) {
			reg.publishToolCall(ctx, "delegate_task")
			maxHops := reg.DelegationMaxHops
			if maxHops <= 0 {
				maxHops = 2 // Fallback default
			}
			out, err := delegateTask(ctx, &input, reg.Store, reg.Policy, maxHops)
			if err != nil {
				return DelegateTaskOutput{}, err
			}
			return *out, nil
		},
	)
}

func registerDelegateAsync(g *genkit.Genkit, reg *Registry) ai.ToolRef {
	return genkit.DefineTool(g, "delegate_task_async",
		"Delegate a task to another agent without waiting for its result. Returns immediately with a delegation ID. Requires tools.delegate_task_async capability.",
		func(ctx *ai.ToolContext, input AsyncDelegateTaskInput) (AsyncDelegateTaskOutput, error) {
			reg.publishToolCall(ctx, "delegate_task_async")
			maxHops := reg.DelegationMaxHops
			if maxHops <= 0 {
				maxHops = 2 // Fallback default
			}
			out, err := delegateTaskAsync(ctx, &input, reg.Store, reg.Policy, maxHops)
			if err != nil {
				return AsyncDelegateTaskOutput{}, err
			}
			return *out, nil
		},
	)
}
