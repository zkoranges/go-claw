# PDR — Project GoClaw (Go-Claw v0.1)

## 1) Title, Version, Status, Changelog

- Title: Product Design Review (Explanatory)
- Product: Project GoClaw
- Version: 2.0.0
- Status: Approved for Architecture Gate
- Date: February 11, 2026

### Changelog

| Version | Date | Summary |
| --- | --- | --- |
| 2.0.0 | 2026-02-11 | Full architecture rewrite aligned to durable queue semantics, explicit ACP delivery model, unified policy enforcement, deterministic skill lifecycle, and operations-first reliability. |
| 1.0.0 | 2026-02-11 | Prior baseline PDR (superseded). |

## 2) Executive Summary (top decisions + why)

1. Keep SQLite, but treat it as a lease-based durable queue with append-only task events.
- Why: embedded ops simplicity with explicit correctness controls (leases, retries, idempotency, DLQ).

2. Make reliability semantics explicit.
- Why: remove "exactly-once" ambiguity; declare at-least-once tasks + at-most-once side effects per idempotency key.

3. Harden ACP contract.
- Why: real clients disconnect; ordered replay cursors and bounded backpressure are required for correctness.

4. Formalize skill lifecycle.
- Why: WASM hot reload without ABI/rollback creates runtime instability.

5. Unify security controls.
- Why: one PDP/PEP model across ACP, WASM, and legacy bridge prevents policy bypass seams.

6. Preserve V1 scope discipline.
- Why: no browser automation in v0.1, but create seam now to avoid rewrite later.

## 3) Problem Statement (OpenClaw pain points and goals)

### 3.1 Operational pain points inherited from OpenClaw-style systems

- Weak queue semantics lead to duplicate work and uncertain retry behavior during crashes.
- Protocol/event delivery lacks replay guarantees, causing UI desynchronization.
- Plugin lifecycle and compatibility drift create upgrade regressions.
- Security decisions split across paths produce inconsistent enforcement.
- Incident debugging is slow without correlated run/event history.

### 3.2 Goals for GoClaw v0.1

1. Durable-by-default orchestration kernel with deterministic recovery.
2. Bounded concurrency with explicit queue fairness and backpressure.
3. Side-effect safety through idempotency contracts.
4. Auditability: every significant decision and state transition is queryable.
5. Strict scope control to avoid framework/glue bloat.

## 4) Design Principles

1. Durable by default
- Persist first, execute second, reconcile deterministically.

2. Bounded concurrency
- Fixed lanes, finite buffers, explicit overflow behavior.

3. Idempotent side effects
- Explicit idempotency keys and dedupe registry.

4. Auditable runs
- Append-only task events + immutable audit log.

5. Explicit security boundaries
- Centralized policy decisions, fail-closed behavior.

6. Minimal surface area / avoid glue bloat
- Small stable contracts; reject speculative feature creep.

## 5) Architecture Overview

### 5.1 Component diagram (ASCII)

```text
                +------------------------------+
                |        ACP Clients           |
                |  Web UI / Mobile / Scripts   |
                +--------------+---------------+
                               |
                               v
+----------------------+   +---+-----------------------------+
|       TUI Ops        |   |         Gateway Layer           |
| status/approvals     |<->| WS / JSON-RPC / auth / replay  |
+----------+-----------+   +---+-----------------------------+
           |                   |
           v                   v
+----------+-------------------+-----------------------------+
|            Orchestration Kernel (Runtime)                  |
| Scheduler (lanes) | Brain Adapter | Recovery | Approval    |
+----------+-------------------+-----------------------------+
           |                   |
           v                   v
+----------+-------------------+-----------------------------+
| Persistence (SQLite)         | Execution (WASM/Legacy)     |
| tasks, events, dedup, audit  | skills + policy enforcement |
+------------------------------+------------------------------+
```

### 5.2 Major data flows

1. Ingress flow
- ACP `agent.chat` -> validate/auth -> persist task + initial event -> enqueue.

2. Execution flow
- Scheduler claims lease -> Brain emits tool intent -> policy check -> tool execution -> transition/event commit.

3. Recovery flow
- Startup scans expired leases + incomplete tasks -> deterministic requeue or terminal routing.

4. Client replay flow
- Client reconnects with cursor -> server replays events in session order or returns replay-gap error.

