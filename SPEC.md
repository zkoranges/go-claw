# SPEC — Project GoClaw (Go-Claw v0.1)

## 1) Title, Version, Status, Changelog

- Title: System Specification (Normative)
- Product: Project GoClaw
- Version: 2.0.0
- Status: Approved for Implementation Gate (v0.1)
- Date: February 11, 2026

### Changelog

| Version | Date | Summary |
| --- | --- | --- |
| 2.0.0 | 2026-02-11 | Full rewrite for durable queue semantics, ACP replay/order contract, unified policy model, deterministic WASM lifecycle, and explicit reliability/operability guarantees. |
| 1.1.0 | 2026-02-11 | Prior baseline document (superseded). |

## 2) Scope (V1 / v0.1) + Explicit Non-Goals (NG list)

### 2.1 V1 Scope (v0.1)

V1 is a single-user local gateway daemon with a durable task engine, ACP WebSocket interface, WASM skills, and a minimal operational TUI.

| Req ID | Normative Requirement | Verification Hook |
| --- | --- | --- |
| GC-SPEC-SCOPE-001 | The product MUST ship as a single executable artifact for each target OS and MUST run without Node.js, Python, or Docker at runtime. | V-SCOPE-001 (Build/packaging evidence) |
| GC-SPEC-SCOPE-002 | The runtime MUST be single-tenant (one local user context per process). | V-SCOPE-002 (Integration + config isolation test) |
| GC-SPEC-SCOPE-003 | The architecture MUST support local ACP clients and local TUI concurrently in one daemon process. | V-SCOPE-003 (Integration multi-client test) |

### 2.2 Explicit Non-Goals (v0.1)

| Req ID | Non-Goal | Verification Hook |
| --- | --- | --- |
| GC-SPEC-NG-001 | V1 SHALL NOT include headless browser automation (Chromium or equivalent). | V-NG-001 (Dependency and capability audit) |
| GC-SPEC-NG-002 | V1 SHALL NOT include distributed clustering or multi-node scheduling. | V-NG-002 (Architecture review + deployment test) |
| GC-SPEC-NG-003 | V1 SHALL NOT provide multi-user data separation within one process. | V-NG-003 (Architecture review) |

## 3) Definitions & Glossary (canonical terms)

- ACP: Agent Client Protocol over WebSocket using JSON-RPC 2.0 plus server notifications.
- Run: End-to-end execution instance spawned by one `agent.chat` request.
- Task: A schedulable work item persisted in SQLite and executed by a worker lane.
- Lane: Bounded worker execution slot.
- Lease: Time-bounded claim ownership on a task (`lease_owner`, `lease_expires_at`).
- Attempt: One claim-execute-finish cycle for a task.
- DLQ: Dead-letter queue state for terminally unhealthy tasks.
- Idempotency Key: Stable key identifying an externally side-effecting tool operation.
- Tool Call: A single invocation request from Brain to a tool runtime.
- Task Event: Append-only state/event record used for audit and replay.
- Policy Decision Point (PDP): Central authorization evaluator.
- Policy Enforcement Point (PEP): Boundary where PDP decisions are enforced.
- Replay Cursor: `event_id` offset used by ACP clients to resume missed events.
- Approval Broker: Asynchronous approval channel used for high-risk actions.

## 4) System Overview (one-page)

GoClaw is a durable-by-default local orchestration kernel with these runtime boundaries:

1. Gateway Layer
- ACP WebSocket server (`/ws`)
- Minimal operational TUI
- Health/metrics endpoints

2. Orchestration Layer
- Scheduler (bounded lanes)
- Brain adapter (LLM/tool intent)
- Recovery manager (leases/retries)

3. Execution Layer
- WASM skill host
- Optional legacy skill bridge (restricted and policy-gated)

4. Control Plane
- SQLite persistence
- Policy engine (PDP + PEP)
- Observability pipeline (run events, logs, audit)

### Core Invariants

- Task execution semantics are at-least-once.
- External side effects are at-most-once per idempotency key.
- Every state transition is auditable via append-only task events.
- All capability use is denied by default unless explicitly allowed by policy.

## 5) Functional Requirements (by subsystem)

All normative requirements in this section use RFC keywords and are authoritative.

### 5.1 Runtime/Daemon

