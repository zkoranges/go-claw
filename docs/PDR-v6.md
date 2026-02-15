# PDR v6: Context & Memory (v0.3)

**Status**: READY FOR IMPLEMENTATION
**Version**: 6.2 (revised)
**Date**: 2026-02-15
**Depends on**: PDR v5 complete (v0.2 shipped)
**Target**: go-claw v0.3-dev
**Phases**: 8 sequential phases, each with hard verification gates

---

## Why This Version Matters

v0.1 built the engine. v0.2 built the car. v0.3 gives it a brain.

Right now, every agent conversation starts from zero. @coder doesn't remember the bug you fixed yesterday. @researcher can't recall the papers it found last week. The D&D game master forgets the party's inventory every turn. Agents are model aliases with system prompts — anyone can do that with shell aliases.

After this PDR, agents accumulate knowledge. Conversations persist across sessions. Agents learn facts about you and your projects. You pin files to an agent's context and they stay current when files change. Teams share knowledge. Failed plan steps get retried with error context. *This is the moat.* No other terminal-native multi-agent tool has persistent, per-agent memory backed by SQLite.

**What ships at the end:**
- Conversation history persisted per agent, loaded on session start
- Sliding window with automatic summarization for long conversations
- Per-agent memory store with relevance scoring and decay
- `/memory list`, `/memory search`, `/memory delete` commands
- Auto-memory extraction: agent learns facts via tool calls, user always notified
- Context pinning: `/pin <file>` with file watcher for live updates
- Shared team knowledge: cross-agent memory and pin access scoped by project
- Executor error-as-input: failed plan steps retry with error context
- `/context` command shows token budget breakdown
- Config hot-reload (pulled agents available without restart)
- Version bumped to v0.3-dev

---

## Architecture: The Memory Hierarchy

Inspired by Letta/MemGPT's tiered model, adapted for GoClaw's local-first SQLite architecture:

```
┌─────────────────────────────────────────────┐
│              CONTEXT WINDOW                  │
│  (what the LLM actually sees per request)    │
│                                              │
│  ┌─────────────┐  ┌──────────────────────┐  │
│  │ System Prompt│  │ Agent Soul           │  │
│  │ (static)     │  │ (from config)        │  │
│  └─────────────┘  └──────────────────────┘  │
│  ┌──────────────────────────────────────┐    │
│  │ Core Memory Block                     │    │
│  │ (key facts, ranked by relevance)      │    │
│  │ "User prefers Go. Project is go-claw."│    │
│  └──────────────────────────────────────┘    │
│  ┌──────────────────────────────────────┐    │
│  │ Pinned Context (live-reloaded)        │    │
│  │ [brain.go] [config.go]               │    │
│  └──────────────────────────────────────┘    │
│  ┌──────────────────────────────────────┐    │
│  │ Shared Team Context (read-only)       │    │
│  │ (pins + memories from team agents)    │    │
│  └──────────────────────────────────────┘    │
│  ┌──────────────────────────────────────┐    │
│  │ Conversation Summary                  │    │
│  │ (compressed older messages)           │    │
│  └──────────────────────────────────────┘    │
│  ┌──────────────────────────────────────┐    │
│  │ Recent Messages                       │    │
│  │ (last N messages, sliding window)     │    │
│  └──────────────────────────────────────┘    │
│  ┌──────────────────────────────────────┐    │
│  │ Tools (auto-memory)                   │    │
│  │ remember_fact(key, value)             │    │
│  └──────────────────────────────────────┘    │
└─────────────────────────────────────────────┘

┌─────────────────────────────────────────────┐
│              SQLite (persistent)              │
│                                              │
│  messages         — full conversation log    │
│  agent_memories   — key-value facts + score  │
│  agent_pins       — pinned file references   │
│  agent_summaries  — compressed history       │
│  agent_shares     — cross-agent access grants│
└─────────────────────────────────────────────┘
```

**Design decisions:**
- **SQLite only.** No vector DB, no embeddings, no external services. Keyword search + relevance scoring is sufficient for v0.3.
- **Per-agent isolation by default.** Each agent has its own messages, memories, and pins. Sharing is opt-in.
- **Shared knowledge opt-in.** Agents on a team can read (not write) each other's memories and pins when explicitly shared.
- **Summarization via LLM.** When conversation exceeds the window, older messages are compressed by the same model. Cached in SQLite.
- **Token budget system.** Each context component has a configurable token budget. Total must fit within model's context window minus output buffer.
- **Auto-memory is transparent.** Agent can call `remember_fact` tool, but user ALWAYS sees a notification. No silent extraction.
- **Relevance decay.** Memories score lower over time if unused. Recently accessed memories stay prominent.
- **File watcher on pins.** Pinned files are re-read when modified on disk. Agents always see current content.

---

## Execution Protocol

Same as PDR v5. Non-negotiable:

1. **Read before writing.** Run every context-gathering command. Codebase is truth, not PDR.
2. **One step at a time.** Complete each step fully before starting the next.
3. **Compilation after every edit.** `go build ./...` after every file change.
4. **Hard gate between phases.** Every gate command must pass.
5. **Commit after each gate.** `git add -A && git commit -m "PDR v6 Phase N: <description>"`
6. **Rollback on catastrophic failure.** `git reset --hard HEAD~1` and retry phase.
7. **Match existing code style.** Read 2-3 files in same package before writing.

---

## Pre-Flight

### Build Verification

```bash
git status
just check        # or: go build ./... && go vet ./...
go test -race ./...
```

All must pass before proceeding.

### Context Gathering

Run ALL of these. Record output. Every phase references these results.

```bash
# === Persistence Layer ===

# Store interface — all methods
grep -B2 -A5 "func.*Store.*)" internal/persistence/store.go | head -80

# Store struct and constructor
grep -B2 -A20 "type Store struct\|func NewStore\|func Open" internal/persistence/store.go | head -40

# Existing tables (migrations)
grep -rn "CREATE TABLE\|CREATE INDEX" internal/persistence/*.go | head -30

# Task/message storage — how messages are currently saved
grep -B5 -A15 "func.*Insert\|func.*Save\|func.*Create.*Task\|func.*Message" internal/persistence/store.go | head -60

# Query methods — how data is retrieved
grep -B5 -A15 "func.*Get\|func.*List\|func.*Find\|func.*Query\|func.*Fetch" internal/persistence/store.go | head -60

# === Memory Package ===

# Does internal/memory/ exist? What's in it?
ls -la internal/memory/*.go 2>/dev/null || echo "No memory package"
grep -rn "func\|type\|interface" internal/memory/*.go 2>/dev/null | head -30

# === Brain / Engine ===

# How the brain builds the prompt/messages array
grep -B5 -A30 "func.*Chat\|func.*Complete\|func.*Send\|func.*buildMessages\|func.*buildPrompt" internal/engine/brain.go | head -80

# System prompt injection — where Soul gets added
grep -B5 -A15 "Soul\|system.*prompt\|SystemPrompt\|role.*system" internal/engine/brain.go | head -40

# What messages look like
grep -B2 -A10 "type Message struct\|type ChatMessage\|role.*user\|role.*assistant" internal/engine/brain.go internal/engine/types.go 2>/dev/null | head -40

# Token counting — does it exist?
grep -rn "token\|Token\|tokenize\|count.*token\|tiktoken" internal/engine/*.go | head -20

# Context window / max tokens config
grep -rn "MaxTokens\|max_tokens\|context.*window\|context.*length\|ContextSize" internal/engine/*.go internal/config/config.go | head -20

# === Tool System ===

# How tools are registered and called
grep -rn "type Tool\|RegisterTool\|ToolCall\|FunctionCall\|tool.*definition\|tools.*schema" internal/engine/*.go | head -20

# Existing tool definitions
grep -rn "func.*Tool\|"function"\|"tool"" internal/engine/*.go | head -20

# Does the engine support tool_use / function_calling?
grep -rn "tool_use\|function_call\|ToolUse\|FunctionCall\|ToolChoice" internal/engine/*.go | head -20

# === TUI / Chat Flow ===

# How user messages get to the brain
grep -B5 -A20 "sendMessage\|submitMessage\|processInput\|handleInput" internal/tui/*.go | head -60

# How responses are displayed
grep -B5 -A15 "appendMessage\|addMessage\|renderMessage\|ChatMsg\|ResponseMsg" internal/tui/*.go | head -40

# Existing /commands
grep -B2 -A10 'case "/\|"/memory\|"/pin\|"/context\|"/remember' internal/tui/*.go | head -30

# === Agent Config ===

# Full agent config entry
grep -B2 -A30 "type AgentConfigEntry struct" internal/config/config.go

# Agent runtime — what fields agents have at runtime
grep -B2 -A20 "type Agent struct" internal/agent/*.go | head -40

# How agents are created/loaded
grep -B5 -A20 "func.*NewAgent\|func.*LoadAgent\|func.*CreateAgent" internal/agent/*.go cmd/goclaw/main.go | head -40

# === Bus Events ===

# Existing event types
grep -rn "type.*Event\|EventType\|const.*Event" internal/bus/*.go | head -20

# === Plan Executor ===

# How plans execute — error handling, step completion
grep -B5 -A30 "func.*Execute\|func.*RunStep\|func.*executeWave\|func.*planStep" internal/coordinator/executor.go | head -60

# Task failure handling
grep -rn "fail\|Fail\|error.*task\|TaskError\|StepError\|retry\|Retry" internal/coordinator/executor.go | head -20

# === Config Hot Reload ===

# Any existing file watcher or reload mechanism
grep -rn "fsnotify\|watcher\|Watch\|reload\|Reload\|HotReload" internal/ --include="*.go" | head -10

# === Existing Tests ===

# Test count by package
go test ./... -count=1 2>&1 | grep "^ok" | wc -l
```

