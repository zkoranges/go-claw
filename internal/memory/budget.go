package memory

import (
	"fmt"
	"strings"
)

// ContextBudget shows the token allocation for the current context window.
type ContextBudget struct {
	ModelLimit   int // model's max context (e.g., 128000)
	OutputBuffer int // reserved for response (e.g., 4096)
	Available    int // ModelLimit - OutputBuffer

	SoulTokens    int // system prompt
	MemoryTokens  int // core memory block
	PinTokens     int // own pinned context
	SharedTokens  int // shared team context
	SummaryTokens int // summary of older messages
	MessageTokens int // recent messages
	TotalUsed     int // sum of above

	Remaining      int // Available - TotalUsed
	MessageCount   int // number of recent messages
	TruncatedCount int // messages summarized away
	PinCount       int // number of pinned items
	SharedPinCount int // number of shared pins
	MemoryCount    int // number of memory items
	SharedMemCount int // number of shared memories
}

// Format returns a human-readable budget display for the user.
func (b *ContextBudget) Format(agentID, modelName string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Context Budget for @%s (%s, %d tokens available)\n", agentID, modelName, b.Available))
	sb.WriteString("─────────────────────────────────────────────\n")

	// Show each context component
	if b.SoulTokens > 0 {
		sb.WriteString(fmt.Sprintf("Soul/System:      %7d tokens\n", b.SoulTokens))
	}

	if b.MemoryCount > 0 {
		sb.WriteString(fmt.Sprintf("Core Memory:      %7d tokens (%d facts)\n", b.MemoryTokens, b.MemoryCount))
	}

	if b.PinCount > 0 {
		sb.WriteString(fmt.Sprintf("Pinned Files:     %7d tokens (%d files)\n", b.PinTokens, b.PinCount))
	}

	if b.SharedMemCount > 0 || b.SharedPinCount > 0 {
		sb.WriteString(fmt.Sprintf("Shared Context:   %7d tokens (%d memories, %d pins from team)\n",
			b.SharedTokens, b.SharedMemCount, b.SharedPinCount))
	}

	if b.TruncatedCount > 0 {
		sb.WriteString(fmt.Sprintf("Summary:          %7d tokens (%d older messages)\n", b.SummaryTokens, b.TruncatedCount))
	}

	if b.MessageCount > 0 {
		sb.WriteString(fmt.Sprintf("Messages:         %7d tokens (%d recent)\n", b.MessageTokens, b.MessageCount))
	}

	sb.WriteString("─────────────────────────────────────────────\n")
	sb.WriteString(fmt.Sprintf("Total Used:       %7d / %d available\n", b.TotalUsed, b.Available))
	sb.WriteString(fmt.Sprintf("Remaining:        %7d tokens (%.0f%%)\n", b.Remaining, float64(b.Remaining)/float64(b.Available)*100))

	return sb.String()
}

// Percentage returns the percentage of available context used.
func (b *ContextBudget) Percentage() float64 {
	if b.Available == 0 {
		return 0
	}
	return float64(b.TotalUsed) / float64(b.Available) * 100
}

// IsLow returns true if remaining space is less than 10% of available.
func (b *ContextBudget) IsLow() bool {
	return b.Remaining < (b.Available / 10)
}
