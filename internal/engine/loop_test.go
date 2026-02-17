package engine

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/config"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/google/uuid"
)

// loopMockBrain implements Brain for loop tests.
type loopMockBrain struct {
	responses []string
	callCount int
}

func (m *loopMockBrain) Respond(ctx context.Context, sessionID, content string) (string, error) {
	if m.callCount < len(m.responses) {
		r := m.responses[m.callCount]
		m.callCount++
		return r, nil
	}
	return "no more responses", nil
}

func (m *loopMockBrain) Stream(ctx context.Context, sessionID, content string, onChunk func(content string) error) error {
	r, err := m.Respond(ctx, sessionID, content)
	if err != nil {
		return err
	}
	return onChunk(r)
}

func openLoopTestStore(t *testing.T) *persistence.Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "loop_test.db")
	store, err := persistence.Open(dbPath, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestLoopRunner_RunToCompletion(t *testing.T) {
	store := openLoopTestStore(t)
	b := bus.New()
	brain := &loopMockBrain{
		responses: []string{"Working on it... TASK_COMPLETE"},
	}

	cfg := config.LoopConfig{
		Enabled:            true,
		MaxSteps:           10,
		MaxTokens:          100000,
		MaxDuration:        "5m",
		CheckpointInterval: 1,
		TerminationKeyword: "TASK_COMPLETE",
	}

	sessionID := uuid.NewString()
	taskID := uuid.NewString()
	runner := NewLoopRunner(brain, store, b, nil, cfg, "test-agent", sessionID)

	result, err := runner.Run(context.Background(), taskID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != LoopStatusCompleted {
		t.Fatalf("expected status %q, got %q", LoopStatusCompleted, result.Status)
	}
	if result.Steps != 1 {
		t.Fatalf("expected 1 step, got %d", result.Steps)
	}
	if !strings.Contains(result.Response, "TASK_COMPLETE") {
		t.Fatalf("expected response to contain TASK_COMPLETE, got %q", result.Response)
	}
}

func TestLoopRunner_MaxStepsBudget(t *testing.T) {
	store := openLoopTestStore(t)
	brain := &loopMockBrain{
		responses: []string{
			"step 1", "step 2", "step 3", "step 4", "step 5",
		},
	}

	cfg := config.LoopConfig{
		Enabled:            true,
		MaxSteps:           3,
		MaxTokens:          100000,
		MaxDuration:        "5m",
		CheckpointInterval: 1,
		TerminationKeyword: "TASK_COMPLETE",
	}

	sessionID := uuid.NewString()
	taskID := uuid.NewString()
	runner := NewLoopRunner(brain, store, nil, nil, cfg, "test-agent", sessionID)

	result, err := runner.Run(context.Background(), taskID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != LoopStatusBudget {
		t.Fatalf("expected status %q, got %q", LoopStatusBudget, result.Status)
	}
	if result.Steps != 3 {
		t.Fatalf("expected 3 steps, got %d", result.Steps)
	}
	if result.Error == nil || !strings.Contains(result.Error.Error(), "max steps") {
		t.Fatalf("expected max steps error, got %v", result.Error)
	}
}

func TestLoopRunner_MaxTokensBudget(t *testing.T) {
	store := openLoopTestStore(t)
	// Each response is ~100 chars, so ~25 tokens. With max_tokens=20, should hit budget after step 1.
	brain := &loopMockBrain{
		responses: []string{
			strings.Repeat("x", 100), // 100 chars = ~25 tokens
			strings.Repeat("x", 100),
		},
	}

	cfg := config.LoopConfig{
		Enabled:            true,
		MaxSteps:           100,
		MaxTokens:          20, // very low budget
		MaxDuration:        "5m",
		CheckpointInterval: 1,
		TerminationKeyword: "TASK_COMPLETE",
	}

	sessionID := uuid.NewString()
	taskID := uuid.NewString()
	runner := NewLoopRunner(brain, store, nil, nil, cfg, "test-agent", sessionID)

	result, err := runner.Run(context.Background(), taskID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != LoopStatusBudget {
		t.Fatalf("expected status %q, got %q", LoopStatusBudget, result.Status)
	}
	if result.Error == nil || !strings.Contains(result.Error.Error(), "token budget") {
		t.Fatalf("expected token budget error, got %v", result.Error)
	}
}

func TestLoopRunner_TimeoutEnforced(t *testing.T) {
	store := openLoopTestStore(t)
	// Brain that is slow to respond
	brain := &loopMockBrain{}
	brain.responses = []string{"working..."}

	cfg := config.LoopConfig{
		Enabled:            true,
		MaxSteps:           100,
		MaxTokens:          100000,
		MaxDuration:        "1ms", // extremely short duration
		CheckpointInterval: 1,
		TerminationKeyword: "TASK_COMPLETE",
	}

	sessionID := uuid.NewString()
	taskID := uuid.NewString()
	runner := NewLoopRunner(brain, store, nil, nil, cfg, "test-agent", sessionID)

	// Sleep briefly to ensure the deadline has passed
	time.Sleep(5 * time.Millisecond)

	result, err := runner.Run(context.Background(), taskID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != LoopStatusTimeout {
		t.Fatalf("expected status %q, got %q", LoopStatusTimeout, result.Status)
	}
}

func TestLoopRunner_CheckpointSaved(t *testing.T) {
	store := openLoopTestStore(t)
	brain := &loopMockBrain{
		responses: []string{
			"step 1 response",
			"step 2 response",
			"step 3 TASK_COMPLETE",
		},
	}

	cfg := config.LoopConfig{
		Enabled:            true,
		MaxSteps:           10,
		MaxTokens:          100000,
		MaxDuration:        "5m",
		CheckpointInterval: 1, // checkpoint every step
		TerminationKeyword: "TASK_COMPLETE",
	}

	sessionID := uuid.NewString()
	taskID := uuid.NewString()
	runner := NewLoopRunner(brain, store, nil, nil, cfg, "test-agent", sessionID)

	result, err := runner.Run(context.Background(), taskID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != LoopStatusCompleted {
		t.Fatalf("expected status %q, got %q", LoopStatusCompleted, result.Status)
	}
	if result.Steps != 3 {
		t.Fatalf("expected 3 steps, got %d", result.Steps)
	}

	// The final checkpoint should be in "completed" status, so LoadLoopCheckpoint
	// (which only returns "running" status) should return ErrNoRows.
	_, loadErr := store.LoadLoopCheckpoint(taskID)
	if loadErr != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows for completed loop, got %v", loadErr)
	}
}

func TestLoopRunner_CrashRecovery(t *testing.T) {
	store := openLoopTestStore(t)
	taskID := uuid.NewString()
	loopID := uuid.NewString()

	// Save a checkpoint simulating a crash at step 2
	cp := &persistence.LoopCheckpoint{
		LoopID:      loopID,
		TaskID:      taskID,
		AgentID:     "test-agent",
		CurrentStep: 2,
		MaxSteps:    10,
		TokensUsed:  50,
		MaxTokens:   100000,
		StartedAt:   time.Now(),
		MaxDuration: 5 * time.Minute,
		Status:      "running",
		Messages:    "[]",
	}
	if err := store.SaveLoopCheckpoint(cp); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	// Create new runner that should resume from checkpoint
	brain := &loopMockBrain{
		responses: []string{"TASK_COMPLETE done"},
	}

	cfg := config.LoopConfig{
		Enabled:            true,
		MaxSteps:           10,
		MaxTokens:          100000,
		MaxDuration:        "5m",
		CheckpointInterval: 1,
		TerminationKeyword: "TASK_COMPLETE",
	}

	sessionID := uuid.NewString()
	runner := NewLoopRunner(brain, store, nil, nil, cfg, "test-agent", sessionID)

	result, err := runner.Run(context.Background(), taskID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != LoopStatusCompleted {
		t.Fatalf("expected status %q, got %q", LoopStatusCompleted, result.Status)
	}
	// Should have resumed from step 2, then done step 3
	if result.Steps != 3 {
		t.Fatalf("expected 3 steps (resumed from 2), got %d", result.Steps)
	}
	// Tokens should include the 50 from checkpoint plus new ones
	if result.TokensUsed <= 50 {
		t.Fatalf("expected tokens > 50 (resumed), got %d", result.TokensUsed)
	}
}

func TestLoopRunner_TerminationKeyword(t *testing.T) {
	store := openLoopTestStore(t)
	brain := &loopMockBrain{
		responses: []string{
			"still working",
			"more work here",
			"all done TASK_COMPLETE finished",
		},
	}

	cfg := config.LoopConfig{
		Enabled:            true,
		MaxSteps:           100,
		MaxTokens:          100000,
		MaxDuration:        "5m",
		CheckpointInterval: 1,
		TerminationKeyword: "TASK_COMPLETE",
	}

	sessionID := uuid.NewString()
	taskID := uuid.NewString()
	runner := NewLoopRunner(brain, store, nil, nil, cfg, "test-agent", sessionID)

	result, err := runner.Run(context.Background(), taskID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != LoopStatusCompleted {
		t.Fatalf("expected status %q, got %q", LoopStatusCompleted, result.Status)
	}
	if result.Steps != 3 {
		t.Fatalf("expected 3 steps, got %d", result.Steps)
	}
	if !strings.Contains(result.Response, "TASK_COMPLETE") {
		t.Fatalf("expected final response to contain keyword")
	}
}

func TestLoopRunner_BusEventsPublished(t *testing.T) {
	store := openLoopTestStore(t)
	b := bus.New()
	sub := b.Subscribe("loop.")
	defer b.Unsubscribe(sub)

	brain := &loopMockBrain{
		responses: []string{"TASK_COMPLETE"},
	}

	cfg := config.LoopConfig{
		Enabled:            true,
		MaxSteps:           10,
		MaxTokens:          100000,
		MaxDuration:        "5m",
		CheckpointInterval: 1,
		TerminationKeyword: "TASK_COMPLETE",
	}

	sessionID := uuid.NewString()
	taskID := uuid.NewString()
	runner := NewLoopRunner(brain, store, b, nil, cfg, "test-agent", sessionID)

	result, err := runner.Run(context.Background(), taskID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != LoopStatusCompleted {
		t.Fatalf("expected completed, got %q", result.Status)
	}

	// Collect events (non-blocking)
	var events []bus.Event
	for {
		select {
		case ev := <-sub.Ch():
			events = append(events, ev)
		default:
			goto done
		}
	}
done:
	// Should have: loop.started, loop.step, loop.completed
	if len(events) < 3 {
		t.Fatalf("expected at least 3 events, got %d", len(events))
	}

	topics := make([]string, len(events))
	for i, ev := range events {
		topics[i] = ev.Topic
	}

	if topics[0] != EventLoopStarted {
		t.Errorf("expected first event %q, got %q", EventLoopStarted, topics[0])
	}
	if topics[1] != EventLoopStep {
		t.Errorf("expected second event %q, got %q", EventLoopStep, topics[1])
	}
	if topics[2] != EventLoopCompleted {
		t.Errorf("expected third event %q, got %q", EventLoopCompleted, topics[2])
	}
}

func TestLoopRunner_DefaultConfig(t *testing.T) {
	store := openLoopTestStore(t)
	brain := &loopMockBrain{
		responses: []string{"TASK_COMPLETE"},
	}

	// Empty config - all defaults should apply
	cfg := config.LoopConfig{
		Enabled: true,
	}

	sessionID := uuid.NewString()
	taskID := uuid.NewString()
	runner := NewLoopRunner(brain, store, nil, nil, cfg, "test-agent", sessionID)

	result, err := runner.Run(context.Background(), taskID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != LoopStatusCompleted {
		t.Fatalf("expected completed with defaults, got %q", result.Status)
	}

	// Verify the default termination keyword works
	if result.Steps != 1 {
		t.Fatalf("expected 1 step with default keyword, got %d", result.Steps)
	}
}

func TestLoopRunner_CustomTermKeyword(t *testing.T) {
	store := openLoopTestStore(t)
	brain := &loopMockBrain{
		responses: []string{
			"working on it...",
			"CUSTOM_DONE here we go",
		},
	}

	cfg := config.LoopConfig{
		Enabled:            true,
		MaxSteps:           10,
		MaxTokens:          100000,
		MaxDuration:        "5m",
		CheckpointInterval: 1,
		TerminationKeyword: "CUSTOM_DONE",
	}

	sessionID := uuid.NewString()
	taskID := uuid.NewString()
	runner := NewLoopRunner(brain, store, nil, nil, cfg, "test-agent", sessionID)

	result, err := runner.Run(context.Background(), taskID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != LoopStatusCompleted {
		t.Fatalf("expected completed, got %q", result.Status)
	}
	if result.Steps != 2 {
		t.Fatalf("expected 2 steps, got %d", result.Steps)
	}
	if !strings.Contains(result.Response, "CUSTOM_DONE") {
		t.Fatalf("expected response to contain custom keyword")
	}
}

func TestLoopRunner_BrainError(t *testing.T) {
	store := openLoopTestStore(t)
	errorBrain := &MockBrain{
		RespondFunc: func(ctx context.Context, sessionID, content string) (string, error) {
			return "", fmt.Errorf("brain exploded")
		},
	}

	cfg := config.LoopConfig{
		Enabled:            true,
		MaxSteps:           10,
		MaxTokens:          100000,
		MaxDuration:        "5m",
		CheckpointInterval: 1,
		TerminationKeyword: "TASK_COMPLETE",
	}

	sessionID := uuid.NewString()
	taskID := uuid.NewString()
	runner := NewLoopRunner(errorBrain, store, nil, nil, cfg, "test-agent", sessionID)

	result, err := runner.Run(context.Background(), taskID)
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if result.Status != LoopStatusFailed {
		t.Fatalf("expected status %q, got %q", LoopStatusFailed, result.Status)
	}
	if result.Error == nil || !strings.Contains(result.Error.Error(), "brain exploded") {
		t.Fatalf("expected brain error, got %v", result.Error)
	}
}

func TestLoopRunner_NilStore(t *testing.T) {
	// Verify loop works without a store (no persistence, no crash recovery)
	brain := &loopMockBrain{
		responses: []string{"TASK_COMPLETE"},
	}

	cfg := config.LoopConfig{
		Enabled:            true,
		MaxSteps:           10,
		MaxTokens:          100000,
		MaxDuration:        "5m",
		CheckpointInterval: 1,
		TerminationKeyword: "TASK_COMPLETE",
	}

	sessionID := uuid.NewString()
	taskID := uuid.NewString()
	runner := NewLoopRunner(brain, nil, nil, nil, cfg, "test-agent", sessionID)

	result, err := runner.Run(context.Background(), taskID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != LoopStatusCompleted {
		t.Fatalf("expected completed, got %q", result.Status)
	}
}
