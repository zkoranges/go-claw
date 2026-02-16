package coordinator

import (
	"context"
	"fmt"
	"strings"
)

// RetryWithError re-runs a failed plan step with error context.
// The agent receives the previous error and can attempt to fix the approach.
func RetryWithError(ctx context.Context, taskRouter ChatTaskRouter, sessionID string,
	step PlanStep, previousError string, attempt int) (string, error) {

	// Build retry prompt that includes the error
	retryPrompt := buildRetryPrompt(step.Prompt, previousError, attempt)

	// Execute step again with retry prompt
	taskID, err := taskRouter.CreateChatTask(ctx, step.AgentID, sessionID, retryPrompt)
	if err != nil {
		return "", fmt.Errorf("failed to create retry task: %w", err)
	}

	return taskID, nil
}

// buildRetryPrompt constructs a new prompt that includes error context.
func buildRetryPrompt(originalPrompt, errorMsg string, attempt int) string {
	var sb strings.Builder

	sb.WriteString("Your previous attempt at this task failed.\n\n")
	sb.WriteString(fmt.Sprintf("Original task: %s\n\n", originalPrompt))
	sb.WriteString(fmt.Sprintf("Error from attempt %d:\n%s\n\n", attempt-1, errorMsg))
	sb.WriteString("Please analyze the error, adjust your approach, and try again.\n")
	sb.WriteString("Be explicit about what you're changing and why.")

	return sb.String()
}

// StepRetryEvent is published when a plan step is retried.
type StepRetryEvent struct {
	ExecutionID string
	StepID      string
	Attempt     int
	Error       string
}
