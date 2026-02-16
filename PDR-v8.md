# PDR-v8: Streaming & Autonomy (v0.5)

**Version**: v0.5
**Theme**: Real-time responses, autonomous agent loops, and production observability
**Predecessor**: PDR-v7 (v0.4 — Tools & Reach)
**Duration**: 4-5 weeks
**Status**: Planning

---

## Table of Contents

1. [Overview](#1-overview)
2. [Existing Code Inventory (Post-v0.4)](#2-existing-code-inventory-post-v04)
3. [Feature 1: Streaming Responses](#3-feature-1-streaming-responses)
4. [Feature 2: Agent Loops with Checkpoints](#4-feature-2-agent-loops-with-checkpoints)
5. [Feature 3: Structured Output & Validation](#5-feature-3-structured-output--validation)
6. [Feature 4: OpenTelemetry Integration](#6-feature-4-opentelemetry-integration)
7. [Feature 5: Gateway Security & Rate Limiting](#7-feature-5-gateway-security--rate-limiting)
8. [Implementation Order](#8-implementation-order)
9. [Verification Protocol](#9-verification-protocol)
10. [Self-Verification Script](#10-self-verification-script)
11. [CLAUDE.md Additions](#11-claudemd-additions)
12. [Risk Register](#12-risk-register)
13. [Definition of Done](#13-definition-of-done)

---

## 1. Overview

v0.5 transforms GoClaw from a request-response system into a real-time, autonomous runtime.
Streaming delivers token-by-token output across every interface (Gateway SSE, WebSocket,
OpenAI-compat, Telegram). Agent loops enable multi-step autonomous execution with
checkpoints, budget limits, and self-termination. Structured output adds JSON Schema
enforcement for reliable machine-consumable responses. OpenTelemetry provides production
visibility with distributed traces and metrics. Gateway security adds per-agent API keys,
rate limiting, and CORS.

### Why These Features, In This Order

After v0.4, agents can reach external systems (MCP), delegate asynchronously, and be
reached via Telegram and A2A. But every interaction is still single-turn and opaque:

- **No streaming**: Users wait for the full response. For complex tasks this means seconds
  of dead silence. Streaming is the #1 UX improvement for any LLM system.
- **No autonomy**: Agents do one inference per task. Multi-step reasoning requires external
  orchestration via plans. Agent loops let agents drive their own execution.
- **No output guarantees**: Tool responses and agent outputs are untyped strings. Structured
  output is required for any programmatic consumer.
- **No visibility**: In production, you can't see what's happening inside agent turns.
  OpenTelemetry traces every LLM call, tool invocation, and delegation.
- **No access control**: The Gateway accepts any request. Rate limiting and per-agent auth
  are table stakes for any multi-user deployment.

### Success Criteria

All of the following MUST be true at completion:

- [ ] Token streaming works via Gateway SSE, WebSocket, and OpenAI-compat endpoints
- [ ] Telegram displays partial responses via progressive message editing
- [ ] TUI renders streaming tokens in real-time
- [ ] Agents can run multi-step loops with configurable termination conditions
- [ ] Loop state checkpointed to SQLite; survives crash/restart
- [ ] Loop budgets (max steps, max tokens, max duration) enforced
- [ ] Structured output with JSON Schema validation works per-agent
- [ ] Invalid structured output triggers automatic retry with error feedback
- [ ] OpenTelemetry traces propagate from Gateway → Engine → Brain → LLM/Tool
- [ ] Metrics exported: request latency, token usage, error rates, active loops
- [ ] Gateway rate limiting enforced per API key
- [ ] Per-agent API key authentication works
- [ ] CORS configurable per-origin
- [ ] All new code has unit tests
- [ ] `just check` passes (build + vet + test)
- [ ] Zero data races under `go test -race ./...`
- [ ] All pre-existing tests continue to pass

---

## 2. Existing Code Inventory (Post-v0.4)

**CRITICAL**: Read this section first. This inventory describes the state AFTER v0.4
is complete. If v0.4 is not fully implemented, complete it first per PDR-v7.

### 2.1 Engine & Brain

**Files**: `internal/engine/engine.go`, `brain.go`

The engine processes tasks through the 8-state machine:

```go
// Engine.processTask() is the core loop — single-turn today:
// 1. Load task from store
// 2. Build context (system prompt + history + injections)
// 3. Call brain.Generate() — BLOCKS until complete response
// 4. Parse tool calls from response
// 5. Execute tools, append results
// 6. If tool calls present → loop back to step 3
// 7. Store final response, mark task complete
```

Brain wraps the Genkit LLM client:

```go
// brain.Generate() calls the LLM and returns the full response.
// There is NO streaming callback — this is the main gap for Feature 1.
type Brain struct {
    genkit   *genkit.Genkit
    registry *tools.Registry
    // ... context, memory, MCP fields from v0.3/v0.4
}

func (b *Brain) Generate(ctx context.Context, msgs []Message) (*Response, error)
```

**What exists**: Full context injection pipeline (compaction, core memory, pinned context,
delegation injection from v0.3/v0.4). Tool execution loop. MCP tool registration.

**What's missing for v0.5**:
| Gap | Description |
|-----|-------------|
| Streaming callback | `Generate()` returns complete response; no token-by-token output |
| Multi-step loops | One inference per `processTask()` call; no autonomous re-invocation |
| Output validation | Response text is untyped string; no schema enforcement |
| Trace context | No span creation or propagation through the generate path |

### 2.2 Gateway

**Files**: `internal/gateway/gateway.go`, `ws.go`, `openai.go`, `a2a.go` (from v0.4)

Routes (post-v0.4):

```go
// Existing routes:
mux.HandleFunc("/api/v1/task", g.handleTask)           // POST — submit task
mux.HandleFunc("/api/v1/task/", g.handleTaskStatus)     // GET — poll status
mux.HandleFunc("/ws", g.handleWebSocket)                // WebSocket — bidirectional
mux.HandleFunc("/openai/v1/chat/completions", g.handleOpenAI) // OpenAI-compat
mux.HandleFunc("/.well-known/agent.json", g.handleAgentCard)  // A2A (v0.4)
mux.HandleFunc("/healthz", g.handleHealthz)
mux.HandleFunc("/metrics", g.handleMetrics)
```

**What's missing for v0.5**:
| Gap | Description |
|-----|-------------|
| SSE streaming endpoint | No `text/event-stream` response path for task results |
| WebSocket streaming frames | WS sends final result only, not partial tokens |
| OpenAI streaming mode | `stream: true` in request body not handled |
| Middleware chain | No auth, rate limiting, CORS, or tracing middleware |
| Per-agent API keys | Single global auth (if any); no per-agent scoping |

### 2.3 Telegram Channel

**File**: `internal/channels/telegram.go`

Post-v0.4, Telegram handles `@agent` routing, plan progress, HITL keyboards, `/plan`
command, and alert display.

**What's missing for v0.5**:
| Gap | Description |
|-----|-------------|
| Progressive editing | Bot sends one message at end; should edit-in-place as tokens arrive |
| Typing indicator | No `sendChatAction("typing")` during generation |

### 2.4 TUI

**Files**: `internal/tui/app.go`, `chat.go`, `activity.go`, `status.go`

Bubble Tea TUI displays task status, chat history, and activity feed. Post-v0.4 shows
delegation status.

**What's missing for v0.5**:
| Gap | Description |
|-----|-------------|
| Streaming display | Chat panel shows final response; no progressive token rendering |
| Loop status | No visualization of agent loop progress (step N/max, budget remaining) |

### 2.5 Persistence & Schema

**File**: `internal/persistence/store.go`, `delegations.go` (from v0.4)

Post-v0.4 schema is **v9** with tables: agents, tasks, task_history, memory, delegations,
and schema_migrations.

**What's missing for v0.5**:
| Gap | Description |
|-----|-------------|
| Loop checkpoints table | No persistence for agent loop state |
| API keys table | No storage for per-agent API keys |
| Rate limit counters | No token bucket state (can be in-memory with optional persistence) |

### 2.6 Config System

**File**: `internal/config/config.go`

Post-v0.4 has: agent config with `mcp_servers`, global MCP config, policy, A2A config,
Telegram config, plans config, and compaction/memory settings from v0.3.

**What's missing for v0.5**:
| Gap | Description |
|-----|-------------|
| Streaming config | No per-agent streaming enable/disable or buffer size |
| Loop config | No max_steps, max_tokens, max_duration per agent |
| Output schema config | No structured_output field per agent |
| OTel config | No telemetry exporter configuration |
| Gateway security config | No rate_limit, cors, api_keys sections |

### 2.7 Policy Engine

**File**: `internal/policy/policy.go`

Post-v0.4 has: `AllowCapability()`, `AllowDomain()`, `AllowMCPTool()`.

No changes needed for v0.5 — capabilities are orthogonal to streaming/loops/observability.

### 2.8 Event Bus

**File**: `internal/bus/bus.go`

In-process pub/sub. Post-v0.4 carries: task events, delegation events, plan progress,
HITL events, alerts.

**New events for v0.5**: streaming token events, loop lifecycle events, loop checkpoint
events. Define constants near their publishers (existing convention).

### 2.9 Key Invariants to Preserve

These are NON-NEGOTIABLE constraints from prior versions:

1. **8-state task machine** — Do NOT add states. Loops are a layer ABOVE the task machine.
2. **Local-first** — No cloud dependencies. OTel exporters are optional and configurable.
3. **Zero API calls in tests** — `GEMINI_API_KEY=""` in all test setups.
4. **Schema migration** — Additive only. v9 → v10. Update both constant and checksum.
5. **Bus events near publisher** — Not centralized.
6. **OnAgentCreated hook** — Extend, don't replace. Preserve skills/WASM/shell/MCP provisioning.
7. **`if ra.Brain != nil` guards** — In any loop over `ListRunningAgents()`.
8. **Both delegate tools** — `delegate_task` (sync) and `delegate_task_async` coexist.

---

## 3. Feature 1: Streaming Responses

### 3.1 Goal

Token-by-token streaming from LLM through every output interface: Gateway SSE, WebSocket,
OpenAI-compat, Telegram (progressive edit), and TUI. Non-streaming paths remain unchanged
as fallback.

### 3.2 Streaming Architecture

The architecture uses a **fan-out pattern**: Brain produces a stream of tokens, Engine
publishes them to the bus, and each consumer (Gateway SSE, WS, Telegram, TUI) subscribes
independently.

```
LLM Provider
    │ (token callback)
    ▼
Brain.GenerateStream()
    │ (StreamChunk channel)
    ▼
Engine.processTask()
    │ (publishes to bus)
    ▼
EventBus
    ├──► Gateway SSE handler  (text/event-stream)
    ├──► Gateway WS handler   (streaming frames)
    ├──► Gateway OpenAI handler (SSE with OpenAI format)
    ├──► Telegram channel     (editMessageText on debounce)
    └──► TUI chat panel       (progressive render)
```

### 3.3 Brain Streaming Implementation

**File**: `internal/engine/brain.go`

Add a streaming variant alongside the existing `Generate()`:

```go
// StreamChunk represents a single token or event from the LLM.
type StreamChunk struct {
    // Token is the text fragment. Empty for non-text events.
    Token string

    // Done is true when the stream is complete. The final chunk carries
    // the full accumulated response in FullResponse.
    Done bool

    // FullResponse is populated only on the final chunk (Done=true).
    // Contains the complete response including any tool calls.
    FullResponse *Response

    // Error is non-nil if the stream encountered an error.
    Error error

    // ToolCallStart is set when the LLM begins a tool call block.
    // Consumers can use this to show "calling tool X..." indicators.
    ToolCallStart *ToolCallEvent
}

type ToolCallEvent struct {
    ToolName string
    ToolID   string
}

// GenerateStream starts a streaming LLM call and returns a channel of chunks.
// The channel is closed when the stream completes (after sending a Done=true chunk).
// The caller MUST drain the channel to avoid goroutine leaks.
//
// Context cancellation stops the stream. The final chunk will have Error set.
//
// If the provider doesn't support streaming, falls back to Generate() and
// sends the full response as a single Done chunk.
func (b *Brain) GenerateStream(ctx context.Context, msgs []Message) (<-chan StreamChunk, error) {
    ch := make(chan StreamChunk, 64) // buffered to reduce backpressure

    go func() {
        defer close(ch)

        // Attempt streaming call via Genkit
        streamCh, err := b.genkit.GenerateStream(ctx, b.model, msgs)
        if err != nil {
            // Fallback: non-streaming generate
            resp, genErr := b.Generate(ctx, msgs)
            if genErr != nil {
                ch <- StreamChunk{Error: genErr, Done: true}
                return
            }
            ch <- StreamChunk{
                Token:        resp.Text,
                Done:         true,
                FullResponse: resp,
            }
            return
        }

        var accumulated strings.Builder
        for token := range streamCh {
            accumulated.WriteString(token.Text)

            select {
            case ch <- StreamChunk{
                Token:         token.Text,
                ToolCallStart: parseToolCallStart(token),
            }:
            case <-ctx.Done():
                ch <- StreamChunk{Error: ctx.Err(), Done: true}
                return
            }
        }

        // Final chunk with full response
        fullResp := buildResponseFromAccumulated(accumulated.String())
        ch <- StreamChunk{
            Done:         true,
            FullResponse: fullResp,
        }
    }()

    return ch, nil
}
```

> **IMPORTANT**: Check if Genkit's Go SDK supports streaming. If it does, use its native
> streaming API. If not, implement streaming at the HTTP level by calling the provider's
> streaming endpoint directly and wrapping the SSE/chunked response. The Gemini API
> supports `streamGenerateContent` — use that if Genkit doesn't wrap it.
>
> **Research step**: Before implementing, read:
> - `b.genkit` type and its available methods
> - The Genkit Go SDK docs for streaming support
> - The Gemini Go SDK `GenerateContentStream()` method
>
> Adapt the implementation above to match whatever streaming API is actually available.

### 3.4 Engine Streaming Integration

**File**: `internal/engine/engine.go`

Modify `processTask()` to use streaming when available:

```go
// Add to Engine struct:
type Engine struct {
    // ... existing fields ...
    streamingEnabled bool // from config; default true
}

// In processTask(), replace the Generate() call:
func (e *Engine) processTask(ctx context.Context, task *persistence.Task) error {
    // ... existing context building ...

    if e.streamingEnabled {
        return e.processTaskStreaming(ctx, task, msgs)
    }
    return e.processTaskBlocking(ctx, task, msgs)
}

// processTaskStreaming handles the streaming path.
func (e *Engine) processTaskStreaming(ctx context.Context, task *persistence.Task, msgs []Message) error {
    streamCh, err := task.Brain.GenerateStream(ctx, msgs)
    if err != nil {
        return fmt.Errorf("stream start: %w", err)
    }

    for chunk := range streamCh {
        if chunk.Error != nil {
            return fmt.Errorf("stream error: %w", chunk.Error)
        }

        if chunk.Token != "" {
            // Publish streaming token to bus
            e.bus.Publish(bus.Event{
                Type: EventStreamToken,
                Data: StreamTokenData{
                    TaskID:  task.ID,
                    AgentID: task.AgentID,
                    Token:   chunk.Token,
                },
            })
        }

        if chunk.ToolCallStart != nil {
            e.bus.Publish(bus.Event{
                Type: EventToolCallStart,
                Data: ToolCallStartData{
                    TaskID:   task.ID,
                    AgentID:  task.AgentID,
                    ToolName: chunk.ToolCallStart.ToolName,
                },
            })
        }

        if chunk.Done {
            // Process tool calls from full response (existing logic)
            if chunk.FullResponse != nil && len(chunk.FullResponse.ToolCalls) > 0 {
                return e.handleToolCalls(ctx, task, chunk.FullResponse)
            }
            // Store final response (existing logic)
            return e.completeTask(ctx, task, chunk.FullResponse)
        }
    }
    return nil
}
```

**Event constants** (define in `engine.go`, near the publisher):

```go
const (
    EventStreamToken   = "stream.token"
    EventStreamDone    = "stream.done"
    EventToolCallStart = "stream.tool_call.start"
)

type StreamTokenData struct {
    TaskID  string `json:"task_id"`
    AgentID string `json:"agent_id"`
    Token   string `json:"token"`
}

type ToolCallStartData struct {
    TaskID   string `json:"task_id"`
    AgentID  string `json:"agent_id"`
    ToolName string `json:"tool_name"`
}
```

### 3.5 Gateway SSE Endpoint

**File**: `internal/gateway/gateway.go` (add route), `internal/gateway/stream.go` (new file)

```go
// New route in gateway setup:
mux.HandleFunc("/api/v1/task/stream", g.handleTaskStream) // GET with task_id param

// In stream.go:
func (g *Gateway) handleTaskStream(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }

    taskID := r.URL.Query().Get("task_id")
    if taskID == "" {
        http.Error(w, "task_id required", http.StatusBadRequest)
        return
    }

    flusher, ok := w.(http.Flusher)
    if !ok {
        http.Error(w, "Streaming not supported", http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("Connection", "keep-alive")
    w.Header().Set("X-Accel-Buffering", "no") // nginx proxy support
    flusher.Flush()

    // Subscribe to stream events for this task
    sub := g.bus.Subscribe(func(e bus.Event) bool {
        switch data := e.Data.(type) {
        case engine.StreamTokenData:
            return data.TaskID == taskID
        case engine.ToolCallStartData:
            return data.TaskID == taskID
        }
        // Also subscribe to task completion/failure
        return e.Type == "task.completed" || e.Type == "task.failed"
    })
    defer g.bus.Unsubscribe(sub)

    ctx := r.Context()
    for {
        select {
        case <-ctx.Done():
            return
        case event := <-sub.Ch:
            switch data := event.Data.(type) {
            case engine.StreamTokenData:
                fmt.Fprintf(w, "data: %s\n\n", jsonMarshal(map[string]string{
                    "type":  "token",
                    "token": data.Token,
                }))
            case engine.ToolCallStartData:
                fmt.Fprintf(w, "data: %s\n\n", jsonMarshal(map[string]string{
                    "type":      "tool_call",
                    "tool_name": data.ToolName,
                }))
            default:
                // Task completed or failed
                fmt.Fprintf(w, "data: %s\n\n", jsonMarshal(map[string]string{
                    "type": "done",
                }))
                flusher.Flush()
                return
            }
            flusher.Flush()
        }
    }
}
```

> **Bus subscription model**: Check how `bus.Subscribe()` works. The above assumes a
> channel-based subscription with a filter function. If the bus uses a different pattern
> (e.g., topic-based or callback-based), adapt accordingly. The key requirement is:
> subscribe to events filtered by task ID, receive them on a channel, unsubscribe on exit.

### 3.6 Gateway OpenAI-Compatible Streaming

**File**: `internal/gateway/openai.go`

The existing `handleOpenAI()` returns a single JSON response. Add streaming mode:

```go
func (g *Gateway) handleOpenAI(w http.ResponseWriter, r *http.Request) {
    // ... existing request parsing ...

    var req OpenAIChatRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        // ... error handling ...
    }

    // Submit task to engine (existing)
    taskID, err := g.submitTask(req)
    // ...

    if req.Stream {
        g.handleOpenAIStream(w, r, taskID, req.Model)
        return
    }

    // ... existing non-streaming response path ...
}

// handleOpenAIStream sends OpenAI-compatible SSE chunks.
// Format: https://platform.openai.com/docs/api-reference/chat/create#streaming
func (g *Gateway) handleOpenAIStream(w http.ResponseWriter, r *http.Request, taskID, model string) {
    flusher, ok := w.(http.Flusher)
    if !ok {
        http.Error(w, "Streaming not supported", http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("Connection", "keep-alive")

    sub := g.bus.Subscribe(/* filter by taskID — same as SSE endpoint */)
    defer g.bus.Unsubscribe(sub)

    completionID := "chatcmpl-" + taskID[:8]

    ctx := r.Context()
    for {
        select {
        case <-ctx.Done():
            return
        case event := <-sub.Ch:
            switch data := event.Data.(type) {
            case engine.StreamTokenData:
                chunk := OpenAIStreamChunk{
                    ID:      completionID,
                    Object:  "chat.completion.chunk",
                    Created: time.Now().Unix(),
                    Model:   model,
                    Choices: []OpenAIStreamChoice{{
                        Index: 0,
                        Delta: OpenAIDelta{Content: data.Token},
                    }},
                }
                fmt.Fprintf(w, "data: %s\n\n", jsonMarshal(chunk))
                flusher.Flush()
            default:
                // Done — send [DONE] sentinel per OpenAI spec
                // Send final chunk with finish_reason
                finalChunk := OpenAIStreamChunk{
                    ID:      completionID,
                    Object:  "chat.completion.chunk",
                    Created: time.Now().Unix(),
                    Model:   model,
                    Choices: []OpenAIStreamChoice{{
                        Index:        0,
                        Delta:        OpenAIDelta{},
                        FinishReason: stringPtr("stop"),
                    }},
                }
                fmt.Fprintf(w, "data: %s\n\n", jsonMarshal(finalChunk))
                fmt.Fprintf(w, "data: [DONE]\n\n")
                flusher.Flush()
                return
            }
        }
    }
}

// Add these types (or extend existing OpenAI types):
type OpenAIStreamChunk struct {
    ID      string               `json:"id"`
    Object  string               `json:"object"`
    Created int64                `json:"created"`
    Model   string               `json:"model"`
    Choices []OpenAIStreamChoice `json:"choices"`
}

type OpenAIStreamChoice struct {
    Index        int        `json:"index"`
    Delta        OpenAIDelta `json:"delta"`
    FinishReason *string    `json:"finish_reason"`
}

type OpenAIDelta struct {
    Role    string `json:"role,omitempty"`
    Content string `json:"content,omitempty"`
}
```

### 3.7 Gateway WebSocket Streaming

**File**: `internal/gateway/ws.go`

Extend the existing WebSocket handler to send streaming frames:

```go
// Add a new message type for streaming:
const (
    WSMsgTypeStreamToken = "stream_token"
    WSMsgTypeStreamDone  = "stream_done"
    WSMsgTypeToolCall    = "tool_call_start"
)

// In the WebSocket read/write loop, subscribe to stream events
// when a task is submitted via WS and relay tokens as they arrive.
//
// Message format:
// {"type": "stream_token", "task_id": "...", "token": "Hello"}
// {"type": "tool_call_start", "task_id": "...", "tool_name": "web_search"}
// {"type": "stream_done", "task_id": "...", "response": {...}}
```

> **Implementation note**: The WS handler likely already has a write goroutine. Add
> stream event subscription alongside existing task status notifications. Use the same
> bus subscription pattern as the SSE handler.

### 3.8 Telegram Progressive Editing

**File**: `internal/channels/telegram.go`

Instead of waiting for the full response and sending one message, Telegram should:

1. Send an initial message immediately ("⏳ Thinking...")
2. Edit that message progressively as tokens arrive
3. Debounce edits to respect Telegram rate limits (max 1 edit/second per message)

```go
// Add to Telegram channel:
const (
    telegramEditDebounce = 1 * time.Second  // max 1 edit per second
    telegramMinChunkSize = 50               // don't edit for tiny fragments
)

// streamToTelegram subscribes to stream events for a task and progressively
// edits a Telegram message.
func (t *TelegramChannel) streamToTelegram(ctx context.Context, chatID int64, taskID string) {
    // Send initial "thinking" message
    msg, err := t.bot.Send(tgbotapi.NewMessage(chatID, "⏳ _Thinking..._"))
    if err != nil {
        t.logger.Error("failed to send initial message", "err", err)
        return
    }
    msgID := msg.MessageID

    sub := t.bus.Subscribe(/* filter by taskID */)
    defer t.bus.Unsubscribe(sub)

    var accumulated strings.Builder
    ticker := time.NewTicker(telegramEditDebounce)
    defer ticker.Stop()
    dirty := false

    for {
        select {
        case <-ctx.Done():
            return
        case event := <-sub.Ch:
            switch data := event.Data.(type) {
            case engine.StreamTokenData:
                accumulated.WriteString(data.Token)
                dirty = true
            default:
                // Done — final edit with complete response
                if dirty {
                    t.editMessage(chatID, msgID, accumulated.String())
                }
                return
            }
        case <-ticker.C:
            if dirty && accumulated.Len() >= telegramMinChunkSize {
                t.editMessage(chatID, msgID, accumulated.String()+"▊")
                dirty = false
            }
        }
    }
}

func (t *TelegramChannel) editMessage(chatID int64, msgID int, text string) {
    edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
    edit.ParseMode = "MarkdownV2"
    if _, err := t.bot.Send(edit); err != nil {
        t.logger.Debug("edit message failed", "err", err, "len", len(text))
        // Non-fatal: Telegram may reject if text hasn't changed
    }
}
```

> **Rate limit note**: Telegram allows ~30 messages/second globally but only ~1
> edit/second per message. The debounce timer handles this. If the agent produces
> tokens faster than 1/s (common), tokens accumulate and are flushed in batches.

### 3.9 TUI Streaming Display

**File**: `internal/tui/chat.go`

The chat panel should render tokens as they arrive:

```go
// Subscribe to stream events in the TUI's Init() or wherever bus subscriptions are set up.
// On each StreamTokenData event, append the token to the active response buffer
// and trigger a Bubble Tea Cmd to re-render the chat view.
//
// Key considerations:
// - Use a strings.Builder as the streaming buffer
// - Append "▊" cursor at the end while streaming
// - Remove cursor and finalize when Done event arrives
// - If user scrolls up during streaming, pause auto-scroll
```

### 3.10 Config Changes

**File**: `internal/config/config.go`

```go
// Add to Config struct:
type StreamingConfig struct {
    Enabled    bool `yaml:"enabled"`     // default: true
    BufferSize int  `yaml:"buffer_size"` // channel buffer, default: 64
}

// In the main Config struct:
type Config struct {
    // ... existing fields ...
    Streaming StreamingConfig `yaml:"streaming,omitempty"`
}
```

Default: streaming enabled globally. Per-agent override is NOT needed in v0.5 —
all agents stream if the provider supports it.

### 3.11 Tests Required

| Test File | What It Verifies | Min Tests |
|-----------|------------------|-----------|
| `internal/engine/brain_stream_test.go` | GenerateStream returns chunks, fallback on non-streaming provider, context cancellation stops stream, error propagation, ToolCallStart parsing | 6 |
| `internal/engine/engine_stream_test.go` | processTaskStreaming publishes bus events, tool call loop works with streaming, non-streaming fallback, stream error handling | 5 |
| `internal/gateway/stream_test.go` | SSE endpoint returns correct Content-Type, streams tokens, sends done event, handles missing task_id, handles client disconnect | 5 |
| `internal/gateway/openai_stream_test.go` | OpenAI streaming format correct, [DONE] sentinel sent, finish_reason in final chunk, non-streaming path unchanged | 4 |
| `internal/channels/telegram_stream_test.go` | Progressive edit with debounce, initial "thinking" message sent, final edit on completion, rate limit respected | 4 |

**Minimum: 24 new tests for Feature 1.**

### 3.12 Verification Commands

```bash
go test ./internal/engine/... -v -count=1 -run "(?i)stream"
go test ./internal/gateway/... -v -count=1 -run "(?i)stream|sse|openai.*stream"
go test ./internal/channels/... -v -count=1 -run "(?i)stream|progressive"
go test -race ./internal/engine/... -count=1
go test -race ./internal/gateway/... -count=1
```

---

## 4. Feature 2: Agent Loops with Checkpoints

### 4.1 Goal

Enable agents to run autonomous multi-step execution loops. An agent loop is a sequence
of LLM calls where the agent decides whether to continue or terminate, with configurable
budget limits and crash-safe checkpoints.

### 4.2 Conceptual Model

```
Task submitted → Agent starts loop
    │
    ├── Step 1: LLM call → response + tool calls → execute tools
    │     └── Checkpoint saved
    ├── Step 2: LLM call (with tool results) → response + tool calls → execute tools
    │     └── Checkpoint saved
    ├── Step N: LLM call → response with "DONE" signal
    │     └── Loop terminates
    │
    └── Budget exceeded → Loop force-terminated, partial result returned
```

**Key distinction**: This is NOT a replacement for the Coordinator's DAG-based plans.
Plans orchestrate multiple agents across steps. Loops let a SINGLE agent reason through
multiple steps autonomously. They compose: a plan step can trigger an agent loop.

### 4.3 Loop Configuration

**File**: `internal/config/config.go`

```go
// Add to AgentConfigEntry:
type AgentConfigEntry struct {
    // ... all existing fields unchanged ...
    Loop LoopConfig `yaml:"loop,omitempty"`
}

type LoopConfig struct {
    // Enabled allows this agent to run multi-step loops.
    // Default: false (agents are single-turn by default for backward compat).
    Enabled bool `yaml:"enabled"`

    // MaxSteps is the maximum number of LLM calls per loop. 0 = unlimited.
    // Default: 25
    MaxSteps int `yaml:"max_steps"`

    // MaxTokens is the token budget for the entire loop (input + output).
    // 0 = unlimited. Default: 100000
    MaxTokens int `yaml:"max_tokens"`

    // MaxDuration is the wall-clock timeout for the loop.
    // Default: "30m". Parsed as time.Duration.
    MaxDuration string `yaml:"max_duration"`

    // CheckpointInterval is how often to persist loop state.
    // Default: 1 (every step). Set higher to reduce DB writes for fast loops.
    CheckpointInterval int `yaml:"checkpoint_interval"`

    // TerminationKeyword is the string the agent includes in its response
    // to signal loop completion. Default: "TASK_COMPLETE"
    TerminationKeyword string `yaml:"termination_keyword"`
}
```

**Example config.yaml**:

```yaml
agents:
  - agent_id: researcher
    model: gemini-2.5-pro
    loop:
      enabled: true
      max_steps: 50
      max_tokens: 200000
      max_duration: "1h"
      checkpoint_interval: 5
      termination_keyword: "RESEARCH_COMPLETE"

  - agent_id: coder
    model: gemini-2.5-pro
    loop:
      enabled: true
      max_steps: 25
      max_duration: "30m"

  - agent_id: responder
    model: gemini-2.5-flash
    # loop not configured → single-turn behavior preserved
```

### 4.4 Loop Engine Implementation

**File**: `internal/engine/loop.go` (new file)

```go
package engine

// LoopState represents the persisted state of an agent loop.
type LoopState struct {
    LoopID      string    `json:"loop_id"`
    TaskID      string    `json:"task_id"`
    AgentID     string    `json:"agent_id"`
    CurrentStep int       `json:"current_step"`
    MaxSteps    int       `json:"max_steps"`
    TokensUsed  int       `json:"tokens_used"`
    MaxTokens   int       `json:"max_tokens"`
    StartedAt   time.Time `json:"started_at"`
    MaxDuration time.Duration `json:"-"`
    Status      LoopStatus `json:"status"`
    // Messages accumulates the full conversation history for the loop.
    // This is the loop's "working memory" — distinct from the agent's core memory.
    Messages    []Message `json:"messages"`
    // LastCheckpoint is the step number of the last persisted checkpoint.
    LastCheckpoint int    `json:"last_checkpoint"`
}

type LoopStatus string

const (
    LoopStatusRunning   LoopStatus = "running"
    LoopStatusCompleted LoopStatus = "completed"
    LoopStatusBudget    LoopStatus = "budget_exceeded"
    LoopStatusTimeout   LoopStatus = "timeout"
    LoopStatusFailed    LoopStatus = "failed"
    LoopStatusCancelled LoopStatus = "cancelled"
)

// LoopRunner executes an agent loop.
type LoopRunner struct {
    brain      *Brain
    store      *persistence.Store
    bus        *bus.Bus
    logger     *slog.Logger
    config     config.LoopConfig
}

// Run executes the loop until termination, budget exhaustion, or error.
// If a checkpoint exists for this task, it resumes from the last checkpoint.
func (lr *LoopRunner) Run(ctx context.Context, task *persistence.Task) (*LoopResult, error) {
    // 1. Check for existing checkpoint (crash recovery)
    state, err := lr.store.LoadLoopCheckpoint(task.ID)
    if err != nil && !errors.Is(err, persistence.ErrNotFound) {
        return nil, fmt.Errorf("load checkpoint: %w", err)
    }

    if state == nil {
        // Fresh loop
        state = &LoopState{
            LoopID:      ulid.New(),
            TaskID:      task.ID,
            AgentID:     task.AgentID,
            MaxSteps:    lr.config.MaxSteps,
            MaxTokens:   lr.config.MaxTokens,
            MaxDuration: parseDuration(lr.config.MaxDuration),
            StartedAt:   time.Now(),
            Status:      LoopStatusRunning,
            Messages:    buildInitialMessages(task), // system prompt + user message
        }
    }

    lr.bus.Publish(bus.Event{
        Type: EventLoopStarted,
        Data: LoopEventData{
            LoopID:   state.LoopID,
            TaskID:   state.TaskID,
            AgentID:  state.AgentID,
            Step:     state.CurrentStep,
            MaxSteps: state.MaxSteps,
        },
    })

    // Create timeout context
    deadline := state.StartedAt.Add(state.MaxDuration)
    loopCtx, cancel := context.WithDeadline(ctx, deadline)
    defer cancel()

    for {
        // Budget checks
        if err := lr.checkBudget(state); err != nil {
            state.Status = LoopStatusBudget
            lr.checkpoint(state)
            return lr.buildResult(state, err)
        }

        select {
        case <-loopCtx.Done():
            state.Status = LoopStatusTimeout
            lr.checkpoint(state)
            return lr.buildResult(state, loopCtx.Err())
        default:
        }

        state.CurrentStep++

        // Publish step start
        lr.bus.Publish(bus.Event{
            Type: EventLoopStep,
            Data: LoopEventData{
                LoopID:  state.LoopID,
                TaskID:  state.TaskID,
                AgentID: state.AgentID,
                Step:    state.CurrentStep,
            },
        })

        // LLM call (streaming if available)
        var resp *Response
        if lr.brain.SupportsStreaming() {
            streamCh, err := lr.brain.GenerateStream(loopCtx, state.Messages)
            if err != nil {
                state.Status = LoopStatusFailed
                lr.checkpoint(state)
                return lr.buildResult(state, err)
            }
            // Drain stream, publish tokens, collect full response
            resp, err = lr.drainStream(loopCtx, streamCh, state)
            if err != nil {
                state.Status = LoopStatusFailed
                lr.checkpoint(state)
                return lr.buildResult(state, err)
            }
        } else {
            var err error
            resp, err = lr.brain.Generate(loopCtx, state.Messages)
            if err != nil {
                state.Status = LoopStatusFailed
                lr.checkpoint(state)
                return lr.buildResult(state, err)
            }
        }

        // Track token usage
        state.TokensUsed += resp.TokensUsed

        // Append assistant response to conversation
        state.Messages = append(state.Messages, Message{
            Role:    "assistant",
            Content: resp.Text,
        })

        // Handle tool calls
        if len(resp.ToolCalls) > 0 {
            toolResults, err := lr.executeTools(loopCtx, resp.ToolCalls)
            if err != nil {
                state.Status = LoopStatusFailed
                lr.checkpoint(state)
                return lr.buildResult(state, err)
            }
            // Append tool results to conversation
            for _, tr := range toolResults {
                state.Messages = append(state.Messages, Message{
                    Role:    "tool",
                    Content: tr.Result,
                    ToolID:  tr.ToolCallID,
                })
            }
        }

        // Check for termination keyword
        if lr.isTerminated(resp.Text, state) {
            state.Status = LoopStatusCompleted
            lr.checkpoint(state)
            lr.bus.Publish(bus.Event{
                Type: EventLoopCompleted,
                Data: LoopEventData{
                    LoopID:  state.LoopID,
                    TaskID:  state.TaskID,
                    AgentID: state.AgentID,
                    Step:    state.CurrentStep,
                },
            })
            return lr.buildResult(state, nil)
        }

        // Checkpoint if interval reached
        if state.CurrentStep%lr.config.CheckpointInterval == 0 {
            lr.checkpoint(state)
        }
    }
}

func (lr *LoopRunner) checkBudget(state *LoopState) error {
    if state.MaxSteps > 0 && state.CurrentStep >= state.MaxSteps {
        return fmt.Errorf("max steps reached: %d", state.MaxSteps)
    }
    if state.MaxTokens > 0 && state.TokensUsed >= state.MaxTokens {
        return fmt.Errorf("token budget exhausted: %d/%d", state.TokensUsed, state.MaxTokens)
    }
    return nil
}

func (lr *LoopRunner) isTerminated(text string, state *LoopState) bool {
    keyword := lr.config.TerminationKeyword
    if keyword == "" {
        keyword = "TASK_COMPLETE"
    }
    return strings.Contains(text, keyword)
}

func (lr *LoopRunner) checkpoint(state *LoopState) {
    state.LastCheckpoint = state.CurrentStep
    if err := lr.store.SaveLoopCheckpoint(state); err != nil {
        lr.logger.Error("failed to save loop checkpoint",
            "loop_id", state.LoopID,
            "step", state.CurrentStep,
            "err", err,
        )
    }
}
```

**Event constants** (define in `loop.go`):

```go
const (
    EventLoopStarted   = "loop.started"
    EventLoopStep      = "loop.step"
    EventLoopCompleted = "loop.completed"
    EventLoopBudget    = "loop.budget_exceeded"
    EventLoopTimeout   = "loop.timeout"
    EventLoopFailed    = "loop.failed"
)

type LoopEventData struct {
    LoopID   string `json:"loop_id"`
    TaskID   string `json:"task_id"`
    AgentID  string `json:"agent_id"`
    Step     int    `json:"step"`
    MaxSteps int    `json:"max_steps,omitempty"`
}
```

### 4.5 Engine Integration

**File**: `internal/engine/engine.go`

Modify `processTask()` to check for loop config:

```go
func (e *Engine) processTask(ctx context.Context, task *persistence.Task) error {
    agentCfg := e.getAgentConfig(task.AgentID)

    // Check if agent has loop enabled
    if agentCfg.Loop.Enabled {
        runner := &LoopRunner{
            brain:  task.Brain,
            store:  e.store,
            bus:    e.bus,
            logger: e.logger,
            config: agentCfg.Loop,
        }
        result, err := runner.Run(ctx, task)
        if err != nil {
            // Mark task failed with loop error
            return e.failTask(ctx, task, err)
        }
        // Store the final loop result as the task response
        return e.completeTask(ctx, task, result.FinalResponse)
    }

    // Existing single-turn path (streaming or blocking)
    if e.streamingEnabled {
        return e.processTaskStreaming(ctx, task, msgs)
    }
    return e.processTaskBlocking(ctx, task, msgs)
}
```

### 4.6 Loop Checkpoint Persistence

**File**: `internal/persistence/store.go` (schema migration), `internal/persistence/loops.go` (new file)

Schema migration v9 → v10:

```sql
-- Add to migration v10:
CREATE TABLE IF NOT EXISTS loop_checkpoints (
    loop_id      TEXT PRIMARY KEY,
    task_id      TEXT NOT NULL REFERENCES tasks(id),
    agent_id     TEXT NOT NULL,
    current_step INTEGER NOT NULL DEFAULT 0,
    max_steps    INTEGER NOT NULL DEFAULT 0,
    tokens_used  INTEGER NOT NULL DEFAULT 0,
    max_tokens   INTEGER NOT NULL DEFAULT 0,
    started_at   DATETIME NOT NULL,
    max_duration INTEGER NOT NULL DEFAULT 0, -- nanoseconds
    status       TEXT NOT NULL DEFAULT 'running',
    messages     TEXT NOT NULL DEFAULT '[]', -- JSON array of messages
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_loop_checkpoints_task ON loop_checkpoints(task_id);
CREATE INDEX IF NOT EXISTS idx_loop_checkpoints_status ON loop_checkpoints(status);
```

```go
// In loops.go:
func (s *Store) SaveLoopCheckpoint(state *LoopState) error {
    messagesJSON, err := json.Marshal(state.Messages)
    if err != nil {
        return fmt.Errorf("marshal messages: %w", err)
    }

    _, err = s.db.Exec(`
        INSERT INTO loop_checkpoints
            (loop_id, task_id, agent_id, current_step, max_steps, tokens_used,
             max_tokens, started_at, max_duration, status, messages, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
        ON CONFLICT(loop_id) DO UPDATE SET
            current_step = excluded.current_step,
            tokens_used = excluded.tokens_used,
            status = excluded.status,
            messages = excluded.messages,
            updated_at = CURRENT_TIMESTAMP`,
        state.LoopID, state.TaskID, state.AgentID,
        state.CurrentStep, state.MaxSteps, state.TokensUsed,
        state.MaxTokens, state.StartedAt, state.MaxDuration.Nanoseconds(),
        string(state.Status), string(messagesJSON),
    )
    return err
}

func (s *Store) LoadLoopCheckpoint(taskID string) (*LoopState, error) {
    row := s.db.QueryRow(`
        SELECT loop_id, task_id, agent_id, current_step, max_steps,
               tokens_used, max_tokens, started_at, max_duration, status, messages
        FROM loop_checkpoints
        WHERE task_id = ? AND status = 'running'
        ORDER BY updated_at DESC LIMIT 1`, taskID)

    var state LoopState
    var messagesJSON string
    var maxDurationNs int64
    err := row.Scan(
        &state.LoopID, &state.TaskID, &state.AgentID,
        &state.CurrentStep, &state.MaxSteps, &state.TokensUsed,
        &state.MaxTokens, &state.StartedAt, &maxDurationNs,
        &state.Status, &messagesJSON,
    )
    if err == sql.ErrNoRows {
        return nil, ErrNotFound
    }
    if err != nil {
        return nil, err
    }

    state.MaxDuration = time.Duration(maxDurationNs)
    if err := json.Unmarshal([]byte(messagesJSON), &state.Messages); err != nil {
        return nil, fmt.Errorf("unmarshal messages: %w", err)
    }
    return &state, nil
}

func (s *Store) CleanupCompletedLoops(olderThan time.Duration) (int, error) {
    result, err := s.db.Exec(`
        DELETE FROM loop_checkpoints
        WHERE status != 'running'
        AND updated_at < datetime('now', ?)`,
        fmt.Sprintf("-%d seconds", int(olderThan.Seconds())),
    )
    if err != nil {
        return 0, err
    }
    n, _ := result.RowsAffected()
    return int(n), nil
}
```

### 4.7 Loop Control Tools

**File**: `internal/tools/loop_control.go` (new file)

Agents in loops can use these tools to manage their own execution:

```go
package tools

// RegisterLoopControlTools registers tools that let an agent manage its loop.
// Only registered for agents with loop.enabled=true.
func RegisterLoopControlTools(registry *Registry) {
    registry.Register(Tool{
        Name:        "checkpoint_now",
        Description: "Force an immediate checkpoint of the current loop state. Use before risky operations.",
        Handler: func(ctx context.Context, input json.RawMessage) (string, error) {
            // The engine intercepts this tool call and triggers a checkpoint.
            // No actual execution needed — the tool call itself is the signal.
            return "Checkpoint saved.", nil
        },
    })

    registry.Register(Tool{
        Name:        "set_loop_status",
        Description: "Update the loop's status message visible to users. Use to report progress.",
        InputSchema: `{"type":"object","properties":{"status":{"type":"string","description":"Progress message, e.g. 'Analyzed 3/10 documents'"}},"required":["status"]}`,
        Handler: func(ctx context.Context, input json.RawMessage) (string, error) {
            var req struct {
                Status string `json:"status"`
            }
            if err := json.Unmarshal(input, &req); err != nil {
                return "", err
            }
            // Publish status update to bus
            busFromCtx(ctx).Publish(bus.Event{
                Type: "loop.status_update",
                Data: map[string]string{
                    "task_id": taskIDFromCtx(ctx),
                    "status":  req.Status,
                },
            })
            return "Status updated.", nil
        },
    })
}
```

> **Tool registration**: Register these tools in the OnAgentCreated hook (or catalog),
> conditioned on `agentCfg.Loop.Enabled`. Agents without loops should NOT see these tools.

### 4.8 System Prompt Injection for Loops

When an agent is in loop mode, inject a system prompt suffix explaining the loop mechanics:

```go
// In brain.go or wherever system prompts are assembled:
const loopSystemSuffix = `
You are running in LOOP MODE. You will be called repeatedly until you complete your task.

## Loop Rules:
- Each response can include tool calls. Tool results will be provided in the next step.
- To signal completion, include the keyword "%s" in your final response.
- You have a budget of %d steps and %d tokens. Use them wisely.
- Use the checkpoint_now tool before risky operations.
- Use set_loop_status to report progress to the user.
- If you cannot make progress, explain why and include the termination keyword.

## Current State:
- Step: %d / %d
- Tokens used: ~%d / %d
`
```

### 4.9 Tests Required

| Test File | What It Verifies | Min Tests |
|-----------|------------------|-----------|
| `internal/engine/loop_test.go` | Loop runs to completion, budget enforcement (steps, tokens, duration), checkpoint save/restore, crash recovery from checkpoint, termination keyword detection, tool execution within loop, stream integration in loop | 10 |
| `internal/persistence/loops_test.go` | SaveLoopCheckpoint UPSERT, LoadLoopCheckpoint returns latest, CleanupCompletedLoops removes old, messages JSON round-trip, ErrNotFound for missing | 6 |
| `internal/tools/loop_control_test.go` | checkpoint_now tool registered, set_loop_status publishes bus event | 3 |

**Minimum: 19 new tests for Feature 2.**

### 4.10 Verification Commands

```bash
go test ./internal/engine/... -v -count=1 -run "(?i)loop"
go test ./internal/persistence/... -v -count=1 -run "(?i)loop|checkpoint"
go test ./internal/tools/... -v -count=1 -run "(?i)loop"
go test -race ./internal/engine/... -count=1
```

---

## 5. Feature 3: Structured Output & Validation

### 5.1 Goal

Enable agents to produce validated JSON output conforming to a user-defined schema. Invalid
output triggers an automatic retry with the validation error as feedback, up to a
configurable retry limit.

### 5.2 Conceptual Model

```
Agent generates response
    │
    ├── No schema configured → pass through (existing behavior)
    │
    └── Schema configured:
         ├── Extract JSON from response (fenced block or raw)
         ├── Validate against JSON Schema
         ├── Valid → return structured result
         └── Invalid → inject error message, retry (up to max_retries)
              └── All retries exhausted → return raw response + validation errors
```

### 5.3 Config Changes

**File**: `internal/config/config.go`

```go
// Add to AgentConfigEntry:
type AgentConfigEntry struct {
    // ... all existing fields unchanged ...
    StructuredOutput *StructuredOutputConfig `yaml:"structured_output,omitempty"`
}

type StructuredOutputConfig struct {
    // Schema is an inline JSON Schema definition.
    // Mutually exclusive with SchemaFile.
    Schema json.RawMessage `yaml:"schema,omitempty"`

    // SchemaFile is a path to a JSON Schema file, relative to config dir.
    // Mutually exclusive with Schema.
    SchemaFile string `yaml:"schema_file,omitempty"`

    // MaxRetries is how many times to retry on validation failure.
    // Default: 2
    MaxRetries int `yaml:"max_retries"`

    // StrictMode rejects responses that don't contain valid JSON at all.
    // When false, non-JSON responses pass through with a warning.
    // Default: false
    StrictMode bool `yaml:"strict_mode"`
}
```

**Example config.yaml**:

```yaml
agents:
  - agent_id: classifier
    model: gemini-2.5-flash
    structured_output:
      max_retries: 3
      strict_mode: true
      schema:
        type: object
        properties:
          category:
            type: string
            enum: [bug, feature, question, docs]
          confidence:
            type: number
            minimum: 0
            maximum: 1
          reasoning:
            type: string
        required: [category, confidence]

  - agent_id: extractor
    model: gemini-2.5-pro
    structured_output:
      schema_file: schemas/extraction.json
      max_retries: 2
```

### 5.4 Validation Engine

**File**: `internal/engine/structured.go` (new file)

> **Action Required**: The agent MUST run `go get github.com/santhosh-tekuri/jsonschema/v6`.

```go
package engine

import (
    "encoding/json"
    "fmt"
    "strings"

    "github.com/santhosh-tekuri/jsonschema/v6"
)

// StructuredValidator validates agent responses against a JSON Schema.
type StructuredValidator struct {
    schema     *jsonschema.Schema
    maxRetries int
    strictMode bool
}

// NewStructuredValidator compiles a JSON Schema for validation.
func NewStructuredValidator(schemaJSON json.RawMessage, maxRetries int, strict bool) (*StructuredValidator, error) {
    compiler := jsonschema.NewCompiler()
    if err := compiler.AddResource("schema.json", bytes.NewReader(schemaJSON)); err != nil {
        return nil, fmt.Errorf("add schema resource: %w", err)
    }
    schema, err := compiler.Compile("schema.json")
    if err != nil {
        return nil, fmt.Errorf("compile schema: %w", err)
    }
    return &StructuredValidator{
        schema:     schema,
        maxRetries: maxRetries,
        strictMode: strict,
    }, nil
}

// ValidateResponse extracts JSON from the agent's response and validates it.
// Returns the parsed JSON and any validation errors.
func (sv *StructuredValidator) ValidateResponse(responseText string) (*StructuredResult, error) {
    jsonStr := extractJSON(responseText)
    if jsonStr == "" {
        if sv.strictMode {
            return nil, &ValidationError{
                Message: "Response does not contain valid JSON",
                Raw:     responseText,
            }
        }
        return &StructuredResult{
            Valid:   false,
            Raw:     responseText,
            Warning: "No JSON found in response; passing through raw text",
        }, nil
    }

    var parsed interface{}
    if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
        return nil, &ValidationError{
            Message: fmt.Sprintf("Invalid JSON: %s", err),
            Raw:     responseText,
        }
    }

    if err := sv.schema.Validate(parsed); err != nil {
        return nil, &ValidationError{
            Message: fmt.Sprintf("Schema validation failed: %s", err),
            Raw:     responseText,
            Parsed:  parsed,
        }
    }

    return &StructuredResult{
        Valid:  true,
        Raw:    responseText,
        JSON:   jsonStr,
        Parsed: parsed,
    }, nil
}

type StructuredResult struct {
    Valid   bool
    Raw     string
    JSON    string
    Parsed  interface{}
    Warning string
}

type ValidationError struct {
    Message string
    Raw     string
    Parsed  interface{}
}

func (e *ValidationError) Error() string { return e.Message }

// extractJSON finds a JSON object or array in the response text.
// Checks for fenced code blocks first, then attempts raw JSON extraction.
func extractJSON(text string) string {
    // 1. Try fenced JSON block: ```json\n...\n```
    if idx := strings.Index(text, "```json"); idx >= 0 {
        start := idx + 7
        if end := strings.Index(text[start:], "```"); end >= 0 {
            return strings.TrimSpace(text[start : start+end])
        }
    }
    // 2. Try generic fenced block: ```\n...\n```
    if idx := strings.Index(text, "```\n"); idx >= 0 {
        start := idx + 4
        if end := strings.Index(text[start:], "```"); end >= 0 {
            candidate := strings.TrimSpace(text[start : start+end])
            if isJSON(candidate) {
                return candidate
            }
        }
    }
    // 3. Try raw JSON: find first { or [ and match to closing
    for i, ch := range text {
        if ch == '{' || ch == '[' {
            candidate := extractBalanced(text[i:])
            if candidate != "" && isJSON(candidate) {
                return candidate
            }
        }
    }
    return ""
}
```

### 5.5 Brain Integration

**File**: `internal/engine/brain.go`

Add validation to the Generate path:

```go
// Add to Brain struct:
type Brain struct {
    // ... existing fields ...
    validator *StructuredValidator // nil if no structured output configured
}

// SetValidator configures structured output validation for this brain.
func (b *Brain) SetValidator(v *StructuredValidator) {
    b.validator = v
}

// In the loop runner (loop.go) or processTask, after getting a response:
func (e *Engine) validateAndRetry(ctx context.Context, brain *Brain, msgs []Message, resp *Response, maxRetries int) (*Response, error) {
    if brain.validator == nil {
        return resp, nil
    }

    for attempt := 0; attempt <= maxRetries; attempt++ {
        result, err := brain.validator.ValidateResponse(resp.Text)
        if err == nil && result.Valid {
            // Attach structured data to response
            resp.StructuredJSON = result.JSON
            resp.StructuredParsed = result.Parsed
            return resp, nil
        }

        if attempt == maxRetries {
            // Return raw response with validation warning
            if err != nil {
                resp.ValidationError = err.Error()
            }
            return resp, nil
        }

        // Inject error feedback and retry
        var errMsg string
        if ve, ok := err.(*ValidationError); ok {
            errMsg = ve.Message
        } else {
            errMsg = result.Warning
        }

        msgs = append(msgs,
            Message{Role: "assistant", Content: resp.Text},
            Message{Role: "user", Content: fmt.Sprintf(
                "Your response did not match the required JSON schema. Error: %s\n\n"+
                    "Please try again, ensuring your response contains valid JSON matching the schema.",
                errMsg,
            )},
        )

        var genErr error
        resp, genErr = brain.Generate(ctx, msgs)
        if genErr != nil {
            return nil, genErr
        }
    }
    return resp, nil
}
```

### 5.6 Provider-Level Structured Output

Some LLM providers (Gemini, OpenAI) support native structured output via response format
parameters. When available, use the provider's native support AND validate locally as a
safety net.

```go
// In brain.go — when building the LLM request:
func (b *Brain) buildGenerateRequest(msgs []Message) *GenerateRequest {
    req := &GenerateRequest{
        Model:    b.model,
        Messages: msgs,
    }
    // If structured output configured AND provider supports it, set response format
    if b.validator != nil && b.supportsNativeSchema() {
        req.ResponseFormat = &ResponseFormat{
            Type:   "json_schema",
            Schema: b.validator.SchemaJSON(),
        }
    }
    return req
}
```

> **Research step**: Check if Genkit's Go SDK supports `response_format` or
> `response_schema` in its generate call. The Gemini API supports
> `generationConfig.responseSchema`. Adapt the implementation to match.

### 5.7 Response Type Extension

**File**: `internal/engine/brain.go` (or wherever Response is defined)

```go
// Add structured output fields to Response:
type Response struct {
    // ... existing fields ...

    // StructuredJSON is the validated JSON string from the response.
    // Empty if no structured output configured or validation failed.
    StructuredJSON string `json:"structured_json,omitempty"`

    // StructuredParsed is the parsed JSON value.
    StructuredParsed interface{} `json:"structured_parsed,omitempty"`

    // ValidationError describes why structured validation failed, if applicable.
    ValidationError string `json:"validation_error,omitempty"`
}
```

### 5.8 Tests Required

| Test File | What It Verifies | Min Tests |
|-----------|------------------|-----------|
| `internal/engine/structured_test.go` | extractJSON from fenced blocks, raw JSON, nested objects, arrays; ValidateResponse passes valid JSON, rejects invalid, handles missing JSON, strict mode, non-strict passthrough | 10 |
| `internal/engine/brain_validate_test.go` | validateAndRetry succeeds on first try, retries on invalid, exhausts retries, error message injection, provider-level schema (if supported) | 5 |

**Minimum: 15 new tests for Feature 3.**

### 5.9 Verification Commands

```bash
go test ./internal/engine/... -v -count=1 -run "(?i)struct|valid|schema|json"
```

---

## 6. Feature 4: OpenTelemetry Integration

### 6.1 Goal

Add distributed tracing and metrics across the entire request lifecycle using
OpenTelemetry. Every significant operation (Gateway request, task processing, LLM call,
tool execution, MCP call, loop step) gets a span. Key metrics (latency, token usage,
error rates, active loops) are exported.

### 6.2 Architecture

```
Gateway request (root span)
    └── Engine.processTask (child span)
         ├── Brain.Generate / GenerateStream (child span)
         │    └── LLM API call (child span, with model + tokens attributes)
         ├── Tool execution (child span per tool)
         │    └── MCP call (child span, if MCP tool)
         ├── Loop step N (child span, if agent loop)
         │    ├── Brain.Generate (child span)
         │    └── Tool execution (child span)
         └── Delegation (child span, if async delegation)
```

### 6.3 OTel Package

**File**: `internal/otel/otel.go` (new package)

```go
package otel

import (
    "context"
    "fmt"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
    "go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
    "go.opentelemetry.io/otel/metric"
    "go.opentelemetry.io/otel/sdk/resource"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
    "go.opentelemetry.io/otel/trace"
)

const (
    TracerName = "goclaw"
    MeterName  = "goclaw"
)

// Config holds OTel configuration.
type Config struct {
    // Enabled turns telemetry on/off. Default: false (opt-in).
    Enabled bool `yaml:"enabled"`

    // Exporter selects the trace exporter: "otlp-http", "otlp-grpc", "stdout", "none".
    // Default: "otlp-http"
    Exporter string `yaml:"exporter"`

    // Endpoint is the OTLP collector endpoint.
    // Default: "localhost:4318" (HTTP) or "localhost:4317" (gRPC)
    Endpoint string `yaml:"endpoint"`

    // ServiceName is the service.name resource attribute.
    // Default: "goclaw"
    ServiceName string `yaml:"service_name"`

    // SampleRate is the trace sampling rate (0.0 to 1.0).
    // Default: 1.0 (sample everything)
    SampleRate float64 `yaml:"sample_rate"`

    // MetricsEnabled enables metrics export alongside traces.
    // Default: true (if Enabled is true)
    MetricsEnabled *bool `yaml:"metrics_enabled,omitempty"`
}

// Provider wraps OTel tracer and meter providers with cleanup.
type Provider struct {
    TracerProvider *sdktrace.TracerProvider
    MeterProvider  metric.MeterProvider
    Tracer         trace.Tracer
    Meter          metric.Meter
    shutdown       func(context.Context) error
}

// Init sets up OpenTelemetry with the given config.
// Returns a Provider that must be Shutdown() on exit.
// If config.Enabled is false, returns a no-op provider.
func Init(ctx context.Context, cfg Config) (*Provider, error) {
    if !cfg.Enabled {
        return &Provider{
            Tracer: otel.Tracer(TracerName),
            Meter:  otel.Meter(MeterName),
            shutdown: func(context.Context) error { return nil },
        }, nil
    }

    res, err := resource.New(ctx,
        resource.WithAttributes(
            semconv.ServiceName(defaultString(cfg.ServiceName, "goclaw")),
            attribute.String("goclaw.version", Version),
        ),
    )
    if err != nil {
        return nil, fmt.Errorf("create resource: %w", err)
    }

    exporter, err := createExporter(ctx, cfg)
    if err != nil {
        return nil, fmt.Errorf("create exporter: %w", err)
    }

    sampler := sdktrace.ParentBased(
        sdktrace.TraceIDRatioBased(defaultFloat(cfg.SampleRate, 1.0)),
    )

    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter),
        sdktrace.WithResource(res),
        sdktrace.WithSampler(sampler),
    )
    otel.SetTracerProvider(tp)

    return &Provider{
        TracerProvider: tp,
        Tracer:         tp.Tracer(TracerName),
        Meter:          otel.Meter(MeterName),
        shutdown:       tp.Shutdown,
    }, nil
}

func (p *Provider) Shutdown(ctx context.Context) error {
    return p.shutdown(ctx)
}

func createExporter(ctx context.Context, cfg Config) (sdktrace.SpanExporter, error) {
    switch cfg.Exporter {
    case "otlp-http", "":
        endpoint := defaultString(cfg.Endpoint, "localhost:4318")
        return otlptracehttp.New(ctx,
            otlptracehttp.WithEndpoint(endpoint),
            otlptracehttp.WithInsecure(), // configurable in future
        )
    case "otlp-grpc":
        endpoint := defaultString(cfg.Endpoint, "localhost:4317")
        return otlptracegrpc.New(ctx,
            otlptracegrpc.WithEndpoint(endpoint),
            otlptracegrpc.WithInsecure(),
        )
    case "stdout":
        return stdouttrace.New(stdouttrace.WithPrettyPrint())
    case "none":
        return nil, nil
    default:
        return nil, fmt.Errorf("unknown exporter: %s", cfg.Exporter)
    }
}
```

### 6.4 Span Helpers

**File**: `internal/otel/spans.go` (new file)

```go
package otel

import (
    "context"

    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/trace"
)

// Standard attribute keys for GoClaw spans.
const (
    AttrAgentID      = attribute.Key("goclaw.agent.id")
    AttrTaskID       = attribute.Key("goclaw.task.id")
    AttrToolName     = attribute.Key("goclaw.tool.name")
    AttrModel        = attribute.Key("goclaw.llm.model")
    AttrTokensInput  = attribute.Key("goclaw.llm.tokens.input")
    AttrTokensOutput = attribute.Key("goclaw.llm.tokens.output")
    AttrLoopID       = attribute.Key("goclaw.loop.id")
    AttrLoopStep     = attribute.Key("goclaw.loop.step")
    AttrMCPServer    = attribute.Key("goclaw.mcp.server")
)

// StartSpan is a convenience wrapper that starts a span with common attributes.
func StartSpan(ctx context.Context, tracer trace.Tracer, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
    return tracer.Start(ctx, name,
        trace.WithAttributes(attrs...),
        trace.WithSpanKind(trace.SpanKindInternal),
    )
}

// StartServerSpan starts a span for an inbound request (Gateway).
func StartServerSpan(ctx context.Context, tracer trace.Tracer, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
    return tracer.Start(ctx, name,
        trace.WithAttributes(attrs...),
        trace.WithSpanKind(trace.SpanKindServer),
    )
}

// StartClientSpan starts a span for an outbound call (LLM API, MCP).
func StartClientSpan(ctx context.Context, tracer trace.Tracer, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
    return tracer.Start(ctx, name,
        trace.WithAttributes(attrs...),
        trace.WithSpanKind(trace.SpanKindClient),
    )
}
```

### 6.5 Instrumentation Points

Add spans to existing code at these locations:

**Gateway** (`internal/gateway/gateway.go`):
```go
// In each handler, at the top:
ctx, span := g.tracer.Start(r.Context(), "gateway.handleTask",
    trace.WithSpanKind(trace.SpanKindServer),
    trace.WithAttributes(otel.AttrAgentID.String(agentID)),
)
defer span.End()
```

**Engine** (`internal/engine/engine.go`):
```go
// In processTask:
ctx, span := e.tracer.Start(ctx, "engine.processTask",
    trace.WithAttributes(
        otel.AttrTaskID.String(task.ID),
        otel.AttrAgentID.String(task.AgentID),
    ),
)
defer span.End()
```

**Brain** (`internal/engine/brain.go`):
```go
// In Generate and GenerateStream:
ctx, span := b.tracer.Start(ctx, "brain.generate",
    trace.WithSpanKind(trace.SpanKindClient),
    trace.WithAttributes(
        otel.AttrModel.String(b.model),
    ),
)
defer func() {
    span.SetAttributes(
        otel.AttrTokensInput.Int(resp.InputTokens),
        otel.AttrTokensOutput.Int(resp.OutputTokens),
    )
    span.End()
}()
```

**Tool execution** (`internal/engine/engine.go` or tool handler):
```go
// Per tool call:
ctx, span := e.tracer.Start(ctx, "tool."+toolName,
    trace.WithAttributes(otel.AttrToolName.String(toolName)),
)
defer span.End()
```

**MCP calls** (`internal/mcp/manager.go`):
```go
// In CallTool:
ctx, span := m.tracer.Start(ctx, "mcp.call_tool",
    trace.WithSpanKind(trace.SpanKindClient),
    trace.WithAttributes(
        otel.AttrMCPServer.String(serverName),
        otel.AttrToolName.String(toolName),
    ),
)
defer span.End()
```

**Loop steps** (`internal/engine/loop.go`):
```go
// Per loop iteration:
ctx, span := lr.tracer.Start(loopCtx, fmt.Sprintf("loop.step.%d", state.CurrentStep),
    trace.WithAttributes(
        otel.AttrLoopID.String(state.LoopID),
        otel.AttrLoopStep.Int(state.CurrentStep),
    ),
)
// ... step execution ...
span.End()
```

### 6.6 Metrics

**File**: `internal/otel/metrics.go` (new file)

```go
package otel

import "go.opentelemetry.io/otel/metric"

// Metrics holds all GoClaw metrics instruments.
type Metrics struct {
    RequestDuration   metric.Float64Histogram
    TaskDuration      metric.Float64Histogram
    LLMCallDuration   metric.Float64Histogram
    TokensUsed        metric.Int64Counter
    ToolCallDuration  metric.Float64Histogram
    ToolCallErrors    metric.Int64Counter
    ActiveLoops       metric.Int64UpDownCounter
    LoopStepsTotal    metric.Int64Counter
    StreamTokens      metric.Int64Counter
    RateLimitRejects  metric.Int64Counter
}

// NewMetrics creates all metric instruments.
func NewMetrics(meter metric.Meter) (*Metrics, error) {
    m := &Metrics{}
    var err error

    m.RequestDuration, err = meter.Float64Histogram("goclaw.request.duration",
        metric.WithDescription("Gateway request duration in seconds"),
        metric.WithUnit("s"),
    )
    if err != nil { return nil, err }

    m.TaskDuration, err = meter.Float64Histogram("goclaw.task.duration",
        metric.WithDescription("Task processing duration in seconds"),
        metric.WithUnit("s"),
    )
    if err != nil { return nil, err }

    m.LLMCallDuration, err = meter.Float64Histogram("goclaw.llm.duration",
        metric.WithDescription("LLM API call duration in seconds"),
        metric.WithUnit("s"),
    )
    if err != nil { return nil, err }

    m.TokensUsed, err = meter.Int64Counter("goclaw.llm.tokens",
        metric.WithDescription("Total tokens consumed"),
    )
    if err != nil { return nil, err }

    m.ToolCallDuration, err = meter.Float64Histogram("goclaw.tool.duration",
        metric.WithDescription("Tool call duration in seconds"),
        metric.WithUnit("s"),
    )
    if err != nil { return nil, err }

    m.ToolCallErrors, err = meter.Int64Counter("goclaw.tool.errors",
        metric.WithDescription("Tool call error count"),
    )
    if err != nil { return nil, err }

    m.ActiveLoops, err = meter.Int64UpDownCounter("goclaw.loop.active",
        metric.WithDescription("Number of currently active agent loops"),
    )
    if err != nil { return nil, err }

    m.LoopStepsTotal, err = meter.Int64Counter("goclaw.loop.steps",
        metric.WithDescription("Total loop steps executed"),
    )
    if err != nil { return nil, err }

    m.StreamTokens, err = meter.Int64Counter("goclaw.stream.tokens",
        metric.WithDescription("Total streaming tokens delivered"),
    )
    if err != nil { return nil, err }

    m.RateLimitRejects, err = meter.Int64Counter("goclaw.ratelimit.rejects",
        metric.WithDescription("Requests rejected by rate limiter"),
    )
    if err != nil { return nil, err }

    return m, nil
}
```

### 6.7 Config Changes

**File**: `internal/config/config.go`

```go
// In the main Config struct:
type Config struct {
    // ... existing fields ...
    Telemetry otel.Config `yaml:"telemetry,omitempty"`
}
```

**Example config.yaml**:

```yaml
telemetry:
  enabled: true
  exporter: otlp-http
  endpoint: localhost:4318
  service_name: goclaw
  sample_rate: 1.0
```

### 6.8 main.go Wiring

```go
// In main.go, early in startup:
otelProvider, err := otel.Init(ctx, cfg.Telemetry)
if err != nil {
    log.Fatal("otel init", "err", err)
}
defer otelProvider.Shutdown(ctx)

metrics, err := otel.NewMetrics(otelProvider.Meter)
if err != nil {
    log.Fatal("otel metrics", "err", err)
}

// Pass tracer and metrics to components:
engine := engine.New(/* ... existing args ... */ otelProvider.Tracer, metrics)
gateway := gateway.New(/* ... existing args ... */ otelProvider.Tracer, metrics)
```

> **Dependency management**: Add `go.opentelemetry.io/otel` and required exporter
> packages. Run `go get` for:
> - `go.opentelemetry.io/otel`
> - `go.opentelemetry.io/otel/sdk`
> - `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp`
> - `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc`
> - `go.opentelemetry.io/otel/exporters/stdout/stdouttrace`
>
> **If the project avoids heavy dependencies**: Consider starting with just the stdout
> exporter for development and otlp-http for production. The gRPC exporter is optional.

### 6.9 Tests Required

| Test File | What It Verifies | Min Tests |
|-----------|------------------|-----------|
| `internal/otel/otel_test.go` | Init with disabled config returns no-op, Init with stdout exporter works, Shutdown cleans up, resource attributes set correctly | 4 |
| `internal/otel/metrics_test.go` | All metric instruments created without error, instrument names match spec | 2 |
| `internal/engine/engine_otel_test.go` | processTask creates span, span has correct attributes, tool calls create child spans, LLM call creates client span | 4 |

**Minimum: 10 new tests for Feature 4.**

> **Test pattern for OTel**: Use `sdktrace.NewTracerProvider()` with an in-memory
> `tracetest.SpanRecorder` exporter. Assert on span names, attributes, and parent-child
> relationships. Do NOT require an external collector in tests.

### 6.10 Verification Commands

```bash
go test ./internal/otel/... -v -count=1
go test ./internal/engine/... -v -count=1 -run "(?i)otel|trace|span"
```

---

## 7. Feature 5: Gateway Security & Rate Limiting

### 7.1 Goal

Add per-agent API key authentication, token-bucket rate limiting, and CORS configuration
to the Gateway. These are production requirements for any multi-user deployment.

### 7.2 Architecture

```
Incoming Request
    │
    ├── CORS middleware (preflight + headers)
    │
    ├── Auth middleware (API key → agent mapping)
    │
    ├── Rate limit middleware (per-key token bucket)
    │
    └── Request size limit middleware
         │
         └── Route handler (existing)
```

### 7.3 API Key Authentication

**File**: `internal/gateway/auth.go` (new file)

```go
package gateway

import (
    "crypto/subtle"
    "net/http"
    "strings"
    "sync"
)

// AuthConfig holds API key authentication settings.
type AuthConfig struct {
    // Enabled turns auth on/off. Default: false (backward compat).
    Enabled bool `yaml:"enabled"`

    // Keys maps API key → allowed agent IDs. Empty agent list = access all agents.
    // Keys are stored as SHA-256 hashes in config; compared using constant-time.
    Keys []APIKeyEntry `yaml:"keys"`
}

type APIKeyEntry struct {
    // Key is the API key string. In production, store hashed.
    Key         string   `yaml:"key"`
    Description string   `yaml:"description,omitempty"`
    AgentIDs    []string `yaml:"agent_ids,omitempty"` // empty = all agents
    RateLimit   int      `yaml:"rate_limit,omitempty"` // requests/minute override
}

// AuthMiddleware validates API keys from the Authorization header.
type AuthMiddleware struct {
    keys    map[string]*APIKeyEntry // key string → entry
    enabled bool
    mu      sync.RWMutex
}

func NewAuthMiddleware(cfg AuthConfig) *AuthMiddleware {
    am := &AuthMiddleware{
        keys:    make(map[string]*APIKeyEntry),
        enabled: cfg.Enabled,
    }
    for i := range cfg.Keys {
        am.keys[cfg.Keys[i].Key] = &cfg.Keys[i]
    }
    return am
}

func (am *AuthMiddleware) Wrap(next http.Handler) http.Handler {
    if !am.enabled {
        return next
    }

    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Skip auth for health check and metrics
        if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" {
            next.ServeHTTP(w, r)
            return
        }

        key := extractAPIKey(r)
        if key == "" {
            http.Error(w, `{"error":"missing API key"}`, http.StatusUnauthorized)
            return
        }

        am.mu.RLock()
        entry, exists := am.lookupKey(key)
        am.mu.RUnlock()

        if !exists {
            http.Error(w, `{"error":"invalid API key"}`, http.StatusForbidden)
            return
        }

        // Inject key entry into context for downstream use (rate limiting, agent filtering)
        ctx := contextWithKeyEntry(r.Context(), entry)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}

func extractAPIKey(r *http.Request) string {
    // Check Authorization: Bearer <key>
    auth := r.Header.Get("Authorization")
    if strings.HasPrefix(auth, "Bearer ") {
        return strings.TrimPrefix(auth, "Bearer ")
    }
    // Check X-API-Key header
    if key := r.Header.Get("X-API-Key"); key != "" {
        return key
    }
    // Check query param (for SSE endpoints where headers are hard)
    return r.URL.Query().Get("api_key")
}

// lookupKey uses constant-time comparison to prevent timing attacks.
func (am *AuthMiddleware) lookupKey(candidate string) (*APIKeyEntry, bool) {
    for k, entry := range am.keys {
        if subtle.ConstantTimeCompare([]byte(candidate), []byte(k)) == 1 {
            return entry, true
        }
    }
    return nil, false
}
```

### 7.4 Rate Limiting

**File**: `internal/gateway/ratelimit.go` (new file)

```go
package gateway

import (
    "net/http"
    "sync"
    "time"
)

// RateLimitConfig holds rate limiting settings.
type RateLimitConfig struct {
    // Enabled turns rate limiting on/off. Default: false.
    Enabled bool `yaml:"enabled"`

    // RequestsPerMinute is the default rate limit per API key.
    // Default: 60
    RequestsPerMinute int `yaml:"requests_per_minute"`

    // BurstSize is the maximum burst above the rate limit.
    // Default: 10
    BurstSize int `yaml:"burst_size"`
}

// TokenBucket implements a simple token bucket rate limiter.
type TokenBucket struct {
    tokens    float64
    maxTokens float64
    refillRate float64 // tokens per second
    lastRefill time.Time
    mu         sync.Mutex
}

func NewTokenBucket(requestsPerMinute, burstSize int) *TokenBucket {
    rate := float64(requestsPerMinute) / 60.0
    return &TokenBucket{
        tokens:     float64(burstSize),
        maxTokens:  float64(burstSize),
        refillRate: rate,
        lastRefill: time.Now(),
    }
}

func (tb *TokenBucket) Allow() bool {
    tb.mu.Lock()
    defer tb.mu.Unlock()

    now := time.Now()
    elapsed := now.Sub(tb.lastRefill).Seconds()
    tb.tokens += elapsed * tb.refillRate
    if tb.tokens > tb.maxTokens {
        tb.tokens = tb.maxTokens
    }
    tb.lastRefill = now

    if tb.tokens >= 1.0 {
        tb.tokens -= 1.0
        return true
    }
    return false
}

// RateLimitMiddleware enforces per-key rate limits.
type RateLimitMiddleware struct {
    buckets  map[string]*TokenBucket // API key → bucket
    config   RateLimitConfig
    metrics  *otel.Metrics // optional
    mu       sync.RWMutex
}

func NewRateLimitMiddleware(cfg RateLimitConfig, metrics *otel.Metrics) *RateLimitMiddleware {
    return &RateLimitMiddleware{
        buckets: make(map[string]*TokenBucket),
        config:  cfg,
        metrics: metrics,
    }
}

func (rl *RateLimitMiddleware) Wrap(next http.Handler) http.Handler {
    if !rl.config.Enabled {
        return next
    }

    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Skip rate limiting for health/metrics
        if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" {
            next.ServeHTTP(w, r)
            return
        }

        key := extractAPIKey(r)
        if key == "" {
            key = r.RemoteAddr // fallback to IP if no auth
        }

        bucket := rl.getBucket(key)
        if !bucket.Allow() {
            if rl.metrics != nil {
                rl.metrics.RateLimitRejects.Add(r.Context(), 1)
            }
            w.Header().Set("Retry-After", "1")
            http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
            return
        }

        next.ServeHTTP(w, r)
    })
}

func (rl *RateLimitMiddleware) getBucket(key string) *TokenBucket {
    rl.mu.RLock()
    bucket, exists := rl.buckets[key]
    rl.mu.RUnlock()
    if exists {
        return bucket
    }

    rl.mu.Lock()
    defer rl.mu.Unlock()
    // Double-check after acquiring write lock
    if bucket, exists = rl.buckets[key]; exists {
        return bucket
    }

    // Check if this key has a custom rate limit
    rpm := rl.config.RequestsPerMinute
    burst := rl.config.BurstSize
    if entry := keyEntryFromCtx(/* need context — see note */); entry != nil && entry.RateLimit > 0 {
        rpm = entry.RateLimit
    }

    bucket = NewTokenBucket(rpm, burst)
    rl.buckets[key] = bucket
    return bucket
}
```

> **Context propagation note**: The rate limiter needs the API key entry from the auth
> middleware to check per-key rate overrides. Since middleware runs in order
> (auth → rate limit), the key entry should already be in context. If the rate limiter
> runs before auth (e.g., to protect the auth check itself), fall back to IP-based
> limiting.

### 7.5 CORS Middleware

**File**: `internal/gateway/cors.go` (new file)

```go
package gateway

import "net/http"

type CORSConfig struct {
    // Enabled turns CORS on/off. Default: false.
    Enabled bool `yaml:"enabled"`

    // AllowedOrigins is the list of allowed origins. Use ["*"] for all.
    // Default: []
    AllowedOrigins []string `yaml:"allowed_origins"`

    // AllowedMethods. Default: ["GET", "POST", "OPTIONS"]
    AllowedMethods []string `yaml:"allowed_methods"`

    // AllowedHeaders. Default: ["Content-Type", "Authorization", "X-API-Key"]
    AllowedHeaders []string `yaml:"allowed_headers"`

    // MaxAge is the preflight cache duration in seconds. Default: 3600
    MaxAge int `yaml:"max_age"`
}

func NewCORSMiddleware(cfg CORSConfig) func(http.Handler) http.Handler {
    if !cfg.Enabled {
        return func(next http.Handler) http.Handler { return next }
    }

    origins := make(map[string]bool)
    allowAll := false
    for _, o := range cfg.AllowedOrigins {
        if o == "*" {
            allowAll = true
        }
        origins[o] = true
    }

    methods := strings.Join(defaultStrings(cfg.AllowedMethods, []string{"GET", "POST", "OPTIONS"}), ", ")
    headers := strings.Join(defaultStrings(cfg.AllowedHeaders, []string{"Content-Type", "Authorization", "X-API-Key"}), ", ")
    maxAge := fmt.Sprintf("%d", defaultInt(cfg.MaxAge, 3600))

    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            origin := r.Header.Get("Origin")
            if origin != "" && (allowAll || origins[origin]) {
                w.Header().Set("Access-Control-Allow-Origin", origin)
                w.Header().Set("Access-Control-Allow-Methods", methods)
                w.Header().Set("Access-Control-Allow-Headers", headers)
                w.Header().Set("Access-Control-Max-Age", maxAge)
            }

            if r.Method == http.MethodOptions {
                w.WriteHeader(http.StatusNoContent)
                return
            }

            next.ServeHTTP(w, r)
        })
    }
}
```

### 7.6 Request Size Limiting

```go
// Simple middleware to reject oversized request bodies:
func RequestSizeLimitMiddleware(maxBytes int64) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
            next.ServeHTTP(w, r)
        })
    }
}
```

### 7.7 Gateway Middleware Wiring

**File**: `internal/gateway/gateway.go`

```go
// In Gateway setup, wrap the mux with middleware:
func (g *Gateway) buildHandler() http.Handler {
    var handler http.Handler = g.mux

    // Apply middleware in reverse order (outermost first)
    handler = g.rateLimiter.Wrap(handler)
    handler = g.auth.Wrap(handler)
    handler = NewCORSMiddleware(g.config.CORS)(handler)
    handler = RequestSizeLimitMiddleware(g.config.MaxRequestSize)(handler)

    // OTel instrumentation (if available)
    // handler = otelhttp.NewHandler(handler, "goclaw.gateway")

    return handler
}
```

### 7.8 Config Changes

**File**: `internal/config/config.go`

```go
// Add to the main Config struct:
type GatewayConfig struct {
    // ... existing fields (port, etc.) ...
    Auth           AuthConfig      `yaml:"auth,omitempty"`
    RateLimit      RateLimitConfig `yaml:"rate_limit,omitempty"`
    CORS           CORSConfig      `yaml:"cors,omitempty"`
    MaxRequestSize int64           `yaml:"max_request_size,omitempty"` // bytes, default: 10MB
}
```

**Example config.yaml**:

```yaml
gateway:
  port: 8080
  auth:
    enabled: true
    keys:
      - key: "sk-prod-abc123..."
        description: "Production frontend"
        agent_ids: [responder, researcher]
        rate_limit: 120
      - key: "sk-dev-xyz789..."
        description: "Development"
        # agent_ids empty = all agents
  rate_limit:
    enabled: true
    requests_per_minute: 60
    burst_size: 10
  cors:
    enabled: true
    allowed_origins: ["https://app.example.com", "http://localhost:3000"]
    max_age: 3600
  max_request_size: 10485760 # 10MB
