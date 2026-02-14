# Project GoClaw

[![Go](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![SQLite](https://img.shields.io/badge/SQLite-WAL-003B57?logo=sqlite&logoColor=white)](https://www.sqlite.org)
[![WASM](https://img.shields.io/badge/Runtime-WASM-624DE8?logo=webassembly&logoColor=white)](https://webassembly.org)
[![Spec](https://img.shields.io/badge/Spec-v2.0.0-blue)]()

GoClaw is a Go rewrite of an OpenClaw-compatible runtime focused on reliability and security depth over surface-area breadth.

## Why It Exists

- OpenClaw-style in-memory execution is brittle under crashes and partial failures.
- GoClaw keeps ACP compatibility but enforces durable state and strict execution controls.
- The target is a single-binary local daemon that is predictable under failure.

## Core Guarantees

- Durable task persistence in SQLite WAL.
- Transactional task state transitions (8-state FSM).
- Lease-based ownership with deterministic crash recovery.
- Default-deny policy enforcement for tool and runtime capabilities.
- Capability-gated WASM execution with bounded runtime limits.
- Append-only audit records for policy and execution decisions.
- ACP replay and backpressure behavior for resilient clients.

## Build

```bash
go build -o dist/goclaw ./cmd/goclaw
```

## Run

```bash
./dist/goclaw
```

Run options:

- `-daemon`: headless daemon mode (no interactive chat TUI).
- `daemon`: subcommand alias for daemon mode (`goclaw daemon`).
- `GOCLAW_NO_TUI=1`: disable interactive TUI without forcing daemon mode.

First run:

- If `~/.goclaw/config.yaml` is missing, Genesis initializes `SOUL.md` and `config.yaml`.

## Test

```bash
go test ./... -count=1
./scripts/verify.sh gate0
```

## Limits (v0.1)

- Single-tenant, single-node runtime.
- No browser automation.
- WASM hot-swap requires `tinygo` in `PATH`.
- At-least-once execution model (side effects must be idempotent).

## Canonical Docs

- Spec: [`SPEC.md`](SPEC.md)
- PDR: [`PDR.md`](PDR.md)
- Feature parity (primary): [`FEATURE_PARITY.md`](FEATURE_PARITY.md)
- Verification report: [`docs/VERIFY_REPORT.md`](docs/VERIFY_REPORT.md)
- Release checklist: [`docs/RELEASE_READINESS_CHECKLIST.md`](docs/RELEASE_READINESS_CHECKLIST.md)