---

# PHASE 1: Conversation Persistence

**Risk**: Medium
**Files**: `internal/persistence/messages.go`, `internal/persistence/messages_test.go`, `internal/persistence/migrations.go` (or equivalent)
**Goal**: Every user message and agent response is saved to SQLite per agent. On session start, recent messages are loaded.

## Step 1.0: Pre-Flight

```bash
# Understand exact Store patterns
cat internal/persistence/store.go | head -100
# Understand existing migration pattern
grep -rn "CREATE TABLE\|migrate\|schema\|version" internal/persistence/*.go | head -20
# Understand how tasks/messages are currently stored
grep -B10 -A30 "func.*Create.*Task\|func.*SaveMessage\|func.*InsertMessage" internal/persistence/store.go | head -80
```

Record the output. ADAPT all code below to match actual patterns.

## Step 1.1: Create Messages Table

Use existing `messages` table (schema v1). Add migration for `metadata` column:

```sql
ALTER TABLE messages ADD COLUMN metadata TEXT DEFAULT '{}';
CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(agent_id, created_at);
```

**ADAPT**: Match existing migration pattern.

```bash
go build ./...
```

## Step 1.2: Add Message Storage Methods

In `internal/persistence/messages.go`:

```go
// SaveMessage persists a chat message for an agent.
// ADAPT: Match existing Store method patterns (context, error handling, tx usage)
func (s *Store) SaveMessage(ctx context.Context, agentID, role, content string, tokenCount int) error {
    // INSERT INTO messages ...
}

// LoadRecentMessages returns the last N messages for an agent, oldest first.
func (s *Store) LoadRecentMessages(ctx context.Context, agentID string, limit int) ([]AgentMessage, error) {
    // SELECT ... FROM messages ORDER BY created_at DESC LIMIT ? then reverse
}

// LoadMessagesSince returns messages after a given timestamp for an agent.
func (s *Store) LoadMessagesSince(ctx context.Context, agentID string, since time.Time) ([]AgentMessage, error) {
    // SELECT ... FROM messages WHERE created_at > ? ORDER BY created_at ASC
}

// CountMessages returns total message count for an agent.
func (s *Store) CountMessages(ctx context.Context, agentID string) (int, error) {
    // SELECT COUNT(*) FROM messages ...
}

// DeleteAgentMessages removes all messages for an agent. Used for /clear.
func (s *Store) DeleteAgentMessages(ctx context.Context, agentID string) error {
    // DELETE FROM messages WHERE agent_id = ?
}

// AgentMessage represents a stored chat message.
type AgentMessage struct {
    ID         int64
    AgentID    string
    Role       string
    Content    string
    TokenCount int
    CreatedAt  time.Time
    SessionID  string
}
```

```bash
go build ./...
```

## Step 1.3: Test Message Storage

In `internal/persistence/messages_test.go`:

```go
// Table-driven tests with t.Run. Minimum 12 subtests:
// - save and load single message
// - save multiple messages, verify order (oldest first)
// - load with limit (save 20, load 10 — get last 10)
// - load for empty agent returns empty slice
// - load for nonexistent agent returns empty slice
// - load messages since timestamp
// - count messages
// - count for empty agent returns 0
// - delete agent messages
// - delete then load returns empty
// - save with empty content (should work — empty message is valid)
// - save with unicode content (emoji, CJK characters)
// - messages are isolated per agent (save to agent A, load from B returns empty)
```

**ADAPT**: Match existing test patterns in the package. Use the same setup/teardown (temp SQLite DB, etc).

```bash
go test ./internal/persistence/ -run "Message" -v -count=1
go build ./...
```

## Step 1.4: Wire Into Chat Flow

**CRITICAL**: This is the hardest step. You need to find where user messages and agent responses flow through the system and add save calls.

```bash
# Find the exact path
grep -rn "sendMessage\|submitMessage\|ChatMsg\|StreamMsg\|appendMessage" internal/tui/*.go | head -20
grep -rn "func.*Chat\|func.*Complete" internal/engine/brain.go | head -10
```

Two save points needed:
1. **User message**: After user submits input, before it reaches the brain
2. **Agent response**: After brain returns response, before it's displayed

**ADAPT**: The save calls must be non-blocking. Use a goroutine or channel if needed — never block the UI waiting for SQLite.

```go
// Example — ADAPT to actual message flow
go func() {
    if err := store.SaveMessage(ctx, agentID, "user", userInput, 0); err != nil {
        log.Printf("failed to save user message: %v", err)  // non-fatal
    }
}()
```

```bash
go build ./...
```

## Step 1.5: Load History on Agent Switch

When user switches to an agent (@@agent, /agents select, startup), load recent messages.

```bash
# Find where agent switching happens
grep -B10 -A20 "SwitchAgent\|switchAgent\|AgentSwitched\|activeAgent" internal/tui/*.go cmd/goclaw/main.go | head -60
```

On switch:
1. Load last N messages (configurable, default 50) via `LoadRecentMessages`
2. Feed them into the TUI's message display so user sees conversation history
3. Feed them into the brain's conversation context for the next API call

**ADAPT**: The TUI likely has a messages slice or viewport. Loaded messages need to be prepended/set in whatever data structure the view uses.

```bash
go build ./...
```

## Step 1.6: Add /clear Command

`/clear` deletes all messages for the current agent and clears the TUI display.

```bash
# Find where commands are dispatched
grep -B5 -A20 'case "/\|handleCommand' internal/tui/*.go | head -40
```

Add `case "/clear":` that calls `store.DeleteAgentMessages` and clears the viewport.

```bash
go build ./...
```

## GATE 1

```bash
# Build + all tests
just check || (go build ./... && go vet ./...)
go test -race -count=1 ./...

# Message table exists
grep -q "agent_messages" internal/persistence/messages.go && echo "PASS" || echo "FAIL"

# CRUD methods exist
for method in SaveMessage LoadRecentMessages LoadMessagesSince CountMessages DeleteAgentMessages; do
    grep -q "func.*$method" internal/persistence/messages.go && echo "PASS: $method" || echo "FAIL: $method"
done

# Test count
TC=$(grep -c "t.Run\|func Test" internal/persistence/messages_test.go)
echo "Message tests: $TC (need ≥12)"
[ "$TC" -ge 12 ] && echo "PASS" || echo "FAIL"

# Tests pass
go test ./internal/persistence/ -run "Message" -v -count=1

# Wired into TUI (SaveMessage called somewhere in tui or engine)
grep -rl "SaveMessage" internal/tui/*.go internal/engine/*.go cmd/goclaw/main.go 2>/dev/null | grep -q . && echo "PASS: wired" || echo "FAIL: not wired"

# /clear command exists
grep -q '"/clear"' internal/tui/*.go && echo "PASS: /clear" || echo "FAIL: /clear"
```

All must pass. Then:

```bash
git add -A && git commit -m "PDR v6 Phase 1: conversation persistence"
```

---

# PHASE 2: Sliding Window & Summarization

**Risk**: Medium-high
**Files**: `internal/memory/window.go`, `internal/memory/window_test.go`, `internal/memory/summarize.go`, `internal/memory/summarize_test.go`, `internal/memory/tokens.go`, `internal/memory/tokens_test.go`, `internal/persistence/summaries.go`, `internal/persistence/summaries_test.go`
**Goal**: When conversation exceeds token budget, older messages are summarized and compressed.

