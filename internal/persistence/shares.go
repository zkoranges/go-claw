package persistence

import (
	"context"
	"database/sql"
	"time"
)

// AgentShare represents a share grant between agents.
type AgentShare struct {
	ID            int64
	SourceAgentID string
	TargetAgentID string
	ShareType     string // "memory", "pin", "all"
	ItemKey       string // specific key or pin source (empty = all of type)
	CreatedAt     time.Time
}

// AddShare creates a share grant from one agent to another.
func (s *Store) AddShare(ctx context.Context, sourceAgentID, targetAgentID, shareType, itemKey string) error {
	query := `
		INSERT INTO agent_shares (source_agent_id, target_agent_id, share_type, item_key)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(source_agent_id, target_agent_id, share_type, item_key) DO NOTHING
	`
	_, err := s.db.ExecContext(ctx, query, sourceAgentID, targetAgentID, shareType, itemKey)
	return err
}

// RemoveShare revokes a share grant.
func (s *Store) RemoveShare(ctx context.Context, sourceAgentID, targetAgentID, shareType, itemKey string) error {
	query := `
		DELETE FROM agent_shares
		WHERE source_agent_id = ? AND target_agent_id = ? AND share_type = ? AND item_key = ?
	`
	_, err := s.db.ExecContext(ctx, query, sourceAgentID, targetAgentID, shareType, itemKey)
	return err
}