```

### 7.9 Tests Required

| Test File | What It Verifies | Min Tests |
|-----------|------------------|-----------|
| `internal/gateway/auth_test.go` | Valid key passes, invalid key rejected, missing key rejected, agent filtering works, constant-time comparison, Bearer + X-API-Key + query param extraction, health/metrics skip auth | 8 |
| `internal/gateway/ratelimit_test.go` | Under limit passes, over limit rejected (429), burst allows spike, token refill over time, per-key isolation, health/metrics skip rate limit | 6 |
| `internal/gateway/cors_test.go` | Preflight returns correct headers, non-origin request passes, disallowed origin blocked, wildcard works | 4 |

**Minimum: 18 new tests for Feature 5.**

### 7.10 Verification Commands

```bash
go test ./internal/gateway/... -v -count=1 -run "(?i)auth|ratelimit|cors"
go test -race ./internal/gateway/... -count=1
```

---

## 8. Implementation Order

Implement in this exact order. Each phase is a gate — all its tests must pass before
proceeding.

```
Phase 1 (Week 1-2): Streaming Responses
  ├── 1a. Brain: GenerateStream + StreamChunk types (brain.go)
  ├── 1b. Engine: processTaskStreaming + bus event publishing (engine.go)
  ├── 1c. Gateway: SSE endpoint (stream.go — new file)
  ├── 1d. Gateway: OpenAI streaming mode (openai.go)
  ├── 1e. Gateway: WebSocket streaming frames (ws.go)
  ├── 1f. Telegram: progressive message editing (telegram.go)
  ├── 1g. TUI: streaming display (chat.go)
  ├── 1h. Config: StreamingConfig (config.go)
  └── 1i. Tests — GATE: go test passes for engine, gateway, channels

