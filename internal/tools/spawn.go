package tools

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
)

const capSpawnTask = "tools.spawn_task"

// SpawnTaskInput is the input for the spawn_task tool.
type SpawnTaskInput struct {
	// Description is a human-readable summary of the subtask.
	Description string `json:"description"`
	// Payload is the task payload (prompt/instructions for the subtask).
	Payload string `json:"payload"`
	// Priority is the task priority (higher = more urgent). Defaults to 0.
	Priority int `json:"priority,omitempty"`
	// ParentTaskID is the ID of the parent task that is delegating work.
	ParentTaskID string `json:"parent_task_id"`
	// SessionID is the session context for the subtask.
	SessionID string `json:"session_id"`
	// TargetAgent routes the task to a specific agent instead of creating a subtask.
	TargetAgent string `json:"target_agent,omitempty"`
}

// SpawnTaskOutput is the output for the spawn_task tool.
type SpawnTaskOutput struct {
	// TaskID is the ID of the newly created subtask.
	TaskID string `json:"task_id"`
	// Status is the initial status of the created subtask.
	Status string `json:"status"`
}

// spawnTask creates a new subtask linked to a parent task. It enforces
// the tools.spawn_task capability via the policy engine and records an
// audit entry for every attempt.
func spawnTask(ctx context.Context, input *SpawnTaskInput, store *persistence.Store, pol policy.Checker) (*SpawnTaskOutput, error) {
	// Policy check: require tools.spawn_task capability.
	if pol == nil || !pol.AllowCapability(capSpawnTask) {
		pv := ""
		if pol != nil {
			pv = pol.PolicyVersion()
		}
		audit.Record("deny", capSpawnTask, "missing_capability", pv, "spawn_task")
		return nil, fmt.Errorf("policy denied capability %q", capSpawnTask)
	}

	pv := pol.PolicyVersion()
	audit.Record("allow", capSpawnTask, "capability_granted", pv, "spawn_task")

	// Validate inputs.
	if input.Description == "" {
		return nil, fmt.Errorf("spawn_task: description must be non-empty")
	}
	if input.Payload == "" {
		return nil, fmt.Errorf("spawn_task: payload must be non-empty")
	}
	if input.ParentTaskID == "" {
		return nil, fmt.Errorf("spawn_task: parent_task_id must be non-empty")
	}
	if input.SessionID == "" {
		return nil, fmt.Errorf("spawn_task: session_id must be non-empty")
	}

	// Route to specific agent if target_agent is set.
	if input.TargetAgent != "" {
		taskID, err := store.CreateTaskForAgent(ctx, input.TargetAgent, input.SessionID, input.Payload)
		if err != nil {
			return nil, fmt.Errorf("spawn_task for agent: %w", err)
		}
		slog.Info("spawn_task: task created for agent",
			"task_id", taskID,
			"target_agent", input.TargetAgent,
			"session_id", input.SessionID,
			"priority", input.Priority,
			"description", input.Description,
		)
		audit.Record("allow", capSpawnTask, "task_for_agent_created", pv, taskID)
		return &SpawnTaskOutput{
			TaskID: taskID,
			Status: string(persistence.TaskStatusQueued),
		}, nil
	}

	// Default: create subtask linked to parent.
	taskID, err := store.CreateSubtask(ctx, input.ParentTaskID, input.SessionID, input.Payload, input.Priority)
	if err != nil {
		return nil, fmt.Errorf("spawn_task: %w", err)
	}

	slog.Info("spawn_task: subtask created",
		"task_id", taskID,
		"parent_task_id", input.ParentTaskID,
		"session_id", input.SessionID,
		"priority", input.Priority,
		"description", input.Description,
	)

	audit.Record("allow", capSpawnTask, "subtask_created", pv, taskID)

	return &SpawnTaskOutput{
		TaskID: taskID,
		Status: string(persistence.TaskStatusQueued),
	}, nil
}

func registerSpawn(g *genkit.Genkit, reg *Registry) ai.ToolRef {
	return genkit.DefineTool(g, "spawn_task",
		"Create a subtask linked to a parent task. Requires tools.spawn_task capability.",
		func(ctx *ai.ToolContext, input SpawnTaskInput) (SpawnTaskOutput, error) {
			reg.publishToolCall(ctx, "spawn_task")
			out, err := spawnTask(ctx, &input, reg.Store, reg.Policy)
			if err != nil {
				return SpawnTaskOutput{}, err
			}
			return *out, nil
		},
	)
}
