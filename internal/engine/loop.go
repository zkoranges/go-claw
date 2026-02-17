package engine

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/config"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/google/uuid"
)

// Loop event topics.
const (
	EventLoopStarted   = "loop.started"
	EventLoopStep      = "loop.step"
	EventLoopCompleted = "loop.completed"
	EventLoopBudget    = "loop.budget_exceeded"
	EventLoopTimeout   = "loop.timeout"
	EventLoopFailed    = "loop.failed"
)

// LoopEventData carries metadata for loop events.
type LoopEventData struct {
	LoopID   string `json:"loop_id"`
	TaskID   string `json:"task_id"`
	AgentID  string `json:"agent_id"`
	Step     int    `json:"step"`
	MaxSteps int    `json:"max_steps,omitempty"`
	Status   string `json:"status,omitempty"`
}

// LoopStatus constants.
const (
	LoopStatusRunning   = "running"
	LoopStatusCompleted = "completed"
	LoopStatusBudget    = "budget_exceeded"
	LoopStatusTimeout   = "timeout"
	LoopStatusFailed    = "failed"
	LoopStatusCancelled = "cancelled"
)

// LoopResult is the final output of a loop execution.
type LoopResult struct {
	Status     string
	Steps      int
	TokensUsed int
	Response   string // final agent response text
	Error      error
}

// LoopRunner executes an agent loop with checkpoints and budget enforcement.
type LoopRunner struct {
	brain     Brain
	store     *persistence.Store
	bus       *bus.Bus
	logger    *slog.Logger
	config    config.LoopConfig
	agentID   string
	sessionID string
}

// NewLoopRunner creates a LoopRunner.
func NewLoopRunner(brain Brain, store *persistence.Store, b *bus.Bus, logger *slog.Logger, cfg config.LoopConfig, agentID, sessionID string) *LoopRunner {
	if logger == nil {
		logger = slog.Default()
	}
	return &LoopRunner{
		brain:     brain,
		store:     store,
		bus:       b,
		logger:    logger,
		config:    cfg,
		agentID:   agentID,
		sessionID: sessionID,
	}
}

