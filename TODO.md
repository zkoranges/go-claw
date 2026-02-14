# TODO — Project GoClaw

**Last Updated:** 2026-02-13
**Baseline:** SPEC v2.0.0, Schema v4, comparison against `reference/goclaw` (Go) and `reference/rust_claw` (Rust)

---

## P0 — Critical Gaps ✅ ALL COMPLETE

### ~~P0-1: OpenRouter LLM Provider~~ ✅

Implemented in `internal/engine/brain.go` (case "openrouter"), `internal/config/config.go`, `internal/tui/genesis.go`, `cmd/goclaw/import.go`.

### ~~P0-2: LLM Provider Failover~~ ✅

Implemented in `internal/engine/failover.go`, `internal/engine/errors.go`, `internal/engine/failover_test.go`. Circuit-breaker with 5-failure threshold, 5min cooldown, error classification.

### ~~P0-3: File System Tools~~ ✅

Implemented in `internal/tools/file.go`, `internal/tools/file_test.go`. 4 Genkit tools: `read_file`, `write_file`, `list_directory`, `edit_file`. Policy-gated, path-traversal protected, atomic writes.

### ~~P0-4: Shell Execution Tool~~ ✅

Implemented in `internal/tools/shell.go`, `internal/tools/shell_test.go`. Policy-gated `exec` tool with deny list (all tokens scanned), pipe/subshell operator blocking, 8KB truncation, secret redaction.

### ~~P0-5: `use_skill` Tool~~ ✅

Implemented in `internal/tools/useskill.go`, `internal/engine/brain.go` (tool registration). Progressive disclosure with on-demand instruction loading.

---

## P1 — High Priority ✅ ALL COMPLETE

### ~~P1-1: Prompt Injection Defense~~ ✅

Implemented in `internal/safety/sanitizer.go`, `internal/safety/leak_detector.go`, `internal/safety/sanitizer_test.go`. Integrated into brain.go `Respond()` and `Stream()` paths.

### ~~P1-2: Skill Hot-Reload Watcher~~ ✅

Implemented in `internal/skills/watcher.go`, `internal/skills/watcher_test.go`. fsnotify-based with 250ms debounce, `tools.updated` gateway broadcast.

### ~~P1-3: Cron/Scheduled Task Execution~~ ✅

Implemented in `internal/cron/scheduler.go`, `internal/cron/scheduler_test.go`. Schema v5 `schedules` table, ACP methods: `cron.list`, `cron.add`, `cron.remove`, `cron.enable`, `cron.disable`.

### ~~P1-4: Subagent / Task Delegation~~ ✅

Implemented in `internal/tools/spawn.go`, `internal/tools/spawn_test.go`. `spawn_task` Genkit tool with `parent_task_id` linkage, policy-gated.

---

## P2 — Medium Priority (competitive advantages)

### ~~P2-1: Memory / Knowledge System (Phase 1)~~ ✅

Implemented in `internal/memory/workspace.go`, `internal/memory/workspace_test.go`, `internal/tools/memory.go`. File-based workspace with Read/Write/Append/List/Search, path-traversal protection, 3 Genkit tools (`memory_search`, `memory_write`, `memory_read`).

**Phase 2 — SQLite FTS5 search (future):**
- Add `memory_documents` table with FTS5 virtual table
- Chunk documents (800 words, 15% overlap)
- Full-text search via `MATCH` queries

---

### ~~P2-2: Docker Sandbox for Shell Execution~~ ✅

Implemented in `internal/tools/docker.go`. Supports ephemeral container execution for the `exec` tool. Configurable via `tools.shell.sandbox`.

**Why:** ref-goclaw has Docker sandbox mode for shell commands. rust_claw has full orchestrator/worker pattern with per-job tokens.

**File to create:** `internal/tools/docker.go` (~200 lines)

```go
type DockerSandbox struct {
    image     string // default "golang:alpine"
    timeout   time.Duration
    memoryMB  int
    networkMode string // "none", "bridge"
}

func (d *DockerSandbox) Exec(ctx context.Context, command string, workDir string) (stdout, stderr string, exitCode int, err error)
```

- Create ephemeral container per command
- Mount `$GOCLAW_HOME/workspace/` as `/workspace` (read-write)
- Network mode configurable (default: `none` for security)
- Auto-remove container after execution
- Timeout enforcement via context

**Config:** `internal/config/config.go`:
```yaml
tools:
  shell:
    sandbox: false           # enable Docker sandbox
    sandbox_image: "golang:alpine"
    sandbox_memory_mb: 512
    sandbox_network: "none"
```

