package persistence

import (
	"context"
	"database/sql"
	"time"
)

const timeLayout = "2006-01-02 15:04:05"

// AgentMemory represents a stored fact with relevance scoring.
type AgentMemory struct {
	ID             int64
	AgentID        string
	Key            string
	Value          string
	Source         string // 'user', 'agent', 'system'
	RelevanceScore float64
	AccessCount    int
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastAccessed   time.Time
}

// SetMemory stores or updates a memory (UPSERT). Resets relevance to 1.0 on update.
func (s *Store) SetMemory(ctx context.Context, agentID, key, value, source string) error {
	stmt := `
		INSERT INTO agent_memories (agent_id, key, value, source, relevance_score, access_count, created_at, updated_at, last_accessed)
		VALUES (?, ?, ?, ?, 1.0, 0, datetime('now'), datetime('now'), datetime('now'))
		ON CONFLICT(agent_id, key) DO UPDATE SET
			value = excluded.value,
			source = excluded.source,
			relevance_score = 1.0,
			updated_at = datetime('now'),
			last_accessed = datetime('now')
	`
	_, err := s.db.ExecContext(ctx, stmt, agentID, key, value, source)
	return err
}

// scanMemory helper parses a memory row with proper time parsing.
func scanMemory(row *sql.Row) (AgentMemory, error) {
	var m AgentMemory
	var createdStr, updatedStr, accessedStr string
	err := row.Scan(&m.ID, &m.AgentID, &m.Key, &m.Value, &m.Source, &m.RelevanceScore, &m.AccessCount, &createdStr, &updatedStr, &accessedStr)
	if err != nil {
		return AgentMemory{}, err
	}
	var parseErr error
	m.CreatedAt, parseErr = time.Parse(timeLayout, createdStr)
	if parseErr != nil {
		m.CreatedAt = time.Now()
	}
	m.UpdatedAt, parseErr = time.Parse(timeLayout, updatedStr)
	if parseErr != nil {
		m.UpdatedAt = time.Now()
	}
	m.LastAccessed, parseErr = time.Parse(timeLayout, accessedStr)
	if parseErr != nil {
		m.LastAccessed = time.Now()
	}
	return m, nil
}

// scanMemoryRows helper parses multiple memory rows.
func scanMemoryRows(rows *sql.Rows) ([]AgentMemory, error) {
	var memories []AgentMemory
	for rows.Next() {
		var m AgentMemory
		var createdStr, updatedStr, accessedStr string
		err := rows.Scan(&m.ID, &m.AgentID, &m.Key, &m.Value, &m.Source, &m.RelevanceScore, &m.AccessCount, &createdStr, &updatedStr, &accessedStr)
		if err != nil {
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(timeLayout, createdStr)
		m.UpdatedAt, _ = time.Parse(timeLayout, updatedStr)
		m.LastAccessed, _ = time.Parse(timeLayout, accessedStr)
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

// GetMemory retrieves a single memory by key.
func (s *Store) GetMemory(ctx context.Context, agentID, key string) (AgentMemory, error) {
	stmt := `
		SELECT id, agent_id, key, value, source, relevance_score, access_count, created_at, updated_at, last_accessed
		FROM agent_memories
		WHERE agent_id = ? AND key = ?
	`
	row := s.db.QueryRowContext(ctx, stmt, agentID, key)
	return scanMemory(row)
}

// ListMemories returns all memories for an agent, ordered by relevance DESC, updated_at DESC.
func (s *Store) ListMemories(ctx context.Context, agentID string) ([]AgentMemory, error) {
	stmt := `
		SELECT id, agent_id, key, value, source, relevance_score, access_count, created_at, updated_at, last_accessed
		FROM agent_memories
		WHERE agent_id = ?
		ORDER BY relevance_score DESC, updated_at DESC
	`
	rows, err := s.db.QueryContext(ctx, stmt, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemoryRows(rows)
}

// ListTopMemories returns the top N memories by relevance score.
func (s *Store) ListTopMemories(ctx context.Context, agentID string, limit int) ([]AgentMemory, error) {
	stmt := `
		SELECT id, agent_id, key, value, source, relevance_score, access_count, created_at, updated_at, last_accessed
		FROM agent_memories
		WHERE agent_id = ?
		ORDER BY relevance_score DESC, updated_at DESC
		LIMIT ?
	`
	rows, err := s.db.QueryContext(ctx, stmt, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemoryRows(rows)
}

// DeleteMemory removes a memory by key.
func (s *Store) DeleteMemory(ctx context.Context, agentID, key string) error {
	stmt := `DELETE FROM agent_memories WHERE agent_id = ? AND key = ?`
	_, err := s.db.ExecContext(ctx, stmt, agentID, key)
	return err
}

// SearchMemories finds memories matching a query on key or value, ordered by relevance.
func (s *Store) SearchMemories(ctx context.Context, agentID, query string) ([]AgentMemory, error) {
	stmt := `
		SELECT id, agent_id, key, value, source, relevance_score, access_count, created_at, updated_at, last_accessed
		FROM agent_memories
		WHERE agent_id = ? AND (key LIKE ? OR value LIKE ?)
		ORDER BY relevance_score DESC, updated_at DESC
	`
	likeQuery := "%" + query + "%"
	rows, err := s.db.QueryContext(ctx, stmt, agentID, likeQuery, likeQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemoryRows(rows)
}

// TouchMemory increments access_count, updates last_accessed, and boosts relevance_score slightly.
func (s *Store) TouchMemory(ctx context.Context, agentID, key string) error {
	stmt := `
		UPDATE agent_memories
		SET access_count = access_count + 1,
		    last_accessed = datetime('now'),
		    relevance_score = MIN(1.0, relevance_score + 0.05)
		WHERE agent_id = ? AND key = ?
	`
	_, err := s.db.ExecContext(ctx, stmt, agentID, key)
	return err
}

// DecayMemories multiplies all relevance_scores by factor (e.g., 0.95 for 5% decay per session).
func (s *Store) DecayMemories(ctx context.Context, agentID string, factor float64) error {
	stmt := `
		UPDATE agent_memories
		SET relevance_score = relevance_score * ?
		WHERE agent_id = ?
	`
	_, err := s.db.ExecContext(ctx, stmt, factor, agentID)
	return err
}

// DeleteAgentMemories removes all memories for an agent.
func (s *Store) DeleteAgentMemories(ctx context.Context, agentID string) error {
	stmt := `DELETE FROM agent_memories WHERE agent_id = ?`
	_, err := s.db.ExecContext(ctx, stmt, agentID)
	return err
}