Phase 2 (Week 2-3): Agent Loops with Checkpoints
  ├── 2a. Schema migration v9→v10 (store.go): loop_checkpoints table
  ├── 2b. Loop store (loops.go — new file): CRUD operations
  ├── 2c. Loop runner (loop.go — new file): core loop logic
  ├── 2d. Engine integration (engine.go): loop-aware processTask
  ├── 2e. Loop control tools (loop_control.go — new file)
  ├── 2f. Config: LoopConfig per agent (config.go)
  ├── 2g. System prompt injection for loop mode (brain.go)
  └── 2h. Tests — GATE: go test passes for engine, persistence, tools

Phase 3 (Week 3): Structured Output & Validation
  ├── 3a. Structured validator (structured.go — new file)
  ├── 3b. Brain integration: validateAndRetry (brain.go)
  ├── 3c. Response type extension (brain.go or types.go)
  ├── 3d. Config: StructuredOutputConfig per agent (config.go)
  ├── 3e. Provider-level schema support (brain.go — conditional)
  └── 3f. Tests — GATE: go test passes for engine

Phase 4 (Week 3-4): OpenTelemetry Integration
  ├── 4a. OTel package (otel/otel.go — new package)
  ├── 4b. Span helpers (otel/spans.go)
  ├── 4c. Metrics instruments (otel/metrics.go)
  ├── 4d. Instrumentation: Gateway spans (gateway.go)
  ├── 4e. Instrumentation: Engine + Brain spans (engine.go, brain.go)
  ├── 4f. Instrumentation: Tool + MCP spans (engine.go, manager.go)
  ├── 4g. Instrumentation: Loop spans (loop.go)
  ├── 4h. Config: telemetry section (config.go)
  ├── 4i. main.go: OTel provider init + shutdown + component wiring
  └── 4j. Tests — GATE: go test passes for otel, engine, gateway

