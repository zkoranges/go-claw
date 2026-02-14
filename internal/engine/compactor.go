package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/tokenutil"
)

// Compactor manages conversation history compaction to stay within
// the LLM context window limits.
type Compactor struct {
	store    *persistence.Store
	brain    Brain
	config   CompactorConfig
	provider string
	model    string
}

// CompactorConfig holds configuration for the Compactor.
type CompactorConfig struct {
	ThresholdRatio float64 // Compact when usage > this ratio of limit (default 0.75).
	KeepRecent     int     // Always keep N most recent messages (default 10).
}

// NewCompactor creates a Compactor.
func NewCompactor(store *persistence.Store, brain Brain, provider, model string, cfg CompactorConfig) *Compactor {
	if cfg.ThresholdRatio <= 0 {
		cfg.ThresholdRatio = 0.75
	}
	if cfg.KeepRecent <= 0 {
		cfg.KeepRecent = 10
	}
	return &Compactor{
		store:    store,
		brain:    brain,
		config:   cfg,
		provider: provider,
		model:    model,
	}
}

// CompactIfNeeded checks session token count against model limit.
// If over threshold: summarize oldest messages using LLM, archive originals,
// insert summary as system message.
// Returns the compacted history ready for LLM context.
func (c *Compactor) CompactIfNeeded(ctx context.Context, sessionID string) ([]persistence.HistoryItem, error) {
	// 1. Load all messages for session (limit 1000 to catch overflow).
	// We load "active" messages (archived_at IS NULL).
	items, err := c.store.ListHistory(ctx, sessionID, 1000)
	if err != nil {
		return nil, fmt.Errorf("list history for compaction: %w", err)
	}

	if len(items) == 0 {
		return items, nil
	}

	// 2. Sum tokens.
	totalTokens := 0
	for _, item := range items {
		totalTokens += item.Tokens
	}

	// 3. Look up context limit.
	limit := ContextLimitForModel(c.provider, c.model)
	available := limit - reservedTokens
	if available < 1000 {
		available = 1000 // Sanity floor
	}

	// 4. Check threshold.
	if float64(totalTokens) < float64(available)*c.config.ThresholdRatio {
		return items, nil
	}

	slog.Info("context limit exceeded, compacting",
		"session_id", sessionID,
		"tokens", totalTokens,
		"limit", limit,
		"available", available)

	// 5. Split messages into old (to compact) and recent (to keep).
	// Constraint A: Keep at least KeepRecent messages.
	// Constraint B: Keep enough recent messages that their token sum < available * 0.6.

	keepCount := c.config.KeepRecent
	if keepCount >= len(items) {
		// Can't compact if we must keep everything.
		// Fallback: just return as is (or maybe truncate if CRITICAL).
		// For now, return as is.
		return items, nil
	}

	// Adjust keepCount to fit within safe window (60% of available).
	safeWindow := int(float64(available) * 0.6)
	recentTokens := 0
	splitIdx := len(items) // Default to keeping everything if calculation fails

	// Scan backwards to find split point
	for i := len(items) - 1; i >= 0; i-- {
		recentTokens += items[i].Tokens
		count := len(items) - i

		if count <= keepCount {
			continue // Must keep at least this many
		}

		if recentTokens > safeWindow {
			splitIdx = i + 1 // Previous item was the last one that fit
			break
		}
		splitIdx = i // This item fits
	}

	// Ensure we don't eliminate everything or nothing
	if splitIdx <= 0 {
		splitIdx = 1 // Compact at least something? Or maybe 0 is fine.
	}
	if splitIdx >= len(items) {
		// Nothing to compact
		return items, nil
	}

	oldItems := items[:splitIdx]
	// recentItems := items[splitIdx:] // kept implicitly by not archiving

	// 6. Build summarization prompt.
	var conversation strings.Builder
	for _, item := range oldItems {
		conversation.WriteString(fmt.Sprintf("%s: %s\n", item.Role, item.Content))
	}

	prompt := fmt.Sprintf(`Summarize the following conversation history into a concise summary that preserves:
- Key facts, decisions, and conclusions
- User preferences and constraints mentioned
- Any ongoing tasks or action items
- Important context needed for future turns

Conversation:
%s`, conversation.String())

	// 7. Call brain.Respond.
	// We need to use a clean context or a specific method to avoid infinite recursion
	// if Respond calls CompactIfNeeded.
	// However, Brain implementation of Respond calls CompactIfNeeded.
	// We need a way to bypass compaction for this call, or use a lower-level generate.
	// BUT, we are inside Engine package. We have the Brain interface.
	// The Brain interface doesn't expose "LowLevelGenerate".
	//
	// SOLUTION: Use a separate "summarize" method on Brain?
	// Or rely on the fact that this specific request shouldn't trigger compaction?
	// It MIGHT trigger compaction if we recursively add history.
	// But here we are NOT passing a sessionID to Respond?
	// Wait, Respond takes sessionID. If we pass the SAME sessionID, it will load history,
	// see it's full, and call CompactIfNeeded again -> infinite loop.
	//
	// We must pass a DIFFERENT or EMPTY sessionID to Respond to avoid loading history.
	// If sessionID is empty/dummy, Respond might fail if it relies on session.
	// Looking at Brain.Respond: it loads history via store.ListHistory(ctx, sessionID, 100).
	// If we pass a dummy session ID that has no history, it won't trigger compaction loop.

	summarySessionID := fmt.Sprintf("summary-%s-%d", sessionID, time.Now().UnixNano())

	// We also don't want the summary prompt itself to be saved to the USER's history.
	// Respond saves the user prompt? No, Respond takes "content" and returns response.
	// It doesn't seem to explicitly save "content" to DB in the code I read (it was `addHistory` inside `Stream`... wait).
	// Let's re-read `Respond` in `brain.go`.

	// `Respond` implementation:
	// ...
	// history, err := b.store.ListHistory(ctx, sessionID, 100)
	// ...
	// resp, err := genkit.Generate(...)
	// return resp.Text(), nil
	//
	// It does NOT seem to save the user message or the assistant response to DB!
	// Wait, `Stream` implementation DOES:
	// "Save final response to history if we accumulated chunks"
	//
	// Typically the CALLER of Respond/Stream (e.g. the API handler or CLI loop) saves the user message.
	// And `Respond` implementation in `brain.go` provided earlier does NOT save the response.
	// This is slightly inconsistent with `Stream` which DOES save.
	//
	// Assuming `Respond` doesn't save side-effects, calling it with a dummy ID is safe.

	summary, err := c.brain.Respond(ctx, summarySessionID, prompt)

	// 8. Fallback to truncation if LLM fails.
	if err != nil {
		slog.Warn("compaction summarization failed, falling back to truncation", "error", err)
		summary = "[History compacted due to length. Older messages were truncated.]"
	}

	// 9. Mark old messages as archived.
	lastOldID := oldItems[len(oldItems)-1].ID
	if err := c.store.ArchiveMessages(ctx, sessionID, lastOldID); err != nil {
		return nil, fmt.Errorf("archive messages: %w", err)
	}

	// 10. Insert summary as a system role message.
	summaryContent := fmt.Sprintf("Previous conversation summary: %s", summary)
	summaryTokens := tokenutil.EstimateTokens(summaryContent)
	if err := c.store.AddHistory(ctx, sessionID, "system", summaryContent, summaryTokens); err != nil {
		return nil, fmt.Errorf("add summary message: %w", err)
	}

	// 11. Return fresh history.
	return c.store.ListHistory(ctx, sessionID, 1000)
}