### 5.3 Concurrency & lifecycle model

- Fixed lane pool handles task attempts.
- Each attempt owns one lease and heartbeats it.
- Cancellation is event-driven with pre/post tool call checks.
- Shutdown drains lanes with bounded timeout and marks unfinished tasks retryable.

## 6) Key Decisions (ADR-style sections)

### 6.1 ADR-01: SQLite queue w/ leases + retries + DLQ + poison-pill handling

Decision
- Adopt SQLite as embedded persistence and durable queue with lease-based claims.

Context
- Need single-binary local deployment and predictable recovery without external queue infra.

Options
1. SQLite + lease scheduler.
2. In-memory queue + periodic snapshots.
3. External queue service.

Pros/Cons
- Option 1 Pros: local simplicity, ACID transitions, easy backup.
- Option 1 Cons: single-writer contention requires careful transaction design.
- Option 2 Cons: crash windows and weaker durability.
- Option 3 Cons: operational complexity violates V1 scope.

Outcome
- Option 1 selected with strict controls: atomic claim, heartbeat leases, exponential backoff+jitter, DLQ, poison-pill classifier.

Consequences
- Must tune busy timeout, indexes, and transaction scope.
- Must provide contention tests and recovery chaos tests.

Verification
- SPEC links: `GC-SPEC-PER-*`, `GC-SPEC-QUE-*`, `GC-SPEC-REL-003`.

### 6.2 ADR-02: Task state machine + idempotency/event/audit model

Decision
- Use explicit 8-state task model and append-only `task_events` as authoritative execution history.

Context
- Prior 4-state model could not express retry wait, cancellation intent, or terminal DLQ behavior safely.

Options
1. Keep simple row-only task status model.
2. Add event log + richer state machine.

Pros/Cons
- Option 2 Pros: better auditability, replay, and deterministic recovery.
- Option 2 Cons: more schema and verification overhead.

Outcome
- Option 2 selected.
- Side-effecting tool calls require idempotency keys and dedupe table.

Consequences
- Data model complexity increases, but incident analysis and correctness improve materially.

Verification
- SPEC links: `GC-SPEC-STM-*`, `GC-SPEC-QUE-006`, `GC-SPEC-OBS-002`, `GC-SPEC-REL-002`.

### 6.3 ADR-03: ACP protocol ordering/backpressure/reconnect/versioning/security/auth

Decision
- Keep JSON-RPC 2.0 over WebSocket, add mandatory handshake, auth, per-session ordering, cursor replay, and bounded backpressure behavior.

Context
- Fire-and-forget websocket notifications are insufficient for multi-client reliability.

Options
1. Best-effort event streaming.
2. Ordered replayable event stream per session.

Pros/Cons
- Option 2 Pros: predictable reconnection behavior; debuggable client state.
- Option 2 Cons: retention and cursor management complexity.

Outcome
- Option 2 selected with explicit replay-gap error handling.

Consequences
- Must enforce event retention policy and replay window.

Verification
- SPEC links: `GC-SPEC-ACP-*`, `GC-SPEC-OBS-002`, `GC-SPEC-REL-004`.

### 6.4 ADR-04: WASM skills ABI/tool schema stability, hot reload, caching, debug hooks

Decision
- Production uses manifest-based precompiled WASM modules; dev mode allows source compilation.

Context
- Runtime compiler availability and ABI drift were critical failure risks.

Options
1. Always compile source at runtime.
2. Precompiled-by-default with optional dev compilation.

Pros/Cons
- Option 2 Pros: operational stability, deterministic activation, lower runtime dependency risk.
- Option 2 Cons: requires packaging workflow.

Outcome
- Option 2 selected with two-phase hot reload and rollback.

Consequences
- Skill manifest and ABI compatibility policy become release-managed artifacts.

Verification
- SPEC links: `GC-SPEC-SKL-*`, `GC-SPEC-CFG-007`.

### 6.5 ADR-05: Policy model enforcement points, SSRF defense, secrets handling/redaction

Decision
- Centralize authorization with PDP/PEP model and enforce fail-closed behavior on all execution paths.

Context
- Mixed control paths (ACP, WASM, legacy) are common bypass vectors.

Options
1. Per-subsystem policy checks.
2. Central PDP with strict enforcement points.

