package tools

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
)

const capDelegateTask = "tools.delegate_task"

// DelegateTaskInput is the input for the delegate_task tool.
type DelegateTaskInput struct {
	// TargetAgent is the agent to delegate the task to.
	TargetAgent string `json:"target_agent"`
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

// delegateTask creates a task for the target agent and blocks until it completes.
func delegateTask(ctx context.Context, input *DelegateTaskInput, store *persistence.Store, pol policy.Checker) (*DelegateTaskOutput, error) {
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
	if input.TargetAgent == "" {
		return nil, fmt.Errorf("delegate_task: target_agent must be non-empty")
	}
	if input.Prompt == "" {
		return nil, fmt.Errorf("delegate_task: prompt must be non-empty")
	}
	if input.SessionID == "" {
		return nil, fmt.Errorf("delegate_task: session_id must be non-empty")
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

	// Create the task for the target agent.
	taskID, err := store.CreateTaskForAgent(ctx, input.TargetAgent, input.SessionID, input.Prompt)
	if err != nil {
		return nil, fmt.Errorf("delegate_task: create task: %w", err)
	}

	slog.Info("delegate_task: task created, waiting for result",
		"task_id", taskID,
		"target_agent", input.TargetAgent,
		"timeout", timeout,
	)

	// Poll for completion.
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return &DelegateTaskOutput{
				TaskID: taskID,
				Status: "CANCELED",
				Error:  "context canceled",
			}, nil
		case <-ticker.C:
			if time.Now().After(deadline) {
				return &DelegateTaskOutput{
					TaskID: taskID,
					Status: "FAILED",
					Error:  fmt.Sprintf("timeout after %s waiting for agent %q", timeout, input.TargetAgent),
				}, nil
			}

			task, err := store.GetTask(ctx, taskID)
			if err != nil {
				return nil, fmt.Errorf("delegate_task: poll task: %w", err)
			}
			if task == nil {
				return nil, fmt.Errorf("delegate_task: task %q not found", taskID)
			}

			switch task.Status {
			case persistence.TaskStatusSucceeded:
				return &DelegateTaskOutput{
					TaskID: taskID,
					Status: string(task.Status),
					Result: task.Result,
				}, nil
			case persistence.TaskStatusFailed, persistence.TaskStatusDeadLetter, persistence.TaskStatusCanceled:
				return &DelegateTaskOutput{
					TaskID: taskID,
					Status: string(task.Status),
					Error:  task.Error,
				}, nil
			}
			// Still running — continue polling.
		}
	}
}

func registerDelegate(g *genkit.Genkit, reg *Registry) ai.ToolRef {
	return genkit.DefineTool(g, "delegate_task",
		"Delegate a task to another agent and wait for its result. The calling agent's turn pauses until the target agent completes. Requires tools.delegate_task capability.",
		func(ctx *ai.ToolContext, input DelegateTaskInput) (DelegateTaskOutput, error) {
			out, err := delegateTask(ctx, &input, reg.Store, reg.Policy)
			if err != nil {
				return DelegateTaskOutput{}, err
			}
			return *out, nil
		},
	)
}
