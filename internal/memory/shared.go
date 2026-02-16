package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/basket/go-claw/internal/persistence"
)

// SharedStore interface for accessing shared knowledge from other agents.
type SharedStore interface {
	GetSharedMemories(ctx context.Context, targetAgentID string) ([]persistence.AgentMemory, error)
	GetSharedPinsForAgent(ctx context.Context, targetAgentID string) ([]persistence.AgentPin, error)
}

// SharedContext loads and formats shared knowledge from team members.
type SharedContext struct {
	store SharedStore
}

// NewSharedContext creates a new shared context formatter.
func NewSharedContext(store SharedStore) *SharedContext {
	return &SharedContext{store: store}
}

// Format returns shared memories and pins as a text block.
// Groups by source agent and includes attribution.
// Returns: formatted text, total token count, error
func (sc *SharedContext) Format(ctx context.Context, agentID string) (string, int, error) {
	// Get shared memories
	sharedMemories, err := sc.store.GetSharedMemories(ctx, agentID)
	if err != nil {
		return "", 0, fmt.Errorf("failed to load shared memories: %w", err)
	}

	// Get shared pins
	sharedPins, err := sc.store.GetSharedPinsForAgent(ctx, agentID)
	if err != nil {
		return "", 0, fmt.Errorf("failed to load shared pins: %w", err)
	}

	// If nothing is shared, return empty
	if len(sharedMemories) == 0 && len(sharedPins) == 0 {
		return "", 0, nil
	}

	// Group by source agent
	type AgentContent struct {
		Memories []persistence.AgentMemory
		Pins     []persistence.AgentPin
	}
	agents := make(map[string]*AgentContent)

	for _, mem := range sharedMemories {
		if agents[mem.AgentID] == nil {
			agents[mem.AgentID] = &AgentContent{}
		}
		agents[mem.AgentID].Memories = append(agents[mem.AgentID].Memories, mem)
	}

	for _, pin := range sharedPins {
		if agents[pin.AgentID] == nil {
			agents[pin.AgentID] = &AgentContent{}
		}
		agents[pin.AgentID].Pins = append(agents[pin.AgentID].Pins, pin)
	}

	// Build formatted output
	var sb strings.Builder
	totalTokens := 0

	sb.WriteString("<shared_knowledge>\n")

	// Sort agents for deterministic output (using a simple approach)
	for sourceAgent := range agents {
		content := agents[sourceAgent]
		sb.WriteString(fmt.Sprintf("From @%s:\n", sourceAgent))

		// Add memories
		for _, mem := range content.Memories {
			sb.WriteString(fmt.Sprintf("  %s: %s\n", mem.Key, mem.Value))
			totalTokens += EstimateTokens(fmt.Sprintf("%s: %s\n", mem.Key, mem.Value))
		}

		// Add pins
		for _, pin := range content.Pins {
			// Show pin label from source
			label := pin.Source
			if pin.PinType == "file" {
				// Extract just the filename
				if idx := strings.LastIndex(label, "/"); idx >= 0 {
					label = label[idx+1:]
				}
			}
			sb.WriteString(fmt.Sprintf("  --- %s ---\n", label))
			sb.WriteString(fmt.Sprintf("  %s\n", pin.Content))
			totalTokens += pin.TokenCount + EstimateTokens(fmt.Sprintf("--- %s ---\n", label))
		}
	}

	sb.WriteString("</shared_knowledge>")

	return sb.String(), totalTokens, nil
}

// EstimateTokens returns an approximate token count using the 4-chars-per-token heuristic.
func (sc *SharedContext) EstimateTokens(text string) int {
	return EstimateTokens(text)
}
