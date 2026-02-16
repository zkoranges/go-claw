package persistence

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Delegation represents an async inter-agent delegation (PDR v7 Phase 2).
type Delegation struct {
	ID          string
	TaskID      string    // links to tasks table (set when task is created)
	ParentAgent string    // agent that requested delegation
	ChildAgent  string    // agent that executes
	Prompt      string    // what was delegated
	Status      string    // "queued", "running", "completed", "failed"
	Result      *string   // output from child agent (nil until completed)
	ErrorMsg    *string   // error message if failed (nil until failed)
	CreatedAt   time.Time
	CompletedAt *time.Time
	Injected    bool      // true once result has been injected into parent's conversation
}

// CreateDelegation stores a new delegation record.
func (s *Store) CreateDelegation(ctx context.Context, d *Delegation) error {
	if d.ID == "" {
		d.ID = uuid.New().String()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO delegations (id, task_id, parent_agent, child_agent, prompt, status, created_at, injected)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.TaskID, d.ParentAgent, d.ChildAgent, d.Prompt, d.Status, d.CreatedAt, 0)
	return err
}

// GetDelegation retrieves a delegation by ID.
func (s *Store) GetDelegation(ctx context.Context, id string) (*Delegation, error) {
	d := &Delegation{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, parent_agent, child_agent, prompt, status, result, error_msg, created_at, completed_at, injected
		FROM delegations WHERE id = ?`, id).
		Scan(&d.ID, &d.TaskID, &d.ParentAgent, &d.ChildAgent, &d.Prompt, &d.Status, &d.Result, &d.ErrorMsg, &d.CreatedAt, &d.CompletedAt, &d.Injected)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// CompleteDelegation updates status to completed and sets result.
func (s *Store) CompleteDelegation(ctx context.Context, id, result string) error {
	now := time.Now()
	_, err := s.db.ExecContext(ctx, `
		UPDATE delegations SET status = 'completed', result = ?, completed_at = ? WHERE id = ?`,
		result, now, id)
	return err
}

// FailDelegation updates status to failed and sets error message.
func (s *Store) FailDelegation(ctx context.Context, id, errMsg string) error {
	now := time.Now()
	_, err := s.db.ExecContext(ctx, `
		UPDATE delegations SET status = 'failed', error_msg = ?, completed_at = ? WHERE id = ?`,
		errMsg, now, id)
	return err
}

// PendingDelegationsForAgent returns rows where parent_agent = agentID AND injected = 0
// AND status IN ('completed', 'failed').
func (s *Store) PendingDelegationsForAgent(ctx context.Context, agentID string) ([]*Delegation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, parent_agent, child_agent, prompt, status, result, error_msg, created_at, completed_at, injected
		FROM delegations
		WHERE parent_agent = ? AND injected = 0 AND status IN ('completed', 'failed')
		ORDER BY created_at ASC`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var delegations []*Delegation
	for rows.Next() {
		d := &Delegation{}
		if err := rows.Scan(&d.ID, &d.TaskID, &d.ParentAgent, &d.ChildAgent, &d.Prompt, &d.Status, &d.Result, &d.ErrorMsg, &d.CreatedAt, &d.CompletedAt, &d.Injected); err != nil {
			return nil, err
		}
		delegations = append(delegations, d)
	}
	return delegations, rows.Err()
}

// MarkDelegationInjected sets injected = true for a delegation.
func (s *Store) MarkDelegationInjected(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE delegations SET injected = 1 WHERE id = ?`, id)
	return err
}

// GetDelegationByTaskID retrieves a delegation linked to a task ID.
func (s *Store) GetDelegationByTaskID(ctx context.Context, taskID string) (*Delegation, error) {
	d := &Delegation{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, parent_agent, child_agent, prompt, status, result, error_msg, created_at, completed_at, injected
		FROM delegations WHERE task_id = ? LIMIT 1`, taskID).
		Scan(&d.ID, &d.TaskID, &d.ParentAgent, &d.ChildAgent, &d.Prompt, &d.Status, &d.Result, &d.ErrorMsg, &d.CreatedAt, &d.CompletedAt, &d.Injected)
	if err != nil {
		return nil, err
	}
	return d, nil
}