## Step 2.0: Pre-Flight

```bash
# Token counting — does it exist?
grep -rn "token\|Token\|tokenize\|count" internal/engine/*.go | head -20

# Context window sizes per model
grep -rn "MaxTokens\|max_tokens\|ContextSize\|context.*window" internal/engine/*.go internal/config/config.go | head -20

# How brain builds the messages array currently
grep -B10 -A40 "func.*buildMessages\|func.*Chat\|messages.*append" internal/engine/brain.go | head -80

# Existing memory package
ls -la internal/memory/*.go 2>/dev/null && cat internal/memory/*.go 2>/dev/null | head -100 || echo "Empty"
```

## Step 2.1: Token Estimation

Token counting doesn't need to be exact. Use the 4-chars-per-token heuristic (widely used, accurate within 10% for English):

```go
// internal/memory/tokens.go
package memory

// EstimateTokens returns an approximate token count for a string.
// Uses the ~4 characters per token heuristic. Accurate within ~10% for English.
// For exact counts, a tokenizer library would be needed (deferred to v0.4).
func EstimateTokens(text string) int {
    return (len(text) + 3) / 4  // round up
}
```

Test in `internal/memory/tokens_test.go`: 5 cases (empty, short, medium, unicode, known reference).

```bash
go build ./...
go test ./internal/memory/ -run "Token" -v -count=1
```

## Step 2.2: Sliding Window

```go
// internal/memory/window.go
package memory

// WindowConfig controls the sliding window behavior.
type WindowConfig struct {
    MaxMessages     int  // max messages to keep in window (default: 50)
    MaxTokens       int  // max total tokens for messages (default: 8000)
    SummaryBudget   int  // tokens reserved for summary (default: 500)
    ReservedTokens  int  // tokens reserved for system prompt + soul + pins + memories (default: 2000)
}

// DefaultWindowConfig returns sensible defaults.
func DefaultWindowConfig() WindowConfig {
    return WindowConfig{
        MaxMessages:    50,
        MaxTokens:      8000,
        SummaryBudget:  500,
        ReservedTokens: 2000,
    }
}

// WindowResult is what the brain receives to build its API call.
type WindowResult struct {
    Summary         string           // compressed older history (may be empty)
    Messages        []WindowMessage  // recent messages that fit in budget
    TotalTokens     int              // estimated tokens used
    TruncatedCount  int              // messages that were summarized away
}

type WindowMessage struct {
    Role    string
    Content string
    Tokens  int
}

// BuildWindow selects which messages fit in the context window.
// Takes all messages (oldest first), returns the subset that fits
// within the token budget, plus an optional summary of truncated messages.
func BuildWindow(messages []WindowMessage, summary string, cfg WindowConfig) WindowResult {
    // 1. Calculate available budget: MaxTokens - ReservedTokens - SummaryBudget
    // 2. Walk messages from newest to oldest, accumulating tokens
    // 3. Stop when budget exceeded or MaxMessages reached
    // 4. Return: summary + fitting messages (oldest first order restored)
}
```

## Step 2.3: Test Sliding Window

In `internal/memory/window_test.go`. Minimum 10 subtests:
- empty messages → empty result
- all messages fit → no truncation, no summary
- messages exceed MaxMessages → oldest dropped
- messages exceed MaxTokens → oldest dropped
- existing summary passed through when messages truncated
- no summary when all messages fit
- single very large message → fits alone
- all messages too large → empty messages, summary only
- token count accurate in result
- truncated count accurate

```bash
go test ./internal/memory/ -run "Window" -v -count=1
```

## Step 2.4: Summarization

```go
// internal/memory/summarize.go
package memory

import "context"

// Summarizer compresses a list of messages into a brief summary.
type Summarizer interface {
    Summarize(ctx context.Context, messages []WindowMessage) (string, error)
}

// LLMSummarizer uses the chat model to summarize conversations.
// ADAPT: This needs access to the brain/engine to make an LLM call.
type LLMSummarizer struct {
    // ADAPT: whatever interface allows making a one-shot LLM call
}

func (s *LLMSummarizer) Summarize(ctx context.Context, messages []WindowMessage) (string, error) {
    // Build a prompt like:
    // "Summarize this conversation in 2-3 sentences, preserving key facts,
    //  decisions, and action items. Be concise."
    // + format messages as "User: ...
Assistant: ...
"
    // Call the LLM, return the summary string
}

// StaticSummarizer is a simple fallback that truncates without LLM.
// Used when no LLM is available or for testing.
type StaticSummarizer struct{}

func (s *StaticSummarizer) Summarize(ctx context.Context, messages []WindowMessage) (string, error) {
    // Return: "[Summary of N earlier messages]"
    // No actual summarization — just a placeholder.
}
```

Test in `internal/memory/summarize_test.go`: 4 subtests (static summarizer, empty messages, single message, multiple messages).

## Step 2.5: Persist Summaries

Add to persistence layer (file: `internal/persistence/summaries.go`):

```sql
CREATE TABLE IF NOT EXISTS agent_summaries (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_id    TEXT    NOT NULL,
    summary     TEXT    NOT NULL,
    msg_count   INTEGER NOT NULL,  -- how many messages this summarizes
    created_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_agent_summaries_agent ON agent_summaries(agent_id);
```

Methods: `SaveSummary`, `LoadLatestSummary`, `DeleteAgentSummaries`.
Test in `internal/persistence/summaries_test.go`: 5 subtests.

```bash
go build ./...
go test ./internal/persistence/ -run "Summary" -v -count=1
```

## Step 2.6: Wire Window Into Brain

**CRITICAL**: The brain currently builds messages from scratch each turn. It needs to use `BuildWindow` to assemble the context.

```bash
# Find EXACTLY how the brain builds its messages array
grep -B5 -A50 "func.*Chat\|func.*buildMessages\|func.*Complete" internal/engine/brain.go | head -100
```

The brain's chat method needs to:
1. Load recent messages from persistence
2. Load latest summary from persistence
3. Call `BuildWindow(messages, summary, config)` to get the windowed subset
4. Prepend system prompt + soul (core memory + pins come in later phases)
5. Append windowed messages
6. Send to LLM API

**ADAPT**: This likely requires changing the brain's Chat signature or adding a new method. Be careful not to break existing callers. Consider adding an optional `ConversationContext` parameter.

```bash
go build ./...
go test -race -count=1 ./...
```

## Step 2.7: Trigger Summarization

When `BuildWindow` truncates messages AND no summary exists for those messages, trigger summarization in the background:

```go
// After BuildWindow, if truncatedCount > 0 and messages were dropped:
go func() {
    truncatedMsgs := allMessages[:truncatedCount]
    summary, err := summarizer.Summarize(ctx, truncatedMsgs)
    if err != nil {
        log.Printf("summarization failed: %v", err)
        return
    }
    store.SaveSummary(ctx, agentID, summary, len(truncatedMsgs))
}()
```

Summarization is async and non-blocking. If it fails, the system works fine — messages just won't have a summary prefix.

```bash
go build ./...
```

## GATE 2

```bash
just check || (go build ./... && go vet ./...)
go test -race -count=1 ./...

# Token estimation
grep -q "func EstimateTokens" internal/memory/tokens.go && echo "PASS" || echo "FAIL"

# Window
grep -q "func BuildWindow" internal/memory/window.go && echo "PASS" || echo "FAIL"
TC=$(grep -c "t.Run\|func Test" internal/memory/window_test.go)
echo "Window tests: $TC (need ≥10)"
[ "$TC" -ge 10 ] && echo "PASS" || echo "FAIL"

# Summarizer interface
grep -q "type Summarizer interface" internal/memory/summarize.go && echo "PASS" || echo "FAIL"

# Summaries table
grep -q "agent_summaries" internal/persistence/summaries.go && echo "PASS" || echo "FAIL"

# Brain uses window
grep -q "BuildWindow\|buildWindow\|WindowResult" internal/engine/brain.go && echo "PASS: brain wired" || echo "FAIL"

go test ./internal/memory/ -v -count=1
go test ./internal/persistence/ -run "Summary" -v -count=1
```

```bash
git add -A && git commit -m "PDR v6 Phase 2: sliding window and summarization"
```

---

# PHASE 3: Agent Memory with Relevance

**Risk**: Medium
**Files**: `internal/persistence/memories.go`, `internal/persistence/memories_test.go`, `internal/memory/core.go`, `internal/memory/core_test.go`
**Goal**: Agents store and recall key-value facts with relevance scoring. Facts injected into context window automatically, ranked by relevance.