Phase 5 (Week 4-5): Gateway Security & Rate Limiting
  ├── 5a. Auth middleware (auth.go — new file)
  ├── 5b. Rate limit middleware (ratelimit.go — new file)
  ├── 5c. CORS middleware (cors.go — new file)
  ├── 5d. Request size limiting (gateway.go)
  ├── 5e. Middleware wiring (gateway.go: buildHandler)
  ├── 5f. Config: GatewayConfig with auth, rate_limit, cors sections (config.go)
  └── 5g. Tests — GATE: go test passes for gateway

```

### Dependency Graph

```
Streaming ─────────── (start first — streaming changes propagate to loops)
     │
     ├──► Agent Loops (uses streaming internally; extends engine)
     │        │
     │        └──► Structured Output (validates within loops and single-turn)
     │
     ├──► OpenTelemetry (instruments streaming, loops, everything)
     │
     └──► Gateway Security (independent; instruments with OTel if available)
```

**Why this order**:
1. Streaming must be first because loops use it internally for real-time feedback.
2. Loops before structured output because validation applies inside loops too.
3. OTel after the main features exist — easier to instrument working code.
4. Security last because it's pure middleware with no feature dependencies.

---

## 9. Verification Protocol

### 9.1 Per-Phase Gate

After completing each phase, run:

```bash
# Phase-specific tests
go test ./internal/<packages-in-phase>/... -v -count=1

