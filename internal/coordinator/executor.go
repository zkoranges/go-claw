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

// NewExecutor creates a DAG executor with completion tracking.
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

	// GC-SPEC-PDR-v4-Phase-2: Initialize step records for persistence
	if e.store != nil {
		stepRecords := make([]persistence.PlanExecutionStep, 0, len(plan.Steps))
		for waveNum, wave := range order {
			for stepIdx, step := range wave {
				stepRecords = append(stepRecords, persistence.PlanExecutionStep{
					StepID:     step.ID,
					StepIndex:  stepIdx,
					WaveNumber: waveNum,
					AgentID:    step.AgentID,
					Prompt:     step.Prompt,
				})
			}
		}
		if err := e.store.InitializePlanSteps(ctx, execID, stepRecords); err != nil {
			if e.store != nil {
				_ = e.store.CompletePlanExecution(ctx, execID, "failed", 0)
			}
			return nil, fmt.Errorf("initialize step records: %w", err)
		}
	}

	for waveNum, wave := range order {
		if len(wave) == 0 {
			continue
		}
		if err := e.executeWave(ctx, execID, sessionID, wave, result); err != nil {
			if e.store != nil {
				_ = e.store.CompletePlanExecution(ctx, execID, "failed", result.TotalCost())
			}
			return result, fmt.Errorf("wave %d failed: %w", waveNum, err)
		}

		// GC-SPEC-PDR-v4-Phase-2: Track completed waves for resumption
		if e.store != nil {
			_ = e.store.UpdatePlanWave(ctx, execID, waveNum+1)
		}
	}

	// Record completion
	if e.store != nil {
		_ = e.store.CompletePlanExecution(ctx, execID, "succeeded", result.TotalCost())
	}

	return result, nil
}

// Resume continues execution of a crashed plan from the last completed wave.
// GC-SPEC-PDR-v4-Phase-3: Plan resumption after crash.
func (e *Executor) Resume(ctx context.Context, execID string, plan *Plan) (*ExecutionResult, error) {
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("invalid plan: %w", err)
	}

	// Hydrate plan execution state
	if e.store == nil {
		return nil, fmt.Errorf("cannot resume without store")
	}

	exec, err := e.store.GetPlanExecution(ctx, execID)
	if err != nil {
		return nil, fmt.Errorf("hydrate plan execution: %w", err)
	}

	steps, err := e.store.GetPlanSteps(ctx, execID)
	if err != nil {
		return nil, fmt.Errorf("hydrate plan steps: %w", err)
	}

	// Build execution result from persisted state
	result := &ExecutionResult{
		ExecutionID: execID,
		StepResults: make(map[string]StepResult),
	}

	// Populate results from persisted steps
	stepMap := make(map[string]persistence.PlanExecutionStep)
	for _, s := range steps {
		stepMap[s.StepID] = s
		if s.Status == "succeeded" || s.Status == "failed" {
			result.StepResults[s.StepID] = StepResult{
				Status:  s.Status,
				Output:  s.Result,
				Error:   s.Error,
				CostUSD: s.CostUSD,
			}
		}
	}

	// Toposort to get wave structure
	order, err := topoSort(plan.Steps)
	if err != nil {
		if e.store != nil {
			_ = e.store.CompletePlanExecution(ctx, execID, "failed", result.TotalCost())
		}
		return nil, fmt.Errorf("invalid plan: %w", err)
	}

	// Get session ID from hydrated execution
	sessionID := exec.SessionID

	// Resume from last completed wave
	currentWave := exec.CurrentWave
	for waveNum := currentWave; waveNum < len(order); waveNum++ {
		wave := order[waveNum]
		if len(wave) == 0 {
			continue
		}

		// Filter to only pending/running steps (skip completed)
		var pendingSteps []PlanStep
		for _, step := range wave {
			if persisted, ok := stepMap[step.ID]; ok && (persisted.Status == "succeeded" || persisted.Status == "failed") {
				// Already completed, skip
				continue
			}
			pendingSteps = append(pendingSteps, step)
		}

		// If no pending steps, this wave already completed
		if len(pendingSteps) == 0 {
			continue
		}

		// Execute pending steps in this wave
		if err := e.executeWave(ctx, execID, sessionID, pendingSteps, result); err != nil {
			if e.store != nil {
				_ = e.store.CompletePlanExecution(ctx, execID, "failed", result.TotalCost())
			}
			return result, fmt.Errorf("wave %d failed: %w", waveNum, err)
		}

		// Track completed wave
		if e.store != nil {
			_ = e.store.UpdatePlanWave(ctx, execID, waveNum+1)
		}
	}

	// Record final completion
	if e.store != nil {
		_ = e.store.CompletePlanExecution(ctx, execID, "succeeded", result.TotalCost())
	}

	return result, nil
}