## Step 3.0: Pre-Flight

```bash
# Check if memory table already exists
grep -rn "memories\|memory" internal/persistence/*.go | head -10

# Check how system prompt is assembled in brain
grep -B10 -A30 "Soul\|system.*prompt\|buildSystem\|SystemMessage" internal/engine/brain.go | head -60
```

## Step 3.1: Memories Table

```sql
CREATE TABLE IF NOT EXISTS agent_memories (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_id        TEXT    NOT NULL,
    key             TEXT    NOT NULL,
    value           TEXT    NOT NULL,
    source          TEXT    DEFAULT 'user',  -- 'user' (manual), 'agent' (auto-extracted), 'system'
    relevance_score REAL    DEFAULT 1.0,     -- 1.0 = max relevance, decays over time
    access_count    INTEGER DEFAULT 0,       -- incremented when memory is loaded into context
    created_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    last_accessed   TEXT    NOT NULL DEFAULT (datetime('now')),
    UNIQUE(agent_id, key)  -- one value per key per agent
);
CREATE INDEX IF NOT EXISTS idx_agent_memories_agent ON agent_memories(agent_id);
CREATE INDEX IF NOT EXISTS idx_agent_memories_relevance ON agent_memories(agent_id, relevance_score DESC);
```

Methods:
```go
func (s *Store) SetMemory(ctx context.Context, agentID, key, value, source string) error
    // UPSERT — if key exists, update value, reset relevance to 1.0, update updated_at

func (s *Store) GetMemory(ctx context.Context, agentID, key string) (AgentMemory, error)

func (s *Store) ListMemories(ctx context.Context, agentID string) ([]AgentMemory, error)
    // ORDER BY relevance_score DESC, updated_at DESC

func (s *Store) ListTopMemories(ctx context.Context, agentID string, limit int) ([]AgentMemory, error)
    // Top N by relevance score — this is what gets injected into context

func (s *Store) DeleteMemory(ctx context.Context, agentID, key string) error

func (s *Store) SearchMemories(ctx context.Context, agentID, query string) ([]AgentMemory, error)
    // LIKE '%query%' on key AND value columns, ordered by relevance

func (s *Store) TouchMemory(ctx context.Context, agentID, key string) error
    // Increment access_count, update last_accessed, boost relevance_score slightly

func (s *Store) DecayMemories(ctx context.Context, agentID string, factor float64) error
    // Multiply all relevance_scores by factor (e.g., 0.95)
    // Called periodically (e.g., once per session start)

// AgentMemory represents a stored fact.
type AgentMemory struct {
    ID             int64
    AgentID        string
    Key            string
    Value          string
    Source         string    // 'user', 'agent', 'system'
    RelevanceScore float64
    AccessCount    int
    CreatedAt      time.Time
    UpdatedAt      time.Time
    LastAccessed   time.Time
}
```

```bash
go build ./...
```

## Step 3.2: Test Memories

In `internal/persistence/memories_test.go`. Minimum 14 subtests:
- set and get
- set overwrites existing (upsert), resets relevance to 1.0
- get nonexistent returns error
- list all memories for agent, ordered by relevance
- list empty agent returns empty
- list top N memories respects limit
- delete memory
- delete nonexistent is no-op
- search by key substring
- search by value substring
- search no match returns empty
- isolation (agent A memories not visible to agent B)
- touch increments access_count and updates last_accessed
- decay reduces all scores by factor

```bash
go test ./internal/persistence/ -run "Memory\|Memories" -v -count=1
```

## Step 3.3: Core Memory Block

```go
// internal/memory/core.go
package memory

// CoreMemoryBlock formats agent memories into a text block
// that gets injected into the system prompt.
type CoreMemoryBlock struct {
    memories []KeyValue
}

type KeyValue struct {
    Key            string
    Value          string
    RelevanceScore float64
}

// Format returns the memory block as text for the system prompt.
// Only includes memories above a minimum relevance threshold (0.1).
// Example output:
//   <core_memory>
//   user_language: Go
//   project: go-claw
//   user_preference: prefers concise responses
//   </core_memory>
func (b *CoreMemoryBlock) Format() string { ... }

// EstimateTokens returns the approximate token count.
func (b *CoreMemoryBlock) EstimateTokens() int { ... }
```

Test in `internal/memory/core_test.go`: 6 subtests (empty, single, multiple, formatting, token estimate, relevance threshold filtering).

## Step 3.4: Inject Core Memory Into Context

The brain needs to include the core memory block between the soul and the conversation messages:

```
[System] You are a senior software engineer... (soul)

[System] <core_memory>
user_language: Go
project: go-claw
user_preference: prefers concise responses
</core_memory>

[User] How do I fix this bug?
[Assistant] ...
```

Also: call `DecayMemories` once per session start (factor 0.95). Call `TouchMemory` for each memory loaded into context.

**ADAPT**: Find where the brain builds the system message and append the core memory block.

```bash
go build ./...
go test -race -count=1 ./...
```

## Step 3.5: TUI Commands

Match ROADMAP.md command names:

```
/memory list                  — List all stored facts for current agent (with relevance scores)
/memory search <query>        — Find memories matching query
/memory delete <key>          — Remove a specific memory
/memory clear                 — Remove ALL memories for current agent (with confirmation)
@agent remember <key> <value> — Store a fact (also works as /remember <key> <value>)
@agent forget <key>           — Remove a fact (also works as /forget <key>)
```

Both `@agent remember` and `/remember` should work. The @mention form routes through the agent (so the agent sees the command), while the / form is a direct TUI command that bypasses the agent.

**ADAPT**: Follow existing command dispatch pattern. The `/memory` command needs subcommand parsing.

```bash
go build ./...
```

## GATE 3

```bash
just check || (go build ./... && go vet ./...)
go test -race -count=1 ./...

# Table + methods
grep -q "agent_memories" internal/persistence/memories.go && echo "PASS" || echo "FAIL"
for m in SetMemory GetMemory ListMemories ListTopMemories DeleteMemory SearchMemories TouchMemory DecayMemories; do
    grep -q "func.*$m" internal/persistence/memories.go && echo "PASS: $m" || echo "FAIL: $m"
done

# Relevance score
grep -q "relevance_score\|RelevanceScore" internal/persistence/memories.go && echo "PASS: relevance" || echo "FAIL"

# Test count
TC=$(grep -c "t.Run\|func Test" internal/persistence/memories_test.go)
echo "Memory tests: $TC (need ≥14)"
[ "$TC" -ge 14 ] && echo "PASS" || echo "FAIL"

# Core memory block
grep -q "func.*Format" internal/memory/core.go && echo "PASS" || echo "FAIL"

# Brain injection
grep -q "core.*memory\|CoreMemory\|coreMemory\|memoryBlock" internal/engine/brain.go && echo "PASS: brain wired" || echo "FAIL"

# TUI commands
grep -q '"/memory"' internal/tui/*.go && echo "PASS: /memory" || echo "FAIL"
grep -q '"/remember"' internal/tui/*.go && echo "PASS: /remember" || echo "FAIL"
grep -q '"/forget"' internal/tui/*.go && echo "PASS: /forget" || echo "FAIL"

go test ./internal/persistence/ -run "Memory\|Memories" -v -count=1
go test ./internal/memory/ -run "Core" -v -count=1
```

```bash
git add -A && git commit -m "PDR v6 Phase 3: agent memory with relevance scoring"
```

---

# PHASE 4: Auto-Memory Extraction

**Risk**: Medium-high
**Files**: `internal/memory/extract.go`, `internal/memory/extract_test.go`, engine tool registration
**Goal**: Agent can call `remember_fact(key, value)` as a tool. User always sees when memory is created. No silent extraction.

## Step 4.0: Pre-Flight

```bash
# CRITICAL: How does the tool system work?
grep -B5 -A30 "type Tool\|RegisterTool\|ToolCall\|FunctionCall" internal/engine/*.go | head -80

# How tools are defined
grep -rn ""function"\|"type".*"function"\|tools.*=\|ToolDef" internal/engine/*.go | head -20

# How tool results are returned to the model
grep -B5 -A20 "tool_result\|ToolResult\|function_result\|FunctionResult" internal/engine/*.go | head -40

# Existing tools
ls internal/engine/tools*.go 2>/dev/null || echo "No tool files"
grep -rn "Name.*:\|"name".*:" internal/engine/tools*.go 2>/dev/null | head -20
```

## Step 4.1: Define remember_fact Tool

The agent gets a new tool it can call during conversation:

