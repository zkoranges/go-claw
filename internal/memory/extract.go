package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// RememberFactArgs is the input for the remember_fact tool.
type RememberFactArgs struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// MemoryCreatedEvent is published to the event bus when a memory is created.
// Topic: "memory.created"
type MemoryCreatedEvent struct {
	AgentID string `json:"agent_id"`
	Key     string `json:"key"`
	Value   string `json:"value"`
	Source  string `json:"source"` // "user" or "agent"
}

// RememberFactToolName is the name of the tool agents can call.
const RememberFactToolName = "remember_fact"

// RememberFactToolDefinition returns the schema for the remember_fact tool.
// Format is compatible with OpenAI/Genkit tool specifications.
func RememberFactToolDefinition() map[string]interface{} {
	return map[string]interface{}{
		"name":        RememberFactToolName,
		"description": "Store an important fact or decision for future reference. Use this when you learn something worth remembering about the user, project, or their preferences. Examples: 'project uses Go 1.22', 'user prefers tabs', 'database is PostgreSQL 15'. Do NOT use for trivial or temporary information.",
		"parameters": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"key": map[string]interface{}{
					"type":        "string",
					"description": "Short descriptive key (e.g., 'preferred_language', 'project_db', 'code_style')",
				},
				"value": map[string]interface{}{
					"type":        "string",
					"description": "The fact to remember (e.g., 'Go 1.22', 'PostgreSQL 15', 'prefers tabs over spaces')",
				},
			},
			"required": []string{"key", "value"},
		},
	}
}

// HandleRememberFact processes a remember_fact tool call.
// Returns the result string or an error.
// Publishes MemoryCreatedEvent to the bus for user notification.
type RememberFactHandler struct {
	Store Store
	Bus   Bus
}

// Store interface for persistence operations.
type Store interface {
	SetMemory(ctx context.Context, agentID, key, value, source string) error
}

// Bus interface for event publishing.
type Bus interface {
	Publish(event interface{})
}

// Handle processes the remember_fact tool call.
func (h *RememberFactHandler) Handle(ctx context.Context, agentID string, input json.RawMessage) (string, error) {
	var args RememberFactArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if args.Key == "" || args.Value == "" {
		return "", fmt.Errorf("key and value are required")
	}

	// Store the memory
	if err := h.Store.SetMemory(ctx, agentID, args.Key, args.Value, "agent"); err != nil {
		return "", fmt.Errorf("failed to save memory: %w", err)
	}

	// Publish event for user notification (async, non-blocking)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in async publish", "recover", r)
			}
		}()
		h.Bus.Publish(MemoryCreatedEvent{
			AgentID: agentID,
			Key:     args.Key,
			Value:   args.Value,
			Source:  "agent",
		})
	}()

	return fmt.Sprintf("Remembered: %s = %s", args.Key, args.Value), nil
}
