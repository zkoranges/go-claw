package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// AgentSummary represents a compressed conversation summary.
type AgentSummary struct {
	ID        int64
	AgentID   string
	Summary   string
	MsgCount  int
	CreatedAt time.Time
}

// SaveSummary stores a conversation summary using the KV store.
// Summaries are stored with key format: "agent_summary:<agentid>" for simplicity.
func (s *Store) SaveSummary(ctx context.Context, agentID, summary string, msgCount int) error {
	key := fmt.Sprintf("agent_summary:%s", agentID)
	data := map[string]interface{}{
		"summary":    summary,
		"msg_count":  msgCount,
		"created_at": time.Now().Format(time.RFC3339),
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal summary: %w", err)
	}
	return s.KVSet(ctx, key, string(jsonData))
}

// LoadLatestSummary retrieves the most recent summary for an agent.
func (s *Store) LoadLatestSummary(ctx context.Context, agentID string) (AgentSummary, error) {
	key := fmt.Sprintf("agent_summary:%s", agentID)
	jsonData, err := s.KVGet(ctx, key)
	if err != nil || jsonData == "" {
		// No summary exists yet
		return AgentSummary{AgentID: agentID}, nil
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		return AgentSummary{}, fmt.Errorf("unmarshal summary: %w", err)
	}

	summary := AgentSummary{
		AgentID: agentID,
		Summary: data["summary"].(string),
	}
	if msgCnt, ok := data["msg_count"].(float64); ok {
		summary.MsgCount = int(msgCnt)
	}
	if createdStr, ok := data["created_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, createdStr); err == nil {
			summary.CreatedAt = t
		}
	}
	return summary, nil
}

// DeleteAgentSummaries removes all summaries for an agent.
func (s *Store) DeleteAgentSummaries(ctx context.Context, agentID string) error {
	key := fmt.Sprintf("agent_summary:%s", agentID)
	// KVSet with empty string effectively "deletes" the KV entry
	return s.KVSet(ctx, key, "")
}