```go
// internal/memory/extract.go
package memory

// RememberFactTool defines the tool schema for auto-memory.
// ADAPT: Match whatever tool definition format the engine uses.
var RememberFactTool = ToolDefinition{
    Name:        "remember_fact",
    Description: "Store an important fact or decision for future reference. Use this when you learn something worth remembering about the user, project, or their preferences. Examples: 'project uses Go 1.22', 'user prefers tabs', 'database is PostgreSQL 15'. Do NOT use for trivial or temporary information.",
    Parameters: map[string]interface{}{
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
```

**ADAPT**: This must match whatever tool/function schema format the engine currently uses. Read the existing tools carefully.

## Step 4.2: Handle remember_fact Calls

When the engine sees a `remember_fact` tool call:

```go
// In the tool handler — ADAPT to actual handler pattern:
func handleRememberFact(ctx context.Context, agentID string, args RememberFactArgs, store *persistence.Store, bus EventBus) (string, error) {
    if args.Key == "" || args.Value == "" {
        return "", fmt.Errorf("key and value are required")
    }

    err := store.SetMemory(ctx, agentID, args.Key, args.Value, "agent")  // source = "agent"
    if err != nil {
        return "", err
    }

    // CRITICAL: Notify user via bus event — no silent extraction
    bus.Publish(MemoryCreatedEvent{
        AgentID: agentID,
        Key:     args.Key,
        Value:   args.Value,
        Source:  "agent",
    })

    return fmt.Sprintf("Remembered: %s = %s", args.Key, args.Value), nil
}

type RememberFactArgs struct {
    Key   string `json:"key"`
    Value string `json:"value"`
}

// MemoryCreatedEvent is published when any memory is created/updated.
type MemoryCreatedEvent struct {
    AgentID string
    Key     string
    Value   string
    Source  string  // "user" or "agent"
}
```

## Step 4.3: TUI Notification

When `MemoryCreatedEvent` is received by the TUI, display a notification:

```
📝 @coder remembered: preferred_language = Go 1.22
```

This appears in the chat flow (like a system message) so the user always sees it. This is non-negotiable — the roadmap says "No silent extraction — user always sees when memory is created."

**ADAPT**: Use whatever event/notification pattern the TUI already has for system messages or status updates.

```bash
# Find how system/status messages are displayed in TUI
grep -B5 -A15 "SystemMsg\|StatusMsg\|infoMsg\|notification\|system.*message" internal/tui/*.go | head -40
```

## Step 4.4: Register Tool With Engine

```bash
# Find where tools are registered
grep -B5 -A20 "RegisterTool\|tools.*=.*\[\|addTool\|ToolSet" internal/engine/*.go | head -40
```

Add `RememberFactTool` to the tool set that gets sent with every API call. The tool is available to ALL agents by default.

**ADAPT**: Some engines pass tools per-request, some register them globally. Match the existing pattern.

## Step 4.5: Add Memory Instruction to Soul

Append to the system prompt (after soul, before core memory):

```
You have access to a remember_fact tool. Use it to store important facts you learn 
about the user, their project, or their preferences. Only remember significant, 
reusable information — not transient conversation details.
```

This goes in the brain's system prompt assembly, not in individual agent souls.

## Step 4.6: Test Auto-Memory

In `internal/memory/extract_test.go`. Minimum 8 subtests:
- tool definition has correct name and parameters
- handle remember_fact saves to store with source "agent"
- handle remember_fact publishes MemoryCreatedEvent to bus
- duplicate key updates value (upsert)
- empty key rejected with error
- empty value rejected with error
- remembered fact appears in ListMemories
- remembered fact has relevance_score 1.0

```bash
go test ./internal/memory/ -run "Extract\|RememberFact\|AutoMemory" -v -count=1
```

## GATE 4

```bash
just check || (go build ./... && go vet ./...)
go test -race -count=1 ./...

# Tool defined
grep -q "remember_fact" internal/memory/extract.go && echo "PASS: tool defined" || echo "FAIL"

# Handler exists
grep -q "handleRememberFact\|HandleRememberFact\|rememberFact" internal/memory/extract.go && echo "PASS: handler" || echo "FAIL"

# Bus event type
grep -q "MemoryCreatedEvent\|MemoryCreated\|memoryCreated" internal/memory/extract.go && echo "PASS: event" || echo "FAIL"

# Registered with engine
grep -rl "remember_fact\|RememberFact" internal/engine/*.go | grep -v _test && echo "PASS: registered" || echo "FAIL: not registered"

# TUI notification
grep -rl "MemoryCreated\|memoryCreated\|remembered:" internal/tui/*.go && echo "PASS: TUI notif" || echo "FAIL"

# Test count
TC=$(grep -c "t.Run\|func Test" internal/memory/extract_test.go)
echo "Auto-memory tests: $TC (need ≥8)"
[ "$TC" -ge 8 ] && echo "PASS" || echo "FAIL"

go test ./internal/memory/ -run "Extract\|RememberFact\|AutoMemory" -v -count=1
```

```bash
git add -A && git commit -m "PDR v6 Phase 4: auto-memory extraction via tool calls"
```

---

# PHASE 5: Context Pinning with File Watcher

**Risk**: Medium
**Files**: `internal/persistence/pins.go`, `internal/persistence/pins_test.go`, `internal/memory/pins.go`, `internal/memory/pins_test.go`
**Goal**: Users pin files to an agent's context. Pinned content always included in LLM calls. Files re-read automatically when modified on disk.

## Step 5.0: Pre-Flight

```bash
# How does the system currently read files?
grep -rn "os.ReadFile\|ioutil.ReadFile\|ReadFile" internal/ --include="*.go" | head -10

# Home directory / config path
grep -B5 -A10 "func HomeDir" internal/config/config.go
```

## Step 5.1: Pins Table

```sql
CREATE TABLE IF NOT EXISTS agent_pins (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_id    TEXT    NOT NULL,
    pin_type    TEXT    NOT NULL,  -- 'file', 'text', 'url'
    source      TEXT    NOT NULL,  -- filepath, URL, or label
    content     TEXT    NOT NULL,  -- actual content (read at pin time)
    token_count INTEGER DEFAULT 0,
    shared      INTEGER DEFAULT 0,  -- 1 = visible to team members
    last_read   TEXT    NOT NULL DEFAULT (datetime('now')),  -- when file was last read from disk
    file_mtime  TEXT    DEFAULT '',  -- mtime of source file when last read
    created_at  TEXT    NOT NULL DEFAULT (datetime('now')),
    UNIQUE(agent_id, source)
);
CREATE INDEX IF NOT EXISTS idx_agent_pins_agent ON agent_pins(agent_id);
CREATE INDEX IF NOT EXISTS idx_agent_pins_shared ON agent_pins(shared) WHERE shared = 1;
```

Methods: `AddPin`, `UpdatePinContent`, `RemovePin`, `ListPins`, `GetPinsForAgent`, `GetSharedPins`.

```bash
go build ./...
```

## Step 5.2: Pin Manager with File Watcher

```go
// internal/memory/pins.go
package memory

// PinManager handles adding, formatting, and live-reloading pinned context.
type PinManager struct {
    store    PinStore  // ADAPT: interface for persistence calls
    maxSize  int64     // max file size in bytes (default: 50*1024 = 50KB)
    pollSecs int       // file change poll interval (default: 10)
    stop     chan struct{}
}

func NewPinManager(store PinStore) *PinManager { ... }

// AddFilePin reads a file and stores its content as a pin.
func (pm *PinManager) AddFilePin(ctx context.Context, agentID, filepath string, shared bool) error {
    info, err := os.Stat(filepath)
    // Validate: file exists, not too large (limit: 50KB default), is text
    content, err := os.ReadFile(filepath)
    // Store in DB with pin_type="file", record file mtime
}

// AddTextPin stores arbitrary text as a pin.
func (pm *PinManager) AddTextPin(ctx context.Context, agentID, label, content string, shared bool) error { ... }

// StartFileWatcher polls pinned files for changes every N seconds.
// When a file's mtime changes, re-read and update the stored content.
func (pm *PinManager) StartFileWatcher(ctx context.Context) {
    go func() {
        ticker := time.NewTicker(time.Duration(pm.pollSecs) * time.Second)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                pm.refreshChangedFiles(ctx)
            case <-pm.stop:
                return
            }
        }
    }()
}

// refreshChangedFiles checks all file-type pins, re-reads if mtime changed.
func (pm *PinManager) refreshChangedFiles(ctx context.Context) {
    // List all file pins, check mtime, re-read if changed
    // Update stored content + mtime + token_count + last_read
    // Log: "Refreshed pin: brain.go (changed on disk)"
}

func (pm *PinManager) Stop() { close(pm.stop) }

// FormatPins returns all pinned content formatted for the context window.
func (pm *PinManager) FormatPins(ctx context.Context, agentID string) (string, int, error) {
    // Returns: formatted text, total token count, error
    // Format:
    //   <pinned_context>
    //   --- brain.go ---
    //   package engine ...
    //   --- config.go ---
    //   package config ...
    //   </pinned_context>
}
```