| Req ID | Normative Requirement | Verification Hook |
| --- | --- | --- |
| GC-SPEC-RUN-001 | The daemon MUST start components in this order: config load, schema migration, recovery scan, policy load, ACP listener, scheduler, optional TUI. Startup SHALL fail closed if any mandatory step fails. | V-RUN-001 (Integration startup-order test) |
| GC-SPEC-RUN-002 | The scheduler MUST enforce bounded concurrency with a configurable lane count and SHALL NOT exceed configured lanes. | V-RUN-002 (Concurrency stress test) |
| GC-SPEC-RUN-003 | The daemon MUST implement graceful shutdown phases: stop intake, cancel leases, drain active lanes, flush events, close DB. | V-RUN-003 (Signal/termination integration test) |
| GC-SPEC-RUN-004 | Runtime components MUST propagate `trace_id`, `run_id`, `task_id`, and `session_id` across all internal calls. | V-RUN-004 (Trace propagation test) |
| GC-SPEC-RUN-005 | Any subsystem initialization failure MUST produce a structured fatal event with explicit reason code before process exit. | V-RUN-005 (Failure injection test) |

### 5.2 Persistence (SQLite)

| Req ID | Normative Requirement | Verification Hook |
| --- | --- | --- |
| GC-SPEC-PER-001 | SQLite MUST run in WAL mode with foreign keys enabled; durability mode MUST default to `synchronous=FULL` for queue and event writes. | V-PER-001 (PRAGMA assertion test) |
| GC-SPEC-PER-002 | The runtime MUST configure a `busy_timeout` and retry transient lock errors with bounded jitter. | V-PER-002 (Lock contention test) |
| GC-SPEC-PER-003 | Write transactions MUST be short-lived and SHALL NOT include network I/O or model/tool execution. | V-PER-003 (Transaction scope instrumentation) |
| GC-SPEC-PER-004 | Queue, event, and audit writes MUST be persisted in ACID transactions with explicit rollback on failure. | V-PER-004 (Fault injection test) |
| GC-SPEC-PER-005 | Backups MUST use an online-consistent SQLite backup mechanism; direct file copy of only the main DB file SHALL NOT be the backup procedure. | V-PER-005 (Backup/restore drill) |
| GC-SPEC-PER-006 | On startup, the daemon MUST run recovery that reconciles expired leases and unfinished tasks without manual intervention. | V-PER-006 (Crash recovery test) |

### 5.3 Task Model & State Machine

#### Canonical Task States

`QUEUED -> CLAIMED -> RUNNING -> SUCCEEDED`

`QUEUED -> CLAIMED -> RUNNING -> RETRY_WAIT -> QUEUED`

`QUEUED|CLAIMED|RUNNING -> CANCELED`

`RUNNING|RETRY_WAIT -> FAILED`

`FAILED -> DEAD_LETTER`

| Req ID | Normative Requirement | Verification Hook |
| --- | --- | --- |
| GC-SPEC-STM-001 | The system MUST use the canonical state set: `QUEUED`, `CLAIMED`, `RUNNING`, `RETRY_WAIT`, `SUCCEEDED`, `FAILED`, `CANCELED`, `DEAD_LETTER`. | V-STM-001 (State enum + transition tests) |
| GC-SPEC-STM-002 | State transitions MUST follow the allowed transition graph; illegal transitions SHALL be rejected atomically. | V-STM-002 (Negative transition tests) |
| GC-SPEC-STM-003 | Every state transition MUST append one immutable `task_event` record in the same transaction as the task row update. | V-STM-003 (Atomicity integration test) |
| GC-SPEC-STM-004 | Completion (`SUCCEEDED`/`FAILED`) MUST include final attempt metadata and terminal reason code. | V-STM-004 (Completion metadata test) |
| GC-SPEC-STM-005 | Cancel requests MUST be represented as explicit events and MUST be observed before and after each tool call boundary. | V-STM-005 (Abort race test) |

### 5.4 Queue Semantics (leases, retries, DLQ, idempotency)

