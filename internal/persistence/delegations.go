package persistence

import (
	"context"
	"database/sql"
	"time"
)

// Delegation represents an async inter-agent delegation (PDR v7 Phase 2).
type Delegation struct {
	ID          string
	TaskID      string    // links to tasks table (set when task is created)
	ParentAgent string    // agent that requested delegation
	ChildAgent  string    // agent that executes
	Prompt      string    // what was delegated
	Status      string    // "queued", "running", "completed", "failed"
	Result      string    // output from child agent
	ErrorMsg    string    // error message if failed
	CreatedAt   time.Time
	CompletedAt *time.Time
	Injected    bool      // true once result has been injected into parent's conversation
}

// CreateDelegation stores a new delegation record.
func (s *Store) CreateDelegation(ctx context.Context, d *Delegation) error {
	// TODO: Phase 2 implementation
	return nil
}

// GetDelegation retrieves a delegation by ID.
func (s *Store) GetDelegation(ctx context.Context, id string) (*Delegation, error) {
	// TODO: Phase 2 implementation
	return nil, sql.ErrNoRows
}

// CompleteDelegation updates status to completed and sets result.
func (s *Store) CompleteDelegation(ctx context.Context, id, result string) error {
	// TODO: Phase 2 implementation
	return nil
}

// FailDelegation updates status to failed and sets error message.
func (s *Store) FailDelegation(ctx context.Context, id, errMsg string) error {
	// TODO: Phase 2 implementation
	return nil
}

// PendingDelegationsForAgent returns rows where parent_agent = agentID AND injected = 0
// AND status IN ('completed', 'failed').
func (s *Store) PendingDelegationsForAgent(ctx context.Context, agentID string) ([]*Delegation, error) {
	// TODO: Phase 2 implementation
	return nil, nil
}

// MarkDelegationInjected sets injected = true for a delegation.
func (s *Store) MarkDelegationInjected(ctx context.Context, id string) error {
	// TODO: Phase 2 implementation
	return nil
}

// GetDelegationByTaskID retrieves a delegation linked to a task ID.
func (s *Store) GetDelegationByTaskID(ctx context.Context, taskID string) (*Delegation, error) {
	// TODO: Phase 2 implementation
	return nil, sql.ErrNoRows
}
