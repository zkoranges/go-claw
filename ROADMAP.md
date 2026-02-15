# GoClaw Roadmap

**Vision**: The Ollama of multi-agent AI. One binary, terminal-native, persistent AI specialists that know your project.

**Current state**: v0.2-dev. Core daemon works. Multi-agent TUI, multi-provider LLM, SQLite persistence, WebSocket API, inter-agent messaging, policy engine, WASM skills, Telegram, cron, delegation plumbing, DAG executor. 556+ tests passing.

---

## v0.2 — The Happy Path

**Theme**: First-run experience that works. Install, type `@coder fix this bug`, get value.

**Duration**: 2-3 weeks

### @Mentions
- `@agent` routes single message to agent, `@@agent` switches sticky context
- Replaces `/agent switch` as primary interaction
- Parser: `@` + alphanumeric/hyphens, terminated by whitespace, prefix position
- Highlight @tokens in cyan in TUI input

### Agent Creation Modal
- `Ctrl+N` or `/agent new` opens Bubbletea form overlay
- Fields: ID, Model (dropdown from configured providers), Soul/Prompt
- Checkbox: Save to config.yaml (default on)
- Soul field: inline text for quick agents, file reference (`soul: ./agents/coder.md`) for serious ones

### Starter Agents
- 3 pre-installed agents on first run: `@coder`, `@researcher`, `@writer`
- High-quality soul prompts, not stubs
- `@coder`: senior developer, knows common languages, debugging, refactoring, code review
- `@researcher`: web search, summarization, source tracking, comparative analysis
- `@writer`: documentation, technical writing, blog posts, commit messages
- Generated into config.yaml on first run only (user can modify/delete)

### `goclaw pull`
- `goclaw pull <url>` — fetch agent config from URL, validate, add to config
- Supports: raw GitHub URLs, Gist URLs, any HTTPS URL returning valid YAML
- Validation: must parse as valid agent config, rejects malformed input
- No registry server — decentralized by design
- Enables: `goclaw pull gist.github.com/user/abc123` shared on Twitter/Slack
- Private repos work if URL is accessible (internal GitLab, authenticated URLs)

### Activity Feed
- Persistent status area above input line, collapsible with hotkey
- Shows: active delegations, plan step progress, completed actions with cost/time
- Subscribes to bus events from delegation and plan execution
- Auto-clears completed items after 30 seconds
- Default: collapsed. Expands when background work starts.

### Polish
- Error messages rewritten for humans (not Go error chains)
- TUI help updated with @mention syntax
- First-run guided setup: detect missing API keys, suggest providers
- `/plan` output as formatted table, not raw JSON

---

## v0.3 — Context and Memory

**Theme**: Agents that know what you're working on and remember what you've decided.

**Duration**: 3-4 weeks

### Context Pinning
- `/pin main.go utils.go config/` — read files into agent context
- `/unpin main.go` — remove from context
- `/pinned` — list currently pinned files
- Implementation: read file content, inject into system prompt or conversation context
- No AST parsing, no vector DB, no RAG — just read the file into the context window
- Works because modern models have 200K+ context windows
- Pinned files persist per agent per session (stored in SQLite)
- File watcher: re-read pinned files when they change on disk

### Agent Memory
- SQLite table: `agent_memories` (agent_id, key, content, source, created_at, relevance_score)
- Explicit memory: `@coder remember we use pgx v5, not database/sql`
- Memory retrieval: relevant memories injected per conversation turn
- `/memory list` — show what agent knows, with timestamps
- `/memory search <query>` — find specific memories
- `/memory delete <id>` — remove a memory
- Users must be able to see and delete everything the agent "knows"

### Auto-Memory (Conservative)
- Extract key decisions only when agent explicitly states a decision
- Pattern: "I'll remember that..." or "Noted: ..." triggers memory save
- No silent extraction — user always sees when memory is created
- Relevance decay: unused memories score lower over time

### Shared Team Knowledge
- Cross-agent memory access scoped by project
- Security agent findings visible to code agent within same project
- Uses task_context table as foundation
- Explicit sharing: `@security share finding-42 with @coder`

### Executor Error-as-Input
- When a plan step fails, feed error output back to the agent as a new message
- Agent gets: "Step failed with: <error output>. Attempt to fix and retry."
- Configurable retry limit per step (default: 2 retries)
- Not a separate system — extends existing agent conversation loop
- Hard failure only after retries exhausted

---

## v0.4 — Tools and Reach

**Theme**: Agents that touch real systems and are reachable everywhere.

**Duration**: 3-4 weeks

### MCP Client (First Class)
- MCP client exists — promote to primary tool integration path
- Per-agent MCP server configuration in YAML
- Auto-discover tools from connected MCP servers
- Policy engine governs which agents can use which MCP tools
- Ship tested configs for: GitHub, filesystem, PostgreSQL, Notion
- Do not build custom integrations — MCP servers already exist for everything

