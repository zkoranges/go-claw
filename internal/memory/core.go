package memory

import (
	"fmt"
	"sort"
)

// CoreMemoryBlock formats agent memories into a text block for system prompt injection.
// Only includes memories with relevance_score above the minimum threshold.
type CoreMemoryBlock struct {
	memories []KeyValue
}

// KeyValue represents a single memory fact.
type KeyValue struct {
	Key            string
	Value          string
	RelevanceScore float64
}

// NewCoreMemoryBlock creates a CoreMemoryBlock from a list of memories.
// Filters memories below the relevance threshold (0.1).
func NewCoreMemoryBlock(memories []KeyValue) *CoreMemoryBlock {
	var filtered []KeyValue
	for _, m := range memories {
		if m.RelevanceScore >= 0.1 {
			filtered = append(filtered, m)
		}
	}
	// Sort by relevance score DESC
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].RelevanceScore > filtered[j].RelevanceScore
	})
	return &CoreMemoryBlock{memories: filtered}
}

// Format returns the memory block as text for injection into the system prompt.
// Empty block returns empty string (no tag markers if no memories).
// Example output:
//
//	<core_memory>
//	user_language: Go
//	project: go-claw
//	user_preference: prefers concise responses
//	</core_memory>
func (b *CoreMemoryBlock) Format() string {
	if len(b.memories) == 0 {
		return ""
	}

	result := "<core_memory>\n"
	for _, m := range b.memories {
		result += fmt.Sprintf("%s: %s\n", m.Key, m.Value)
	}
	result += "</core_memory>"
	return result
}

// EstimateTokens returns the approximate token count for the formatted block.
// Uses the ~4 characters per token heuristic.
func (b *CoreMemoryBlock) EstimateTokens() int {
	formatted := b.Format()
	return EstimateTokens(formatted)
}
