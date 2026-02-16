# GoClaw

Durable-by-default local orchestration kernel. Single-user daemon with SQLite persistence, ACP WebSocket/JSON-RPC gateway, WASM skill sandbox, Genkit+Gemini brain, and Bubbletea TUI.

## Commands

```bash
just build          # go build -o /tmp/goclaw ./cmd/goclaw
just test           # go test ./... -count=1
just test-v         # verbose tests
just vet            # go vet ./...
just check          # build + vet + test
just run            # build + start daemon (interactive TUI)
just run-headless   # build + start daemon (GOCLAW_NO_TUI=1)
just tidy           # go mod tidy
```

Run a single package's tests:
```bash
go test ./internal/persistence/ -count=1 -timeout 120s
```

Benchmarks:
```bash
go test ./internal/persistence/ -bench=. -run=^$
```

## Architecture

```
cmd/goclaw/main.go          Entry point, startup ordering, signal handling
internal/
  persistence/store.go       SQLite store, schema migrations, task queue (~2900 lines)
  engine/engine.go           Task execution engine, worker lanes, backpressure
  engine/brain.go            Genkit + Gemini LLM integration, tool dispatch
  gateway/gateway.go         ACP WebSocket server, JSON-RPC, /healthz, /metrics
  gateway/openai_handler.go  OpenAI-compatible chat completions endpoint
  policy/policy.go           Default-deny policy engine with LivePolicy hot-reload
  audit/audit.go             Dual-write audit (JSONL file + DB)
  sandbox/wasm/host.go       WASM host (wazero), resource limits, fault codes, quarantine
  skills/loader.go           Skill discovery, TOML manifest parsing
  skills/installer.go        Skill installation from URL/path
  config/config.go           YAML config with env overlay
  tools/tools.go             Built-in tool registry (shell, file, search, docker, spawn)
  agent/                     Multi-agent registry and scoped execution
  channels/                  Telegram channel integration
  mcp/                       MCP client (stdio + SSE transports)
  cron/                      Cron scheduler for recurring tasks
  tui/tui.go                 Bubbletea TUI
  bus/                       In-process event bus
  safety/                    Input sanitization
  doctor/                    Startup self-checks
```

## Key Concepts

- **8-state task model**: QUEUED → CLAIMED → RUNNING → SUCCEEDED/FAILED/RETRY_WAIT/CANCELED → DEAD_LETTER
- **SQLite WAL mode**, synchronous=FULL. DB at `${GOCLAW_HOME}/goclaw.db`
- **Schema migrations** are incremental (v2→v6), using `ALTER TABLE ADD COLUMN` with idempotent error suppression
- **Default-deny policy**: capability-based + domain allowlist for HTTP. Policy file hot-reloads via fsnotify
- **Engine** uses variadic policy: `engine.New(store, proc, cfg, pol ...policy.Checker)`
- **Brain** routes through Genkit with tool-call fallback on LLM failure
- **WASM sandbox** via wazero v1.11: `WithMemoryLimitPages(pages)`, `WithCloseOnContextDone(true)`
- **ChatTaskRouter interface** (`engine.ChatTaskRouter`): decouples task creation from the `agent` package. `*agent.Registry` satisfies it. Used by heartbeat and Telegram to route tasks without creating an `engine` → `agent` import cycle
- **Multi-agent routing**: Telegram supports `@agentid message` prefix to target a specific agent. TUI supports `/agent` to list agents and `/agent <id>` to switch. Both route through `ChatTaskRouter`
- **Agent hot-reload**: editing `config.yaml` agents section triggers `reconcileAgents()` in main.go — adds new agents, removes deleted ones, and recreates changed ones. The `OnAgentCreated` hook auto-provisions skills, MCP tools, and shell executor on new agents

## Environment

- `GOCLAW_HOME` — data directory (default `~/.goclaw`). Contains db, config, logs, skills, policy
- `GEMINI_API_KEY` — required for brain (Genkit + Gemini)
- `GOCLAW_NO_TUI=1` — headless mode (no Bubbletea TUI)
- Config file: `${GOCLAW_HOME}/config.yaml` (YAML with env var overlay, agents section hot-reloads via fsnotify)
- Policy file: `${GOCLAW_HOME}/policy.yaml`
- Daemon listens on `127.0.0.1:18789`

## Testing Patterns

