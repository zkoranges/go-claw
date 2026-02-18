# GoClaw

[![CI](https://github.com/zkoranges/go-claw/actions/workflows/ci.yml/badge.svg)](https://github.com/zkoranges/go-claw/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![SQLite](https://img.shields.io/badge/SQLite-WAL-003B57?logo=sqlite&logoColor=white)](https://sqlite.org)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**Run AI agent teams locally with crash recovery, tool access, and zero cloud dependencies.**

GoClaw is a single-binary daemon that queues agent tasks in SQLite, executes them through any LLM provider, and guarantees nothing is silently lost — even if you `kill -9` the process mid-task.

```
$ goclaw
GoClaw v0.5-dev | SQLite WAL | 3 workers | localhost:18789
coder — gemini-2.5-pro
> @coder implement a fibonacci function in Go
[task queued] id=a1b2c3 status=QUEUED
[coder] Here's a simple fibonacci function...
```

## Why

Agent frameworks treat tasks as ephemeral — crash and your work vanishes. GoClaw persists every state change before acknowledging it, like a database transaction. Orphaned work is automatically recovered on restart.

## Features

**Durable by default.** SQLite WAL task queue with lease-based ownership. 8-state task machine (QUEUED through DEAD_LETTER). Every transition is transactional with an append-only audit trail. Crash recovery on restart.

**Multi-provider LLM.** Gemini, Anthropic, OpenAI, OpenRouter, Ollama — switch at runtime via `/model`. Ollama models auto-detect tool support. No API key needed for local models.

**Agent teams.** Named agents with independent brains, worker pools, and task queues. Inter-agent delegation with hop counting and deadlock prevention. Memory sharing across agents. `@mentions` for routing.

**Context and memory.** Conversation compaction via LLM summarization. Persistent core memory per agent. Pin files or text with auto-update on change. Relevance decay over time. Token budget visibility via `/context`.

**OpenAI-compatible API.** Drop-in `/v1/chat/completions` with streaming, sampling parameters, structured output, and tool-call visibility. Route to agents via `model: "agent:<id>"`. Works with the Python `openai` SDK, `curl`, and any compatible client.

**Tools and integrations.** MCP client (stdio + SSE, per-agent policy control). Built-in shell, filesystem, web search, process spawning. WASM skill sandbox with memory limits and quarantine. Telegram bot with human-in-the-loop gates.

**Streaming and autonomy.** SSE endpoint for real-time token delivery. Agent loops with configurable budgets, termination keywords, and crash-recovery checkpoints. Structured JSON output with schema validation and auto-retry.

**Safety.** Default-deny policy engine with hot-reload. WASM sandbox (wazero, pure Go). Gateway security: API key auth, per-key rate limiting, CORS. OpenTelemetry traces and metrics (zero overhead when disabled).

## Use cases

### Multi-agent business team

Define specialized agents that delegate work to each other:

```yaml
# ~/.goclaw/config.yaml
agents:
  - agent_id: manager
    model: gemini-2.5-pro
    soul: "You coordinate between marketer and dev. Break tasks into subtasks and delegate."
    worker_count: 4
  - agent_id: marketer
    model: claude-sonnet-4-5-20250929
    soul: "B2B lead generation strategist. Research ICPs, craft outreach sequences."
    provider: anthropic
  - agent_id: dev
    model: gemini-2.5-flash
    soul: "Full-stack developer. Build landing pages, set up tracking, deploy."
```

```bash
curl http://127.0.0.1:18789/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"agent:manager","messages":[{"role":"user","content":"Create a lead gen campaign targeting SaaS CTOs"}]}'
```

The manager delegates research to the marketer and implementation to the dev, each using their own LLM and tools.

### Self-hosted AI gateway

Expose any LLM behind an OpenAI-compatible API with policy controls and audit logging:

```bash
# Stream responses with tool-call visibility
curl -N http://127.0.0.1:18789/v1/chat/completions \
  -H "Authorization: Bearer $(cat ~/.goclaw/auth.token)" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "agent:researcher",
    "messages": [{"role": "user", "content": "search for Go 1.24 release notes"}],
    "stream": true,
    "temperature": 0.7
  }'
```

### Local dev assistant with Ollama

Zero-cost setup with no API key — just Ollama running locally:

```yaml
# ~/.goclaw/config.yaml
llm:
  provider: ollama
  openai_model: qwen3:8b
```

```bash
goclaw  # starts with local Ollama, no API key needed
```

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

Set an LLM API key (or skip for Ollama):

| Provider | Env var |
|---|---|
| Google Gemini (default) | `GEMINI_API_KEY` |
| Anthropic | `ANTHROPIC_API_KEY` |
| OpenAI | `OPENAI_API_KEY` |
| OpenRouter | `OPENROUTER_API_KEY` |
| Ollama (local) | None — runs on `localhost:11434` |

```bash
export GEMINI_API_KEY="your-key"
goclaw              # interactive TUI
goclaw --daemon     # headless, logs to stdout
```

First run generates `config.yaml`, `policy.yaml`, `SOUL.md`, `auth.token`, and the SQLite database under `~/.goclaw`. Three starter agents (coder, researcher, writer) are available immediately. Create new agents with Ctrl+N or pull from URLs with `goclaw pull <url>`.

### Development

```bash
just build          # compile to /tmp/goclaw
just run            # build + launch (interactive TUI)
just test           # go test ./... -count=1
just check          # build + vet + test
```

## OpenAI-compatible API

`POST /v1/chat/completions` — works with any OpenAI SDK client, `curl`, or IDE plugin.

```bash
curl http://127.0.0.1:18789/v1/chat/completions \
  -H "Authorization: Bearer $(cat ~/.goclaw/auth.token)" \
  -H "Content-Type: application/json" \
  -d '{"model":"goclaw-v1","messages":[{"role":"user","content":"hello"}]}'
```

**With the Python `openai` SDK:**

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

Supports `temperature`, `top_p`, `max_tokens`, `stop`, `stream`, `response_format`, `user`. Streaming includes real-time `tool_calls` deltas and per-chunk `usage` reporting. Agent routing via `model: "agent:<id>"`. Models listed at `GET /v1/models`.

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

## Status

**v0.5-dev** — 840+ tests across 29 packages. Single-user local daemon (same model as Ollama or a local Jupyter kernel). Under active development; APIs may change. See [SPEC.md](SPEC.md) for full design rationale.

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

See [SPEC.md](SPEC.md) for the full system specification (90+ normative requirements).

## License

[MIT](LICENSE)
