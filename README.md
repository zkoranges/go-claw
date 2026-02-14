# GoClaw

[![CI](https://github.com/zkoranges/go-claw/actions/workflows/ci.yml/badge.svg)](https://github.com/zkoranges/go-claw/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![SQLite](https://img.shields.io/badge/SQLite-WAL-003B57?logo=sqlite&logoColor=white)](https://sqlite.org)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Status](https://img.shields.io/badge/Status-WIP-orange)](#status)

> **Work in progress.** GoClaw is under active development. APIs, config formats, and internals may change without notice. Not yet recommended for production use.

A durable local orchestration kernel for AI agents. Single binary, single user, crash-recoverable.

GoClaw runs a local daemon that accepts agent tasks over a WebSocket protocol (ACP), persists them in SQLite, and executes them through an LLM brain with tool access — using Go's goroutine-per-worker concurrency model for parallel task execution, heartbeat monitoring, and event fan-in across subsystems. No work is silently lost, even under `kill -9`.

## Why

Agent orchestration requires durable task queues, crash recovery, lease-based ownership, policy enforcement, and sandboxed skill execution. GoClaw implements these as a single-process daemon backed by SQLite WAL, so no external services (Redis, Postgres, message brokers) are needed.

Go was chosen for its concurrency primitives and deployment model. The engine uses goroutine-per-worker pools, per-task heartbeat goroutines, and channel-based event fan-in across subsystems (config watcher, skill watcher, WASM hot-reload, event bus). Context propagation handles cancellation and timeouts throughout. The result is a single statically-linked binary with no runtime dependencies beyond SQLite.

## What it does

- **Durable task queue** backed by SQLite WAL. Tasks survive crashes and restarts. Lease-based ownership with automatic recovery of orphaned work.
- **8-state task machine** (QUEUED, CLAIMED, RUNNING, RETRY_WAIT, SUCCEEDED, FAILED, CANCELED, DEAD_LETTER) with transactional state transitions and append-only event audit trail.
- **Multi-agent support.** Named agents with independent brains, worker pools, and task queues. Agents can be defined in `config.yaml` and hot-reloaded at runtime.
- **ACP WebSocket gateway** implementing JSON-RPC 2.0 with version negotiation, bearer token auth, cursor-based replay, and explicit backpressure signaling.
- **LLM brain** via Genkit + Gemini (with multi-provider failover to Anthropic, OpenAI, and OpenRouter). Tool calls are schema-validated before execution.
- **Default-deny policy engine** with capability-based access control and domain allowlists. Policy hot-reloads via fsnotify; invalid config fails closed.
- **WASM skill sandbox** via wazero with memory limits, CPU fuel metering, execution timeouts, fault-count quarantine, and two-phase hot reload with rollback.
- **Built-in tools**: shell execution (with optional Docker sandboxing), filesystem operations, web search (Brave / Perplexity / DuckDuckGo), MCP client, process spawning.
- **Operational TUI** (Bubbletea) with interactive chat, agent switching (`/agent`), skill management, and model selection.
- **Cron scheduler** for recurring tasks with 5-field cron expressions.
- **Multi-channel input**: Telegram bot integration (with `@agent` routing), OpenAI-compatible `/v1/chat/completions` endpoint.
- **Context compaction.** When conversation history approaches the model's context window, older messages are summarized via LLM and archived. Recent messages are preserved intact. Falls back to truncation if summarization fails.
- **Structured observability**: JSON logs, dual-write audit (file + DB), `/healthz` and `/metrics` endpoints.

## Use cases

- **Persistent personal assistant.** A local agent that stays running, remembers context across sessions, and can act on scheduled tasks — without depending on a cloud service staying up.
- **Agent teams.** Define specialized agents (researcher, coder, reviewer) in `config.yaml`, each with its own LLM provider, system prompt, and tool access. Route tasks between them via the ACP gateway or Telegram.
- **Unattended automation.** Cron-triggered tasks, heartbeat monitoring, and retry-with-backoff mean you can queue work and walk away. Failures dead-letter instead of disappearing.
- **Skill development sandbox.** Write WASM skills with memory limits and fault quarantine. Hot-reload during development. The sandbox prevents a buggy skill from taking down the daemon.
- **Self-hosted AI gateway.** Expose the OpenAI-compatible `/v1/chat/completions` endpoint backed by any supported provider. Add policy controls and audit logging that upstream APIs don't offer.
- **Local MCP host.** Connect MCP servers (stdio or SSE) and expose their tools to agents through a single policy-controlled interface.

## Design constraints

- **No browser automation.** No Chromium, no Puppeteer, no headless rendering.
- **No distributed clustering.** Single-node, single-process. No multi-node scheduling, no consensus protocol.
- **No multi-tenancy.** One user context per daemon process. No user isolation within a single instance.
- **No mobile or desktop clients.** TUI and WebSocket are the interfaces.
- **At-least-once delivery.** Tasks are durable and lease-protected, but a crash in the narrow window between completing work and writing success will cause a retry. This is the standard trade-off for single-node systems — retry is safer than silent loss. Idempotency keys guard side effects.

## Status

**v0.1-dev** — under active development.

The core subsystems (persistence, engine, gateway, policy, WASM sandbox, multi-agent) are implemented and tested. The project is undergoing refactoring and is not yet stable. Spec: [SPEC.md](SPEC.md).

## Install

**Quick install** (requires Go 1.24+ and git):

```bash
curl -fsSL https://raw.githubusercontent.com/zkoranges/go-claw/main/install.sh | bash
```

Or clone and run locally:

```bash
git clone https://github.com/zkoranges/go-claw.git
cd go-claw
./install.sh
```

The script checks prerequisites, builds from source, installs the binary to `/usr/local/bin` (configurable via `INSTALL_DIR`), and creates `~/.goclaw`.

## Build and run

Requires Go 1.24+ and an LLM API key. Set one of the following in `.env` or environment:

| Provider | Env var | Notes |
|---|---|---|
| Google Gemini | `GEMINI_API_KEY` | Default provider |
| Anthropic | `ANTHROPIC_API_KEY` | Claude models |
| OpenAI | `OPENAI_API_KEY` | GPT models |
| OpenRouter | `OPENROUTER_API_KEY` | Multi-model gateway |

Configure the active provider in `config.yaml` under `llm.provider`, or use `/model` in the TUI to switch interactively.

```bash
just build          # compile to /tmp/goclaw
just run            # build + launch (interactive TUI)
just run-headless   # build + launch (no TUI, logs to stdout)
just test           # go test ./... -count=1
just check          # build + vet + test
just fmt            # format all Go files
```

First run creates `~/.goclaw/` with `config.yaml`, `policy.yaml`, `SOUL.md`, `auth.token`, and the SQLite database.

## Configuration

All state lives under `GOCLAW_HOME` (default `~/.goclaw`):

```
~/.goclaw/
  config.yaml       # Runtime config (YAML, env var overlay, agent hot-reload)
  policy.yaml       # Capability allowlists, domain allowlists
  goclaw.db         # SQLite (WAL mode, synchronous=FULL)
  auth.token        # Bearer token (auto-generated on first run)
  SOUL.md           # Agent identity (generated by genesis wizard)
  skills/           # User WASM skills and SKILL.md definitions
  logs/
    system.jsonl    # Structured operational logs
    audit.jsonl     # Append-only security audit log
```

Precedence: environment variables > `config.yaml` > defaults.

## Project layout

```
cmd/goclaw/          Daemon entry point, CLI subcommands
internal/
  persistence/       SQLite store, schema migrations, task queue
  engine/            Task execution engine, worker lanes, brain integration
  gateway/           ACP WebSocket server, REST API, OpenAI-compat endpoint
  policy/            Default-deny policy engine, hot-reload
  audit/             Dual-write audit (JSONL + DB)
  sandbox/wasm/      WASM host (wazero), resource limits, quarantine
  sandbox/legacy/    Legacy shell skill bridge (restricted)
  skills/            Skill loader, installer, SKILL.md parser
  tools/             Built-in tools, search providers, MCP bridge
  agent/             Multi-agent registry, scoped execution
  config/            YAML config, env overlay, fsnotify watcher
  channels/          Telegram integration
  mcp/               MCP client (stdio + SSE)
  cron/              Cron scheduler
  tui/               Bubbletea TUI
  bus/               In-process event bus
  safety/            Input sanitization
  doctor/            Startup self-checks
  telemetry/         Structured logging setup
```

## Documentation

| Document | Purpose |
|---|---|
| [SPEC.md](SPEC.md) | System specification |
| [PDR.md](PDR.md) | Product design rationale |

## License

[MIT](LICENSE)