# Then full suite
just check
go test -race ./... -count=1
```

Do NOT proceed to the next phase until the gate passes.

### 9.2 Full Suite Gate (After All Phases)

```bash
# 1. Compile
go build ./...

# 2. Vet
go vet ./...

# 3. Full test suite
go test ./... -count=1

# 4. Race detector
go test -race ./... -count=1

# 5. Test count delta
BASELINE=$(cat .test-count-baseline 2>/dev/null || echo 0)
CURRENT=$(grep -r "func Test" internal/ cmd/ tools/ 2>/dev/null | wc -l | tr -d ' ')
DELTA=$((CURRENT - BASELINE))
echo "Tests: $CURRENT (baseline: $BASELINE, delta: +$DELTA)"
[ "$DELTA" -ge 80 ] || echo "WARN: Expected >=80 new tests, got $DELTA"

# 6. New file existence
for f in \
    "internal/gateway/stream.go" \
    "internal/gateway/stream_test.go" \
    "internal/gateway/auth.go" \
    "internal/gateway/auth_test.go" \
    "internal/gateway/ratelimit.go" \
    "internal/gateway/ratelimit_test.go" \
    "internal/gateway/cors.go" \
    "internal/gateway/cors_test.go" \
    "internal/engine/loop.go" \
    "internal/engine/loop_test.go" \
    "internal/engine/structured.go" \
    "internal/engine/structured_test.go" \
    "internal/persistence/loops.go" \
    "internal/persistence/loops_test.go" \
    "internal/tools/loop_control.go" \
    "internal/tools/loop_control_test.go" \
    "internal/otel/otel.go" \
    "internal/otel/otel_test.go" \
    "internal/otel/spans.go" \
    "internal/otel/metrics.go" \
    "internal/otel/metrics_test.go"; do
    [ -f "$f" ] && echo "OK: $f" || echo "MISSING: $f"