**Dependency:** Add `github.com/docker/docker` to go.mod

---

### ~~P2-3: Message Bus / Event System~~ ✅

Implemented in `internal/bus/bus.go`, `internal/bus/bus_test.go`. Pub/sub with prefix matching, non-blocking fan-out, 100-event buffer per subscriber, concurrent-safe.

---

### ~~P2-4: Multi-Channel Messaging~~ ✅

Implemented in `internal/channels/telegram.go`. Supports Telegram bot integration with allowlist. Maps users to persistent sessions and routes replies via task completion polling.

**Why:** ref-goclaw supports 11 chat platforms. We only have ACP WebSocket.

**Start with Telegram** (highest value, good Go SDK):

**File to create:** `internal/channels/telegram.go` (~200 lines)
- Use `github.com/go-telegram-bot-api/telegram-bot-api/v5`
- Map incoming messages → `engine.CreateChatTask()`
- Map task completion → bot reply
- Config: `channels.telegram.token`, `channels.telegram.allowed_ids`
- Policy gate: `AllowCapability("channel.telegram")`

**File to create:** `internal/channels/channel.go` (~50 lines) — interface:
```go
type Channel interface {
    Name() string
    Start(ctx context.Context) error
    Stop() error
}
```

---

### ~~P2-5: Web Gateway / REST API (Phase 1)~~ ✅

Implemented in `internal/gateway/gateway.go`, `internal/gateway/gateway_api_test.go`. 6 endpoints: `GET /api/tasks`, `GET /api/tasks/{id}`, `GET /api/sessions`, `GET /api/sessions/{id}/messages`, `GET /api/skills`, `GET /api/config`. All require Bearer auth.

**Phase 2 — Static HTML dashboard (future):**
- Embed static HTML/CSS/JS via `go:embed`
- Single-page app with chat, tasks, skills, logs views

---

## P3 — Lower Priority (nice-to-have)

### P3-1: Browser Automation via CDP

**Ref:** ref-goclaw has 15 CDP-based browser tools. Out of scope per SPEC NG-001 (non-goal), but worth reconsidering.

**Decision:** Keep as non-goal unless user demand justifies it.

---

### P3-5: Terminal Line Wrapping in TUI ✅

**Issue:** Lines don't wrap when terminal is narrow — content just gets hidden.

**Location:** `internal/tui/chat_tui.go`

**Fix:** Use `m.width` (already tracked) to wrap long lines in `renderHistoryLines()`.

---

### ~~P3-2: Context Compaction~~ ✅

Implemented in `internal/engine/compactor.go`, `internal/engine/context_limits.go`, `internal/persistence/store.go` (archiving). Replaces naive truncation with intelligent LLM-based summarization when context limits are exceeded.

**Why:** rust_claw auto-summarizes old turns when context exceeds threshold. Prevents context window overflow on long sessions.

**File to create:** `internal/engine/compaction.go` (~100 lines)
- Monitor session message count / estimated token count
- When threshold exceeded (e.g., 50 messages or ~32K tokens): summarize oldest N messages into a single summary message
- Replace original messages with summary + recent messages
- Use LLM to generate summary (or simple extraction of key facts)

---

### ~~P3-3: MCP Client Support~~ ✅

Implemented in `internal/mcp/` (client, manager, transport) and `internal/tools/mcp_bridge.go`. Supports JSON-RPC 2.0 over stdio transport. Discovers tools and registers them with Genkit.

**Why:** Growing ecosystem of pre-built MCP servers (GitHub, Notion, Postgres, etc.).

**File to create:** `internal/tools/mcp/client.go` (~200 lines)
- JSON-RPC over HTTP/stdio
- Tool discovery via `tools/list`
- Tool execution via `tools/call`
- Register discovered MCP tools as Genkit tools

**Config:**
```yaml
mcp:
  servers:
    - name: "github"
      command: "npx @modelcontextprotocol/server-github"
      env:
        GITHUB_TOKEN: "${GITHUB_TOKEN}"
```

---

### ~~P3-4: Heartbeat System~~ ✅

Implemented in `internal/engine/heartbeat.go`. Periodically runs a system check based on `HEARTBEAT.md` checklist. Creates a task for the agent to review system status.

**Why:** rust_claw has periodic execution (default: 30min) that reads HEARTBEAT.md and runs agent turn.

**File to create:** `internal/engine/heartbeat.go` (~80 lines)
- Configurable interval (default: 30min)
- Reads `$GOCLAW_HOME/workspace/HEARTBEAT.md` checklist
- Creates a task with checklist prompt
- If findings, notifies via gateway event
- If nothing, marks as "HEARTBEAT_OK" (no notification)

