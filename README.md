# GoClaw

[![CI](https://github.com/zkoranges/go-claw/actions/workflows/ci.yml/badge.svg)](https://github.com/zkoranges/go-claw/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![SQLite](https://img.shields.io/badge/SQLite-WAL-003B57?logo=sqlite&logoColor=white)](https://sqlite.org)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**Run AI agents locally with crash recovery, tool access, and zero cloud dependencies.**

GoClaw is a single-binary daemon that queues agent tasks in SQLite, executes them through LLM providers (Gemini, Claude, GPT, OpenRouter), and guarantees nothing is silently lost — even if you `kill -9` the process mid-task. Orphaned work is automatically recovered on restart.

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
- **Multi-provider LLM brain.** Gemini (default), Anthropic, OpenAI, OpenRouter — with automatic failover. Tool calls are schema-validated before execution. Switch providers at runtime via `/model`.
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

### Safety and control

- **Default-deny policy engine.** Capability-based access control with domain allowlists. Hot-reloads via fsnotify; invalid config fails closed.
- **WASM skill sandbox.** Memory limits, CPU fuel metering, execution timeouts, fault-count quarantine, and two-phase hot reload with rollback. Powered by [wazero](https://wazero.io) (pure Go, no CGo).
- **Built-in tools.** Shell execution (with optional Docker sandboxing), filesystem ops, web search (Brave / Perplexity / DuckDuckGo), MCP client, process spawning.

### Operations

- **Multi-agent support.** Named agents with independent brains, worker pools, and task queues. Define them in `config.yaml` and hot-reload at runtime.
- **Interactive TUI.** Chat, agent switching (`/agent`), skill management, model selection — built with [Bubbletea](https://github.com/charmbracelet/bubbletea).
- **Multi-channel input.** Telegram bot (with `@agent` routing), OpenAI-compatible `/v1/chat/completions` endpoint, ACP WebSocket gateway (JSON-RPC 2.0).
- **Cron scheduler.** Recurring tasks with standard 5-field cron expressions.
- **Observability.** Structured JSON logs, dual-write audit (file + DB), `/healthz` and `/metrics` endpoints.

## Use cases

- **Persistent personal assistant.** A local agent that stays running, remembers context across sessions, and acts on scheduled tasks — without depending on a cloud service.
- **Agent teams.** Specialized agents (researcher, coder, reviewer) each with their own LLM provider, system prompt, and tool access. Route tasks via ACP, Telegram, or the TUI.
- **Unattended automation.** Queue work and walk away. Cron triggers, heartbeat monitoring, retry-with-backoff. Failures dead-letter instead of disappearing.
- **Self-hosted AI gateway.** OpenAI-compatible endpoint backed by any provider, with policy controls and audit logging that upstream APIs don't offer.
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

**v0.5-dev** — 800+ tests across 29 packages. Under active development; APIs may change.

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
