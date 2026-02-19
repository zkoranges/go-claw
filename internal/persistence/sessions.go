package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *Store) EnsureSession(ctx context.Context, sessionID string) error {
	if _, err := uuid.Parse(sessionID); err != nil {
		return fmt.Errorf("invalid session_id: %w", err)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, created_at)
		VALUES (?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO NOTHING;
	`, sessionID)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (s *Store) AddHistory(ctx context.Context, sessionID, agentID, role, content string, tokens int) error {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "system", "user", "assistant", "tool":
	default:
		return fmt.Errorf("invalid role %q", role)
	}
	if agentID == "" {
		agentID = "default"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (session_id, agent_id, role, content, tokens, created_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP);
	`, sessionID, agentID, role, content, tokens)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}
	return nil
}

// ClearSessionMessages deletes all messages for a session+agent pair.
// Used by the OpenAI-compatible API to implement stateless request semantics:
// the client provides full conversation history on each request, so we
// replace the DB state rather than appending duplicates.
func (s *Store) ClearSessionMessages(ctx context.Context, sessionID, agentID string) error {
	if agentID == "" {
		agentID = "default"
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE session_id = ? AND agent_id = ?`, sessionID, agentID)
	if err != nil {
		return fmt.Errorf("clear session messages: %w", err)
	}
	return nil
}

func (s *Store) ListHistory(ctx context.Context, sessionID, agentID string, limit int) ([]HistoryItem, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var rows *sql.Rows
	var err error
	if agentID != "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, session_id, role, content, tokens, created_at
			FROM messages
			WHERE session_id = ? AND agent_id = ? AND archived_at IS NULL
			ORDER BY id ASC
			LIMIT ?;
		`, sessionID, agentID, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, session_id, role, content, tokens, created_at
			FROM messages
			WHERE session_id = ? AND archived_at IS NULL
			ORDER BY id ASC
			LIMIT ?;
		`, sessionID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var out []HistoryItem
	for rows.Next() {
		var item HistoryItem
		if err := rows.Scan(&item.ID, &item.SessionID, &item.Role, &item.Content, &item.Tokens, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		item.Text = item.Content // GC-SPEC-ACP-009: populate alias.
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("message rows: %w", err)
	}
	return out, nil
}

func (s *Store) ArchiveMessages(ctx context.Context, sessionID string, beforeID int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE messages
		SET archived_at = CURRENT_TIMESTAMP
		WHERE session_id = ? AND id <= ? AND archived_at IS NULL;
	`, sessionID, beforeID)
	if err != nil {
		return fmt.Errorf("archive messages: %w", err)
	}
	return nil
}

// LoadRecentMessages returns the last N messages for an agent, oldest first.
// Phase 1: Conversation persistence - loads history for TUI display.
func (s *Store) LoadRecentMessages(ctx context.Context, agentID string, sessionID string, limit int) ([]HistoryItem, error) {
	// Delegate to ListHistory which handles the per-agent filtering.
	return s.ListHistory(ctx, sessionID, agentID, limit)
}

// LoadMessagesSince returns messages after a given timestamp for an agent.
// Phase 1: Conversation persistence - filters by time range.
func (s *Store) LoadMessagesSince(ctx context.Context, agentID string, sessionID string, since time.Time) ([]HistoryItem, error) {
	if agentID == "" {
		agentID = "default"
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, role, content, tokens, created_at
		FROM messages
		WHERE session_id = ? AND agent_id = ? AND created_at > ? AND archived_at IS NULL
		ORDER BY created_at ASC;
	`, sessionID, agentID, since)
	if err != nil {
		return nil, fmt.Errorf("query messages since: %w", err)
	}
	defer rows.Close()

	var out []HistoryItem
	for rows.Next() {
		var item HistoryItem
		if err := rows.Scan(&item.ID, &item.SessionID, &item.Role, &item.Content, &item.Tokens, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		item.Text = item.Content
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("message rows: %w", err)
	}
	return out, nil
}

// CountMessages returns total message count for an agent.
// Phase 1: Conversation persistence - for stats.
func (s *Store) CountMessages(ctx context.Context, agentID string, sessionID string) (int, error) {
	if agentID == "" {
		agentID = "default"
	}
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM messages
		WHERE session_id = ? AND agent_id = ? AND archived_at IS NULL;
	`, sessionID, agentID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count messages: %w", err)
	}
	return count, nil
}

// DeleteAgentMessages removes all messages for an agent. Used for /clear.
// Phase 1: Conversation persistence - clear command.
func (s *Store) DeleteAgentMessages(ctx context.Context, agentID string, sessionID string) error {
	if agentID == "" {
		agentID = "default"
	}
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM messages
		WHERE session_id = ? AND agent_id = ? AND archived_at IS NULL;
	`, sessionID, agentID)
	if err != nil {
		return fmt.Errorf("delete agent messages: %w", err)
	}
	return nil
}