## Step 5.3: Test Pins

Minimum 12 subtests across persistence and memory packages:

Persistence (`internal/persistence/pins_test.go`):
- add file pin and retrieve
- add text pin
- remove pin
- list pins for agent
- duplicate pin (same source) → update content
- update pin content and mtime
- shared pin visible via GetSharedPins
- non-shared pin NOT visible via GetSharedPins

Memory (`internal/memory/pins_test.go`):
- add file pin (create temp file, pin it, verify content stored)
- file not found → error
- file too large → error with message
- pin content formatting output correct
- file watcher detects change (create file → pin → modify file → trigger refresh → verify updated content)

```bash
go test ./internal/persistence/ -run "Pin" -v -count=1
go test ./internal/memory/ -run "Pin" -v -count=1
```

## Step 5.4: Wire Pins Into Brain

Pinned content goes between core memory and conversation:

```
[System] Soul + Core Memory
[System] <pinned_context>
--- brain.go ---
...
</pinned_context>
[Summary of older conversation]
[Recent messages]
```

**ADAPT**: Add to the same place where core memory was injected in Phase 3.

## Step 5.5: TUI Commands

Match ROADMAP.md names:

```
/pin <filepath>             — Pin a file to current agent's context (live-reloaded)
/pin <f1> <f2> <dir/>       — Pin multiple files or directory contents
/pin text <label> <content> — Pin arbitrary text
/pin shared <filepath>      — Pin a file, shared with team members
/unpin <source>             — Remove a pin
/pinned                     — List all pinned files for current agent (with token counts)
```

Note: ROADMAP uses `/pinned` for listing, not `/pins`.

## Step 5.6: Token Budget Enforcement

Pins consume context window space. The total budget check:

```go
soulTokens := EstimateTokens(soul)
memoryTokens := coreMemoryBlock.EstimateTokens()
pinTokens := pinFormatted.totalTokens
windowBudget := modelMaxTokens - soulTokens - memoryTokens - pinTokens - outputBuffer

if windowBudget < minWindowTokens {
    return ErrContextBudgetExceeded  // Too many pins — warn user
}

result := BuildWindow(messages, summary, WindowConfig{MaxTokens: windowBudget, ...})
```

```bash
go build ./...
```

## GATE 5

```bash
just check || (go build ./... && go vet ./...)
go test -race -count=1 ./...

# Pins table with file watcher fields
grep -q "agent_pins" internal/persistence/pins.go && echo "PASS" || echo "FAIL"
grep -q "file_mtime\|FileMtime" internal/persistence/pins.go && echo "PASS: mtime tracked" || echo "FAIL"

# Pin manager with watcher
grep -q "func.*AddFilePin" internal/memory/pins.go && echo "PASS" || echo "FAIL"
grep -q "func.*StartFileWatcher\|func.*refreshChangedFiles" internal/memory/pins.go && echo "PASS: file watcher" || echo "FAIL"

# Test count
P_TC=$(grep -c "t.Run\|func Test" internal/persistence/pins_test.go 2>/dev/null || echo 0)
M_TC=$(grep -c "t.Run\|func Test" internal/memory/pins_test.go 2>/dev/null || echo 0)
TOTAL=$((P_TC + M_TC))
echo "Pin tests total: $TOTAL (need ≥12)"
[ "$TOTAL" -ge 12 ] && echo "PASS" || echo "FAIL"

# Brain wired
grep -q "pinned.*context\|FormatPins\|pinContent\|pinnedBlock" internal/engine/brain.go && echo "PASS: brain wired" || echo "FAIL"

# TUI commands
grep -q '"/pin"' internal/tui/*.go && echo "PASS: /pin" || echo "FAIL"
grep -q '"/unpin"' internal/tui/*.go && echo "PASS: /unpin" || echo "FAIL"
grep -q '"/pinned"' internal/tui/*.go && echo "PASS: /pinned" || echo "FAIL"

go test ./internal/memory/ -run "Pin" -v -count=1
go test ./internal/persistence/ -run "Pin" -v -count=1
```

```bash
git add -A && git commit -m "PDR v6 Phase 5: context pinning with file watcher"
```

---

# PHASE 6: Shared Team Knowledge

**Risk**: Medium
**Files**: `internal/persistence/shares.go`, `internal/persistence/shares_test.go`, `internal/memory/shared.go`, `internal/memory/shared_test.go`
**Goal**: Agents on a team can read each other's memories and pins when explicitly shared. Scoped by project/team.

## Step 6.0: Pre-Flight

```bash
# How teams are defined
grep -B5 -A20 "team\|Team\|group\|Group" internal/config/config.go | head -40

# How agents know about each other
grep -rn "AllAgents\|listAgents\|agentIDs\|AgentRegistry" internal/agent/*.go internal/config/config.go | head -20
```

## Step 6.1: Shares Table

```sql
CREATE TABLE IF NOT EXISTS agent_shares (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    source_agent_id TEXT NOT NULL,  -- agent sharing the data
    target_agent_id TEXT NOT NULL,  -- agent receiving access (or '*' for all)
    share_type      TEXT NOT NULL,  -- 'memory', 'pin', 'all'
    item_key        TEXT DEFAULT '', -- specific memory key or pin source (empty = all of type)
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source_agent_id, target_agent_id, share_type, item_key)
);
CREATE INDEX IF NOT EXISTS idx_agent_shares_target ON agent_shares(target_agent_id);
```

Methods:
```go
func (s *Store) AddShare(ctx context.Context, sourceAgentID, targetAgentID, shareType, itemKey string) error
func (s *Store) RemoveShare(ctx context.Context, sourceAgentID, targetAgentID, shareType, itemKey string) error
func (s *Store) ListSharesFor(ctx context.Context, targetAgentID string) ([]AgentShare, error)
func (s *Store) GetSharedMemories(ctx context.Context, targetAgentID string) ([]AgentMemory, error)
    // Returns memories from other agents that are shared with targetAgentID
func (s *Store) GetSharedPinsForAgent(ctx context.Context, targetAgentID string) ([]AgentPin, error)
    // Returns pins from other agents that are shared with targetAgentID
```

## Step 6.2: Test Shares

In `internal/persistence/shares_test.go`. Minimum 10 subtests:
- share memory from agent A to agent B
- agent B can read shared memory via GetSharedMemories
- agent C (not shared with) cannot read it
- share pin from A to B
- share with wildcard '*' — all agents can read
- remove share — access revoked
- share specific key only — only that key visible
- list shares for an agent
- duplicate share is no-op
- shared memories appear with correct source agent attribution

```bash
go test ./internal/persistence/ -run "Share" -v -count=1
```

## Step 6.3: Shared Context Block

```go
// internal/memory/shared.go
package memory

// SharedContext loads and formats shared knowledge from team members.
type SharedContext struct {
    store SharedStore  // ADAPT: interface for share-related persistence calls
}

// Format returns shared memories and pins as a text block.
// Example:
//   <shared_knowledge>
//   From @security:
//     finding-42: SQL injection in /api/users endpoint
//   From @researcher:
//     --- notes.md ---
//     ...
//   </shared_knowledge>
func (sc *SharedContext) Format(ctx context.Context, agentID string) (string, int, error) {
    // 1. Get shared memories for this agent
    // 2. Get shared pins for this agent
    // 3. Group by source agent
    // 4. Format as labeled blocks
    // Returns: formatted text, total token count, error
}
```

Test in `internal/memory/shared_test.go`: 4 subtests (empty, memories only, pins only, mixed from multiple agents).

## Step 6.4: Inject Shared Context Into Brain

Shared context goes after the agent's own pins, before conversation:

```
[System] Soul + Core Memory
[System] <pinned_context> (own pins) </pinned_context>
[System] <shared_knowledge> (team members' shared items) </shared_knowledge>
[Summary]
[Recent messages]
```

**ADAPT**: Add to the brain's prompt assembly, after own pins.

## Step 6.5: TUI Commands

