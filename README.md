# GoClaw

[![CI](https://github.com/zkoranges/go-claw/actions/workflows/ci.yml/badge.svg)](https://github.com/zkoranges/go-claw/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![SQLite](https://img.shields.io/badge/SQLite-WAL-003B57?logo=sqlite&logoColor=white)](https://sqlite.org)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**Run AI agents locally with crash recovery, tool access, and zero cloud dependencies.**

GoClaw is a single-binary daemon that queues agent tasks in SQLite, executes them through LLM providers (Gemini, Claude, GPT, OpenRouter, Ollama), and guarantees nothing is silently lost — even if you `kill -9` the process mid-task. Orphaned work is automatically recovered on restart.

```
$ goclaw
GoClaw v0.5-dev | SQLite WAL | 3 workers | localhost:18789
coder — gemini-2.5-pro
> @coder implement a fibonacci function in Go
[task queued] id=a1b2c3 status=QUEUED
[coder] Here's a simple fibonacci function...
```

## Why

Most agent frameworks treat task execution as ephemeral — if the process dies, queued and in-flight work disappears. GoClaw treats agent tasks like a database treats transactions: every state change is persisted before it's acknowledged. Built on [Firebase Genkit](https://github.com/firebase/genkit) for structured tool-use orchestration and Go's goroutine-based concurrency for parallel execution across worker pools.

## Features

### Core

- **Durable task queue.** SQLite WAL with lease-based ownership. Tasks survive crashes. Orphaned work is automatically reclaimed on restart.
- **8-state task machine.** QUEUED → CLAIMED → RUNNING → SUCCEEDED/FAILED/RETRY_WAIT/CANCELED → DEAD_LETTER. Every transition is transactional with an append-only audit trail.
- **Multi-provider LLM brain.** Gemini (default), Anthropic, OpenAI, OpenRouter, Ollama — with automatic failover. Tool calls are schema-validated before execution. Ollama models auto-detect tool support via `/api/show`. Switch providers at runtime via `/model`.
- **Context compaction.** When conversation history approaches the context window, older messages are summarized via LLM and archived. Recent messages stay intact.
- **@Mentions.** Route messages to specific agents with `@coder <msg>` syntax. Sticky agent switching with `@agent` shorthand.
- **Starter agents.** Three built-in agents (coder, researcher, writer) available on first run. Create custom agents via Ctrl+N or pull from URLs with `goclaw pull`.
- **Community agent library.** `goclaw pull <url>` fetches agent configs from any HTTPS URL, validates them, and adds to your local setup.

### Context & Memory (v0.3)

- **Conversation history.** Auto-compaction of old messages with LLM summarization. Token counts tracked and displayed via `/context`.
- **Core memory.** Persistent facts stored per agent via `/remember <key> <value>` and `/memory` commands. Relevance decay over time.
- **Pinned context.** Pin files or text snippets for ongoing use. File watcher detects changes and auto-updates pins. Useful for code, docs, and specs.
- **Agent memory sharing.** `/share <memory> with <agent>` — broadcast memories to teammates. Wildcard shares (`/share all with @writer`) grant team-wide access.
- **Executor retry with error context.** Failed plan steps automatically retry with the previous error injected as context. Agents see what broke and why.
- **Context budget visibility.** `/context` command shows token allocation across soul, memory, pins, shared context, summaries, and messages. Know when you're running low.

### Tools & Integration (v0.4)

- **MCP client.** Per-agent MCP server connections (stdio + SSE transport) with policy-controlled tool discovery. Define servers per agent in `config.yaml`.
- **Inter-agent delegation.** `delegate_task` tool for blocking task handoff between agents with hop counting, deadlock prevention, and capability-based routing.
- **A2A protocol.** `GET /.well-known/agent.json` endpoint for agent-to-agent discovery and interoperability.
- **Telegram deep integration.** Human-in-the-loop approval gates, plan progress updates, and alert tool for proactive notifications.

### Streaming & Autonomy (v0.5)

- **Streaming responses.** SSE endpoint (`/api/v1/task/stream`) for real-time token delivery. OpenAI-compatible streaming with tool-call deltas. Telegram progressive message editing during generation.
- **Agent loops.** Autonomous iteration with configurable budgets (token, step, iteration), termination keywords, and crash-recovery checkpoints persisted to SQLite.
- **Structured output.** JSON Schema validation with `extractJSON` for mixed LLM output and `ValidateAndRetry` for automatic re-prompting on schema violations.
- **OpenTelemetry.** Traces and 10 metric instruments (histograms, counters, up-down counters) with configurable OTLP exporters. Zero overhead when disabled.
- **Gateway security.** API key authentication (Bearer, X-API-Key, query param), per-key token bucket rate limiting, and configurable CORS. All disabled by default.

### Safety and control

- **Default-deny policy engine.** Capability-based access control with domain allowlists. Hot-reloads via fsnotify; invalid config fails closed.
- **WASM skill sandbox.** Memory limits, CPU fuel metering, execution timeouts, fault-count quarantine, and two-phase hot reload with rollback. Powered by [wazero](https://wazero.io) (pure Go, no CGo).
- **Built-in tools.** Shell execution (with optional Docker sandboxing), filesystem ops, web search (Brave / Perplexity / DuckDuckGo), MCP client, process spawning.

### Operations

- **Multi-agent support.** Named agents with independent brains, worker pools, and task queues. Define them in `config.yaml` and hot-reload at runtime.
- **Interactive TUI.** Chat, agent switching (`/agent`), skill management, model selection — built with [Bubbletea](https://github.com/charmbracelet/bubbletea).
- **OpenAI-compatible API.** Drop-in `/v1/chat/completions` endpoint with streaming (SSE), sampling parameters (`temperature`, `top_p`, `max_tokens`, `stop`), structured output (`response_format`), real-time tool-call visibility in streaming chunks, and split `usage` reporting. Works with the Python `openai` SDK, `curl`, IDE plugins, and any OpenAI-compatible client. Agent routing via `model: "agent:<id>"`.
- **Multi-channel input.** Telegram bot (with `@agent` routing), ACP WebSocket gateway (JSON-RPC 2.0), OpenAI-compatible REST API.
- **Cron scheduler.** Recurring tasks with standard 5-field cron expressions.
- **Observability.** Structured JSON logs, dual-write audit (file + DB), `/healthz` and `/metrics` endpoints.

## Use cases

- **Persistent personal assistant.** A local agent that stays running, remembers context across sessions, and acts on scheduled tasks — without depending on a cloud service.
- **Agent teams.** Specialized agents (researcher, coder, reviewer) each with their own LLM provider, system prompt, and tool access. Route tasks via ACP, Telegram, or the TUI.
- **Unattended automation.** Queue work and walk away. Cron triggers, heartbeat monitoring, retry-with-backoff. Failures dead-letter instead of disappearing.
- **Self-hosted AI gateway.** Full OpenAI-compatible API backed by any provider, with streaming tool visibility, sampling parameter passthrough, structured output, policy controls, and audit logging that upstream APIs don't offer.
- **WASM skill sandbox.** Develop skills with memory limits and fault quarantine. Hot-reload during development. A buggy skill can't take down the daemon.
- **Local MCP host.** Connect MCP servers (stdio or SSE) and expose their tools through a policy-controlled interface.

## Install

Requires Go 1.24+ and git.

```bash
# one-liner
curl -fsSL https://raw.githubusercontent.com/zkoranges/go-claw/main/install.sh | bash

# or from source
git clone https://github.com/zkoranges/go-claw.git && cd go-claw && ./install.sh
```

Installs to `/usr/local/bin` (configurable via `INSTALL_DIR`) and creates `~/.goclaw`.

## Quick start

Set an LLM API key:

| Provider | Env var |
|---|---|
| Google Gemini (default) | `GEMINI_API_KEY` |
| Anthropic | `ANTHROPIC_API_KEY` |
| OpenAI | `OPENAI_API_KEY` |
| OpenRouter | `OPENROUTER_API_KEY` |
| Ollama (local) | None — runs locally on `localhost:11434` |

```bash
export GEMINI_API_KEY="your-key"
goclaw              # interactive TUI
goclaw --daemon     # headless, logs to stdout
```

First run generates `config.yaml`, `policy.yaml`, `SOUL.md`, `auth.token`, and the SQLite database under `~/.goclaw`. Three starter agents (coder, researcher, writer) are available immediately. Create new agents with Ctrl+N or pull from community URLs using `goclaw pull <url>`.

### Development

```bash
just build          # compile to /tmp/goclaw
just run            # build + launch (interactive TUI)
just run-headless   # build + launch (headless)
just test           # go test ./... -count=1
just check          # build + vet + test
```

## OpenAI-compatible API

GoClaw exposes a full OpenAI-compatible endpoint at `POST /v1/chat/completions`. Use it with any OpenAI SDK client, `curl`, or IDE plugin.

**Basic request:**

```bash
curl http://127.0.0.1:18789/v1/chat/completions \
  -H "Authorization: Bearer $(cat ~/.goclaw/auth.token)" \
  -H "Content-Type: application/json" \
  -d '{"model":"goclaw-v1","messages":[{"role":"user","content":"hello"}]}'
```

**With sampling parameters and streaming:**

```bash
curl -N http://127.0.0.1:18789/v1/chat/completions \
  -H "Authorization: Bearer $(cat ~/.goclaw/auth.token)" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "agent:researcher",
    "messages": [{"role": "user", "content": "search for Go 1.24 features"}],
    "stream": true,
    "temperature": 0.7,
    "max_tokens": 500
  }'
```

**With Python `openai` SDK:**

```python
from openai import OpenAI
client = OpenAI(base_url="http://127.0.0.1:18789/v1", api_key="your-auth-token")
resp = client.chat.completions.create(
    model="agent:coder",
    messages=[{"role": "user", "content": "write a fibonacci function in Go"}],
    temperature=0.3,
)
print(resp.choices[0].message.content)
```

**Supported features:** `temperature`, `top_p`, `max_tokens`, `stop`, `stream`, `response_format`, `user`, `tools` (accepted, ignored — tools run autonomously). Streaming responses include real-time `tool_calls` deltas and per-chunk `usage` reporting. Agent routing via `model: "agent:<id>"`. Models listed at `GET /v1/models`.

## Configuration

All state lives under `GOCLAW_HOME` (default `~/.goclaw`):

```
~/.goclaw/
  config.yaml       # Runtime config (YAML, env var overlay, agent definitions)
  policy.yaml       # Capability allowlists, domain allowlists
  goclaw.db         # SQLite (WAL mode, synchronous=FULL)
  auth.token        # Bearer token (auto-generated)
  SOUL.md           # Agent identity
  skills/           # WASM skills and SKILL.md definitions
  logs/
    system.jsonl    # Structured operational logs
    audit.jsonl     # Append-only security audit log
```

Precedence: environment variables > `config.yaml` > defaults.

## Scope

GoClaw is a local, single-user daemon — same model as Ollama or a local Jupyter kernel. For multi-user setups, run separate instances with different `GOCLAW_HOME` directories.

Not included: browser automation, distributed clustering, mobile/desktop clients. See [SPEC.md](SPEC.md) for full design rationale.

Task delivery is at-least-once. A crash between completing work and writing success causes a retry — safer than silent loss. Idempotency keys guard side effects.

## Status

**v0.5-dev** — 840+ tests across 29 packages. Under active development; APIs may change.

| Subsystem | Status |
|---|---|
| Persistence (SQLite WAL, schema v14) | Stable |
| Task engine (workers, leases, retry) | Stable |
| ACP gateway (WebSocket, REST, OpenAI-compat) | Stable |
| Policy engine (hot-reload) | Stable |
| WASM sandbox (wazero) | Stable |
| Multi-agent (registry, routing) | Stable |
| Streaming responses (SSE, bus events) | Stable |
| Agent loops (checkpoints, budgets) | Stable |
| Structured output (JSON Schema validation) | Stable |
| OpenTelemetry (traces, metrics) | Stable |
| Gateway security (auth, rate limit, CORS) | Stable |
| TUI | Functional |
| Telegram integration | Functional |

## Documentation

| Document | Purpose |
|---|---|
| [SPEC.md](SPEC.md) | System specification (90+ normative requirements) |
| [PDR.md](PDR.md) | Product design rationale |

## Contributing

```
cmd/goclaw/          Daemon entry point, CLI subcommands
internal/
  persistence/       SQLite store, schema migrations, task queue
  engine/            Task execution, worker lanes, brain integration
  gateway/           ACP WebSocket, REST API, OpenAI-compat endpoint
  policy/            Default-deny policy engine, hot-reload
  audit/             Dual-write audit (JSONL + DB)
  sandbox/wasm/      WASM host (wazero), resource limits, quarantine
  skills/            Skill loader, installer, SKILL.md parser
  tools/             Built-in tools, search providers, MCP bridge
  agent/             Multi-agent registry, scoped execution
  config/            YAML config, env overlay, fsnotify watcher
  channels/          Telegram integration
  mcp/               MCP client (stdio + SSE)
  otel/              OpenTelemetry integration (traces, metrics)
  cron/              Cron scheduler
  tui/               Bubbletea TUI
```

## License

[MIT](LICENSE)