### Telegram Deep Integration
- Plan progress updates pushed to Telegram
- HITL approval gates via inline buttons
- `/plan` command from Telegram triggers workflows
- Proactive alerts: "Security scan found 3 critical issues"
- Rich formatting: code blocks, tables, inline results
- Voice messages → text → agent task (stretch goal)

### True Async Delegation
- Engine-level support for mid-conversation context injection
- `delegate_task_async` returns immediately, agent continues chatting
- Result injected when ready: "@security finished: found 2 issues"
- TUI notification with option to view full result

### A2A Protocol (Experimental)
- Read-only agent card at `/.well-known/agent.json`
- GoClaw agents discoverable by external A2A clients
- Passive only — signals interoperability, minimal maintenance
- Do not invest in debugging other implementations
- Upgrade to full A2A when ecosystem matures

---

## v0.5 — Autonomy

**Theme**: Agents that plan, adapt, and work while you're away.

**Duration**: 4-6 weeks

### LLM-Generated Plans (Pattern 4)
- New tool: `create_plan` — agent generates DAG from natural language
- Planning prompt with decomposition examples
- Agent selects targets by capability match
- Human approval gate before execution (configurable: auto/approve/review)
- "Review this PR" → agent builds research + security + code + docs pipeline

### Smart Routing
- System suggests best agent for a task based on capability matching
- "This looks like a security question — routing to @security" (with override)
- Learns from routing corrections via memory system

### Context Compaction v2
- Extract key facts into agent memory before compacting old messages
- Conversations get shorter but knowledge persists
- Agent references decisions from 100 conversations ago via memory, not context

### More Templates
- Expand to 8-10 agents: security-auditor, devops-monitor, test-engineer, architect, project-manager
- `/agent install security-auditor` — one command setup
- Templates include: soul prompt, capabilities, suggested MCP servers, example plans
- Community-contributed templates via `goclaw pull`

---

## v1.0 — Production

**Theme**: Stable, documented, community-ready.

**Duration**: 4-6 weeks

### Stability
- All 4 interaction patterns reliable
- Graceful degradation: one agent failure doesn't cascade
- Rate limit handling across all providers
- Connection recovery for Telegram and WebSocket
- Comprehensive error messages for every failure mode

### Performance
- Agent startup < 500ms
- Pinned file loading < 1s for typical project
- Memory retrieval < 100ms per turn
- Concurrent plan execution with resource limits

### Documentation
- Getting started guide (5 minutes to first agent)
- Agent authoring guide (writing good souls)
- Plan authoring guide (workflow design patterns)
- API reference (WebSocket, REST, OpenAI-compat)
- Architecture guide for contributors

### Community
- Plugin system for custom tools (Go interface)
- `goclaw pull` ecosystem matured — curated list of community agents
- Plan sharing format (importable YAML)
- Changelog and migration guides

### OpenClaw Protocol
- Formalize the protocol GoClaw implements
- Specification: agent discovery, capability declaration, delegation contract, plan format, context sharing, event schema
- Reference implementation IS GoClaw
- Opens door for alternative implementations

---

## Summary

| Version | Theme | Hook | Duration |
|---------|-------|------|----------|
| **v0.2** | Happy Path | Install, `@coder fix this`, it works | 2-3 weeks |
| **v0.3** | Context & Memory | Agent knows your project, remembers decisions | 3-4 weeks |
| **v0.4** | Tools & Reach | Agents open PRs, read DBs, alert on Telegram | 3-4 weeks |
| **v0.5** | Autonomy | "Review this PR" → multi-agent pipeline runs | 4-6 weeks |
| **v1.0** | Production | Stable, documented, community ecosystem | 4-6 weeks |

**Total to v1.0**: ~4-6 months

---

## Principles

1. **Each version is independently useful.** v0.2 is a better chat tool even if you stop there. v0.3 adds memory that works without v0.4's MCP. No version requires future versions to deliver value.

2. **Persistent context is the moat.** Every feature should either build agent knowledge or leverage it. If a feature doesn't make agents smarter over time, question its priority.

3. **Local-first is non-negotiable.** No feature requires a cloud service. Cloud integrations (MCP servers, A2A) are optional enhancements, not dependencies.

4. **Don't build what the model can do.** No project type detection heuristics — the LLM reads your files and adapts. No custom integrations — MCP servers already exist. No semantic indexing — context windows are big enough. Build infrastructure, not intelligence.

5. **Decentralize the ecosystem.** `goclaw pull` from any URL. No central registry to maintain. Community shares agents via Gists, repos, and blog posts. The viral loop is the sharing mechanism, not a platform.

6. **Resist the framework trap.** GoClaw is a tool you run, not a library you import. Every feature is configurable in YAML and usable from the TUI. If it requires writing Go code to use, it's wrong.