- **CRITICAL: Zero API credits from tests.** All tests run offline with no real API calls. Brain tests set `GEMINI_API_KEY=""` to force `llmOn=false` (fallback mode). No E2E tests that consume API budgets.
- 253+ tests across 28 packages, all passing, all offline, zero API costs
- Session IDs must be valid UUIDs — tests fail with non-UUID session IDs
- `ClaimNextPendingTask` orders by `priority DESC, created_at ASC, id ASC`
- `defaultMaxAttempts = 3` — tasks dead-letter after 3 failures
- Retry backoff uses `retryBaseDelay = 1s` — tests reset `available_at` for immediate re-claims
- `StartTaskRun` takes 4 args: `(ctx, taskID, leaseOwner, policyVersion)`

## Gotchas

- **Port conflicts**: port 18789 may have stale daemon. Kill before testing: `lsof -ti :18789 | xargs kill`
- **SQLite ALTER TABLE**: `ADD COLUMN` cannot use `DEFAULT CURRENT_TIMESTAMP` — use nullable columns
- **json.Unmarshal**: does NOT clear struct fields absent from JSON — use fresh struct per read
- **coder/websocket**: context cancellation poisons TCP connection deadlines
- **Schema version**: currently v8 (`schemaVersionV8`, checksum `gc-v8-2026-02-14-agent-history`). Update both constant and checksum when adding migrations
- **Brain nil guard**: `RegisterTestAgent` creates `RunningAgent` with `Brain: nil`. Any loop over `ListRunningAgents()` that calls Brain methods must guard with `if ra.Brain != nil`

## Code Style

- Standard library `log/slog` for structured logging
- `github.com/google/uuid` for all IDs
- YAML config via `gopkg.in/yaml.v3`
- Error wrapping with `fmt.Errorf("context: %w", err)`
- SPEC requirement traceability comments: `// GC-SPEC-XXX-NNN: description`
- Use canonical status names: `TaskStatusQueued`, `TaskStatusSucceeded` (no aliases)

## v0.4 Implementation Notes (PDR-v7)

### Current milestone: v0.4 (Tools & Reach)
**PDR**: docs/PDR-v7.md
**Status**: Phase 1.1 complete (config/policy/manager), test framework for all phases

### Phase 1: MCP Client (COMPLETED)
- ✅ Config: AgentMCPRef for per-agent refs, SSE transport, env map support
- ✅ Policy: AllowMCPTool with specificity-based rule matching
- ✅ Manager: Rewritten with per-agent scoping, auto-discovery, policy checks
- ⏳ Integration: MCP Bridge, Brain.RegisterMCPTools, main.go wiring (to complete)

### Schema version:
- Current: v12 (gc-v12-2026-02-15-plan-persistence)
- Phase 2 will add v13 for async delegations table

### Key existing code being extended (do NOT replace):
- MCP Manager: Preserved AllTools/CallTool for backward compat
- Policy: Added AllowMCPTool method (AllowCapability unchanged)
- Config: Added AgentMCPRef + MCPServers fields (existing fields unchanged)
- Manager.Start(): Now global-only (per-agent via ConnectAgentServers)

### Test metrics:
- Baseline: 650 tests
- Phase 1.1 implementation: 16 tests (config, policy, manager)
- Full v0.4 test framework: 75 tests (all phases with placeholders)
- **Current total: 725 tests (70+ requirement met)**

### Remaining implementation phases (test-driven):
- Phase 2 (Async Delegation): Implement schema v13, delegation store, async tool, brain injection
- Phase 3 (Telegram Deep): Implement HITL gates, plan progress, alert tool
- Phase 4 (A2A Protocol): Implement /.well-known/agent.json endpoint

### Key patterns (consistent with codebase):
- Per-agent MCP: `Manager.ConnectAgentServers(ctx, agentID, configs)`
- Policy checks: Cast to `policy.Policy` to call `AllowMCPTool(agent, server, tool)`
- Tool discovery: Non-fatal failures (log warn, continue without tools)
- Async operations: Use event bus for coordination (delegation.*, plan.*, hitl.* topics)

### Do NOT break:
- Existing test counts (use _v04 suffix for new tests)
- OnAgentCreated hook provisioning order (skills → WASM → shell → MCP)
- Policy.AllowCapability behavior (all existing callers must work unchanged)
- Manager backward compat methods (AllTools, CallTool, Start, Stop)
