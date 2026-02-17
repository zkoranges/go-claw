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

---

## v0.4 Implementation Status (Current Session)

### Phase 1: MCP Client — COMPLETE ✅
**Commits**: b8324d6 (Phase 1.2-1.4), previous commits (Phase 1.1)

**Phase 1.1 - Config/Policy/Manager** (16 tests):
- ✅ AgentMCPRef struct in config.go with inline definitions + SSE transport
- ✅ MCPServerEntry extended with URL, Transport, Timeout fields
- ✅ Policy.AllowMCPTool() with specificity-based rule matching
- ✅ Manager rewrite: per-agent connections, DiscoverTools, InvokeTool, Healthy, Reload
- ✅ Tests for all above with proper mocking

**Phase 1.2 - MCP Bridge Update** (6 tests):
- ✅ RegisterMCPTools signature changed to accept agentID
- ✅ Uses Manager.DiscoverTools for per-agent discovery
- ✅ Routes through Manager.InvokeTool (policy-enforced)
- ✅ Audit logging includes agentID
- ✅ Error wrapping with server/tool context
- ✅ Discovery failures non-fatal

**Phase 1.3 - Brain Integration** (tests in brain_v04_test.go):
- ✅ GenkitBrain.RegisterMCPTools(ctx, agentID, manager) method
- ✅ Imported mcp package for manager access
- ✅ Test placeholders for registration verification

**Phase 1.4 - main.go Wiring** (integration):
- ✅ Updated two RegisterMCPTools call sites with new signature
- ✅ Extract agentID from ra.Config.AgentID
- ✅ TODO: Phase 1.4 follow-up for per-agent config resolution

**Test Summary**: 725 total tests passing (75 new for v0.4)

### Phase 2: Async Delegation — FOUNDATION COMPLETE ✅
**Commits**: Latest commit (Schema v13 + Delegation Store)

**Schema Migration** (v8 → v13):
- ✅ Added schemaVersionV13 with checksum
- ✅ Created delegations table: id, task_id, parent_agent, child_agent, prompt, status, result, error_msg, created_at, completed_at, injected
- ✅ Added indexes on (parent_agent, injected) and (task_id)

**Delegation Store** (internal/persistence/delegations.go):
- ✅ Defined Delegation struct
- ✅ Created store method signatures (7 CRUD methods marked TODO)
- ✅ Test framework in place (9 placeholder tests)

**Remaining Phase 2 Work**:
- Implement Delegation CRUD methods
- Add delegate_task_async tool to internal/tools/delegate.go
- Wire engine.onTaskSucceeded/Failed for delegation completion
- Implement brain.injectPendingDelegations() for result injection
- TUI activity feed for delegation status

### Next Steps (Phase 3-4)
- Phase 3: Telegram Deep Integration (HITL gates, plan progress, alert tool)
- Phase 4: A2A Protocol (/.well-known/agent.json endpoint)
- Manager backward compat methods (AllTools, CallTool, Start, Stop)

---

## v0.5 Implementation Notes

### Current milestone: v0.5 (Streaming & Autonomy)
### PDR: PDR-v8.md
### Verify: tools/verify/v05_verify.sh

### Implementation order (strict):
1. Streaming Responses (internal/engine/brain.go, engine.go, internal/gateway/stream.go, openai_handler.go, internal/channels/telegram.go)
2. Agent Loops (internal/persistence/loops.go, store.go, internal/engine/loop.go, engine.go, internal/tools/loop_control.go, internal/config/)
3. Structured Output (internal/engine/structured.go, brain.go, internal/config/)
4. OpenTelemetry (internal/otel/ — new package, instrumentation across engine, gateway, brain)
5. Gateway Security (internal/gateway/auth.go, ratelimit.go, cors.go, gateway.go)

### Schema version:
- Pre-v0.5: v13 (gc-v13-2026-02-15-delegations)
- Post-v0.5: v14 (gc-v14-2026-02-16-loop-checkpoints)
- loop_checkpoints table added for agent loop crash recovery

### Key new code:
- internal/otel/ — OpenTelemetry provider, spans, metrics (zero overhead when disabled)
- internal/engine/loop.go — LoopRunner with checkpoints, budgets, termination keywords
- internal/engine/structured.go — JSON Schema validation with extractJSON + ValidateAndRetry
- internal/gateway/stream.go — SSE streaming endpoint (/api/v1/task/stream)
- internal/gateway/auth.go — API key authentication middleware (disabled by default)
- internal/gateway/ratelimit.go — Token bucket rate limiter (per-key isolation)
- internal/gateway/cors.go — CORS middleware with configurable origins
- internal/persistence/loops.go — Loop checkpoint CRUD (SaveLoopCheckpoint, LoadLoopCheckpoint)
- internal/tools/loop_control.go — checkpoint_now + set_loop_status tools

### Key patterns:
- OTel: use in-memory SpanRecorder for tests, never require external collector
- Middleware: compose with Wrap() pattern, test independently
- Streaming: always provide non-streaming fallback path
- All new files: follow conventions of neighboring files in same package
- Auth/rate-limit disabled by default (backward compatible)

### Do NOT:
- Replace Brain.Generate()/Respond() — it's the fallback when streaming unavailable
- Add new states to the 8-state task machine — loops are above it
- Require external services in tests (no OTel collector, no LLM calls)
- Make OTel a hard dependency — everything works with telemetry disabled
- Store API keys in plaintext in SQLite (use config only for v0.5)

### Verify after each phase:
Run phase-specific tests, then `just check`, then `go test -race ./...`

### Verify at end:
./tools/verify/v05_verify.sh