Pros/Cons
- Option 2 Pros: consistent policy semantics and auditing.
- Option 2 Cons: requires disciplined boundary integration.

Outcome
- Option 2 selected with policy version pinning per task attempt and immutable audit records.

Consequences
- Policy schema governance and approval broker become operational requirements.

Verification
- SPEC links: `GC-SPEC-SEC-*`, `GC-SPEC-OBS-003`, `GC-SPEC-DATA-007`.

### 6.6 ADR-06: Observability model with trace IDs, run history, replay/debug workflow

Decision
- Treat observability as first-class data model: structured logs, task events, audit logs, incident export.

Context
- 2AM failures require correlated evidence, not ad hoc logs.

Options
1. Logs + metrics only.
2. Logs + metrics + replayable run events + audit channel.

Pros/Cons
- Option 2 Pros: deterministic debugging and traceability.
- Option 2 Cons: storage overhead and retention tuning.

Outcome
- Option 2 selected.

Consequences
- Requires retention policy, incident bundle tooling, and replay tests.

Verification
- SPEC links: `GC-SPEC-OBS-*`, `GC-SPEC-DATA-005..008`.

### 6.7 ADR-07: TUI-first operational strategy + remote UI seam

Decision
- Keep TUI minimal and operational-only; all critical controls also available via ACP.

Context
- TUI-only safety controls break headless and remote workflows.

Options
1. TUI as primary control plane.
2. ACP-first control plane with TUI as one surface.

Pros/Cons
- Option 2 Pros: avoids deadlocks and supports remote clients cleanly.
- Option 2 Cons: requires approval broker and event contracts.

Outcome
- Option 2 selected.

Consequences
- TUI cannot host unique business logic.

Verification
- SPEC links: `GC-SPEC-TUI-*`, `GC-SPEC-SEC-008`, `GC-SPEC-ACP-*`.

### 6.8 ADR-08: Non-goal retained: no browser in v0.1, but seam reserved

Decision
- Keep browser automation out of v0.1 scope; reserve protocol and policy seams for future capability.

Context
- Browser support is high-complexity and out-of-scope for first reliability release.

Options
1. Add browser now.
2. Defer browser but design seam now.

Pros/Cons
- Option 2 Pros: protects v0.1 scope while preventing rewrite later.
- Option 2 Cons: requires explicit placeholder capability taxonomy.

Outcome
- Option 2 selected.

Consequences
- Capability model includes future class placeholders, but no runtime browser implementation.

Verification
- SPEC links: `GC-SPEC-NG-001`, compatibility checks in acceptance.

## 7) Failure Modes & Mitigations

| ID | 2AM Failure Scenario | Mitigation (Design-Level) | Test / Chaos Suggestion |
| --- | --- | --- | --- |
| FM-01 | Crash after external side effect but before completion commit | Idempotency key + dedupe registry + retry reason codes | Kill between tool return and commit |
| FM-02 | Two workers execute same task | Atomic lease claim + lease token validation on completion | 50-way claim race test |
| FM-03 | Long-running task loses lease heartbeat | Automatic lease expiry and reclaim with attempt increment | Pause worker thread > lease TTL |
| FM-04 | Retry storm saturates queue | Backoff+jitter+max attempts+DLQ | Inject 100% failing tool responses |
| FM-05 | Poison task repeatedly fails with same signature | Poison-pill classifier routes to DLQ early | Replay same deterministic fault |
| FM-06 | ACP client disconnect loses events | Cursor replay and replay-gap error | Reconnect at random intervals |
| FM-07 | Slow client causes memory growth | Bounded outbound queue + deterministic disconnect | throttle receiver and burst events |
| FM-08 | Invalid policy hot reload | Keep last valid policy, fail closed, emit audit alert | Push malformed policy.yaml |
| FM-09 | Headless environment blocks risky action awaiting TUI | Approval Broker via ACP + timeout default deny | Run daemon without TTY and trigger approval-required action |
| FM-10 | Skill hot reload breaks active tool | Stage/validate/activate with rollback on failure | Reload invalid ABI module during active load |
| FM-11 | DB lock contention causes request timeouts | Busy timeout + short tx + contention tuning + write batching | Multi-session write stress on slow disk |
| FM-12 | Backup restore loses WAL changes | Online backup API + restore integrity drill | Backup during write load, restore, compare event counts |