| Req ID | Normative Requirement | Verification Hook |
| --- | --- | --- |
| GC-SPEC-QUE-001 | Task claim MUST be atomic and lease-based, selecting only claimable tasks (`QUEUED` and due) and assigning `lease_owner` + `lease_expires_at`. | V-QUE-001 (Concurrent claim race test) |
| GC-SPEC-QUE-002 | Workers MUST heartbeat leases while running; missed heartbeat beyond lease expiry SHALL make task reclaimable. | V-QUE-002 (Lease expiry/heartbeat test) |
| GC-SPEC-QUE-003 | Retry scheduling MUST use exponential backoff with jitter and a configurable cap (`max_attempts`). | V-QUE-003 (Retry timing test) |
| GC-SPEC-QUE-004 | Tasks that exceed `max_attempts` MUST transition to `DEAD_LETTER`; they SHALL NOT be auto-retried further. | V-QUE-004 (DLQ transition test) |
| GC-SPEC-QUE-005 | Poison-pill detection MUST classify repeated equivalent failures and MAY short-circuit to `DEAD_LETTER` before `max_attempts`. | V-QUE-005 (Poison-pill heuristic test) |
| GC-SPEC-QUE-006 | Side-effecting tool calls MUST include a stable idempotency key and MUST be deduplicated against prior successful side effects. | V-QUE-006 (Idempotency dedupe test) |
| GC-SPEC-QUE-007 | Scheduler fairness MUST prevent session starvation using priority + aging policy. | V-QUE-007 (Fairness simulation test) |
| GC-SPEC-QUE-008 | Saturation MUST apply backpressure at intake while preserving task durability; requests SHALL NOT be dropped silently. | V-QUE-008 (Load/backpressure integration test) |

### 5.5 ACP WebSocket + JSON-RPC (contract, reconnect, backpressure, ordering, versioning, security)

#### ACP Methods (v1)

Client -> Server:
- `system.hello`
- `agent.chat`
- `agent.abort`
- `session.history`
- `session.events.subscribe`
- `system.status`

Server -> Client notifications:
- `session.event`
- `tools.updated`
- `approval.required`
- `system.backpressure`

| Req ID | Normative Requirement | Verification Hook |
| --- | --- | --- |
| GC-SPEC-ACP-001 | ACP transport MUST be WebSocket at `/ws` and payloads MUST conform to JSON-RPC 2.0 framing. | V-ACP-001 (Contract test suite) |
| GC-SPEC-ACP-002 | Connections MUST perform `system.hello` version negotiation before processing task-mutating methods. | V-ACP-002 (Handshake protocol test) |
| GC-SPEC-ACP-003 | Local auth MUST be enforced using a bearer token; unauthenticated connections SHALL be rejected. | V-ACP-003 (Auth rejection test) |
| GC-SPEC-ACP-004 | Browser-originated connections MUST pass explicit Origin allowlist validation. | V-ACP-004 (Origin policy test) |
| GC-SPEC-ACP-005 | Server notifications MUST carry monotonic `event_id` per session and include correlation fields (`trace_id`, `run_id`, `task_id`). | V-ACP-005 (Event schema/order test) |
| GC-SPEC-ACP-006 | Ordering guarantee SHALL be per-session FIFO by `event_id`; global ordering across sessions is not guaranteed. | V-ACP-006 (Ordering semantics test) |
| GC-SPEC-ACP-007 | Reconnect replay MUST support `from_event_id`; if cursor is too old, server MUST return explicit replay-gap error. | V-ACP-007 (Reconnect/replay gap test) |
| GC-SPEC-ACP-008 | Outbound event buffering MUST be bounded per connection; overflow MUST emit `system.backpressure` and close connection deterministically. | V-ACP-008 (Backpressure saturation test) |
| GC-SPEC-ACP-009 | Protocol compatibility MUST retain OpenClaw field aliases where required (`text` alias for `content`) for v0.1 clients. | V-ACP-009 (Compatibility contract test) |

### 5.6 “Brain” abstraction boundary (Genkit integration; tool calling contract)

| Req ID | Normative Requirement | Verification Hook |
| --- | --- | --- |
| GC-SPEC-BRN-001 | Brain integration MUST be behind an interface boundary; scheduler and persistence layers SHALL NOT depend on provider-specific SDK types. | V-BRN-001 (Architecture dependency test) |
| GC-SPEC-BRN-002 | Brain outputs MUST be normalized into deterministic tool-intent records before execution. | V-BRN-002 (Tool-intent normalization test) |
| GC-SPEC-BRN-003 | Tool-call contracts MUST be schema-versioned and validated prior to execution; invalid intent SHALL fail task attempt safely. | V-BRN-003 (Schema validation test) |
| GC-SPEC-BRN-004 | Context compaction MUST preserve provenance references to original message IDs; compaction SHALL NOT delete provenance metadata. | V-BRN-004 (Compaction provenance test) |