done

# 7. Schema version check
grep -q "schemaVersionV10\|schemaVersion.*= 10" internal/persistence/store.go && \
    echo "OK: schema v10" || echo "FAIL: schema not bumped to v10"

# 8. OTel dependency check
grep -q "go.opentelemetry.io/otel" go.mod && \
    echo "OK: OTel dependency present" || echo "FAIL: OTel dependency missing"
```

### 9.3 Acceptance Criteria Checklist

```
Feature 1: Streaming Responses
  [ ] Brain.GenerateStream returns StreamChunk channel
  [ ] Fallback to non-streaming Generate on unsupported providers
  [ ] Engine publishes stream.token events to bus
  [ ] Gateway SSE endpoint returns text/event-stream
  [ ] Gateway SSE streams tokens for active tasks
  [ ] Gateway SSE sends done event on completion
  [ ] OpenAI-compat streaming mode returns correct SSE format
  [ ] OpenAI-compat sends [DONE] sentinel
  [ ] WebSocket sends streaming frames
  [ ] Telegram sends initial "thinking" message
  [ ] Telegram progressively edits message with tokens
  [ ] Telegram respects 1/s edit rate limit
  [ ] TUI renders tokens progressively
  [ ] Context cancellation stops stream cleanly
  [ ] Config: streaming.enabled controls feature
  [ ] 24+ new tests pass