```
/share <key> with <agent>      — Share a specific memory with another agent
/share all with <agent>        — Share all memories with another agent
/share pin <source> with <agent> — Share a pinned file with another agent
/unshare <key> from <agent>    — Revoke sharing
/shared                        — List what's shared with current agent
```

```bash
go build ./...
```

## GATE 6

```bash
just check || (go build ./... && go vet ./...)
go test -race -count=1 ./...

# Shares table
grep -q "agent_shares" internal/persistence/shares.go && echo "PASS" || echo "FAIL"

# Methods
for m in AddShare RemoveShare ListSharesFor GetSharedMemories GetSharedPinsForAgent; do
    grep -q "func.*$m" internal/persistence/shares.go && echo "PASS: $m" || echo "FAIL: $m"
done

# Test count
TC=$(grep -c "t.Run\|func Test" internal/persistence/shares_test.go)
echo "Share tests: $TC (need ≥10)"
[ "$TC" -ge 10 ] && echo "PASS" || echo "FAIL"

# Shared context formatter
grep -q "func.*Format" internal/memory/shared.go && echo "PASS" || echo "FAIL"

# Brain wired
grep -q "shared.*knowledge\|SharedContext\|sharedContext\|sharedBlock" internal/engine/brain.go && echo "PASS: brain wired" || echo "FAIL"

# TUI commands
grep -q '"/share"' internal/tui/*.go && echo "PASS: /share" || echo "FAIL"
grep -q '"/shared"' internal/tui/*.go && echo "PASS: /shared" || echo "FAIL"

go test ./internal/persistence/ -run "Share" -v -count=1
go test ./internal/memory/ -run "Shared" -v -count=1
```

```bash
git add -A && git commit -m "PDR v6 Phase 6: shared team knowledge"
```

---

# PHASE 7: Executor Error-as-Input

**Risk**: Medium
**Files**: `internal/coordinator/executor.go`, `internal/coordinator/retry.go`, `internal/coordinator/retry_test.go`
**Goal**: When a plan step fails, feed error output back to the agent as a new message. Agent attempts to fix and retry. Configurable retry limit.

## Step 7.0: Pre-Flight

```bash
# How does plan execution work?
grep -B5 -A40 "func.*Execute\|func.*RunStep\|func.*executeWave\|func.*planStep" internal/coordinator/executor.go | head -100

# Current error handling in executor
grep -B5 -A15 "fail\|Fail\|error\|Error\|StepResult\|StepStatus" internal/coordinator/executor.go | head -60

# How does a step result get back to the plan?
grep -B5 -A20 "StepResult\|StepComplete\|stepDone\|stepFinished" internal/coordinator/executor.go | head -40

# Existing retry config
grep -rn "retry\|Retry\|MaxRetry\|max_retries\|RetryCount" internal/coordinator/*.go internal/config/config.go | head -10
```

## Step 7.1: Retry Configuration

Add to plan step config:

```go
// ADAPT: find existing PlanStep struct and add:
type PlanStep struct {
    // ... existing fields ...
    MaxRetries int `yaml:"max_retries"` // default: 2
}
```

Also update the plan step YAML format:

```yaml
plans:
  my-plan:
    steps:
      - id: build
        agent: coder
        input: "Build the project"
        max_retries: 2  # default is 2 if omitted
```

## Step 7.2: Error-as-Input Loop

When a plan step fails, instead of immediately marking the plan as failed:

```go
// internal/coordinator/retry.go
package coordinator

// RetryWithError re-runs a failed plan step, injecting the error as context.
func RetryWithError(ctx context.Context, step PlanStep, previousError string, attempt int, brain Brain) (StepResult, error) {
    retryPrompt := fmt.Sprintf(
        "Your previous attempt at this task failed.

Original task: %s

Error from attempt %d:
%s

Please analyze the error, fix your approach, and try again.",
        step.Prompt,
        attempt,
        previousError,
    )
    // Execute the step with retryPrompt instead of step.Prompt
    // ADAPT: use whatever method runs a single plan step
}
```

## Step 7.3: Wire Into Executor

```bash
# Find the step execution loop
grep -B10 -A30 "func.*executeStep\|func.*runStep\|for.*step\|range.*steps" internal/coordinator/executor.go | head -60
```

Modify the executor's step runner:

```go
// Pseudocode — ADAPT to actual executor structure
func executeStep(ctx context.Context, step PlanStep, brain Brain, bus EventBus) StepResult {
    maxRetries := step.MaxRetries
    if maxRetries == 0 {
        maxRetries = 2  // default
    }

    result := runStepOnce(ctx, step, brain)
    attempt := 1

    for result.Status == "failed" && attempt <= maxRetries {
        attempt++
        log.Printf("Step %s failed (attempt %d/%d), retrying with error context",
            step.ID, attempt, maxRetries+1)

        // Publish retry event for activity feed
        bus.Publish(StepRetryEvent{
            StepID:  step.ID,
            Attempt: attempt,
            Error:   result.Error,
        })

        result = RetryWithError(ctx, step, result.Error, attempt, brain)
    }

    return result
}
```

## Step 7.4: Test Error-as-Input

In `internal/coordinator/retry_test.go`. Minimum 8 subtests:
- step succeeds first try — no retry
- step fails then retry succeeds — returns success
- step fails all retries — returns final error
- retry prompt includes original task text
- retry prompt includes error from previous attempt
- max_retries = 0 means no retries (one attempt only)
- max_retries = 1 means one retry (two total attempts)
- StepRetryEvent published to bus on each retry

```bash
go test ./internal/coordinator/ -run "Retry\|ErrorAsInput" -v -count=1
```

## GATE 7

```bash
just check || (go build ./... && go vet ./...)
go test -race -count=1 ./...

# Retry logic exists
grep -q "func.*RetryWithError\|func.*retryWithError" internal/coordinator/retry.go && echo "PASS" || echo "FAIL"

# MaxRetries field
grep -q "MaxRetries\|max_retries" internal/coordinator/*.go && echo "PASS: config" || echo "FAIL"

# Wired into executor (referenced outside retry.go and test files)
grep -rl "RetryWithError\|retryWithError\|retryStep" internal/coordinator/*.go | grep -v retry.go | grep -v _test.go | grep -q . && echo "PASS: wired" || echo "FAIL"

# Bus event
grep -q "StepRetry\|stepRetry\|RetryEvent" internal/coordinator/retry.go && echo "PASS: event" || echo "FAIL"

# Test count
TC=$(grep -c "t.Run\|func Test" internal/coordinator/retry_test.go)
echo "Retry tests: $TC (need ≥8)"
[ "$TC" -ge 8 ] && echo "PASS" || echo "FAIL"

go test ./internal/coordinator/ -run "Retry\|ErrorAsInput" -v -count=1
```

```bash
git add -A && git commit -m "PDR v6 Phase 7: executor error-as-input with retry"
```

---

# PHASE 8: Context Visibility, Hot-Reload, Help, README, Version

**Risk**: Low
**Files**: `internal/memory/budget.go`, `internal/memory/budget_test.go`, `internal/config/watcher.go`, `internal/config/watcher_test.go`, TUI help, README.md, main.go
**Goal**: Polish, documentation, version bump.

## Step 8.1: Context Budget Calculator

```go
// internal/memory/budget.go
package memory

// ContextBudget shows the token allocation for the current context window.
type ContextBudget struct {
    ModelLimit      int    // model's max context (e.g., 128000)
    OutputBuffer    int    // reserved for response (e.g., 4096)
    Available       int    // ModelLimit - OutputBuffer

    SoulTokens      int
    MemoryTokens    int    // core memory block
    PinTokens       int    // own pinned context
    SharedTokens    int    // shared team context
    SummaryTokens   int
    MessageTokens   int    // recent messages
    TotalUsed       int

    Remaining       int    // Available - TotalUsed
    MessageCount    int
    TruncatedCount  int    // messages summarized away
    PinCount        int
    SharedPinCount  int
    MemoryCount     int
    SharedMemCount  int
}

// Format returns a human-readable budget display.
func (b *ContextBudget) Format() string {
    // Context Budget for @coder (gemini-2.5-pro, 1M tokens)
    // ─────────────────────────────────────
    // Soul/System:     850 tokens
    // Core Memory:     120 tokens (3 facts)
    // Pinned Files:  2,400 tokens (2 files)
    // Shared Context:  480 tokens (1 memory, 1 pin from @security)
    // Summary:         380 tokens (45 older msgs)
    // Messages:      3,200 tokens (12 recent)
    // ─────────────────────────────────────
    // Total Used:    7,430 / 123,904 available
    // Remaining:   116,474 tokens (94%)
}
```

