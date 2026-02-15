package persistence

import (
	"context"
	"time"
)

// AgentPin represents a pinned file or text snippet for an agent.
type AgentPin struct {
	ID        int64
	AgentID   string
	PinType   string // 'file', 'text'
	Source    string // filepath, URL, or label
	Content   string
	TokenCount int
	Shared    bool
	LastRead  time.Time
	FileMtime string
	CreatedAt time.Time
}

// AddPin adds or updates a pinned file/text.
func (s *Store) AddPin(ctx context.Context, agentID, pinType, source, content string, shared bool) error {
	stmt := `
		INSERT INTO agent_pins (agent_id, pin_type, source, content, token_count, shared, last_read, created_at)
		VALUES (?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
		ON CONFLICT(agent_id, source) DO UPDATE SET
			content = excluded.content,
			token_count = excluded.token_count,
			shared = excluded.shared,
			last_read = datetime('now')
	`
	tokenCount := (len(content) + 3) / 4
	_, err := s.db.ExecContext(ctx, stmt, agentID, pinType, source, content, tokenCount, shared)
	return err
}

// RemovePin deletes a pin.
func (s *Store) RemovePin(ctx context.Context, agentID, source string) error {
	stmt := `DELETE FROM agent_pins WHERE agent_id = ? AND source = ?`
	_, err := s.db.ExecContext(ctx, stmt, agentID, source)
	return err
}

// ListPins returns all pins for an agent.
func (s *Store) ListPins(ctx context.Context, agentID string) ([]AgentPin, error) {
	stmt := `
		SELECT id, agent_id, pin_type, source, content, token_count, shared, last_read, file_mtime, created_at
		FROM agent_pins
		WHERE agent_id = ?
		ORDER BY created_at DESC
	`
	rows, err := s.db.QueryContext(ctx, stmt, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pins []AgentPin
	for rows.Next() {
		var p AgentPin
		var lastReadStr, createdStr, mtimeStr string
		err := rows.Scan(&p.ID, &p.AgentID, &p.PinType, &p.Source, &p.Content, &p.TokenCount, &p.Shared, &lastReadStr, &mtimeStr, &createdStr)
		if err != nil {
			return nil, err
		}
		p.LastRead, _ = time.Parse(timeLayout, lastReadStr)
		p.FileMtime = mtimeStr
		p.CreatedAt, _ = time.Parse(timeLayout, createdStr)
		pins = append(pins, p)
	}
	return pins, rows.Err()
}

// GetPin retrieves a single pin.
func (s *Store) GetPin(ctx context.Context, agentID, source string) (AgentPin, error) {
	stmt := `
		SELECT id, agent_id, pin_type, source, content, token_count, shared, last_read, file_mtime, created_at
		FROM agent_pins
		WHERE agent_id = ? AND source = ?
	`
	var p AgentPin
	var lastReadStr, createdStr, mtimeStr string
	row := s.db.QueryRowContext(ctx, stmt, agentID, source)
	err := row.Scan(&p.ID, &p.AgentID, &p.PinType, &p.Source, &p.Content, &p.TokenCount, &p.Shared, &lastReadStr, &mtimeStr, &createdStr)
	if err != nil {
		return AgentPin{}, err
	}
	p.LastRead, _ = time.Parse(timeLayout, lastReadStr)
	p.FileMtime = mtimeStr
	p.CreatedAt, _ = time.Parse(timeLayout, createdStr)
	return p, nil
}

// UpdatePinContent updates content and mtime for a pin.
func (s *Store) UpdatePinContent(ctx context.Context, agentID, source, content, mtime string) error {
	stmt := `
		UPDATE agent_pins
		SET content = ?, token_count = ?, file_mtime = ?, last_read = datetime('now')
		WHERE agent_id = ? AND source = ?
	`
	tokenCount := (len(content) + 3) / 4
	_, err := s.db.ExecContext(ctx, stmt, content, tokenCount, mtime, agentID, source)
	return err
}

// GetSharedPins returns pins shared with an agent.
func (s *Store) GetSharedPins(ctx context.Context, targetAgentID string) ([]AgentPin, error) {
	stmt := `
		SELECT id, agent_id, pin_type, source, content, token_count, shared, last_read, file_mtime, created_at
		FROM agent_pins
		WHERE shared = 1
		ORDER BY created_at DESC
	`
	rows, err := s.db.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pins []AgentPin
	for rows.Next() {
		var p AgentPin
		var lastReadStr, createdStr, mtimeStr string
		err := rows.Scan(&p.ID, &p.AgentID, &p.PinType, &p.Source, &p.Content, &p.TokenCount, &p.Shared, &lastReadStr, &mtimeStr, &createdStr)
		if err != nil {
			return nil, err
		}
		p.LastRead, _ = time.Parse(timeLayout, lastReadStr)
		p.FileMtime = mtimeStr
		p.CreatedAt, _ = time.Parse(timeLayout, createdStr)
		pins = append(pins, p)
	}
	return pins, rows.Err()
}
