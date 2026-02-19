package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// --- Agent CRUD (multi-agent support) ---

// CreateAgent persists a new agent record to the agents table.
func (s *Store) CreateAgent(ctx context.Context, rec AgentRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agents (agent_id, display_name, provider, model, soul, worker_count,
			task_timeout_seconds, max_queue_depth, skills_filter, policy_overrides,
			api_key_env, agent_emoji, preferred_search, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
	`, rec.AgentID, rec.DisplayName, rec.Provider, rec.Model, rec.Soul, rec.WorkerCount,
		rec.TaskTimeoutSeconds, rec.MaxQueueDepth, rec.SkillsFilter, rec.PolicyOverrides,
		rec.APIKeyEnv, rec.AgentEmoji, rec.PreferredSearch, rec.Status)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	return nil
}

// GetAgent returns the agent record for the given ID, or nil if not found.
func (s *Store) GetAgent(ctx context.Context, agentID string) (*AgentRecord, error) {
	var rec AgentRecord
	err := s.db.QueryRowContext(ctx, `
		SELECT agent_id, display_name, provider, model, soul, worker_count,
			task_timeout_seconds, max_queue_depth, skills_filter, policy_overrides,
			api_key_env, agent_emoji, preferred_search, status, created_at, updated_at
		FROM agents WHERE agent_id = ?;
	`, agentID).Scan(&rec.AgentID, &rec.DisplayName, &rec.Provider, &rec.Model, &rec.Soul,
		&rec.WorkerCount, &rec.TaskTimeoutSeconds, &rec.MaxQueueDepth, &rec.SkillsFilter,
		&rec.PolicyOverrides, &rec.APIKeyEnv, &rec.AgentEmoji, &rec.PreferredSearch,
		&rec.Status, &rec.CreatedAt, &rec.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get agent: %w", err)
	}
	return &rec, nil
}

// ListAgents returns all agent records ordered by creation time.
func (s *Store) ListAgents(ctx context.Context) ([]AgentRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT agent_id, display_name, provider, model, soul, worker_count,
			task_timeout_seconds, max_queue_depth, skills_filter, policy_overrides,
			api_key_env, agent_emoji, preferred_search, status, created_at, updated_at
		FROM agents ORDER BY created_at ASC;
	`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()
	var out []AgentRecord
	for rows.Next() {
		var rec AgentRecord
		if err := rows.Scan(&rec.AgentID, &rec.DisplayName, &rec.Provider, &rec.Model, &rec.Soul,
			&rec.WorkerCount, &rec.TaskTimeoutSeconds, &rec.MaxQueueDepth, &rec.SkillsFilter,
			&rec.PolicyOverrides, &rec.APIKeyEnv, &rec.AgentEmoji, &rec.PreferredSearch,
			&rec.Status, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list agents: iterate: %w", err)
	}
	return out, nil
}

// UpdateAgentStatus sets the status field for the given agent (e.g. "active", "stopped").
func (s *Store) UpdateAgentStatus(ctx context.Context, agentID, status string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE agents SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE agent_id = ?;
	`, status, agentID)
	if err != nil {
		return fmt.Errorf("update agent status: %w", err)
	}
	n, rowsErr := res.RowsAffected()
	if rowsErr != nil {
		return fmt.Errorf("update agent status: rows affected: %w", rowsErr)
	}
	if n == 0 {
		return fmt.Errorf("agent %q not found", agentID)
	}
	return nil
}

// DeleteAgent removes an agent and its inter-agent messages in a single transaction.
func (s *Store) DeleteAgent(ctx context.Context, agentID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete agent: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `DELETE FROM agents WHERE agent_id = ?;`, agentID)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	n, rowsErr := res.RowsAffected()
	if rowsErr != nil {
		return fmt.Errorf("delete agent: rows affected: %w", rowsErr)
	}
	if n == 0 {
		return fmt.Errorf("agent %q not found", agentID)
	}

	// Clean up inter-agent messages to/from the deleted agent.
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_messages WHERE from_agent = ? OR to_agent = ?;`, agentID, agentID); err != nil {
		return fmt.Errorf("delete agent messages: %w", err)
	}

	// Clean up delegations involving the deleted agent.
	if _, err := tx.ExecContext(ctx, `DELETE FROM delegations WHERE parent_agent = ? OR child_agent = ?;`, agentID, agentID); err != nil {
		return fmt.Errorf("delete agent delegations: %w", err)
	}

	// Cancel orphaned tasks (QUEUED or CLAIMED) belonging to the deleted agent.
	// Running tasks are left to their engine's drain/timeout. Terminal tasks are kept for audit.
	if _, err := tx.ExecContext(ctx, `
		UPDATE tasks SET status = ?, error = 'agent_deleted', last_error_code = ?,
			updated_at = CURRENT_TIMESTAMP, lease_owner = NULL, lease_expires_at = NULL
		WHERE agent_id = ? AND status IN (?, ?);
	`, TaskStatusCanceled, ReasonCanceled, agentID, TaskStatusQueued, TaskStatusClaimed); err != nil {
		return fmt.Errorf("cancel orphaned tasks: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete agent: commit: %w", err)
	}
	return nil
}

// CreateTaskForAgent creates a new task scoped to the specified agent.
func (s *Store) CreateTaskForAgent(ctx context.Context, agentID, sessionID, payload string) (string, error) {
	return s.createTask(ctx, agentID, sessionID, payload)
}

// ClaimNextPendingTaskForAgent claims the highest-priority pending task for the given agent.
func (s *Store) ClaimNextPendingTaskForAgent(ctx context.Context, agentID string) (*Task, error) {
	return s.claimNextPendingTask(ctx, agentID)
}

// QueueDepthForAgent returns the number of queued tasks for a specific agent.
func (s *Store) QueueDepthForAgent(ctx context.Context, agentID string) (int, error) {
	var pending int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM tasks WHERE status=? AND agent_id=?;`, TaskStatusQueued, agentID).Scan(&pending); err != nil {
		return 0, fmt.Errorf("queue depth for agent: %w", err)
	}
	return pending, nil
}

// AgentMessage represents a row in the agent_messages table.
type AgentMessage struct {
	ID        int64     `json:"id"`
	FromAgent string    `json:"from_agent"`
	ToAgent   string    `json:"to_agent"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// SendAgentMessage stores a message from one agent to another.
func (s *Store) SendAgentMessage(ctx context.Context, from, to, content string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_messages (from_agent, to_agent, content) VALUES (?, ?, ?);
	`, from, to, content)
	if err != nil {
		return fmt.Errorf("send agent message: %w", err)
	}
	return nil
}

// ReadAgentMessages returns unread messages for an agent and marks them as read.
// Uses a transaction to prevent duplicate delivery under concurrent access.
func (s *Store) ReadAgentMessages(ctx context.Context, agentID string, limit int) ([]AgentMessage, error) {
	if limit <= 0 {
		limit = 10
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("read agent messages: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, from_agent, to_agent, content, created_at
		FROM agent_messages
		WHERE to_agent = ? AND read_at IS NULL
		ORDER BY created_at ASC
		LIMIT ?;
	`, agentID, limit)
	if err != nil {
		return nil, fmt.Errorf("read agent messages: %w", err)
	}
	defer rows.Close()

	var msgs []AgentMessage
	var idArgs []any
	for rows.Next() {
		var m AgentMessage
		if err := rows.Scan(&m.ID, &m.FromAgent, &m.ToAgent, &m.Content, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan agent message: %w", err)
		}
		msgs = append(msgs, m)
		idArgs = append(idArgs, m.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent messages: %w", err)
	}

	// Mark as read within the same transaction using parameterized query.
	if len(idArgs) > 0 {
		placeholders := strings.Repeat("?,", len(idArgs))
		placeholders = placeholders[:len(placeholders)-1] // trim trailing comma
		query := `UPDATE agent_messages SET read_at = CURRENT_TIMESTAMP WHERE id IN (` + placeholders + `);`
		if _, err := tx.ExecContext(ctx, query, idArgs...); err != nil {
			return nil, fmt.Errorf("mark messages read: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("read agent messages: commit: %w", err)
	}

	return msgs, nil
}

// PeekAgentMessages returns the count of unread messages for an agent.
func (s *Store) PeekAgentMessages(ctx context.Context, agentID string) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM agent_messages WHERE to_agent = ? AND read_at IS NULL;
	`, agentID).Scan(&count); err != nil {
		return 0, fmt.Errorf("peek agent messages: %w", err)
	}
	return count, nil
}

// TotalAgentMessageCount returns the total number of inter-agent messages (GC-SPEC-OBS-004).
func (s *Store) TotalAgentMessageCount(ctx context.Context) (int64, error) {
	var count int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM agent_messages;
	`).Scan(&count); err != nil {
		return 0, fmt.Errorf("total agent message count: %w", err)
	}
	return count, nil
}

// TotalDelegationCount returns the total number of delegate_task invocations recorded in the audit log.
func (s *Store) TotalDelegationCount(ctx context.Context) (int64, error) {
	var count int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM audit_log WHERE action='tools.delegate_task' AND reason='task_delegated';
	`).Scan(&count); err != nil {
		return 0, fmt.Errorf("total delegation count: %w", err)
	}
	return count, nil
}
