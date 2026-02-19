# GoClaw vs OpenClaw: Feature Parity

This document is the canonical parity overview for GoClaw.

- Last updated: 2026-02-19
- GoClaw baseline: v0.5-dev (838+ tests, 29 packages)
- OpenClaw baseline: 2026.2.9

## Canonical Data Sources

- Row-level parity data: [`docs/parity/parity.yaml`](docs/parity/parity.yaml)
- Generated scorecard: [`docs/parity/scorecard.generated.md`](docs/parity/scorecard.generated.md)
- Release gates: [`docs/RELEASE_READINESS_CHECKLIST.md`](docs/RELEASE_READINESS_CHECKLIST.md)

Use this command to refresh the scorecard:

```bash
go run ./tools/parity/scorecard -in docs/parity/parity.yaml -out docs/parity/scorecard.generated.md
```

## Current Scorecard Snapshot

| Category | OpenClaw | GoClaw | GoClaw-Only | Verified | Total |
| --- | --- | --- | --- | --- | --- |
| Gateway System | 14/23 | 18/23 | 9 | 14/23 | 23 |
| Memory & Context | 5/10 | 6/10 | 6 | 6/10 | 10 |
| Messaging Channels | 8/10 | 3/10 | 2 | 3/10 | 10 |
| Model Providers & LLM | 8/9 | 7/9 | 1 | 7/9 | 9 |
| Multi-Agent & Orchestration | 6/10 | 6/10 | 3 | 5/10 | 10 |
| Observability & Ops | 1/6 | 6/6 | 5 | 6/6 | 6 |
| Persistence & Reliability | 0/17 | 17/17 | 17 | 16/17 | 17 |
| Search & Tools | 7/14 | 12/14 | 7 | 11/14 | 14 |
| Security Features | 18/23 | 15/23 | 6 | 14/23 | 23 |
| Skills & Extensions | 8/10 | 7/10 | 2 | 7/10 | 10 |
| Streaming & Autonomy | 5/11 | 11/11 | 6 | 11/11 | 11 |

**Totals**: 143 features tracked across 11 sections.

## What This Means

**GoClaw strengths (v0.5-dev)**:
- Persistence and reliability is the widest moat: 17/17 GoClaw-implemented, all GoClaw-only. SQLite-backed durable execution, lease-based ownership, crash recovery, dead-letter queues, priority aging — none of this exists in OpenClaw's in-memory model.
- Observability is a GoClaw differentiator: 6/6 implemented (5 GoClaw-only). OpenTelemetry tracing and metrics, dual-write audit logs, event bus with backpressure monitoring.
- Streaming and autonomy is fully implemented: 11/11 GoClaw, 6 GoClaw-only. Agent loops with checkpoints, structured output validation, token budget enforcement — these are novel capabilities.
- Memory subsystem (v0.3): Auto-memory extraction, relevance decay, pinned context, shared team knowledge — 6 GoClaw-only features vs OpenClaw's vector-search approach.
- Gateway gained 5 new features in v0.5: SSE streaming, API key auth, rate limiting, CORS, A2A agent card.

**OpenClaw strengths**:
- Channel breadth: 8/10 channels implemented vs GoClaw's 3/10. WhatsApp, Slack, Discord, Signal, Google Chat, WebChat all missing from GoClaw.
- Sub-agent orchestration: Spawning, parallel orchestration, cascade stop, binding rules — 4 features GoClaw lacks.
- Model provider ecosystem: 20+ providers with API key rotation vs GoClaw's 7 providers.
- Security hardening: TLS 1.3, DM pairing, safe bins allowlist, LD*/DYLD* validation, webhook signatures — 7 gaps remain.
- Skills ecosystem: ClawHub registry, TypeScript plugins, plugin config slots — 3 features GoClaw lacks.

**Both strong on**: Security core (14/23 verified), search tooling (11/14), model provider basics (7/9), skill loading and hot-reload.

## Priority Backlog

P3 (future):

1. Sub-agent orchestration (spawning, parallel, cascade stop, binding rules)
2. Vector/embedding memory search + hybrid BM25
3. Additional messaging channels (WhatsApp, Slack, Discord, Signal, etc.)
4. TypeScript plugin system + ClawHub registry
5. Gateway lock (PID-based), launchd/systemd integration
6. Broad provider ecosystem (20+), API key rotation
7. Security hardening (TLS 1.3, safe bins, LD*/DYLD* validation)

Completed (previously P2/P3):

1. ~~MCP client~~ — per-agent connections with policy enforcement (v0.4).
2. ~~Streaming responses~~ — SSE endpoint, OpenAI streaming, Telegram progressive editing (v0.5).
3. ~~Agent loops~~ — LoopRunner with checkpoints, budgets, termination keywords (v0.5).
4. ~~Structured output~~ — JSON Schema validation with ValidateAndRetry (v0.5).
5. ~~OpenTelemetry~~ — trace provider, 10 metric instruments (v0.5).
6. ~~Gateway security~~ — API key auth, rate limiting, CORS middleware (v0.5).
7. ~~Delegation~~ — blocking delegation with hop counting, deadlock prevention (v0.3).
8. ~~Memory subsystem~~ — auto-memory, decay, pins, shared knowledge (v0.3).
9. ~~Multi-agent~~ — registry, @mentions, hot-reload (v0.2-v0.3).
10. ~~Doctor diagnostics~~ — system health check CLI.
11. ~~OpenAI-compatible API~~ — standard /v1/chat/completions endpoint.

Out of scope:

1. Mobile and native desktop clients.
2. Browser automation stack (NG-001).
3. Canvas hosting (A2UI) (NG-003).
4. Bonjour/mDNS discovery, Tailscale integration (NG-002).
5. iMessage / BlueBubbles.
6. Distributed clustering and multi-node orchestration.
7. Voice / TTS integration.

## Intentional Design Deviations

GoClaw is not a line-by-line port. Major intentional differences:

1. Single static Go binary rather than Node runtime stack.
2. Durable SQLite-backed execution rather than in-memory task flow.
3. Capability-gated WASM execution and default-deny policy baseline.
4. Append-only audit and replay-oriented operational model.
5. Agent loops with checkpoint persistence for autonomous operation.
6. OpenTelemetry-native observability (traces + metrics).

## Maintenance Rules

1. Treat this file as the executive parity overview only; keep row-level detail in `docs/parity/parity.yaml`.
2. Regenerate `docs/parity/scorecard.generated.md` after parity metadata changes.
3. Update the date in this file whenever parity status changes.
4. Do not duplicate large parity tables here once they are migrated into `docs/parity/parity.yaml`.

## Review Cadence

- Weekly review: Friday.
- Also run parity review immediately after parity-impacting merges.
- Apply release gates before release-bound merges:
  - [`docs/RELEASE_READINESS_CHECKLIST.md`](docs/RELEASE_READINESS_CHECKLIST.md)