// Run executes the loop until termination, budget exhaustion, or error.
func (lr *LoopRunner) Run(ctx context.Context, taskID string) (*LoopResult, error) {
	// Check for existing checkpoint (crash recovery)
	var cp *persistence.LoopCheckpoint
	if lr.store != nil {
		var err error
		cp, err = lr.store.LoadLoopCheckpoint(taskID)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("load checkpoint: %w", err)
		}
	}

	maxSteps := lr.config.MaxSteps
	if maxSteps == 0 {
		maxSteps = 25 // default
	}
	maxTokens := lr.config.MaxTokens
	if maxTokens == 0 {
		maxTokens = 100000 // default
	}
	maxDur := parseDuration(lr.config.MaxDuration)
	checkpointInterval := lr.config.CheckpointInterval
	if checkpointInterval == 0 {
		checkpointInterval = 1 // default: every step
	}
	termKeyword := lr.config.TerminationKeyword
	if termKeyword == "" {
		termKeyword = "TASK_COMPLETE"
	}

	var loopID string
	var currentStep, tokensUsed int
	var startedAt time.Time
	var lastResponse string

	if cp != nil {
		// Resume from checkpoint
		loopID = cp.LoopID
		currentStep = cp.CurrentStep
		tokensUsed = cp.TokensUsed
		startedAt = cp.StartedAt
		lr.logger.Info("resuming loop from checkpoint", "loop_id", loopID, "step", currentStep)
	} else {
		loopID = uuid.NewString()
		startedAt = time.Now()
	}

	if lr.bus != nil {
		lr.bus.Publish(EventLoopStarted, LoopEventData{
			LoopID:   loopID,
			TaskID:   taskID,
			AgentID:  lr.agentID,
			Step:     currentStep,
			MaxSteps: maxSteps,
		})
	}

	// Create timeout context
	deadline := startedAt.Add(maxDur)
	loopCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	for {
		// Budget check: steps
		if currentStep >= maxSteps {
			status := LoopStatusBudget
			lr.saveCheckpoint(loopID, taskID, currentStep, maxSteps, tokensUsed, maxTokens, startedAt, maxDur, status)
			if lr.bus != nil {
				lr.bus.Publish(EventLoopBudget, LoopEventData{LoopID: loopID, TaskID: taskID, AgentID: lr.agentID, Step: currentStep, Status: status})
			}
			return &LoopResult{Status: status, Steps: currentStep, TokensUsed: tokensUsed, Response: lastResponse, Error: fmt.Errorf("max steps reached: %d", maxSteps)}, nil
		}
		// Budget check: tokens
		if maxTokens > 0 && tokensUsed >= maxTokens {
			status := LoopStatusBudget
			lr.saveCheckpoint(loopID, taskID, currentStep, maxSteps, tokensUsed, maxTokens, startedAt, maxDur, status)
			if lr.bus != nil {
				lr.bus.Publish(EventLoopBudget, LoopEventData{LoopID: loopID, TaskID: taskID, AgentID: lr.agentID, Step: currentStep, Status: status})
			}
			return &LoopResult{Status: status, Steps: currentStep, TokensUsed: tokensUsed, Response: lastResponse, Error: fmt.Errorf("token budget exhausted: %d/%d", tokensUsed, maxTokens)}, nil
		}

		// Timeout check
		select {
		case <-loopCtx.Done():
			status := LoopStatusTimeout
			lr.saveCheckpoint(loopID, taskID, currentStep, maxSteps, tokensUsed, maxTokens, startedAt, maxDur, status)
			if lr.bus != nil {
				lr.bus.Publish(EventLoopTimeout, LoopEventData{LoopID: loopID, TaskID: taskID, AgentID: lr.agentID, Step: currentStep, Status: status})
			}
			return &LoopResult{Status: status, Steps: currentStep, TokensUsed: tokensUsed, Response: lastResponse, Error: loopCtx.Err()}, nil
		default:
		}

		currentStep++

		// Publish step event
		if lr.bus != nil {
			lr.bus.Publish(EventLoopStep, LoopEventData{LoopID: loopID, TaskID: taskID, AgentID: lr.agentID, Step: currentStep})
		}

		// Build prompt with loop context suffix
		loopSuffix := fmt.Sprintf("\n\n[Loop step %d/%d | tokens ~%d/%d | Include \"%s\" when done]",
			currentStep, maxSteps, tokensUsed, maxTokens, termKeyword)

		// LLM call via Brain.Stream (publishes stream.token events for progressive output)
		var replyBuf strings.Builder
		prompt := "Continue working on the task." + loopSuffix
		streamErr := lr.brain.Stream(loopCtx, lr.sessionID, prompt, func(chunk string) error {
			replyBuf.WriteString(chunk)
			if lr.bus != nil {
				lr.bus.Publish("stream.token", map[string]string{
					"task_id":  taskID,
					"agent_id": lr.agentID,
					"chunk":    chunk,
				})
			}
			return nil
		})
		if streamErr != nil {
			status := LoopStatusFailed
			lr.saveCheckpoint(loopID, taskID, currentStep, maxSteps, tokensUsed, maxTokens, startedAt, maxDur, status)
			if lr.bus != nil {
				lr.bus.Publish(EventLoopFailed, LoopEventData{LoopID: loopID, TaskID: taskID, AgentID: lr.agentID, Step: currentStep, Status: status})
			}
			return &LoopResult{Status: status, Steps: currentStep, TokensUsed: tokensUsed, Response: lastResponse, Error: streamErr}, nil
		}
		reply := replyBuf.String()

		// Estimate tokens used (rough approximation: ~4 chars per token)
		tokensUsed += len(reply) / 4
		lastResponse = reply

		// Check for termination keyword
		if strings.Contains(reply, termKeyword) {
			status := LoopStatusCompleted
			lr.saveCheckpoint(loopID, taskID, currentStep, maxSteps, tokensUsed, maxTokens, startedAt, maxDur, status)
			if lr.bus != nil {
				lr.bus.Publish(EventLoopCompleted, LoopEventData{LoopID: loopID, TaskID: taskID, AgentID: lr.agentID, Step: currentStep, Status: status})
			}
			return &LoopResult{Status: status, Steps: currentStep, TokensUsed: tokensUsed, Response: reply}, nil
		}

		// Checkpoint at configured interval
		if currentStep%checkpointInterval == 0 {
			lr.saveCheckpoint(loopID, taskID, currentStep, maxSteps, tokensUsed, maxTokens, startedAt, maxDur, LoopStatusRunning)
		}
	}
}

func (lr *LoopRunner) saveCheckpoint(loopID, taskID string, step, maxSteps, tokensUsed, maxTokens int, startedAt time.Time, maxDur time.Duration, status string) {
	if lr.store == nil {
		return
	}
	cp := &persistence.LoopCheckpoint{
		LoopID:      loopID,
		TaskID:      taskID,
		AgentID:     lr.agentID,
		CurrentStep: step,
		MaxSteps:    maxSteps,
		TokensUsed:  tokensUsed,
		MaxTokens:   maxTokens,
		StartedAt:   startedAt,
		MaxDuration: maxDur,
		Status:      status,
		Messages:    "[]", // simplified - full message tracking would be more complex
	}
	if err := lr.store.SaveLoopCheckpoint(cp); err != nil {
		lr.logger.Error("failed to save loop checkpoint", "loop_id", loopID, "step", step, "err", err)
	}
}

func parseDuration(s string) time.Duration {
	if s == "" {
		return 30 * time.Minute // default
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 30 * time.Minute
	}
	return d
}