Feature 2: Agent Loops with Checkpoints
  [ ] Schema migrated to v10 with loop_checkpoints table
  [ ] LoopRunner executes multi-step loop
  [ ] Loop terminates on keyword detection
  [ ] Max steps budget enforced
  [ ] Max tokens budget enforced
  [ ] Max duration timeout enforced
  [ ] Checkpoints saved to SQLite at configured interval
  [ ] Crash recovery resumes from last checkpoint
  [ ] Loop events published to bus (started, step, completed, budget, timeout)
  [ ] Loop control tools registered for loop-enabled agents only
  [ ] set_loop_status publishes bus event
  [ ] System prompt suffix injected in loop mode
  [ ] Single-turn agents unaffected (backward compat)
  [ ] Streaming works within loops
  [ ] 19+ new tests pass

Feature 3: Structured Output & Validation
  [ ] extractJSON handles fenced blocks, raw JSON, nested objects
  [ ] ValidateResponse validates against JSON Schema
  [ ] Invalid response triggers retry with error feedback
  [ ] Max retries exhausted returns raw response with error
  [ ] Strict mode rejects non-JSON responses
  [ ] Non-strict mode passes through with warning
  [ ] Provider-level schema support used when available
  [ ] StructuredJSON and StructuredParsed populated on Response
  [ ] Config: structured_output per agent with schema/schema_file
  [ ] 15+ new tests pass

Feature 4: OpenTelemetry Integration
  [ ] OTel provider initializes with config
  [ ] Disabled config returns no-op provider
  [ ] stdout exporter works for development
  [ ] otlp-http exporter configured correctly
  [ ] Gateway creates server spans on requests
  [ ] Engine creates internal spans for task processing
  [ ] Brain creates client spans for LLM calls with token attributes
  [ ] Tool calls create child spans
  [ ] MCP calls create client spans with server name
  [ ] Loop steps create spans with loop attributes
  [ ] All metrics instruments created without error
  [ ] Provider.Shutdown called on process exit
  [ ] 10+ new tests pass

Feature 5: Gateway Security & Rate Limiting
  [ ] Auth middleware validates Bearer token
  [ ] Auth middleware validates X-API-Key header
  [ ] Auth middleware validates api_key query param (for SSE)
  [ ] Invalid key returns 403
  [ ] Missing key returns 401
  [ ] Per-key agent filtering works
  [ ] Health/metrics endpoints skip auth
  [ ] Rate limiter rejects over-limit requests with 429
  [ ] Rate limiter returns Retry-After header
  [ ] Burst allows spike above rate
  [ ] Token bucket refills over time
  [ ] Per-key rate isolation
  [ ] CORS preflight returns correct headers
  [ ] CORS rejects disallowed origins
  [ ] Request size limit enforced
  [ ] Middleware wired in correct order
  [ ] Auth disabled by default (backward compat)
  [ ] 18+ new tests pass
```

---

## 10. Self-Verification Script

Create this file at `tools/verify/v05_verify.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

echo "=========================================="
echo " GoClaw v0.5 Verification"
echo "=========================================="
echo ""

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

PASS=0
FAIL=0
WARN=0

pass() { echo -e "  ${GREEN}✓${NC} $1"; PASS=$((PASS + 1)); }
fail() { echo -e "  ${RED}✗${NC} $1"; FAIL=$((FAIL + 1)); }
warn() { echo -e "  ${YELLOW}!${NC} $1"; WARN=$((WARN + 1)); }

# Capture baseline
BASELINE=$(cat .test-count-baseline 2>/dev/null || echo 0)

echo "── Build & Vet ──"

if go build ./... 2>/dev/null; then
    pass "Build succeeds"
else fail "Build fails"; fi

if go vet ./... 2>/dev/null; then
    pass "Vet clean"
else fail "Vet errors"; fi

echo ""

# ── Feature 1: Streaming ────────────────────
echo "── Feature 1: Streaming Responses ──"

[ -f internal/gateway/stream.go ] && pass "stream.go exists" || fail "stream.go missing"
[ -f internal/gateway/stream_test.go ] && pass "stream_test.go exists" || fail "stream_test.go missing"

grep -qi "GenerateStream\|StreamChunk" internal/engine/brain.go 2>/dev/null && \
    pass "GenerateStream in brain" || fail "GenerateStream missing"

grep -qi "text/event-stream" internal/gateway/stream.go 2>/dev/null && \
    pass "SSE content type in stream handler" || fail "SSE content type missing"

grep -qi "EventStreamToken\|stream\.token" internal/engine/engine.go 2>/dev/null && \
    pass "Stream token bus event in engine" || fail "Stream token event missing"

grep -qi "stream.*true\|handleOpenAIStream\|openai.*stream" internal/gateway/openai.go 2>/dev/null && \
    pass "OpenAI streaming mode" || fail "OpenAI streaming missing"

grep -qi "editMessageText\|editMessage\|progressive" internal/channels/telegram.go 2>/dev/null && \
    pass "Telegram progressive editing" || fail "Telegram progressive editing missing"

STREAM_TEST_COUNT=$(grep -c "func Test" internal/gateway/stream_test.go 2>/dev/null || echo 0)
[ "$STREAM_TEST_COUNT" -ge 4 ] && pass "Stream tests: $STREAM_TEST_COUNT (≥4)" || \
    fail "Stream tests: $STREAM_TEST_COUNT (<4)"

if go test ./internal/engine/... -count=1 -timeout 120s -run "(?i)stream" 2>/dev/null; then
    pass "Engine stream tests pass"
else fail "Engine stream tests fail"; fi

if go test ./internal/gateway/... -count=1 -timeout 120s -run "(?i)stream|sse" 2>/dev/null; then
    pass "Gateway stream tests pass"
else fail "Gateway stream tests fail"; fi

echo ""

# ── Feature 2: Agent Loops ───────────────────
echo "── Feature 2: Agent Loops ──"

[ -f internal/engine/loop.go ] && pass "loop.go exists" || fail "loop.go missing"
[ -f internal/engine/loop_test.go ] && pass "loop_test.go exists" || fail "loop_test.go missing"
[ -f internal/persistence/loops.go ] && pass "loops.go (persistence) exists" || fail "loops.go missing"
[ -f internal/persistence/loops_test.go ] && pass "loops_test.go exists" || fail "loops_test.go missing"
[ -f internal/tools/loop_control.go ] && pass "loop_control.go exists" || fail "loop_control.go missing"

grep -qi "LoopRunner\|LoopState\|LoopStatus" internal/engine/loop.go 2>/dev/null && \
    pass "LoopRunner implemented" || fail "LoopRunner missing"

grep -qi "loop_checkpoints" internal/persistence/store.go 2>/dev/null && \
    pass "loop_checkpoints migration" || fail "loop_checkpoints table missing"

grep -qi "SaveLoopCheckpoint\|LoadLoopCheckpoint" internal/persistence/loops.go 2>/dev/null && \
    pass "Loop checkpoint CRUD" || fail "Loop checkpoint CRUD missing"

grep -qi "checkpoint_now\|set_loop_status" internal/tools/loop_control.go 2>/dev/null && \
    pass "Loop control tools" || fail "Loop control tools missing"

grep -qi "LoopConfig\|loop.*enabled\|max_steps" internal/config/config.go 2>/dev/null && \
    pass "LoopConfig in config" || fail "LoopConfig missing"

