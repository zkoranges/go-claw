package coordinator

import (
	"context"
	"strings"
	"testing"
)

// mockChatTaskRouter is a test double for ChatTaskRouter.
type mockChatTaskRouter struct {
	calls []struct {
		agentID   string
		sessionID string
		prompt    string
	}
	taskIDCounter int
}

func (m *mockChatTaskRouter) CreateChatTask(ctx context.Context, agentID, sessionID, prompt string) (string, error) {
	m.calls = append(m.calls, struct {
		agentID   string
		sessionID string
		prompt    string
	}{agentID, sessionID, prompt})

	m.taskIDCounter++
	return "task-" + string(rune(m.taskIDCounter)), nil
}

func (m *mockChatTaskRouter) CreateMessageTask(ctx context.Context, agentID, sessionID, prompt string, _ int) (string, error) {
	return m.CreateChatTask(ctx, agentID, sessionID, prompt)
}

func TestRetryWithError(t *testing.T) {
	t.Run("retry_creates_new_task_with_error_context", func(t *testing.T) {
		router := &mockChatTaskRouter{}
		ctx := context.Background()
		step := PlanStep{
			ID:      "build",
			AgentID: "coder",
			Prompt:  "Build the project",
		}

		previousError := "compilation failed: syntax error on line 42"
		attempt := 2

		taskID, err := RetryWithError(ctx, router, "session-123", step, previousError, attempt)
		if err != nil {
			t.Fatalf("RetryWithError failed: %v", err)
		}

		if taskID == "" {
			t.Errorf("expected non-empty task ID")
		}

		// Verify task was created
		if len(router.calls) != 1 {
			t.Errorf("expected 1 task creation, got %d", len(router.calls))
		}

		call := router.calls[0]
		if call.agentID != "coder" {
			t.Errorf("expected coder agent, got %s", call.agentID)
		}
		if call.sessionID != "session-123" {
			t.Errorf("expected session-123, got %s", call.sessionID)
		}

		// Verify prompt includes error context
		if !strings.Contains(call.prompt, "failed") {
			t.Errorf("prompt should mention failure")
		}
		if !strings.Contains(call.prompt, "Build the project") {
			t.Errorf("prompt should include original task")
		}
		if !strings.Contains(call.prompt, "syntax error") {
			t.Errorf("prompt should include error message")
		}
	})

	t.Run("retry_prompt_includes_original_task", func(t *testing.T) {
		router := &mockChatTaskRouter{}
		ctx := context.Background()
		step := PlanStep{
			ID:      "step1",
			AgentID: "agent1",
			Prompt:  "Analyze the data and create a report",
		}

		_, _ = RetryWithError(ctx, router, "sess-1", step, "timeout", 2)

		prompt := router.calls[0].prompt
		if !strings.Contains(prompt, "Analyze the data and create a report") {
			t.Errorf("retry prompt missing original task text")
		}
	})

	t.Run("retry_prompt_includes_error_message", func(t *testing.T) {
		router := &mockChatTaskRouter{}
		ctx := context.Background()
		step := PlanStep{
			ID:      "step1",
			AgentID: "agent1",
			Prompt:  "Do something",
		}

		errorMsg := "Database connection timeout after 30 seconds"
		_, _ = RetryWithError(ctx, router, "sess-1", step, errorMsg, 2)

		prompt := router.calls[0].prompt
		if !strings.Contains(prompt, errorMsg) {
			t.Errorf("retry prompt missing error message")
		}
	})

	t.Run("retry_prompt_includes_attempt_number", func(t *testing.T) {
		router := &mockChatTaskRouter{}
		ctx := context.Background()
		step := PlanStep{
			ID:      "step1",
			AgentID: "agent1",
			Prompt:  "Do something",
		}

		_, _ = RetryWithError(ctx, router, "sess-1", step, "error", 3)

		prompt := router.calls[0].prompt
		if !strings.Contains(prompt, "attempt") {
			t.Errorf("retry prompt should mention attempt number")
		}
	})

	t.Run("retry_preserves_agent_and_session", func(t *testing.T) {
		router := &mockChatTaskRouter{}
		ctx := context.Background()
		step := PlanStep{
			ID:      "process",
			AgentID: "researcher",
			Prompt:  "Research the topic",
		}

		_, _ = RetryWithError(ctx, router, "session-abc", step, "network error", 2)

		call := router.calls[0]
		if call.agentID != "researcher" {
			t.Errorf("expected researcher agent, got %s", call.agentID)
		}
		if call.sessionID != "session-abc" {
			t.Errorf("expected session-abc, got %s", call.sessionID)
		}
	})
}

func TestBuildRetryPrompt(t *testing.T) {
	t.Run("includes_original_prompt", func(t *testing.T) {
		original := "Build the application"
		error := "failed"
		prompt := buildRetryPrompt(original, error, 2)

		if !strings.Contains(prompt, original) {
			t.Errorf("retry prompt missing original prompt")
		}
	})

	t.Run("includes_error_message", func(t *testing.T) {
		original := "Do something"
		error := "out of memory"
		prompt := buildRetryPrompt(original, error, 2)

		if !strings.Contains(prompt, error) {
			t.Errorf("retry prompt missing error message")
		}
	})

	t.Run("includes_attempt_info", func(t *testing.T) {
		prompt := buildRetryPrompt("task", "error", 3)

		if !strings.Contains(prompt, "attempt") {
			t.Errorf("retry prompt should reference attempt")
		}
	})

	t.Run("indicates_failure", func(t *testing.T) {
		prompt := buildRetryPrompt("task", "error", 2)

		if !strings.Contains(prompt, "failed") {
			t.Errorf("retry prompt should indicate failure")
		}
	})

	t.Run("requests_adjustment", func(t *testing.T) {
		prompt := buildRetryPrompt("task", "error", 2)

		if !strings.Contains(prompt, "adjust") && !strings.Contains(prompt, "fix") {
			t.Errorf("retry prompt should request adjustment or fix")
		}
	})
}

func TestStepRetryEvent(t *testing.T) {
	t.Run("event_has_all_fields", func(t *testing.T) {
		event := StepRetryEvent{
			ExecutionID: "exec-123",
			StepID:      "step-1",
			Attempt:     2,
			Error:       "test error",
		}

		if event.ExecutionID != "exec-123" {
			t.Errorf("execution ID not preserved")
		}
		if event.StepID != "step-1" {
			t.Errorf("step ID not preserved")
		}
		if event.Attempt != 2 {
			t.Errorf("attempt not preserved")
		}
		if event.Error != "test error" {
			t.Errorf("error not preserved")
		}
	})
}