### 5.7 Skills: modular system, SKILL.md compatibility, WASM runtime, GitHub installation

GoClaw implements a modular skill system compatible with the [Agent Skills open standard](https://agentskills.io/specification). Skills are loaded from multiple sources with precedence-based resolution (project > user > installed > builtin), support the canonical YAML frontmatter format (`---` delimiters + markdown body), and can be installed from GitHub repositories via `goclaw skill install <url>`. Installed skills start with zero capabilities (default-deny) and must be explicitly granted permissions in `policy.yaml`.

Three skill types are supported under one registry:

| Type | Execution | Policy Gate |
|------|-----------|-------------|
| **WASM** | wazero sandbox with memory/CPU/time limits | `wasm.*` capabilities |
| **Legacy** | `/bin/sh -lc` in workspace directory | `legacy.run` + `legacy.dangerous` |
| **Instruction** | Injected into Brain prompt context | `skill.inject` capability |

Skills use progressive disclosure: only `name` + `description` are loaded at startup; full instructions load on demand when the Brain activates a skill.

#### WASM Runtime Requirements

| Req ID | Normative Requirement | Verification Hook |
| --- | --- | --- |
| GC-SPEC-SKL-001 | Production skill loading MUST consume compiled `.wasm` modules with manifest metadata; source compilation is dev-mode only. | V-SKL-001 (Mode-gating test) |
| GC-SPEC-SKL-002 | Skill ABI MUST be versioned; ABI mismatch SHALL prevent activation and preserve prior active version. | V-SKL-002 (ABI mismatch test) |
| GC-SPEC-SKL-003 | Hot reload MUST be two-phase (`stage -> validate -> activate`) with atomic rollback on any failure. | V-SKL-003 (Atomic reload rollback test) |
| GC-SPEC-SKL-004 | Skill module cache MUST key by content hash and ABI version to avoid stale artifact reuse. | V-SKL-004 (Cache correctness test) |
| GC-SPEC-SKL-005 | Each skill invocation MUST enforce CPU/memory/time limits and emit deterministic timeout/fault reason codes. | V-SKL-005 (Resource-limit test) |
| GC-SPEC-SKL-006 | Debug hooks MUST emit structured lifecycle events (`compile`, `load`, `activate`, `invoke`, `fault`) correlated to trace IDs. | V-SKL-006 (Lifecycle event test) |
| GC-SPEC-SKL-007 | Repeated skill faults above threshold MUST quarantine the skill until explicit operator re-enable. | V-SKL-007 (Auto-quarantine test) |

#### Skill System Requirements

| Req ID | Normative Requirement | Verification Hook |
| --- | --- | --- |
| GC-SPEC-SKL-008 | The SKILL.md parser MUST support canonical YAML frontmatter (`---` delimiters) with markdown body, plain YAML, and markdown-fallback formats in that order. | V-SKL-008 (Parser format test) |
| GC-SPEC-SKL-009 | Skill loading MUST resolve sources in precedence order: project, user, installed, builtin. Name collisions MUST be won by highest-priority source. | V-SKL-009 (Precedence test) |
| GC-SPEC-SKL-010 | Skills installed from external sources MUST start with zero granted capabilities and MUST be explicitly enabled in policy. | V-SKL-010 (Default-deny install test) |
| GC-SPEC-SKL-011 | Eligibility filtering MUST exclude skills with unmet runtime requirements (missing bins, env vars, wrong OS) from the Brain's active catalog without error. | V-SKL-011 (Eligibility filter test) |
| GC-SPEC-SKL-012 | Skill provenance (source type, source URL, install ref) MUST be persisted in the skill registry and included in audit log entries. | V-SKL-012 (Provenance audit test) |

### 5.8 Policy/Security (default deny, allowlist governance, SSRF constraints, secrets/redaction)

| Req ID | Normative Requirement | Verification Hook |
| --- | --- | --- |
| GC-SPEC-SEC-001 | Authorization MUST be default-deny across all capability classes. | V-SEC-001 (Policy default-deny test) |
| GC-SPEC-SEC-002 | All execution paths (ACP methods, WASM host calls, legacy bridge) MUST enforce authorization through a centralized PDP contract. | V-SEC-002 (Cross-path enforcement test) |
| GC-SPEC-SEC-003 | Policy decisions MUST be versioned and task execution MUST pin policy version at attempt start. | V-SEC-003 (Policy pinning test) |
| GC-SPEC-SEC-004 | HTTP egress policy MUST enforce SSRF constraints: deny loopback, link-local, private CIDRs, non-http(s) schemes by default. | V-SEC-004 (SSRF defense test) |
| GC-SPEC-SEC-005 | Secret-bearing fields MUST be redacted prior to log, event, and error persistence. | V-SEC-005 (Redaction test) |
| GC-SPEC-SEC-006 | Every allow/deny/approval decision MUST be recorded in immutable audit log with reason and policy version. | V-SEC-006 (Audit completeness test) |
| GC-SPEC-SEC-007 | Policy reload on invalid config MUST fail closed (retain previous valid policy and reject unknown capabilities). | V-SEC-007 (Invalid policy reload test) |
| GC-SPEC-SEC-008 | High-risk legacy actions MUST require Approval Broker workflow; TUI-only confirmation SHALL NOT be the sole approval path. | V-SEC-008 (Headless approval test) |

### 5.9 Observability (trace IDs, structured logs, audit log, run replay)

| Req ID | Normative Requirement | Verification Hook |
| --- | --- | --- |
| GC-SPEC-OBS-001 | Structured logs MUST be JSON with required fields: timestamp, level, component, trace_id, run_id/task_id when applicable. | V-OBS-001 (Log schema test) |
| GC-SPEC-OBS-002 | `task_events` MUST provide complete run timeline sufficient for deterministic replay of control flow (excluding external side effects). | V-OBS-002 (Replay completeness test) |
| GC-SPEC-OBS-003 | Audit logs MUST be physically separated from normal logs and append-only at application layer. | V-OBS-003 (Audit append-only test) |
| GC-SPEC-OBS-004 | Metrics endpoint MUST expose queue depth, active lanes, lease expiries, retries, DLQ size, and policy deny rate. | V-OBS-004 (Metrics coverage test) |
| GC-SPEC-OBS-005 | Health endpoint MUST report DB status, policy version, replay backlog status, and skill runtime status. | V-OBS-005 (Health contract test) |
| GC-SPEC-OBS-006 | Incident export MUST support bounded run bundle (events + redacted logs + config metadata hash) for offline debugging. | V-OBS-006 (Incident export test) |

### 5.10 TUI (minimal operational UI)

| Req ID | Normative Requirement | Verification Hook |
| --- | --- | --- |
| GC-SPEC-TUI-001 | TUI MUST be operational-only (status, approvals, diagnostics) and SHALL NOT contain unique business logic unavailable to ACP clients. | V-TUI-001 (Feature parity review) |
| GC-SPEC-TUI-002 | TUI MUST display active lanes, queue depth, retry pressure, DLQ count, and policy alerts in near-real-time. | V-TUI-002 (Manual operational check) |
| GC-SPEC-TUI-003 | TUI MUST surface Approval Broker requests with approve/deny actions and explicit timeout behavior. | V-TUI-003 (Approval UX test) |
| GC-SPEC-TUI-004 | In non-TTY mode, approval and operations MUST remain available through ACP methods/events without deadlock. | V-TUI-004 (Headless integration test) |

## 6) Data Model

### 6.1 Tables, indexes, constraints

The following entities are normative for v0.1.

| Table | Purpose | Required Fields |
| --- | --- | --- |
| `schema_migrations` | Migration ledger | `version PK`, `checksum`, `applied_at` |
| `sessions` | Session identity | `id PK`, `created_at`, `updated_at` |
| `messages` | Conversation history | `id PK`, `session_id FK`, `role`, `content`, `tokens`, `created_at`, `archived_at NULL` |
| `tasks` | Current task projection | `id PK`, `session_id FK`, `state`, `priority`, `attempt`, `max_attempts`, `available_at`, `lease_owner`, `lease_expires_at`, `cancel_requested`, `last_error_code`, `created_at`, `updated_at` |
| `task_events` | Append-only run/event timeline | `event_id PK`, `task_id FK`, `session_id FK`, `run_id`, `event_type`, `state_from`, `state_to`, `payload_json`, `created_at` |
| `tool_call_dedup` | Idempotency registry | `idempotency_key PK`, `tool_name`, `request_hash`, `side_effect_status`, `result_hash`, `created_at`, `updated_at` |
| `skill_registry` | Skill metadata + lifecycle | `skill_id PK`, `version`, `abi_version`, `content_hash`, `state`, `last_fault_at`, `fault_count` |
| `policy_versions` | Policy lineage | `policy_version PK`, `checksum`, `loaded_at`, `source` |
| `approvals` | Approval Broker queue | `approval_id PK`, `task_id FK`, `capability`, `resource`, `status`, `expires_at`, `resolved_at` |
| `kv_store` | Persistent key-value memory | `key PK`, `value`, `updated_at` |
| `audit_log` | Security/audit trail | `audit_id PK`, `trace_id`, `subject`, `action`, `decision`, `reason`, `policy_version`, `created_at` |

Required indexes:
- `tasks(state, available_at, priority, created_at)`
- `tasks(lease_expires_at)`
- `task_events(session_id, event_id)`
- `task_events(task_id, event_id)`
- `messages(session_id, id)`
- `approvals(status, expires_at)`

### 6.2 Migrations strategy, backward compatibility, downgrade story

| Req ID | Normative Requirement | Verification Hook |
| --- | --- | --- |
| GC-SPEC-DATA-001 | Schema migrations MUST be forward-only, monotonic, and checksummed. | V-DATA-001 (Migration ledger test) |
| GC-SPEC-DATA-002 | Startup MUST refuse to run if DB schema version is newer than binary-supported max version. | V-DATA-002 (Version guard test) |
| GC-SPEC-DATA-003 | Each migration MUST run inside a transaction and MUST emit migration audit events. | V-DATA-003 (Migration atomicity test) |
| GC-SPEC-DATA-004 | Downgrade SHALL be supported only via restore-from-backup snapshot, not reverse migrations. | V-DATA-004 (Restore rollback drill) |

### 6.3 Retention policy + PII handling

| Req ID | Normative Requirement | Verification Hook |
| --- | --- | --- |
| GC-SPEC-DATA-005 | Retention defaults MUST be configurable and MUST include separate windows for logs, task events, and audit logs. | V-DATA-005 (Retention config test) |
| GC-SPEC-DATA-006 | User-triggered purge MUST delete or tombstone PII-bearing records across messages, events, and logs according to policy. | V-DATA-006 (PII purge test) |
| GC-SPEC-DATA-007 | Redaction metadata MUST be retained to prove sanitization without retaining secret values. | V-DATA-007 (Redaction metadata test) |
| GC-SPEC-DATA-008 | Retention jobs MUST be idempotent and auditable. | V-DATA-008 (Retention idempotency test) |

## 7) Configuration

### 7.1 File locations

- `~/.goclaw/config.yaml`
- `~/.goclaw/policy.yaml`
- `~/.goclaw/SOUL.md`
- `~/.goclaw/AGENTS.md`
- `~/.goclaw/auth.token`
- `~/.goclaw/goclaw.db`
- `~/.goclaw/logs/system.jsonl`
- `~/.goclaw/logs/audit.jsonl`

### 7.2 Precedence and reload rules

| Req ID | Normative Requirement | Verification Hook |
| --- | --- | --- |
| GC-SPEC-CFG-001 | Configuration precedence MUST be `environment variables > config.yaml > defaults`. | V-CFG-001 (Config precedence test) |
| GC-SPEC-CFG-002 | Policy and SOUL files MAY hot-reload after validation; DB path and network bind settings SHALL require process restart. | V-CFG-002 (Reload behavior test) |
| GC-SPEC-CFG-003 | Invalid hot-reload input MUST keep prior valid runtime state and emit explicit audit + alert events. | V-CFG-003 (Invalid reload resilience test) |
| GC-SPEC-CFG-004 | `auth.token` MUST be generated on first run if missing and MUST use restrictive file permissions. | V-CFG-004 (Token bootstrap test) |
| GC-SPEC-CFG-005 | The daemon MUST expose the active config fingerprint (hash only) in `system.status`. | V-CFG-005 (Status fingerprint test) |
| GC-SPEC-CFG-006 | Policy file changes MUST be versioned and persisted in `policy_versions` before becoming active. | V-CFG-006 (Policy versioning test) |
| GC-SPEC-CFG-007 | Legacy skill mode MUST be explicitly enabled by config flag; default is disabled. | V-CFG-007 (Legacy mode default test) |

## 8) Performance & Resource Targets

| Req ID | Normative Requirement | Verification Hook |
| --- | --- | --- |
| GC-SPEC-PERF-001 | Cold startup on reference hardware MUST complete in <= 1.0s to ACP readiness. | V-PERF-001 (Startup benchmark) |
| GC-SPEC-PERF-002 | Idle memory on reference hardware SHOULD remain <= 120 MB. | V-PERF-002 (Memory profile report) |
| GC-SPEC-PERF-003 | Scheduler MUST sustain at least 10 concurrent active sessions and 4 active lanes without lock-thrash failure. | V-PERF-003 (Load test) |
| GC-SPEC-PERF-004 | Queue claim/update path MUST maintain p95 DB write transaction duration <= 50ms under nominal load. | V-PERF-004 (DB latency profiling) |
| GC-SPEC-PERF-005 | ACP reconnect replay for 1,000 events MUST complete within 2s on reference hardware. | V-PERF-005 (Replay performance test) |
| GC-SPEC-PERF-006 | Skill reload path MUST avoid global scheduler stalls; activation SHALL be non-blocking for unrelated lanes. | V-PERF-006 (Hot-reload contention test) |

## 9) Reliability & Recovery Semantics

| Req ID | Normative Requirement | Verification Hook |
| --- | --- | --- |
| GC-SPEC-REL-001 | Task execution semantics MUST be at-least-once; this guarantee SHALL be documented in all user-visible reliability docs. | V-REL-001 (Doc + behavior conformance review) |
| GC-SPEC-REL-002 | Side-effect execution semantics MUST be at-most-once per idempotency key when tool reports success. | V-REL-002 (Duplicate side-effect chaos test) |
| GC-SPEC-REL-003 | Crash recovery MUST reclaim expired leases and requeue unfinished tasks deterministically. | V-REL-003 (SIGKILL recovery test) |
| GC-SPEC-REL-004 | Replay semantics MUST allow ACP clients to reconstruct session timeline from `task_events` plus `messages` history. | V-REL-004 (Timeline reconstruction test) |
| GC-SPEC-REL-005 | Graceful shutdown MUST enforce bounded drain timeout and MUST mark unfinished work for safe retry on next start. | V-REL-005 (Shutdown-drain test) |
| GC-SPEC-REL-006 | Backup/restore procedure MUST define target RPO <= 5s and RTO <= 5min under nominal local conditions. | V-REL-006 (Disaster recovery drill) |
| GC-SPEC-REL-007 | Recovery and retry behavior MUST be deterministic with explicit reason codes for each retry and terminal state. | V-REL-007 (Retry reason-code test) |

## 10) Acceptance Criteria & Verification

### 10.1 Gate-0 checklist (must pass)

1. `GC-SPEC-SCOPE-001`: Build outputs single executable artifacts per target OS.
2. `GC-SPEC-PER-001`: WAL + durability PRAGMAs verified at runtime.
3. `GC-SPEC-STM-001` + `GC-SPEC-STM-002`: Canonical state machine enforced.
4. `GC-SPEC-QUE-001`: Atomic lease claim verified under race.
5. `GC-SPEC-ACP-001` + `GC-SPEC-ACP-002`: ACP contract + handshake validated.
6. `GC-SPEC-ACP-003`: Auth required for ACP.
7. `GC-SPEC-SEC-001` + `GC-SPEC-SEC-002`: Default-deny and centralized policy enforcement active.
8. `GC-SPEC-OBS-001`: Structured logging schema validated.
9. `GC-SPEC-DATA-001`: Migration ledger/checksum flow in place.
10. `GC-SPEC-REL-003`: Crash recovery deterministic for leased tasks.

### 10.2 v0.1 checklist (must pass)

1. Gate-0 completed with evidence artifacts.
2. Retry + DLQ + poison-pill paths validated (`GC-SPEC-QUE-003/004/005`).
3. ACP replay/backpressure paths validated (`GC-SPEC-ACP-007/008`).
4. Idempotent side-effect dedupe validated (`GC-SPEC-QUE-006`, `GC-SPEC-REL-002`).
5. WASM ABI/hot-reload/rollback validated (`GC-SPEC-SKL-002/003`).
6. SSRF + redaction + audit requirements validated (`GC-SPEC-SEC-004/005/006`).
7. Incident replay/export validated (`GC-SPEC-OBS-002/006`).
8. Backup/restore drill passes target RPO/RTO (`GC-SPEC-PER-005`, `GC-SPEC-REL-006`).
9. Headless approval workflow validated (`GC-SPEC-SEC-008`, `GC-SPEC-TUI-004`).
10. Non-goals verified unchanged (`GC-SPEC-NG-001/002/003`).

### 10.3 Evidence matrix (unit/integration/manual) mapped to requirement IDs

| Requirement IDs | Verification | Evidence Type |
| --- | --- | --- |
| `GC-SPEC-SCOPE-001..003` | V-SCOPE-* | Build artifact report + integration transcript |
| `GC-SPEC-NG-001..003` | V-NG-* | Architecture/dependency audit checklist |
| `GC-SPEC-RUN-001..005` | V-RUN-* | Integration tests + startup/shutdown logs |
| `GC-SPEC-PER-001..006` | V-PER-* | Unit/integration DB tests + recovery drills |
| `GC-SPEC-STM-001..005` | V-STM-* | State transition unit tests + race tests |
| `GC-SPEC-QUE-001..008` | V-QUE-* | Concurrency/chaos/load tests |
| `GC-SPEC-ACP-001..009` | V-ACP-* | Protocol contract tests + reconnect simulations |
| `GC-SPEC-BRN-001..004` | V-BRN-* | Interface boundary tests + compaction tests |
| `GC-SPEC-SKL-001..007` | V-SKL-* | Skill lifecycle tests + fault injection |
| `GC-SPEC-SEC-001..008` | V-SEC-* | Security tests + policy audit evidence |
| `GC-SPEC-OBS-001..006` | V-OBS-* | Log schema tests + replay/export validation |
| `GC-SPEC-TUI-001..004` | V-TUI-* | Manual ops checks + headless integration tests |
| `GC-SPEC-DATA-001..008` | V-DATA-* | Migration/retention/PII tests |
| `GC-SPEC-CFG-001..007` | V-CFG-* | Config precedence + reload tests |
| `GC-SPEC-PERF-001..006` | V-PERF-* | Benchmark/load reports |
| `GC-SPEC-REL-001..007` | V-REL-* | Chaos/recovery/disaster drills |

## 11) Traceability Hooks

### 11.1 Requirement ID format

- Format: `GC-SPEC-<DOMAIN>-<NNN>`
- Domain set: `SCOPE`, `NG`, `RUN`, `PER`, `STM`, `QUE`, `ACP`, `BRN`, `SKL`, `SEC`, `OBS`, `TUI`, `DATA`, `CFG`, `PERF`, `REL`
- IDs are immutable after merge; deprecations retain historical IDs with explicit status markers.

### 11.2 Required updates to traceability artifacts

1. `docs/SPEC_INDEX.md`
- Add all new requirement IDs with one-line normative summary.
- Mark superseded IDs from previous SPEC as deprecated.

2. `docs/TRACEABILITY.md`
- Map each `GC-SPEC-*` ID to at least one verification hook and one evidence artifact path.
- Keep Gate-0 and v0.1 sections synchronized with Section 10 of this SPEC.

3. `TODO.md`
- Track implementation work and resolved decisions.
- Skill system phases, remaining work, and testing gaps.

## 12) Compatibility Notes (MANDATORY)

### 12.1 Deviations from old_SPEC/old_PDR

1. Durability and semantics clarified:
- Old claim "recover exact state after power loss" is replaced with explicit guarantees:
  - at-least-once task execution
  - at-most-once side effects per idempotency key
  - deterministic recovery via leases + events

2. Task model expanded:
- Replaces 4-state task model (`PENDING/RUNNING/COMPLETED/FAILED`) with canonical 8-state model including `RETRY_WAIT` and `DEAD_LETTER`.

3. ACP hardened:
- Replaces fire-and-forget event assumptions with explicit ordering, cursor replay, backpressure handling, and auth/origin validation.

4. WASM lifecycle hardened:
- Introduces ABI versioning, two-phase hot reload, rollback, quarantine, and manifest-based production loading.

5. Security model unified:
- Replaces fragmented policy checks with centralized PDP/PEP enforcement and approval broker.

6. Backup model corrected:
- Replaces "copy DB file" guidance with online-consistent backup requirement.

### 12.2 Migration notes

- Legacy field aliasing (`text`/`content`) remains for ACP compatibility in v0.1.
- Existing deployments need migration steps to add lease, event, dedup, policy version, and approval tables before enabling scheduler.
- Existing operational docs need reliability language updates to match Section 9 semantics.

### 12.3 Renamed concepts

- "Task Queue" -> "Lease-based Durable Queue"
- "History" -> "Messages" (canonical table name)
- "TUI confirmation" -> "Approval Broker" (TUI is one UI surface)