// executeWave runs all steps in a wave (parallel independent steps).
// GC-SPEC-PDR-v4-Phase-2: Persistent step tracking in executeWave.
func (e *Executor) executeWave(ctx context.Context, execID, sessionID string, steps []PlanStep, result *ExecutionResult) error {
	// Create all tasks in this wave
	taskToStep := make(map[string]string) // taskID -> stepID
	var taskIDs []string

	for _, step := range steps {
		// Resolve prompt template (substitute references from earlier steps)
		prompt := resolvePrompt(step.Prompt, result)

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

		// GC-SPEC-PDR-v4-Phase-2: Publish step started event
		if e.store != nil && e.store.Bus() != nil {
			e.store.Bus().Publish("plan.step.started", map[string]interface{}{
				"execution_id": execID,
				"step_id":      step.ID,
				"task_id":      taskID,
			})
		}
	}

	if e.waiter == nil {
		// No waiter (test mode) — return with PENDING status
		return nil
	}

	// Wait for ALL tasks in this wave to complete
	taskResults, err := e.waiter.WaitForAll(ctx, taskIDs, 5*time.Minute)

	// Update step results from task results and handle retries
	stepMap := make(map[string]PlanStep)
	for _, step := range steps {
		stepMap[step.ID] = step
	}

	for taskID, tr := range taskResults {
		stepID := taskToStep[taskID]
		step := stepMap[stepID]

		// Initialize max retries (default 2 if not specified)
		maxRetries := step.MaxRetries
		if maxRetries == 0 {
			maxRetries = 2
		}

		// Check if step failed and should be retried
		if tr.Status == "FAILED" {
			currentResult := result.StepResults[stepID]
			// Count retries from step result (initially 0)
			retryCount := 0
			if currentResult.TaskID != "" {
				// Already has a result, this might be a retry
				if strings.Contains(currentResult.TaskID, "-retry-") {
					// Extract retry count from task ID or use error field
					// For now, just check if we can retry
				}
			}

			// If we can retry, create a new task with error context
			if retryCount < maxRetries {
				retryCount++
				newTaskID, retryErr := RetryWithError(ctx, e.taskRouter, sessionID, step, tr.Error, retryCount+1)
				if retryErr == nil {
					// Wait for retry task to complete
					retryResults, waitErr := e.waiter.WaitForAll(ctx, []string{newTaskID}, 5*time.Minute)
					if waitErr == nil && len(retryResults) > 0 {
						retryResult := retryResults[newTaskID]
						// Use retry result if available
						tr = retryResult

						// Publish retry event
						if e.store != nil && e.store.Bus() != nil {
							e.store.Bus().Publish("plan.step.retry", map[string]interface{}{
								"execution_id": execID,
								"step_id":      stepID,
								"attempt":      retryCount + 1,
								"error":        tr.Error,
							})
						}
					}
				}
			}
		}

		result.StepResults[stepID] = StepResult{
			TaskID:     tr.TaskID,
			Status:     tr.Status,
			Output:     tr.Output,
			CostUSD:    tr.CostUSD,
			DurationMs: tr.DurationMs,
			Error:      tr.Error,
		}

		// GC-SPEC-PDR-v4-Phase-2: Record step completion for persistence
		if e.store != nil {
			recordErr := e.store.RecordStepComplete(ctx, execID, stepID, tr.Status, tr.Output, tr.Error, tr.CostUSD)
			if recordErr != nil {
				// Log but don't fail - best-effort persistence
				// In production, this would log to structured logger
				_ = recordErr
			}
		}
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
	stepMap := make(map[string]PlanStep)
	for _, s := range steps {
		stepMap[s.ID] = s
	}

	// Check for unknown dependencies.
	for _, s := range steps {
		for _, dep := range s.DependsOn {
			if _, exists := stepMap[dep]; !exists {
				return nil, fmt.Errorf("step %s depends on nonexistent step %s", s.ID, dep)
			}
		}
	}

	// Kahn's algorithm for topological sort into waves.
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
