package persistence

import (
	"database/sql"
	"fmt"
	"time"
)

// LoopCheckpoint represents persisted state of an agent loop.
type LoopCheckpoint struct {
	LoopID      string        `json:"loop_id"`
	TaskID      string        `json:"task_id"`
	AgentID     string        `json:"agent_id"`
	CurrentStep int           `json:"current_step"`
	MaxSteps    int           `json:"max_steps"`
	TokensUsed  int           `json:"tokens_used"`
	MaxTokens   int           `json:"max_tokens"`
	StartedAt   time.Time     `json:"started_at"`
	MaxDuration time.Duration `json:"-"`
	Status      string        `json:"status"`   // running, completed, budget_exceeded, timeout, failed, cancelled
	Messages    string        `json:"messages"` // JSON array
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

// SaveLoopCheckpoint upserts a loop checkpoint.
func (s *Store) SaveLoopCheckpoint(cp *LoopCheckpoint) error {
	_, err := s.db.Exec(`
		INSERT INTO loop_checkpoints
			(loop_id, task_id, agent_id, current_step, max_steps, tokens_used,
			 max_tokens, started_at, max_duration, status, messages, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(loop_id) DO UPDATE SET
			current_step = excluded.current_step,
			tokens_used = excluded.tokens_used,
			status = excluded.status,
			messages = excluded.messages,
			updated_at = CURRENT_TIMESTAMP`,
		cp.LoopID, cp.TaskID, cp.AgentID,
		cp.CurrentStep, cp.MaxSteps, cp.TokensUsed,
		cp.MaxTokens, cp.StartedAt, cp.MaxDuration.Nanoseconds(),
		cp.Status, cp.Messages,
	)
	return err
}

// LoadLoopCheckpoint loads the latest running checkpoint for a task.
func (s *Store) LoadLoopCheckpoint(taskID string) (*LoopCheckpoint, error) {
	row := s.db.QueryRow(`
		SELECT loop_id, task_id, agent_id, current_step, max_steps,
			   tokens_used, max_tokens, started_at, max_duration, status, messages
		FROM loop_checkpoints
		WHERE task_id = ? AND status = 'running'
		ORDER BY updated_at DESC LIMIT 1`, taskID)

	var cp LoopCheckpoint
	var maxDurationNs int64
	err := row.Scan(
		&cp.LoopID, &cp.TaskID, &cp.AgentID,
		&cp.CurrentStep, &cp.MaxSteps, &cp.TokensUsed,
		&cp.MaxTokens, &cp.StartedAt, &maxDurationNs,
		&cp.Status, &cp.Messages,
	)
	if err == sql.ErrNoRows {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, err
	}
	cp.MaxDuration = time.Duration(maxDurationNs)
	return &cp, nil
}

// CleanupCompletedLoops removes non-running loop checkpoints older than the given duration.
func (s *Store) CleanupCompletedLoops(olderThan time.Duration) (int, error) {
	result, err := s.db.Exec(`
		DELETE FROM loop_checkpoints
		WHERE status != 'running'
		AND updated_at < datetime('now', ?)`,
		fmt.Sprintf("-%d seconds", int(olderThan.Seconds())),
	)
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}
