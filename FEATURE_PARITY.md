# GoClaw vs OpenClaw: Feature Parity

This document is the canonical parity overview for GoClaw.

- Last updated: 2026-02-13
- GoClaw baseline: v0.1 (SPEC v2.0.0)
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
| Gateway System | 14/18 | 11/18 | 4 | 7/18 | 18 |
| Persistence & Reliability | 0/15 | 15/15 | 15 | 15/15 | 15 |
| Search & Tools | 7/11 | 9/11 | 4 | 9/11 | 11 |
| Security Features | 18/23 | 16/23 | 6 | 14/23 | 23 |

## What This Means

- GoClaw leads in persistence and runtime reliability controls (15/15, all GoClaw-only).
- Security parity jumped from 13→15 with prompt injection defense + leak detection. 6 remaining gaps are OpenClaw-specific hardening (safe bins, LD_PRELOAD, DM pairing).
- Search & tools jumped from 6→8 with file tools + shell execution. Remaining gaps: MCP client, dynamic tool builder.
- Gateway gained REST API endpoints. Remaining: OpenAI-compatible API, doctor diagnostics.
- All P0 and P1 items from the TODO are complete. Remaining work is P2-P3 scope.

## Priority Backlog

P2 (near-term):

(None - all P2 items complete)

P3 (future):

1. Memory Phase 2 — FTS5 full-text search over workspace.

Completed (previously P1/P2/P3):

1. ~~Additional model providers~~ — OpenRouter added (100+ models).
2. ~~LLM failover~~ — circuit-breaker with error classification.
3. ~~File/shell tools~~ — full filesystem + command execution.
4. ~~Prompt injection defense~~ — sanitizer + leak detector in Respond/Stream.
5. ~~Cron scheduling~~ — 5-field cron with ACP methods.
6. ~~REST API~~ — 6 endpoints for tasks/sessions/skills/config.
7. ~~Context compaction~~ — intelligent history summarization.
8. ~~MCP client~~ — JSON-RPC over stdio support.
9. ~~Docker sandbox~~ — ephemeral containers for shell execution.
10. ~~Multi-channel messaging~~ — Telegram bot integration.
11. ~~Doctor diagnostics~~ — system health check CLI.
12. ~~Heartbeat system~~ — periodic automated agent turns.
13. ~~OpenAI-compatible API~~ — standard /v1/chat/completions endpoint.

Out of scope for v0.1/v0.2:

1. Mobile and native desktop clients.
2. Browser automation stack (NG-001).
3. Distributed clustering and multi-node orchestration.

## Intentional Design Deviations

GoClaw is not a line-by-line port. Major intentional differences:

1. Single static Go binary rather than Node runtime stack.
2. Durable SQLite-backed execution rather than in-memory task flow.
3. Capability-gated WASM execution and default-deny policy baseline.
4. Append-only audit and replay-oriented operational model.

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