LOOP_TEST_COUNT=$(grep -c "func Test" internal/engine/loop_test.go 2>/dev/null || echo 0)
[ "$LOOP_TEST_COUNT" -ge 8 ] && pass "Loop tests: $LOOP_TEST_COUNT (≥8)" || \
    fail "Loop tests: $LOOP_TEST_COUNT (<8)"

if go test ./internal/engine/... -count=1 -timeout 120s -run "(?i)loop" 2>/dev/null; then
    pass "Loop tests pass"
else fail "Loop tests fail"; fi

if go test ./internal/persistence/... -count=1 -timeout 120s -run "(?i)loop|checkpoint" 2>/dev/null; then
    pass "Loop persistence tests pass"
else fail "Loop persistence tests fail"; fi

echo ""

# ── Feature 3: Structured Output ─────────────
echo "── Feature 3: Structured Output ──"

[ -f internal/engine/structured.go ] && pass "structured.go exists" || fail "structured.go missing"
[ -f internal/engine/structured_test.go ] && pass "structured_test.go exists" || fail "structured_test.go missing"

grep -qi "StructuredValidator\|ValidateResponse\|extractJSON" internal/engine/structured.go 2>/dev/null && \
    pass "StructuredValidator implemented" || fail "StructuredValidator missing"

grep -qi "StructuredOutput\|structured_output" internal/config/config.go 2>/dev/null && \
    pass "StructuredOutputConfig in config" || fail "StructuredOutputConfig missing"

grep -qi "validateAndRetry\|SetValidator\|validator" internal/engine/brain.go 2>/dev/null && \
    pass "Validation in brain" || fail "Validation missing from brain"

STRUCT_TEST_COUNT=$(grep -c "func Test" internal/engine/structured_test.go 2>/dev/null || echo 0)
[ "$STRUCT_TEST_COUNT" -ge 8 ] && pass "Structured tests: $STRUCT_TEST_COUNT (≥8)" || \
    fail "Structured tests: $STRUCT_TEST_COUNT (<8)"

if go test ./internal/engine/... -count=1 -timeout 120s -run "(?i)struct|valid|schema" 2>/dev/null; then
    pass "Structured output tests pass"
else fail "Structured output tests fail"; fi

echo ""

# ── Feature 4: OpenTelemetry ─────────────────
echo "── Feature 4: OpenTelemetry ──"

[ -d internal/otel ] && pass "otel package exists" || fail "otel package missing"
[ -f internal/otel/otel.go ] && pass "otel.go exists" || fail "otel.go missing"
[ -f internal/otel/spans.go ] && pass "spans.go exists" || fail "spans.go missing"
[ -f internal/otel/metrics.go ] && pass "metrics.go exists" || fail "metrics.go missing"
[ -f internal/otel/otel_test.go ] && pass "otel_test.go exists" || fail "otel_test.go missing"

grep -q "go.opentelemetry.io/otel" go.mod 2>/dev/null && \
    pass "OTel dependency in go.mod" || fail "OTel dependency missing"

grep -qi "Telemetry\|telemetry" internal/config/config.go 2>/dev/null && \
    pass "Telemetry config" || fail "Telemetry config missing"

grep -qi "tracer\|trace\.Span\|span\.End" internal/engine/engine.go 2>/dev/null && \
    pass "Tracing in engine" || fail "Tracing missing from engine"

grep -qi "tracer\|trace\.Span\|span\.End" internal/engine/brain.go 2>/dev/null && \
    pass "Tracing in brain" || fail "Tracing missing from brain"

if go test ./internal/otel/... -count=1 -timeout 120s 2>/dev/null; then
    pass "OTel tests pass"
else fail "OTel tests fail"; fi

echo ""

# ── Feature 5: Gateway Security ──────────────
echo "── Feature 5: Gateway Security ──"

[ -f internal/gateway/auth.go ] && pass "auth.go exists" || fail "auth.go missing"
[ -f internal/gateway/auth_test.go ] && pass "auth_test.go exists" || fail "auth_test.go missing"
[ -f internal/gateway/ratelimit.go ] && pass "ratelimit.go exists" || fail "ratelimit.go missing"
[ -f internal/gateway/ratelimit_test.go ] && pass "ratelimit_test.go exists" || fail "ratelimit_test.go missing"
[ -f internal/gateway/cors.go ] && pass "cors.go exists" || fail "cors.go missing"
[ -f internal/gateway/cors_test.go ] && pass "cors_test.go exists" || fail "cors_test.go missing"

grep -qi "AuthMiddleware\|AuthConfig" internal/gateway/auth.go 2>/dev/null && \
    pass "Auth middleware implemented" || fail "Auth middleware missing"

grep -qi "TokenBucket\|RateLimitMiddleware" internal/gateway/ratelimit.go 2>/dev/null && \
    pass "Rate limiter implemented" || fail "Rate limiter missing"

grep -qi "CORSConfig\|Access-Control" internal/gateway/cors.go 2>/dev/null && \
    pass "CORS middleware implemented" || fail "CORS middleware missing"

grep -qi "buildHandler\|Wrap\|middleware" internal/gateway/gateway.go 2>/dev/null && \
    pass "Middleware wired in gateway" || fail "Middleware wiring missing"

AUTH_TEST_COUNT=$(grep -c "func Test" internal/gateway/auth_test.go 2>/dev/null || echo 0)
[ "$AUTH_TEST_COUNT" -ge 6 ] && pass "Auth tests: $AUTH_TEST_COUNT (≥6)" || \
    fail "Auth tests: $AUTH_TEST_COUNT (<6)"

RL_TEST_COUNT=$(grep -c "func Test" internal/gateway/ratelimit_test.go 2>/dev/null || echo 0)
[ "$RL_TEST_COUNT" -ge 4 ] && pass "Rate limit tests: $RL_TEST_COUNT (≥4)" || \
    fail "Rate limit tests: $RL_TEST_COUNT (<4)"

if go test ./internal/gateway/... -count=1 -timeout 120s -run "(?i)auth|rate|cors" 2>/dev/null; then
    pass "Security tests pass"
else fail "Security tests fail"; fi

echo ""

# ── Global Checks ──────────────────────────
echo "── Global Checks ──"

if go test ./... -count=1 -timeout 300s 2>/dev/null; then
    pass "Full test suite passes"
else fail "Full test suite fails"; fi

if go test -race ./... -count=1 -timeout 300s 2>/dev/null; then
    pass "Race detector clean"
else fail "Race conditions detected"; fi

# Test count delta
CURRENT=$(grep -r "func Test" internal/ cmd/ tools/ 2>/dev/null | wc -l | tr -d ' ')
if [ "$BASELINE" -gt 0 ]; then
    DELTA=$((CURRENT - BASELINE))
    echo "  Tests: $CURRENT (baseline: $BASELINE, delta: +$DELTA)"
    [ "$DELTA" -ge 80 ] && pass "Test delta: +$DELTA (≥80)" || \
        warn "Test delta: +$DELTA (target ≥80)"
else
    echo "  Tests: $CURRENT (no baseline captured — run baseline step first)"
    warn "No test baseline — cannot verify delta"
fi

# Schema version
grep -q "schemaVersionV10\|schemaVersion.*= 10\|SchemaVersion.*10" internal/persistence/store.go 2>/dev/null && \
    pass "Schema v10" || fail "Schema not bumped to v10"

# Version string
grep -q "v0\.5" cmd/goclaw/main.go 2>/dev/null && \
    pass "Version string updated" || warn "Version string not updated"

echo ""
echo "=========================================="
echo " Results"
echo "=========================================="
echo -e "  ${GREEN}Passed${NC}: $PASS"
echo -e "  ${RED}Failed${NC}: $FAIL"
echo -e "  ${YELLOW}Warnings${NC}: $WARN"
echo ""

if [ "$FAIL" -eq 0 ]; then
    echo -e "${GREEN}🎉 v0.5 VERIFICATION PASSED${NC}"
    exit 0
else
    echo -e "${RED}💀 v0.5 VERIFICATION FAILED — $FAIL issue(s)${NC}"
    exit 1
fi
```

---

## 11. CLAUDE.md Additions

Append the following to the project's `CLAUDE.md`:

```markdown
## v0.5 Implementation Notes

### Current milestone: v0.5 (Streaming & Autonomy)
### PDR: docs/PDR-v8.md
### Verify: tools/verify/v05_verify.sh

### Before starting:
1. Ensure v0.4 is COMPLETE (run tools/verify/v04_verify.sh first)
2. Read §2 of PDR-v8.md (Existing Code Inventory Post-v0.4) — understand post-v0.4 state
3. Capture test baseline: `grep -r "func Test" internal/ cmd/ tools/ | wc -l > .test-count-baseline`
4. Run `just check` to confirm clean starting state

### Implementation order (strict):
1. Streaming Responses (internal/engine/brain.go, engine.go, internal/gateway/stream.go, openai.go, ws.go, internal/channels/telegram.go, internal/tui/chat.go)
2. Agent Loops (internal/persistence/loops.go, store.go, internal/engine/loop.go, engine.go, internal/tools/loop_control.go, internal/config/)
3. Structured Output (internal/engine/structured.go, brain.go, internal/config/)
4. OpenTelemetry (internal/otel/ — new package, instrumentation across engine, gateway, brain, mcp)
5. Gateway Security (internal/gateway/auth.go, ratelimit.go, cors.go, gateway.go)

### Schema version:
- Current (post-v0.4): v9
- Target: v10 (add loop_checkpoints table)
- Update BOTH schemaVersionCurrent AND checksum in persistence/store.go

### Key existing code to extend (do NOT replace):
- Brain.Generate() — keep it; GenerateStream is additive alongside it
- Engine.processTask() — extend with streaming path and loop detection, don't rewrite
- Gateway route registration — add new routes, don't restructure existing
- OnAgentCreated hook — extend with loop tools and validator, keep all prior provisioning
- OpenAI handler — extend with streaming mode, don't break non-streaming
- Telegram message handling — extend with progressive editing, don't break existing commands
- All v0.4 code (MCP, async delegation, HITL, A2A) — MUST continue working

### Key patterns:
- All new tables: SQLite, WAL mode, migration in store.go, bump schema version
- All new tools: register in internal/tools/catalog.go
- Bus events: define constants near publisher, not centralized
- Tests: table-driven, OFFLINE (zero API credits), -count=1, guard Brain nil
- OTel: use in-memory SpanRecorder for tests, never require external collector
- Middleware: compose with Wrap() pattern, test independently
- Streaming: always provide non-streaming fallback path
- All new files: follow conventions of neighboring files in same package

### Do NOT:
- Replace Brain.Generate() — it's the fallback when streaming unavailable
- Add new states to the 8-state task machine — loops are above it
- Require external services in tests (no OTel collector, no LLM calls)
- Break existing v0.4 features (MCP, delegation, Telegram HITL, A2A)
- Make OTel a hard dependency — everything works with telemetry disabled
- Store API keys in plaintext in SQLite (use config only for v0.5)
- Add cloud dependencies (local-first is non-negotiable)
- Forget backward compat: auth/rate-limit disabled by default

### Verify after each phase:
Run phase-specific tests, then `just check`, then `go test -race ./...`

### Verify at end:
./tools/verify/v05_verify.sh
```

---

## 12. Risk Register

| Risk | Impact | Mitigation |
|------|--------|------------|
| Genkit Go SDK doesn't support streaming | Feature 1 blocked | Fall back to direct Gemini API `streamGenerateContent`; wrap in Brain abstraction |
| Stream goroutine leak if consumer doesn't drain | Memory leak | Buffered channel (64); context cancellation stops producer; document "MUST drain" contract |
| Telegram edit rate limit causes API errors | User sees stale partial response | Debounce at 1/s; catch and ignore 429 errors on edit; show final result regardless |
| Agent loop runs indefinitely despite budget | Resource exhaustion | Hard context deadline from MaxDuration; triple-check budget enforcement before each step |
| Checkpoint messages JSON grows unbounded | SQLite row too large | Compact loop messages using same compaction logic as v0.3 context; cap at 100 messages |
| JSON Schema library not available in Go | Feature 3 blocked | Multiple options: `santhosh-tekuri/jsonschema`, `xeipuv/gojsonschema`, `qri-io/jsonschema`; evaluate before starting |
| OpenTelemetry SDK adds binary size / complexity | Build time, dependency count | OTel is optional (disabled by default); consider build tags if size is critical |
| Rate limiter state lost on restart | Burst after restart | Acceptable for v0.5; in-memory token buckets refill naturally; add persistence in v0.6 if needed |
| Auth middleware breaks existing unauthenticated clients | All API consumers break | Auth disabled by default; explicit opt-in; health/metrics always skip auth |
| Constant-time key comparison not actually constant-time | Timing attack possible | Use `crypto/subtle.ConstantTimeCompare`; test with timing analysis if paranoid |
| Loop checkpoint corrupted | Loop can't resume | Validate checkpoint on load; fall back to fresh start on corruption; log warning |
| Structured output retry loop with streaming causes double-publish | Duplicate tokens in stream | Only stream the final successful attempt; buffer retries internally |
| CORS misconfiguration leaks data cross-origin | Security vulnerability | Default to disabled; require explicit origin list; no wildcard in production examples |

---

## 13. Definition of Done

v0.5 is **done** when:

1. `./tools/verify/v05_verify.sh` exits with code 0 and 0 failures
2. All items in §9.3 Acceptance Criteria Checklist are checked
3. `just check` passes
4. `go test -race ./... -count=1` is clean
5. `go vet ./...` is clean
6. Schema version is v10 in `internal/persistence/store.go`
7. Version string is `v0.5-dev` in `cmd/goclaw/main.go`
8. `go.opentelemetry.io/otel` present in `go.mod`
9. README.md status table updated (Streaming → Stable, Loops → Stable, OTel → Stable)
10. PROGRESS.md updated with v0.5 completion entry
11. Git tag `v0.5-dev` created