// ListSharesFor returns all shares granted TO a specific agent.
func (s *Store) ListSharesFor(ctx context.Context, targetAgentID string) ([]AgentShare, error) {
	query := `
		SELECT id, source_agent_id, target_agent_id, share_type, item_key, created_at
		FROM agent_shares
		WHERE target_agent_id = ?
		ORDER BY created_at DESC
	`
	rows, err := s.db.QueryContext(ctx, query, targetAgentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var shares []AgentShare
	for rows.Next() {
		var share AgentShare
		var createdAt string
		if err := rows.Scan(&share.ID, &share.SourceAgentID, &share.TargetAgentID, &share.ShareType, &share.ItemKey, &createdAt); err != nil {
			return nil, err
		}
		// Parse timestamp
		if t, err := time.Parse("2006-01-02 15:04:05", createdAt); err == nil {
			share.CreatedAt = t
		}
		shares = append(shares, share)
	}
	return shares, rows.Err()
}

// GetSharedMemories returns memories from other agents that are shared with targetAgentID.
func (s *Store) GetSharedMemories(ctx context.Context, targetAgentID string) ([]AgentMemory, error) {
	query := `
		SELECT m.id, m.agent_id, m.key, m.value, m.source, m.relevance_score, m.access_count,
		       m.created_at, m.updated_at, m.last_accessed
		FROM agent_memories m
		WHERE m.agent_id IN (
			SELECT DISTINCT source_agent_id FROM agent_shares
			WHERE (target_agent_id = ? OR target_agent_id = '*') AND (share_type = 'memory' OR share_type = 'all')
		)
		ORDER BY m.agent_id, m.relevance_score DESC
	`
	rows, err := s.db.QueryContext(ctx, query, targetAgentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []AgentMemory
	for rows.Next() {
		var mem AgentMemory
		var createdAt, updatedAt, lastAccessed string
		if err := rows.Scan(&mem.ID, &mem.AgentID, &mem.Key, &mem.Value, &mem.Source,
			&mem.RelevanceScore, &mem.AccessCount, &createdAt, &updatedAt, &lastAccessed); err != nil {
			return nil, err
		}
		// Parse timestamps
		if t, err := time.Parse("2006-01-02 15:04:05", createdAt); err == nil {
			mem.CreatedAt = t
		}
		if t, err := time.Parse("2006-01-02 15:04:05", updatedAt); err == nil {
			mem.UpdatedAt = t
		}
		if t, err := time.Parse("2006-01-02 15:04:05", lastAccessed); err == nil {
			mem.LastAccessed = t
		}
		memories = append(memories, mem)
	}
	return memories, rows.Err()
}

// GetSharedPinsForAgent returns pins from other agents that are shared with targetAgentID.
func (s *Store) GetSharedPinsForAgent(ctx context.Context, targetAgentID string) ([]AgentPin, error) {
	query := `
		SELECT p.id, p.agent_id, p.pin_type, p.source, p.content, p.token_count,
		       p.shared, p.last_read, p.file_mtime, p.created_at
		FROM agent_pins p
		WHERE p.agent_id IN (
			SELECT DISTINCT source_agent_id FROM agent_shares
			WHERE (target_agent_id = ? OR target_agent_id = '*') AND (share_type = 'pin' OR share_type = 'all')
		)
		ORDER BY p.agent_id, p.created_at DESC
	`
	rows, err := s.db.QueryContext(ctx, query, targetAgentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pins []AgentPin
	for rows.Next() {
		var pin AgentPin
		var createdAt, lastRead string
		if err := rows.Scan(&pin.ID, &pin.AgentID, &pin.PinType, &pin.Source, &pin.Content,
			&pin.TokenCount, &pin.Shared, &lastRead, &pin.FileMtime, &createdAt); err != nil {
			return nil, err
		}
		// Parse timestamps
		if t, err := time.Parse("2006-01-02 15:04:05", createdAt); err == nil {
			pin.CreatedAt = t
		}
		if t, err := time.Parse("2006-01-02 15:04:05", lastRead); err == nil {
			pin.LastRead = t
		}
		pins = append(pins, pin)
	}
	return pins, rows.Err()
}

// GetSharedMemoriesByKey returns specific shared memories accessible to targetAgentID.
func (s *Store) GetSharedMemoriesByKey(ctx context.Context, targetAgentID, key string) ([]AgentMemory, error) {
	query := `
		SELECT m.id, m.agent_id, m.key, m.value, m.source, m.relevance_score, m.access_count,
		       m.created_at, m.updated_at, m.last_accessed
		FROM agent_memories m
		WHERE m.agent_id IN (
			SELECT DISTINCT source_agent_id FROM agent_shares
			WHERE target_agent_id = ? AND (share_type = 'memory' OR share_type = 'all')
		)
		AND m.key = ?
		ORDER BY m.relevance_score DESC
	`
	rows, err := s.db.QueryContext(ctx, query, targetAgentID, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []AgentMemory
	for rows.Next() {
		var mem AgentMemory
		if err := rows.Scan(&mem.ID, &mem.AgentID, &mem.Key, &mem.Value, &mem.Source,
			&mem.RelevanceScore, &mem.AccessCount, &mem.CreatedAt, &mem.UpdatedAt, &mem.LastAccessed); err != nil {
			return nil, err
		}
		memories = append(memories, mem)
	}
	return memories, rows.Err()
}

// IsMemoryShared checks if a specific memory from sourceAgent is shared with targetAgent.
func (s *Store) IsMemoryShared(ctx context.Context, sourceAgentID, targetAgentID, memoryKey string) (bool, error) {
	query := `
		SELECT COUNT(*) FROM agent_shares
		WHERE source_agent_id = ? AND (target_agent_id = ? OR target_agent_id = '*')
		AND (
			(share_type = 'memory' AND item_key = '') OR
			(share_type = 'memory' AND item_key = ?) OR
			share_type = 'all'
		)
	`
	var count int
	err := s.db.QueryRowContext(ctx, query, sourceAgentID, targetAgentID, memoryKey).Scan(&count)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	return count > 0, nil
}

// IsPinShared checks if a specific pin from sourceAgent is shared with targetAgent.
func (s *Store) IsPinShared(ctx context.Context, sourceAgentID, targetAgentID, pinSource string) (bool, error) {
	query := `
		SELECT COUNT(*) FROM agent_shares
		WHERE source_agent_id = ? AND (target_agent_id = ? OR target_agent_id = '*')
		AND (
			(share_type = 'pin' AND item_key = '') OR
			(share_type = 'pin' AND item_key = ?) OR
			share_type = 'all'
		)
	`
	var count int
	err := s.db.QueryRowContext(ctx, query, sourceAgentID, targetAgentID, pinSource).Scan(&count)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	return count > 0, nil
}