## 8) Operational Plan

### 8.1 Start/stop lifecycle

Start sequence:
1. Load config and token.
2. Open SQLite and apply migrations.
3. Load and validate policy.
4. Reconcile leases/retries.
5. Start ACP listener and metrics/health endpoints.
6. Start scheduler.
7. Start optional TUI.

Stop sequence:
1. Stop intake.
2. Signal cancel to active lanes.
3. Drain until timeout.
4. Persist terminal drain events.
5. Flush logs and close DB.

### 8.2 Upgrades and migrations

- Forward-only migrations with checksums.
- Pre-upgrade backup required.
- Binary refuses to run on unsupported future schema.
- Downgrade via snapshot restore only.

### 8.3 Backups and restore

- Use online-consistent backup procedure.
- Verify via periodic restore drill.
- Track RPO/RTO evidence in release artifacts.

### 8.4 Retention

- Separate retention windows for logs, task events, audit.
- Purge jobs are idempotent and audited.

### 8.5 Safe shutdown and recovery

- Unfinished tasks remain recoverable by lease timeout.
- Recovery reasons are event-coded for operator diagnosis.

## 9) Verification Strategy

### 9.1 Required tests by subsystem

1. Queue correctness
- Claim races, lease heartbeat/expiry, retry timing, DLQ routing, poison-pill handling.

2. Persistence
- PRAGMA assertions, transaction atomicity, migration consistency, lock contention behavior.

3. ACP protocol
- Handshake/version negotiation, auth/origin checks, replay ordering, replay gap behavior, backpressure disconnect.

4. Skills
- ABI compatibility, staged activation, rollback, cache correctness, fault quarantine.

5. Security
- Default deny, cross-path policy enforcement, SSRF defenses, redaction correctness, audit completeness.

6. Observability
- Log schema, event completeness for replay, incident export fidelity.

7. Operations
- Startup/shutdown lifecycle, backup/restore drills, retention jobs.

### 9.2 Evidence expectations

- Unit evidence: deterministic tests for state transitions, schemas, and policy logic.
- Integration evidence: end-to-end task runs with crash/reconnect/security scenarios.
- Manual evidence: operational runbooks, restoration drill signoff, approval flow screenshots/transcripts.
- Performance evidence: benchmark reports tied to SPEC `GC-SPEC-PERF-*` IDs.

## 10) Risks & Open Questions

### 10.1 Resolved by this rewrite

1. Queue ambiguity resolved with lease-based claim and explicit retry/DLQ semantics.
2. Protocol ambiguity resolved with replay/backpressure/version/auth contract.
3. Skill lifecycle ambiguity resolved with ABI/manifest/rollback/quarantine model.
4. Security seam risk reduced via centralized PDP/PEP and approval broker.
5. Reliability language corrected to explicit at-least-once and idempotent side-effect semantics.

### 10.2 Remaining open questions (with defaults and rationale)

| OQ ID | Question | Default for v0.1 | Rationale | Closure Criterion |
| --- | --- | --- | --- | --- |
| OQ-01 | Exact skill manifest signing scheme | Local trust + checksum verification only | Avoid introducing heavy PKI workflow in v0.1 while preserving integrity checks | Decide signing strategy before v0.3 distribution expansion |
| OQ-02 | Long-term event retention cost model | 30-day task event retention default | Balances replay/debug utility with local storage constraints | Storage telemetry review after first production cohort |
| OQ-03 | Optional process isolation for high-risk skills | In-process WASM only in v0.1, quarantine on repeated faults | Keeps v0.1 scope tight; observability added to justify future sidecar decision | Evaluate incident data by v0.3 |
| OQ-04 | Browser capability rollout | Deferred; capability taxonomy reserved only | Preserves non-goal while preventing future contract rewrite | ADR proposal required before any browser implementation |

### 10.3 Scope control rules (anti-bloat)

1. No new execution substrate in v0.1 besides existing WASM + optional legacy bridge.
2. No feature accepted without requirement ID + verification hook + evidence owner.
3. No protocol fields added without version negotiation and compatibility tests.
4. No reliability claims beyond those expressed in SPEC Section 9.
5. No TUI-only controls for critical workflows.