---

## Testing Gaps

| # | Test | Package | Priority | Status |
|---|------|---------|----------|--------|
| T-1 | SIGKILL chaos test | `tools/verify/sigkill_chaos/` | HIGH | ✅ DONE |
| T-5 | TUI model unit tests | `internal/tui/` | LOW | ✅ DONE |
| T-6 | Telemetry logger tests | `internal/telemetry/` | LOW | ✅ DONE |
| T-8 | Genesis wizard flow test | `internal/tui/` | MEDIUM | ✅ DONE |

---

## Existing Work (Completed)

These items from previous TODOs are done:

- [x] Phase 1: Parser rewrite (3-stage ParseSkillMD in `internal/sandbox/legacy/skill.go`)
- [x] Phase 2: Multi-source skill loading (`internal/skills/loader.go`)
- [x] Phase 3: GitHub installation (`internal/skills/installer.go`, `cmd/goclaw/skill.go`)
- [x] Phase 4: Brain integration with progressive disclosure (`internal/engine/brain.go`)
- [x] Phase 5: Daemon wiring (`cmd/goclaw/main.go` startup sequence)
- [x] Skill registry schema (migration v3 with provenance columns)
- [x] Task sub-types for multi-step research (migration v4, `type` column)
- [x] `/metrics` authentication (Bearer token required)
- [x] `goclaw status` CLI command (`cmd/goclaw/status.go`)
- [x] `goclaw import` command (`cmd/goclaw/import.go`)
- [x] E2E research loop test (`internal/smoke/research_loop_test.go`)
- [x] No browser imports test (`internal/smoke/non_goals_test.go`)
- [x] P0-1: OpenRouter LLM provider (`internal/engine/brain.go`, config, genesis, import)
- [x] P0-2: LLM failover with circuit breakers (`internal/engine/failover.go`, `errors.go`)
- [x] P0-3: File system tools — read/write/list/edit (`internal/tools/file.go`)
- [x] P0-4: Shell execution tool with deny list (`internal/tools/shell.go`)
- [x] P0-5: use_skill tool for progressive disclosure (`internal/tools/useskill.go`)
- [x] P1-1: Prompt injection defense + leak detection (`internal/safety/`)
- [x] P1-2: Skill hot-reload watcher (`internal/skills/watcher.go`)
- [x] P1-3: Cron scheduler with schedules table (`internal/cron/scheduler.go`)
- [x] P1-4: Subagent spawn_task tool (`internal/tools/spawn.go`)
- [x] P2-1: Memory workspace Phase 1 (`internal/memory/workspace.go`, `internal/tools/memory.go`)
- [x] P2-3: Message bus pub/sub (`internal/bus/bus.go`)
- [x] P2-5: REST API Phase 1 — 6 endpoints (`internal/gateway/gateway.go`)
- [x] P3-2: Context Compaction (`internal/engine/compactor.go`)
- [x] P3-3: MCP Client (`internal/mcp/`)
- [x] P2-2: Docker Sandbox (`internal/tools/docker.go`)
- [x] P2-4: Multi-Channel Messaging (`internal/channels/telegram.go`)
- [x] P2-3: Doctor Diagnostics (`internal/doctor/`)
- [x] P3-4: Heartbeat System (`internal/engine/heartbeat.go`)
- [x] P3-1: OpenAI-compatible API (`internal/gateway/openai_handler.go`)
- [x] T-1: SIGKILL chaos test (`tools/verify/sigkill_chaos/main.go`)
- [x] T-8: Genesis wizard flow test (`internal/tui/genesis_flow_test.go`)

---

## Resolved Decisions

- **WASM compiler bundling (OQ-1)**: `tinygo` stays as external `$PATH` dependency. Pre-compiled `.wasm` loads without it.
- **SKILL.md format (OQ-2)**: Parser supports canonical Agent Skills spec (YAML frontmatter + markdown body) with backward compatibility for GoClaw V1 plain YAML format.
- **Browser automation (NG-001)**: Remains a non-goal per SPEC.
- **Distributed clustering**: Out of scope for v0.1/v0.2.
- **OpenRouter approach**: Use `openai_compatible` Genkit plugin with OpenRouter base URL — no custom provider code needed.
- **OpenAI-compatible API**: Full `/v1/chat/completions` endpoint with streaming support, stateless-to-stateful bridge via ephemeral sessions.
- **agent.chat.stream**: WebSocket streaming via `agent.chat.stream` RPC method, pushes chunks as notifications.