Test in `internal/memory/budget_test.go`: 5 subtests.

## Step 8.2: /context Command

```
/context          — Show current context budget breakdown
/context full     — Also show memory keys, pin filenames, shared sources
```

## Step 8.3: Config Hot-Reload

Ensure the existing `internal/config/watcher.go` handles agent registry updates without restart.

1.  Verify `watcher.go` triggers a reload event on `config.yaml` write.
2.  In `main.go`, ensure the reload handler calls `reconcileAgents` correctly (add/remove/update agents based on new config).
3.  Ensure memory-related config (like window size if added) is updated on reload.

Test in `internal/config/watcher_test.go`: 5 subtests:
- detect file change triggers callback
- no change → no callback
- callback receives valid updated Config
- stop prevents further callbacks
- missing file doesn't crash

## Step 8.4: Wire Watcher

Start the watcher on app startup. On change:
1. Re-read config
2. Update agent registry with any new/changed agents
3. Emit bus event: `ConfigReloadedEvent{AgentCount: N}`
4. Log: "Config reloaded: N agents"

## Step 8.5: Help Text Update

Add to `/help`:

```
Memory & Context:
    /memory list               List stored facts for current agent
    /memory search <query>     Search agent memory
    /memory delete <key>       Remove a stored fact
    /memory clear              Clear all memories (with confirmation)
    /remember <key> <value>    Store a fact (shortcut)
    /forget <key>              Remove a fact (shortcut)
    /pin <filepath>            Pin file to agent context (live-reloaded)
    /unpin <source>            Remove pinned file
    /pinned                    List pinned files with token counts
    /share <key> with <agent>  Share memory with team agent
    /shared                    List shared knowledge
    /context                   Show context window budget
    /clear                     Clear conversation history
```

## Step 8.6: README Update

Add "Context & Memory" section to README:
- Conversation history persists across sessions
- `/memory` commands for facts with relevance decay
- Auto-memory: agent learns and you see it happen
- `/pin` for files with live reload when files change
- `/share` for team knowledge across agents
- `/context` to see token budget allocation
- Executor retry on plan step failure

## Step 8.7: Version Bump

Change version to `v0.3-dev` in `cmd/goclaw/main.go`.

## GATE 8

```bash
just check || (go build ./... && go vet ./...)
go test -race -count=1 ./...

# Budget calculator with shared fields
grep -q "type ContextBudget struct" internal/memory/budget.go && echo "PASS" || echo "FAIL"
grep -q "SharedTokens\|SharedPinCount" internal/memory/budget.go && echo "PASS: shared in budget" || echo "FAIL"

# /context command
grep -q '"/context"' internal/tui/*.go && echo "PASS: /context" || echo "FAIL"

# Config watcher
grep -q "type Watcher struct" internal/config/watcher.go && echo "PASS" || echo "FAIL"
TC=$(grep -c "t.Run\|func Test" internal/config/watcher_test.go)
echo "Watcher tests: $TC (need ≥5)"
[ "$TC" -ge 5 ] && echo "PASS" || echo "FAIL"

# Help updated with memory commands
grep -q "/memory\|/pin\|/context\|/share" internal/tui/*.go && echo "PASS: help" || echo "FAIL"

# README
grep -qE "memory|pin|context|share|remember" README.md && echo "PASS: README" || echo "FAIL"

# Version
grep -q "v0.3" cmd/goclaw/main.go && echo "PASS: version" || echo "FAIL"

go test ./internal/config/ -run "Watch" -v -count=1
go test ./internal/memory/ -run "Budget" -v -count=1
```

```bash
git add -A && git commit -m "PDR v6 Phase 8: context visibility, hot-reload, help, README, v0.3-dev"
```

---

# Post-Implementation Verification

```bash
# Full suite
go test -race -count=1 ./...

# Phase-by-phase test verification
echo "=== Phase 1: Conversation Persistence ==="
go test ./internal/persistence/ -run "Message" -v -count=1

echo "=== Phase 2: Sliding Window ==="
go test ./internal/memory/ -run "Token|Window|Summarize" -v -count=1
go test ./internal/persistence/ -run "Summary" -v -count=1

echo "=== Phase 3: Agent Memory ==="
go test ./internal/persistence/ -run "Memory|Memories" -v -count=1
go test ./internal/memory/ -run "Core" -v -count=1

echo "=== Phase 4: Auto-Memory ==="
go test ./internal/memory/ -run "Extract|RememberFact|AutoMemory" -v -count=1

echo "=== Phase 5: Context Pinning ==="
go test ./internal/persistence/ -run "Pin" -v -count=1
go test ./internal/memory/ -run "Pin" -v -count=1

echo "=== Phase 6: Shared Knowledge ==="
go test ./internal/persistence/ -run "Share" -v -count=1
go test ./internal/memory/ -run "Shared" -v -count=1

echo "=== Phase 7: Error-as-Input ==="
go test ./internal/coordinator/ -run "Retry|ErrorAsInput" -v -count=1

echo "=== Phase 8: Polish ==="
go test ./internal/memory/ -run "Budget" -v -count=1
go test ./internal/config/ -run "Watch" -v -count=1

# Functional: first-run with memory
rm -rf /tmp/test-v03
GOCLAW_HOME=/tmp/test-v03 timeout 5s ./dist/goclaw 2>/dev/null || true
ls -la /tmp/test-v03/*.db /tmp/test-v03/*.sqlite 2>/dev/null || echo "Check DB location"
rm -rf /tmp/test-v03

# Version
./dist/goclaw --version 2>&1 | grep -q "v0.3" && echo "PASS" || echo "FAIL"

# Git history
git log --oneline | grep "PDR v6" | head -10

# Total test count
echo "Total test packages passing: $(go test ./... -count=1 2>&1 | grep '^ok' | wc -l)"
```

---

# What This PDR Does NOT Cover (Deferred)

- **Semantic search / embeddings** — v0.4. Keyword search + relevance scoring is sufficient.
- **Cross-agent memory writing** — agents only READ shared memories, not write to each other's. v0.4.
- **MCP client expansion** — v0.4
- **Telegram deep integration** — v0.4
- **True async delegation** — v0.4
- **A2A protocol** — v0.4
- **LLM-generated plans** — v0.5
- **Smart routing** — v0.5
- **Context compaction v2** (extract facts before compacting) — v0.5
- **Web UI** — not planned
- **Conversation branching** — too complex for v0.3
- **Streaming summarization** — summarize while streaming. Deferred.

---

# Rollback Reference

- Single phase: `git reset --hard HEAD~1`
- All v0.3: `git reset --hard <pre-v6-hash>` (record before starting)
- Corrupted DB: `rm ~/.goclaw/*.db` then restart (loses all memory — nuclear option)

---

# Summary of Test Requirements

| Phase | Package | Test File | Min Tests |
|-------|---------|-----------|-----------|
| 1 | persistence | messages_test.go | 12 |
| 2 | memory | tokens_test.go | 5 |
| 2 | memory | window_test.go | 10 |
| 2 | memory | summarize_test.go | 4 |
| 2 | persistence | summaries_test.go | 5 |
| 3 | persistence | memories_test.go | 14 |
| 3 | memory | core_test.go | 6 |
| 4 | memory | extract_test.go | 8 |
| 5 | persistence | pins_test.go | 8 |
| 5 | memory | pins_test.go | 5 |
| 6 | persistence | shares_test.go | 10 |
| 6 | memory | shared_test.go | 4 |
| 7 | coordinator | retry_test.go | 8 |
| 8 | memory | budget_test.go | 5 |
| 8 | config | watcher_test.go | 5 |
| **Total** | | | **≥109 new tests** |

---

# ROADMAP Alignment Checklist

| Roadmap v0.3 Feature | PDR Phase | Status |
|---|---|---|
| Context pinning (`/pin`, `/unpin`, `/pinned`) | Phase 5 | ✅ |
| File watcher: re-read pinned files on disk change | Phase 5 | ✅ |
| Agent memory with `relevance_score` and decay | Phase 3 | ✅ |
| Explicit memory: `@coder remember ...` | Phase 3 | ✅ |
| `/memory list`, `/memory search`, `/memory delete` | Phase 3 | ✅ |
| Auto-memory (conservative, transparent to user) | Phase 4 | ✅ |
| Shared team knowledge (cross-agent read access) | Phase 6 | ✅ |
| Executor error-as-input with configurable retry | Phase 7 | ✅ |
| Config hot-reload | Phase 8 | ✅ |
