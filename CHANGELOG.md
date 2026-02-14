# Changelog

All notable changes to GoClaw are documented in this file.

## [0.1-dev] - 2026-02-14

### Initial Implementation (a1e96fe)

The initial commit established the complete GoClaw codebase: a durable-by-default local AI agent orchestration daemon. 159 files, approximately 40,000 lines of Go.

Core systems delivered:

- **Persistence layer**: SQLite WAL store with schema migrations (v2-v7), 8-state task model (QUEUED -> CLAIMED -> RUNNING -> SUCCEEDED/FAILED/RETRY_WAIT/CANCELED -> DEAD_LETTER), lease-based claiming, retry with exponential backoff, dead-letter queue, crash recovery, priority aging, configurable retention
- **Task engine**: Worker lane pools, backpressure (queue depth limits), streaming execution, heartbeat-based lease renewal, task abort with context cancellation
- **LLM brain**: Firebase Genkit integration with Google Gemini, Anthropic, OpenAI, OpenRouter providers. Tool-call dispatch, context compaction, failover with circuit breaker
- **ACP gateway**: WebSocket JSON-RPC 2.0 server with 20+ methods, Bearer token auth, origin allowlist, FIFO replay, backpressure close. REST API for tasks/sessions/skills/config. OpenAI-compatible /v1/chat/completions endpoint
- **Policy engine**: Default-deny capability-based access control, domain allowlist with SSRF protection, path allowlist, hot-reload via fsnotify, version tracking
- **WASM sandbox**: wazero v1.11 host with memory limits, execution timeouts, fault classification (TIMEOUT, MEMORY_EXCEEDED, NO_EXPORT, FAULT, QUARANTINED), quarantine system, hot-swap
- **Skill system**: SKILL.md discovery (project > user > installed > builtin), eligibility checks (bins, env, OS), collision detection, installation from URL/path, DB registry, fsnotify watcher
- **Multi-agent**: Registry with per-agent provider/model/workers/timeout/policy, persistence and restore, runtime creation/removal, config hot-reload via reconcileAgents()
- **TUI**: Bubbletea chat interface with agent/model/session selectors, slash commands (/help, /agents, /skills, /model, /config, /session, /allow, /domains), genesis setup wizard, input history, emacs keybindings, streaming display
- **Observability**: Structured JSON logging with secret redaction, dual-write audit (JSONL + DB), /healthz, /metrics (JSON + Prometheus), trace ID propagation, task event recording
- **Built-in tools**: Shell execution (deny list, injection blocking, Docker sandbox option), filesystem ops (read/write/edit/list with policy checks), web search (Brave/Perplexity/DuckDuckGo), URL reader, memory workspace, MCP client bridge, process spawning
- **Channels**: Telegram bot integration with @agentid routing and allowed_ids security
- **Cron**: Scheduler with robfig/cron expressions
- **Other**: Event bus, KV store, input sanitization, leak detection, doctor self-checks, import command, status command

Also included: SPEC.md (v2.0.0, 101 normative requirements), PDR.md (design rationale), CI workflow, parity tracking tools, verification tools.

### Codebase Cleanup (5813c36)

- Removed obsolete documentation: USER_STORIES.md, ASSUMPTIONS.md, COMPARE_RUST_REFERENCE.md, LEGACY_SKILLS_LIMITATIONS.md, RISK_REGISTER.md, TRACEABILITY.md, VERIFY_REPORT.md
- Removed verification tools that were superseded: acp_ws_check, backup_restore_drill, chaos_kill, incident_export, lease_recovery_crash, non_goals_audit, policy_default_check, sigkill_chaos
- Trimmed TODO.md from 276 lines to essential items
- Added ChatTaskRouter interface to engine package for decoupling heartbeat/channels from agent package (avoids import cycle)
- Added multi-agent routing to Telegram (`@agentid message` prefix)
- Improved TUI agent commands and session handling

### Code Refinement (00d4546)

- Removed unused code and simplified engine, brain, and store implementations
- Added install script and LICENSE (MIT)
- Cleaned up test helpers and smoke tests
- Improved justfile with additional commands

### Multi-Provider and API Polish (2dd13b6)

- Updated README with comprehensive documentation
- Improved OpenAI-compatible endpoint handling
- Enhanced brain initialization for multiple providers
- Improved store operations for agent management

### Inter-Agent Communication (e3e09e2)

- Added `delegate_task` tool: blocking delegation from one agent to another with timeout (default 120s, max 300s), cancel propagation, and self-delegation guard
- Added `send_message` / `read_messages` tools: async inter-agent messaging via `agent_messages` DB table with backpressure (1000 unread cap) and self-send guard
- Added `spawn_task` tool: non-blocking task creation with parent linkage
- Added agent context propagation via `shared.WithAgentID()` / `shared.AgentID()`
- Added schema v7 migration for `agent_messages` table
- Added TUI agent selector sub-view with interactive navigation
- Expanded TUI `/agents` command: new, remove, switch, team creation

### Inter-Agent Bug Fixes (c41cf66)

- Fixed delegate_task payload format: wrap prompt in `chatTaskPayload` JSON (`{"content":"..."}`) matching engine's expected format
- Fixed self-delegation deadlock guard using `shared.AgentID(ctx)` comparison
- Fixed send_message target agent existence check via DB query
- Fixed message backpressure check for unread message count
- Added DeleteAgent cascade to also delete from `agent_messages` table
- Added `delegate_task` and `send_message` to policy knownCapabilities

### Policy Cleanup (edbf71f)

- Reorganized policy capability validation

### Skill Watcher Fix (4f7ba49)

- Fixed skill watcher not detecting directory removal events by handling fsnotify Remove events in addition to Create/Write

### TUI Agent Usability (47b6b9d)

- Improved `/agents` command output formatting and help text
- Enhanced agent creation flow in TUI
- Better error messages for agent operations
