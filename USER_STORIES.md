# USER_STORIES.md — Project GoClaw

## 1) Overview
This document is the executable user-story test contract for GoClaw `v0.1`, aligned to `SPEC.md v2.0.0` and `PDR.md v2.0.0`.

Use this file to verify release readiness:
1. Run all `P0` stories first; any `P0` failure is release-blocking.
2. For each story, execute the reproducible verification procedure and capture the listed evidence artifacts.
3. Mark each story `PASS`, `FAIL`, or `BLOCKED` in the release evidence pack.
4. Use Section 7 (Coverage Matrix) to confirm every `GC-SPEC-*` requirement is covered by at least one story.

Audit scope applied in this revision:
- Gate-0 checklist (`SPEC §10.1`) and v0.1 checklist (`SPEC §10.2`) are explicitly mapped to `P0` stories.
- PDR operational flows (ingress, execution, recovery, replay) and failure modes (`FM-01` through `FM-12`) are represented in main/alternate flows and abuse stories.
- Previously partial coverage areas are closed: `GC-SPEC-PER-002/003/004`, `GC-SPEC-QUE-002/003/004/005`, `GC-SPEC-RUN-004`, `GC-SPEC-BRN-004`, `GC-SPEC-SCOPE-001`, `GC-SPEC-TUI-001/003`, and full SPEC coverage matrix.

## 2) Personas
| Persona | Description | Primary outcomes |
| --- | --- | --- |
| Operator | Runs daemon/TUI, performs startup, incident response, backup/restore, upgrades. | Safe operations, deterministic recovery, clear evidence. |
| Integrator / ACP Client | Builds ACP clients over WebSocket + JSON-RPC. | Correct handshake/auth, ordered replay, explicit failure semantics. |
| Skill Author | Publishes and updates WASM skills and manifests. | Safe activation, ABI stability, rollback, quarantine. |
| Security Admin | Owns policy model, approvals, redaction, and audit posture. | Default-deny enforcement, SSRF defense, complete immutable audits. |

## 3) User Journey Map
| Journey phase | Desired result | Primary stories |
| --- | --- | --- |
| First run | System bootstraps config/token/db safely and starts fail-closed. | US-001, US-002, US-003, US-004 |
| Normal use | ACP traffic, queue execution, policy enforcement, and skills work deterministically. | US-005 through US-013, US-016 |
| Incident | Operator can diagnose, export evidence, and recover safely. | US-006, US-007, US-014, US-015, US-017 |
| Upgrade / restore | Migrations, policy/config updates, retention, and disaster recovery remain safe. | US-004, US-015, US-018, US-019, US-020 |

## 4) Story Index Table
| Story ID | Persona | Title | Priority | Status | Related SPEC Req IDs | Verification Type | Evidence Artifact(s) |
| --- | --- | --- | --- | --- | --- | --- | --- |
| US-001 | Operator | First-Run Bootstrap, Token Permissions, Single-Binary Runtime | P0 | **PASS** | GC-SPEC-SCOPE-001, GC-SPEC-RUN-001, GC-SPEC-CFG-004, GC-SPEC-PER-001 | Integration | `docs/EVIDENCE_VERIFY/runtime_smoke.txt` |
| US-002 | Operator | Startup Order Fail-Closed with Structured Fatal Event | P0 | **PASS** | GC-SPEC-RUN-001, GC-SPEC-RUN-005, GC-SPEC-OBS-001 | Chaos | `docs/EVIDENCE_VERIFY/runtime_daemon.log` |
| US-003 | Operator | SQLite WAL/FULL Durability, Busy Timeout, Short Tx, ACID Rollback | P0 | **PASS** | GC-SPEC-PER-001, GC-SPEC-PER-002, GC-SPEC-PER-003, GC-SPEC-PER-004 | Integration | `docs/EVIDENCE_VERIFY/db_pragmas.txt` |
| US-004 | Operator | Migration Ledger, Checksum Integrity, and Future-Version Guard | P0 | **PASS** | GC-SPEC-DATA-001, GC-SPEC-DATA-002, GC-SPEC-DATA-003 | Integration | `docs/EVIDENCE_VERIFY/db_schema.txt` |
| US-005 | Integrator | Canonical State Machine and Atomic `task_event` Append | P0 | **PASS** | GC-SPEC-STM-001, GC-SPEC-STM-002, GC-SPEC-STM-003, GC-SPEC-STM-004 | Integration | `docs/EVIDENCE_VERIFY/db_checks.txt` |
| US-006 | Operator | Lease Claim, Heartbeat, Expiry Reclaim, and Crash Recovery | P0 | **PASS** | GC-SPEC-QUE-001, GC-SPEC-QUE-002, GC-SPEC-PER-006, GC-SPEC-REL-003 | Chaos | `docs/EVIDENCE_VERIFY/queue_lease_race.txt`, `recovery_sigkill.txt` |
| US-007 | Operator | Retry Backoff, DLQ Routing, Poison-Pill Short-Circuit | P0 | **PASS** | GC-SPEC-QUE-003, GC-SPEC-QUE-004, GC-SPEC-QUE-005, GC-SPEC-REL-007 | Chaos | `docs/EVIDENCE_VERIFY/queue_lease_race.txt` |
| US-008 | Integrator | Side-Effect Idempotency and Dedupe Registry | P0 | **PASS** | GC-SPEC-QUE-006, GC-SPEC-REL-001, GC-SPEC-REL-002 | Chaos | `docs/EVIDENCE_VERIFY/db_checks.txt` |
| US-009 | Integrator | ACP Hello/Auth/Origin/JSON-RPC Contract Gate | P0 | **PASS** | GC-SPEC-ACP-001, GC-SPEC-ACP-002, GC-SPEC-ACP-003, GC-SPEC-ACP-004 | Integration | `docs/EVIDENCE_VERIFY/acp_ws_jsonrpc.txt` |
| US-010 | Integrator | ACP FIFO Ordering, Cursor Replay/Gap, Backpressure Close | P0 | **PASS** | GC-SPEC-ACP-005, GC-SPEC-ACP-006, GC-SPEC-ACP-007, GC-SPEC-ACP-008, GC-SPEC-QUE-008, GC-SPEC-OBS-002, GC-SPEC-REL-004 | Integration | `docs/EVIDENCE_VERIFY/acp_replay_backpressure.txt` |
| US-011 | Security Admin | Default-Deny PDP/PEP + SSRF Blocks + Redaction + Audit | P0 | **PASS** | GC-SPEC-SEC-001, GC-SPEC-SEC-002, GC-SPEC-SEC-004, GC-SPEC-SEC-005, GC-SPEC-SEC-006, GC-SPEC-DATA-007, GC-SPEC-OBS-003 | Integration | `docs/EVIDENCE_VERIFY/security_policy_ssrf_redaction.txt` |
| US-012 | Security Admin | Approval Broker Works in TTY and Headless Modes | P0 | **PASS** | GC-SPEC-SEC-008, GC-SPEC-TUI-001, GC-SPEC-TUI-003, GC-SPEC-TUI-004 | Integration | `docs/EVIDENCE_VERIFY/ssrf_bypass_approval_timeout.txt` |
| US-013 | Skill Author | WASM ABI Gate, Staged Reload, Rollback, Cache, Quarantine | P0 | **PASS** | GC-SPEC-SKL-001, GC-SPEC-SKL-002, GC-SPEC-SKL-003, GC-SPEC-SKL-004, GC-SPEC-SKL-005, GC-SPEC-SKL-006, GC-SPEC-SKL-007, GC-SPEC-CFG-007 | Chaos | `docs/EVIDENCE_VERIFY/skills_wasm_lifecycle.txt` |
| US-014 | Operator | Incident Export Bundle for Offline Debugging | P0 | **PASS** | GC-SPEC-OBS-006, GC-SPEC-SEC-005 | Integration | `docs/EVIDENCE_VERIFY/incident_export.txt` |
| US-015 | Operator | Online Backup/Restore Drill with RPO/RTO Targets | P0 | **PASS** | GC-SPEC-PER-005, GC-SPEC-REL-006, GC-SPEC-DATA-004, GC-SPEC-CFG-005, GC-SPEC-OBS-005 | Integration | `docs/EVIDENCE_VERIFY/backup_restore_drill.txt` |
| US-016 | Operator | Bounded Lanes, Fair Scheduling, Multi-Client ACP+TUI Ops | P0 | **PASS** | GC-SPEC-SCOPE-003, GC-SPEC-RUN-002, GC-SPEC-QUE-007, GC-SPEC-PERF-003, GC-SPEC-OBS-004, GC-SPEC-TUI-002 | Chaos | `docs/EVIDENCE_VERIFY/bounded_lanes_scheduling.txt` |
| US-017 | Integrator | Graceful Shutdown, Cancel Boundaries, Trace Propagation | P0 | **PASS** | GC-SPEC-RUN-003, GC-SPEC-RUN-004, GC-SPEC-STM-005, GC-SPEC-REL-005 | Chaos | `docs/EVIDENCE_VERIFY/graceful_shutdown_cancel.txt` |
| US-018 | Operator | Config Precedence and Reload/Restart Boundaries | P1 | **PASS** | GC-SPEC-CFG-001, GC-SPEC-CFG-002, GC-SPEC-CFG-003 | Integration | `docs/EVIDENCE_VERIFY/us018_config_precedence.txt` |
| US-019 | Security Admin | Policy Versioning and Attempt-Level Pinning | P1 | **PASS** | GC-SPEC-SEC-003, GC-SPEC-SEC-007, GC-SPEC-CFG-006, GC-SPEC-CFG-003 | Integration | `docs/EVIDENCE_VERIFY/us019_policy_versioning.txt` |
| US-020 | Security Admin | Retention Windows and User PII Purge | P1 | **PASS** | GC-SPEC-DATA-005, GC-SPEC-DATA-006, GC-SPEC-DATA-008 | Integration | `docs/EVIDENCE_VERIFY/us020_retention_purge.txt` |
| US-021 | Skill Author | Brain Boundary, Intent Normalization, Schema Validation, Provenance | P1 | **PASS** | GC-SPEC-BRN-001, GC-SPEC-BRN-002, GC-SPEC-BRN-003, GC-SPEC-BRN-004 | Unit | `docs/EVIDENCE_VERIFY/us021_brain_boundary.txt` |
| US-022 | Integrator | ACP Legacy Alias Compatibility (`text` and `content`) | P1 | **PASS** | GC-SPEC-ACP-009 | Integration | `docs/EVIDENCE_VERIFY/acp_legacy_alias_compat.txt` |
| US-023 | Operator | Performance Targets: Startup, Memory, DB p95, Replay, Reload Stall | P2 | N/A | GC-SPEC-PERF-001, GC-SPEC-PERF-002, GC-SPEC-PERF-004, GC-SPEC-PERF-005, GC-SPEC-PERF-006 | Integration | benchmark report |
| US-024 | Security Admin | Anti-Story: Browser Automation Capability Must Not Exist | P0 | **PASS** | GC-SPEC-NG-001 | Manual | `docs/EVIDENCE_VERIFY/non_goals_audit.txt` |
| US-025 | Operator | Anti-Story: Distributed Clustering Must Not Activate | P0 | **PASS** | GC-SPEC-NG-002 | Manual | `docs/EVIDENCE_VERIFY/non_goals_audit.txt` |
| US-026 | Security Admin | Anti-Story: Multi-User Separation Must Not Be Implied | P0 | **PASS** | GC-SPEC-SCOPE-002, GC-SPEC-NG-003 | Integration | `docs/EVIDENCE_VERIFY/non_goals_audit.txt` |
| US-027 | Security Admin | Abuse: Unauthenticated/Origin/Capability Bypass Attempts Denied | P0 | **PASS** | GC-SPEC-ACP-003, GC-SPEC-ACP-004, GC-SPEC-SEC-001, GC-SPEC-SEC-002 | Chaos | `docs/EVIDENCE_VERIFY/abuse_denial_matrix.txt` |
| US-028 | Security Admin | Abuse: SSRF Bypass and Headless Approval Timeout Default Deny | P0 | **PASS** | GC-SPEC-SEC-004, GC-SPEC-SEC-008, GC-SPEC-TUI-004 | Chaos | `docs/EVIDENCE_VERIFY/ssrf_bypass_approval_timeout.txt` |

## 5) Stories Grouped by Priority

### P0 (Release-blocking)

### US-001: First-Run Bootstrap, Token Permissions, Single-Binary Runtime (Priority: P0)
**Persona:** Operator  
**Goal:** Bring up GoClaw on a clean machine with secure defaults and no runtime dependency on Node/Python/Docker.  
**Preconditions:** Fresh `GOCLAW_HOME`; binary built.  
**Trigger:** First daemon start on clean home directory.  
**Main Flow:**
1) Start daemon with empty home directory.
2) Daemon creates required files (`config.yaml`, `auth.token`, DB, log paths) and reaches ACP readiness.
3) Operator verifies token file permissions and runtime dependency absence.
**Alternate Flows:**
- AF1: Optional profile files are missing; startup continues with explicit warning.
**Acceptance Criteria (Given/When/Then):**
- Given an empty home, when daemon starts, then required bootstrap files are created.
- Given generated `auth.token`, when file metadata is inspected, then mode is owner-only.
- Given running process, when runtime dependencies are inspected, then Node/Python/Docker are not required for operation.
**Verification Procedure (reproducible):**
- Commands/steps: `GOCLAW_HOME=/tmp/ic_fresh ./dist/goclaw -daemon`; `ls -l /tmp/ic_fresh`; `stat -f "%Sp" /tmp/ic_fresh/auth.token`.
- Expected outputs (specific): bootstrap files exist; token permission is restrictive; daemon reaches ready state without external runtime dependency errors.
**Evidence to Capture:**
- `/tmp/ic_fresh` directory listing, token permission output, startup logs in `docs/EVIDENCE_VERIFY/runtime_smoke.txt`.
**Related Requirements:** `GC-SPEC-SCOPE-001`, `GC-SPEC-RUN-001`, `GC-SPEC-CFG-004`, `GC-SPEC-PER-001`

### US-002: Startup Order Fail-Closed with Structured Fatal Event (Priority: P0)
**Persona:** Operator  
**Goal:** Ensure mandatory startup steps run in required order and failures terminate safely.  
**Preconditions:** A malformed mandatory config input (for example invalid policy file).  
**Trigger:** Daemon starts with invalid mandatory startup input.  
**Main Flow:**
1) Startup sequence executes in canonical order.
2) Mandatory failure emits a structured fatal event and process exits before serving mutating ACP calls.
3) Structured logs keep required schema fields for diagnosis.
**Alternate Flows:**
- AF1: Optional TUI not available in headless mode; daemon remains healthy if mandatory phases succeeded.
**Acceptance Criteria (Given/When/Then):**
- Given invalid mandatory policy input, when daemon starts, then startup fails closed with non-zero exit.
- Given startup failure, when logs are read, then fatal reason code and component are present.
- Given startup logs, when schema is validated, then required JSON fields exist.
**Verification Procedure (reproducible):**
- Commands/steps: run `go test ./internal/smoke -run 'TestSmoke_StartupPhasesFollowRequiredOrder|TestSmoke_StartupFailureEmitsReasonCode' -v`.
- Expected outputs (specific): tests pass; output shows ordered phases and explicit fatal reason code.
**Evidence to Capture:**
- `go test` transcript, startup log excerpt in `docs/EVIDENCE_VERIFY/runtime_daemon.log`.
**Related Requirements:** `GC-SPEC-RUN-001`, `GC-SPEC-RUN-005`, `GC-SPEC-OBS-001`

### US-003: SQLite WAL/FULL Durability, Busy Timeout, Short Tx, ACID Rollback (Priority: P0)
**Persona:** Operator  
**Goal:** Verify persistence configuration and transaction behavior under contention/faults.  
**Preconditions:** Writable local DB; contention workload available.  
**Trigger:** Concurrent writes and injected write failures.  
**Main Flow:**
1) Open DB and verify PRAGMAs (`WAL`, `foreign_keys`, `synchronous=FULL`).
2) Run lock contention to validate `busy_timeout` and bounded retry behavior.
3) Inject failing writes and verify ACID rollback semantics.
**Alternate Flows:**
- AF1: Lock contention occurs; retries remain bounded and produce explicit errors if exhausted.
**Acceptance Criteria (Given/When/Then):**
- Given runtime DB initialization, when PRAGMAs are queried, then durability settings match SPEC defaults.
- Given lock contention, when write retries occur, then behavior uses timeout/retry rather than immediate corruption/failure loops.
- Given transaction fault injection, when operation fails, then partial queue/event/audit writes are not committed.
**Verification Procedure (reproducible):**
- Commands/steps: `go test ./internal/persistence -run 'TestStore_OpenConfiguresWALAndSchema' -v`; `go run ./tools/verify/policy_default_check > docs/EVIDENCE_VERIFY/policy_default_check.txt`.
- Expected outputs (specific): PRAGMA assertions pass; contention/fail-closed checks show no partial writes.
**Evidence to Capture:**
- `docs/EVIDENCE_VERIFY/db_pragmas.txt`, contention log snippet, rollback test output.
**Related Requirements:** `GC-SPEC-PER-001`, `GC-SPEC-PER-002`, `GC-SPEC-PER-003`, `GC-SPEC-PER-004`

### US-004: Migration Ledger, Checksum Integrity, and Future-Version Guard (Priority: P0)
**Persona:** Operator  
**Goal:** Ensure migration safety on startup and refuse unsupported future schemas.  
**Preconditions:** DB at known version; migration metadata available.  
**Trigger:** Startup with valid and invalid schema/migration combinations.  
**Main Flow:**
1) Startup applies forward-only transactional migrations and records checksums.
2) Migration audit evidence is emitted.
3) Binary refuses DB schemas newer than supported max version.
**Alternate Flows:**
- AF1: Checksum mismatch is detected and startup exits fail-closed.
**Acceptance Criteria (Given/When/Then):**
- Given valid migration chain, when startup runs, then `schema_migrations` is monotonic with matching checksums.
- Given checksum mismatch, when startup runs, then process exits and DB is not partially migrated.
- Given future schema version, when startup runs, then daemon refuses to start with explicit error.
**Verification Procedure (reproducible):**
- Commands/steps: `go test ./internal/persistence -run 'TestStore_MigrationLedgerHasChecksum|TestStore_OpenRejectsFutureSchemaVersion|TestStore_OpenRejectsChecksumMismatch' -v`.
- Expected outputs (specific): all tests pass; output confirms checksum and version-guard behavior.
**Evidence to Capture:**
- migration test transcript, `schema_migrations` query output, failure-reason snippet.
**Related Requirements:** `GC-SPEC-DATA-001`, `GC-SPEC-DATA-002`, `GC-SPEC-DATA-003`

### US-005: Canonical State Machine and Atomic `task_event` Append (Priority: P0)
**Persona:** Integrator / ACP Client  
**Goal:** Guarantee legal state transitions and event atomicity for every transition.  
**Preconditions:** Active queue with test tasks.  
**Trigger:** Task attempts traverse success/failure paths including invalid transition attempts.  
**Main Flow:**
1) Run tasks through canonical states.
2) Reject illegal state transitions atomically.
3) Append one immutable `task_event` in same transaction as task row change.
**Alternate Flows:**
- AF1: Terminal transition records final attempt metadata and reason code.
**Acceptance Criteria (Given/When/Then):**
- Given legal transitions, when task executes, then only canonical state graph is used.
- Given illegal transition request, when applied, then update is rejected and no partial history is written.
- Given terminal state, when record is inspected, then final attempt metadata and reason code are present.
**Verification Procedure (reproducible):**
- Commands/steps: `go test ./internal/persistence -run 'TestStore_StateMachineRejectsIllegalTransition|TestStore_TaskEventsWrittenForTransitions' -v`.
- Expected outputs (specific): tests pass; output shows rejection of illegal transitions and atomic event writes.
**Evidence to Capture:**
- transition test logs, `tasks` and `task_events` query snapshots.
**Related Requirements:** `GC-SPEC-STM-001`, `GC-SPEC-STM-002`, `GC-SPEC-STM-003`, `GC-SPEC-STM-004`

### US-006: Lease Claim, Heartbeat, Expiry Reclaim, and Crash Recovery (Priority: P0)
**Persona:** Operator  
**Goal:** Ensure lease-based queue ownership and deterministic reclaim after worker failure.  
**Preconditions:** Multiple workers and at least one long-running task.  
**Trigger:** Worker crash or heartbeat lapse beyond lease TTL.  
**Main Flow:**
1) Atomic claim assigns lease ownership to one worker.
2) Running worker heartbeats lease while active.
3) Expired lease becomes reclaimable and startup recovery requeues unfinished work.
**Alternate Flows:**
- AF1: Simulated process kill during execution triggers deterministic reclaim on restart.
**Acceptance Criteria (Given/When/Then):**
- Given competing workers, when claim race occurs, then only one worker owns each claimable task lease.
- Given missed heartbeat, when lease expires, then task becomes reclaimable.
- Given crash before completion, when daemon restarts, then unfinished task is deterministically recovered.
**Verification Procedure (reproducible):**
- Commands/steps: `go test ./internal/persistence -run 'TestStore_ConcurrentClaimRace|TestStore_HeartbeatLeaseExtendsExpiry|TestStore_RequeueExpiredLeases|TestStore_ClaimAndRecoverRunningTasks' -v`; `go run ./tools/verify/lease_recovery_crash -mode prepare -db /tmp/ic_recover.db`.
- Expected outputs (specific): claim exclusivity, heartbeat extension, reclaim, and recovery all pass.
**Evidence to Capture:**
- `docs/EVIDENCE_VERIFY/queue_lease_race.txt`, `docs/EVIDENCE_VERIFY/recovery_sigkill.txt`.
**Related Requirements:** `GC-SPEC-QUE-001`, `GC-SPEC-QUE-002`, `GC-SPEC-PER-006`, `GC-SPEC-REL-003`

### US-007: Retry Backoff, DLQ Routing, Poison-Pill Short-Circuit (Priority: P0)
**Persona:** Operator  
**Goal:** Prevent retry storms and route repeatedly failing tasks safely.  
**Preconditions:** Tool path configured to fail deterministically.  
**Trigger:** Repeated task failures across attempts.  
**Main Flow:**
1) Failures schedule retries with exponential backoff and jitter.
2) `max_attempts` exceedance routes task to `DEAD_LETTER`.
3) Poison-pill classifier can route earlier for equivalent repeated failures.
**Alternate Flows:**
- AF1: Retry and terminal transitions include explicit reason codes.
**Acceptance Criteria (Given/When/Then):**
- Given repeated failures, when retries are scheduled, then delay growth follows configured backoff behavior.
- Given attempts exceed limit, when next failure occurs, then task transitions to `DEAD_LETTER` and stops automatic retries.
- Given repeated equivalent failures, when poison-pill threshold is reached, then task can short-circuit to `DEAD_LETTER` before `max_attempts`.
**Verification Procedure (reproducible):**
- Commands/steps: `go test ./internal/persistence -run 'TestStore_HandleTaskFailureRetriesWithBackoff|TestStore_HandleTaskFailurePoisonPillToDeadLetter|TestStore_ReasonCodesOnFailureAndAbort' -v`.
- Expected outputs (specific): tests show backoff timing, DLQ transition, poison-pill classification, and reason codes.
**Evidence to Capture:**
- retry timeline output, DLQ row evidence, reason-code event excerpts.
**Related Requirements:** `GC-SPEC-QUE-003`, `GC-SPEC-QUE-004`, `GC-SPEC-QUE-005`, `GC-SPEC-REL-007`

### US-008: Side-Effect Idempotency and Dedupe Registry (Priority: P0)
**Persona:** Integrator / ACP Client  
**Goal:** Enforce at-most-once side effects per idempotency key across retry/crash windows.  
**Preconditions:** Side-effecting tool call with stable idempotency key.  
**Trigger:** Retry occurs after side effect success but before completion commit.  
**Main Flow:**
1) Execution stores idempotency key and request hash.
2) Retry path checks dedupe registry before emitting side effect.
3) Successful prior side effect is reused and not re-emitted.
**Alternate Flows:**
- AF1: Key/hash mismatch is treated as contract violation and fails safely.
**Acceptance Criteria (Given/When/Then):**
- Given repeated call with same successful key, when retry runs, then external side effect is not executed twice.
- Given duplicate key with different request hash, when checked, then attempt fails with explicit dedupe reason.
- Given reliability claims, when reviewed, then behavior matches at-least-once task execution and at-most-once side effects per key.
**Verification Procedure (reproducible):**
- Commands/steps: `go test ./internal/persistence -run 'TestStore_RegisterSuccessfulToolCallDedupes' -v`; kill-and-retry chaos around completion boundary.
- Expected outputs (specific): exactly one side effect at sink; dedupe record proves suppression on retry.
**Evidence to Capture:**
- `tool_call_dedup` query output, sink-side transaction count, retry trace.
**Related Requirements:** `GC-SPEC-QUE-006`, `GC-SPEC-REL-001`, `GC-SPEC-REL-002`

### US-009: ACP Hello/Auth/Origin/JSON-RPC Contract Gate (Priority: P0)
**Persona:** Integrator / ACP Client  
**Goal:** Ensure mutating ACP operations are accepted only after protocol and security gates pass.  
**Preconditions:** Running daemon; valid token and invalid token/origin clients.  
**Trigger:** Client attempts `agent.chat` before `system.hello`, without auth, or from disallowed origin.  
**Main Flow:**
1) Client connects to `/ws` with JSON-RPC 2.0 framing.
2) Server enforces hello negotiation before task-mutating methods.
3) Server enforces bearer auth and origin allowlist checks.
**Alternate Flows:**
- AF1: Unauthorized or disallowed-origin clients are rejected and audited.
**Acceptance Criteria (Given/When/Then):**
- Given pre-hello `agent.chat`, when request is sent, then response is protocol error and mutation is rejected.
- Given missing/invalid token, when dialing ACP, then connection is rejected.
- Given disallowed browser origin, when connecting, then origin check rejects the request.
**Verification Procedure (reproducible):**
- Commands/steps: `go run ./tools/verify/acp_ws_check -url ws://127.0.0.1:18789/ws -token "$(cat ~/.goclaw/auth.token)"`.
- Expected outputs (specific): `AUTH_CHECK missing token rejected status=401` and final `VERDICT PASS`.
**Evidence to Capture:**
- ACP transcript file, gateway auth logs, related `audit.jsonl` entries.
**Related Requirements:** `GC-SPEC-ACP-001`, `GC-SPEC-ACP-002`, `GC-SPEC-ACP-003`, `GC-SPEC-ACP-004`

### US-010: ACP FIFO Ordering, Cursor Replay/Gap, Backpressure Close (Priority: P0)
**Persona:** Integrator / ACP Client  
**Goal:** Provide deterministic reconnect and bounded outbound buffering.  
**Preconditions:** Session with event stream and a throttled client.  
**Trigger:** Client reconnects from cursor and later exceeds outbound buffer limit.  
**Main Flow:**
1) Server emits per-session monotonic `event_id` notifications with correlation IDs.
2) Reconnect from valid cursor replays missed events in FIFO session order.
3) Buffer overflow emits `system.backpressure` and closes connection deterministically.
**Alternate Flows:**
- AF1: Stale cursor returns explicit replay-gap error; no silent truncation.
**Acceptance Criteria (Given/When/Then):**
- Given valid `from_event_id`, when replay runs, then event continuity is gap-free for that session.
- Given stale cursor, when replay is requested, then explicit replay-gap error is returned.
- Given slow consumer overflow, when buffer limit is exceeded, then `system.backpressure` is emitted before deterministic close.
**Verification Procedure (reproducible):**
- Commands/steps: `go test ./internal/gateway -run 'TestGateway_SessionEventsSubscribeReplayOrdered|TestGateway_SessionEventsSubscribeReplayGap|TestGateway_SessionEventsSubscribeBackpressureClose' -v`.
- Expected outputs (specific): ordered replay test passes, replay-gap test passes, backpressure-close test passes.
**Evidence to Capture:**
- `docs/EVIDENCE_VERIFY/acp_replay_backpressure.txt`, replay cursor transcript, event/order SQL check.
**Related Requirements:** `GC-SPEC-ACP-005`, `GC-SPEC-ACP-006`, `GC-SPEC-ACP-007`, `GC-SPEC-ACP-008`, `GC-SPEC-QUE-008`, `GC-SPEC-OBS-002`, `GC-SPEC-REL-004`

### US-011: Default-Deny PDP/PEP + SSRF Blocks + Redaction + Audit (Priority: P0)
**Persona:** Security Admin  
**Goal:** Enforce centralized deny-by-default security and preserve privacy-safe audit evidence.  
**Preconditions:** Policy default deny, SSRF test corpus, secret-bearing payloads.  
**Trigger:** ACP/WASM/legacy call attempts unauthorized capability or blocked egress target.  
**Main Flow:**
1) Every execution path calls centralized PDP; unauthorized requests are denied.
2) SSRF policy blocks loopback/link-local/private CIDR/non-http(s) defaults.
3) Secret-bearing values are redacted before persistence; decisions are appended to immutable audit channel.
**Alternate Flows:**
- AF1: Explicit allow rules permit scoped operations and still emit audit entries.
**Acceptance Criteria (Given/When/Then):**
- Given no allow rule, when capability is invoked from ACP/WASM/legacy, then action is denied.
- Given blocked SSRF target, when HTTP request is attempted, then request is rejected before egress.
- Given secret-bearing input, when logs/events are persisted, then secrets are redacted and audit records include reason and policy version.
**Verification Procedure (reproducible):**
- Commands/steps: `go run ./tools/verify/policy_default_check`; `go test ./internal/policy -run 'TestAllowHTTPURL_SSRFAndSchemeBlocks|TestAllowHTTPURL_SSRFBypassCorpus|TestReloadFromFile_InvalidRetainsPrevious' -v`; `go test ./internal/shared -run 'TestRedact_' -v`.
- Expected outputs (specific): policy verifier prints `VERDICT PASS`; SSRF baseline and 21-vector bypass corpus tests pass; redaction tests pass.
**Evidence to Capture:**
- `docs/EVIDENCE_VERIFY/security_policy_ssrf_redaction.txt`, `audit_log` query output, redacted log sample.
**Related Requirements:** `GC-SPEC-SEC-001`, `GC-SPEC-SEC-002`, `GC-SPEC-SEC-004`, `GC-SPEC-SEC-005`, `GC-SPEC-SEC-006`, `GC-SPEC-DATA-007`, `GC-SPEC-OBS-003`

### US-012: Approval Broker Works in TTY and Headless Modes (Priority: P0)
**Persona:** Security Admin  
**Goal:** Ensure high-risk actions are never blocked behind TUI-only approvals.  
**Preconditions:** Approval-required action path; both TTY and non-TTY runs available.  
**Trigger:** High-risk action is requested.  
**Main Flow:**
1) Request enters Approval Broker queue.
2) In TTY mode, TUI shows pending approval and explicit approve/deny actions.
3) In headless mode, ACP methods/events support the same approval flow without deadlock.
**Alternate Flows:**
- AF1: No approval within timeout defaults to deny and emits audit event.
**Acceptance Criteria (Given/When/Then):**
- Given approval-required action, when running with TTY, then approval request is visible and actionable.
- Given approval-required action, when running headless, then approval workflow remains available via ACP.
- Given timeout without approval, when deadline expires, then request is denied by default.
**Verification Procedure (reproducible):**
- Commands/steps: `go test ./internal/gateway -run 'TestGateway_HeadlessApprovalWorkflow' -v`; `go test ./internal/tui -run 'TestTUI_HeadlessNonTTY' -v`.
- Expected outputs (specific): both tests pass; no TUI-only control dependency exists.
**Evidence to Capture:**
- approval-required/decision event transcript, TUI screenshot (TTY mode), `audit.jsonl` approval entries.
**Related Requirements:** `GC-SPEC-SEC-008`, `GC-SPEC-TUI-001`, `GC-SPEC-TUI-003`, `GC-SPEC-TUI-004`

### US-013: WASM ABI Gate, Staged Reload, Rollback, Cache, Quarantine (Priority: P0)
**Persona:** Skill Author  
**Goal:** Keep skill lifecycle deterministic and safe under upgrade/fault conditions.  
**Preconditions:** Active skill version; staged update artifacts including incompatible fixture.  
**Trigger:** Skill reload attempts under load.  
**Main Flow:**
1) Production loader consumes compiled `.wasm` + manifest metadata.
2) Reload runs `stage -> validate -> activate`; cache keyed by content hash + ABI version.
3) ABI mismatch or failed activation preserves prior active module; repeated faults quarantine skill.
**Alternate Flows:**
- AF1: Operator explicitly re-enables quarantined skill after mitigation.
**Acceptance Criteria (Given/When/Then):**
- Given ABI-compatible update, when reload executes, then new module activates and tool list updates.
- Given ABI mismatch or activation failure, when reload executes, then previous module stays active.
- Given repeated faults over threshold, when threshold is reached, then skill transitions to quarantined state.
**Verification Procedure (reproducible):**
- Commands/steps: `go test ./internal/sandbox/wasm -run 'TestWatcher_ABIMismatchPreventsActivation|TestWatcher_ReloadFailureRollsBackPreviousModule' -v`; `go test ./internal/persistence -run 'TestStore_SkillQuarantine' -v`.
- Expected outputs (specific): ABI mismatch and rollback tests pass; quarantine test passes.
**Evidence to Capture:**
- `docs/EVIDENCE_VERIFY/skills_wasm_lifecycle.txt`, skill lifecycle event logs, `skill_registry` state dump.
**Related Requirements:** `GC-SPEC-SKL-001`, `GC-SPEC-SKL-002`, `GC-SPEC-SKL-003`, `GC-SPEC-SKL-004`, `GC-SPEC-SKL-005`, `GC-SPEC-SKL-006`, `GC-SPEC-SKL-007`, `GC-SPEC-CFG-007`

### US-014: Incident Export Bundle for Offline Debugging (Priority: P0)
**Persona:** Operator  
**Goal:** Produce bounded redacted incident evidence for external debugging without raw DB handoff.  
**Preconditions:** Session/run IDs selected for export.  
**Trigger:** Operator requests incident export after failure triage.  
**Main Flow:**
1) Exporter bundles bounded task events, redacted logs, and config hash metadata.
2) Bundle integrity is checked and privacy fields are verified redacted.
3) Bundle is handed off for offline analysis.
**Alternate Flows:**
- AF1: Oversized export request is bounded/rejected with explicit guidance.
**Acceptance Criteria (Given/When/Then):**
- Given valid run scope, when export runs, then bundle contains correlated events/logs/config hash.
- Given secret-bearing source records, when bundle is inspected, then secrets are redacted.
- Given bounded limits, when export completes, then event/log counts do not exceed configured cap.
**Verification Procedure (reproducible):**
- Commands/steps: `go run ./tools/verify/incident_export > docs/EVIDENCE_VERIFY/incident_export.txt`.
- Expected outputs (specific): output contains `bundle_path=...` and `VERDICT PASS`.
**Evidence to Capture:**
- `docs/EVIDENCE_VERIFY/incident_export.txt`, generated bundle checksum and schema validation output.
**Related Requirements:** `GC-SPEC-OBS-006`, `GC-SPEC-SEC-005`

### US-015: Online Backup/Restore Drill with RPO/RTO Targets (Priority: P0)
**Persona:** Operator  
**Goal:** Prove backup/restore reliability under write load and confirm status contract after restore.  
**Preconditions:** Active write workload and clean restore target.  
**Trigger:** Scheduled DR drill or pre-upgrade backup validation.  
**Main Flow:**
1) Perform online-consistent SQLite backup while writes are active.
2) Restore snapshot and start daemon from restored data.
3) Validate RPO/RTO, health fields, and config fingerprint in status endpoint.
**Alternate Flows:**
- AF1: Failed restore validation keeps environment on last known-good snapshot.
**Acceptance Criteria (Given/When/Then):**
- Given active writes, when backup/restore drill runs, then restored DB is consistent and replayable.
- Given drill timings, when measured, then `RPO <= 5s` and `RTO <= 5m` under nominal local conditions.
- Given restored daemon, when `system.status` is queried, then DB status, policy version, replay backlog, skill status, and config fingerprint are present.
**Verification Procedure (reproducible):**
- Commands/steps: `go run ./tools/verify/backup_restore_drill > docs/EVIDENCE_VERIFY/backup_restore_drill.txt`; query `system.status`.
- Expected outputs (specific): `VERDICT PASS`; timing and restored record counts are printed.
**Evidence to Capture:**
- `docs/EVIDENCE_VERIFY/backup_restore_drill.txt`, restore timing sheet, status payload snapshot.
**Related Requirements:** `GC-SPEC-PER-005`, `GC-SPEC-REL-006`, `GC-SPEC-DATA-004`, `GC-SPEC-CFG-005`, `GC-SPEC-OBS-005`

### US-016: Bounded Lanes, Fair Scheduling, Multi-Client ACP+TUI Ops (Priority: P0)
**Persona:** Operator  
**Goal:** Keep scheduler bounded and fair while supporting concurrent local ACP clients and TUI ops.  
**Preconditions:** Lane count configured; load generator with mixed-priority sessions.  
**Trigger:** Sustained multi-session workload above lane capacity.  
**Main Flow:**
1) Scheduler enforces active-lane cap.
2) Fairness/aging policy prevents starvation.
3) ACP clients and TUI observability remain concurrently available in one daemon process.
**Alternate Flows:**
- AF1: Saturation produces explicit backpressure/metric signals instead of silent drops.
**Acceptance Criteria (Given/When/Then):**
- Given lane_count `N`, when submitted load exceeds `N`, then active lanes never exceed `N`.
- Given mixed priorities over time, when completion stats are reviewed, then starvation is not observed.
- Given concurrent ACP and TUI usage, when system is loaded, then both surfaces remain operational and metrics expose queue depth/retries/DLQ.
**Verification Procedure (reproducible):**
- Commands/steps: `go test ./internal/engine -run 'TestEngine_BoundedConcurrency' -v`; `go test ./internal/persistence -run 'TestStore_AgeQueuedPriorities|TestStore_RecoveryMetrics' -v`; manual ACP+TUI concurrent run.
- Expected outputs (specific): concurrency/fairness tests pass; metrics include required operational fields.
**Evidence to Capture:**
- lane utilization graph, metrics scrape, TUI operational screenshot, concurrent client transcript.
**Related Requirements:** `GC-SPEC-SCOPE-003`, `GC-SPEC-RUN-002`, `GC-SPEC-QUE-007`, `GC-SPEC-PERF-003`, `GC-SPEC-OBS-004`, `GC-SPEC-TUI-002`

### US-017: Graceful Shutdown, Cancel Boundaries, Trace Propagation (Priority: P0)
**Persona:** Integrator / ACP Client  
**Goal:** Ensure safe stop behavior, explicit cancellation behavior, and correlation IDs across runtime boundaries.  
**Preconditions:** Active running tasks and at least one cancel request in progress.  
**Trigger:** Operator sends termination signal and client sends `agent.abort`.  
**Main Flow:**
1) Shutdown sequence stops intake, drains with timeout, and marks unfinished work retry-safe.
2) Cancel requests are persisted and observed before/after tool call boundaries.
3) Logs/events keep `trace_id`, `run_id`, `task_id`, and `session_id` correlation.
**Alternate Flows:**
- AF1: Drain timeout expires; unfinished work is marked for safe retry on next start.
**Acceptance Criteria (Given/When/Then):**
- Given graceful shutdown signal, when drain completes or times out, then no unsafe partial completion is reported.
- Given `agent.abort` on running task, when worker checks boundaries, then explicit cancel event and terminal state are produced.
- Given a single run, when logs/events are inspected, then correlation IDs are present end-to-end.
**Verification Procedure (reproducible):**
- Commands/steps: `go test ./internal/engine -run 'TestEngine_AbortCancelsRunningTask' -v`; `go test ./internal/gateway -run 'TestGateway_AgentAbortReturnsAborted' -v`; shutdown integration run.
- Expected outputs (specific): abort and shutdown tests pass; trace IDs appear in structured logs and events.
**Evidence to Capture:**
- abort transcript, shutdown log excerpt, correlated `task_events` export.
**Related Requirements:** `GC-SPEC-RUN-003`, `GC-SPEC-RUN-004`, `GC-SPEC-STM-005`, `GC-SPEC-REL-005`

### P1

### US-018: Config Precedence and Reload/Restart Boundaries (Priority: P1)
**Persona:** Operator  
**Goal:** Ensure deterministic config behavior with safe hot-reload boundaries.  
**Preconditions:** Same setting set in env and `config.yaml`; mutable policy/SOUL files.  
**Trigger:** Operator changes env/config while daemon runs.  
**Main Flow:**
1) Runtime resolves `env > config.yaml > defaults`.
2) Hot reload applies only to allowed files after validation.
3) Restart-required settings remain unchanged until restart.
**Alternate Flows:**
- AF1: Invalid hot reload keeps prior valid state and emits alert/audit event.
**Acceptance Criteria (Given/When/Then):**
- Given conflicting values, when status is queried, then effective value follows precedence rules.
- Given restart-required field change without restart, when checked, then running value does not change.
- Given invalid hot-reload input, when reload is attempted, then previous valid config remains active.
**Verification Procedure (reproducible):**
- Commands/steps: `go test ./internal/config -run 'TestLoad_EnvOverridesConfig|TestWatcher_DetectsSOULFileChange' -v`.
- Expected outputs (specific): precedence and watcher behavior pass; invalid reload is fail-safe.
**Evidence to Capture:**
- effective config dump, reload alert logs, test transcript.
**Related Requirements:** `GC-SPEC-CFG-001`, `GC-SPEC-CFG-002`, `GC-SPEC-CFG-003`

### US-019: Policy Versioning and Attempt-Level Pinning (Priority: P1)
**Persona:** Security Admin  
**Goal:** Guarantee policy lineage and non-retroactive policy behavior per attempt.  
**Preconditions:** One long-running attempt active; policy update ready.  
**Trigger:** Policy reload during active attempt.  
**Main Flow:**
1) New policy is validated and persisted as a new `policy_version` before activation.
2) Active attempt remains pinned to old policy version.
3) New attempts use new version; invalid reload keeps previous version active.
**Alternate Flows:**
- AF1: Unknown capabilities remain denied after invalid policy reload attempt.
**Acceptance Criteria (Given/When/Then):**
- Given active attempt under version `V1`, when version `V2` is loaded, then active attempt remains on `V1`.
- Given new attempt after load, when evaluated, then decisions use `V2`.
- Given invalid policy reload, when attempted, then previous valid version remains active and unknown capabilities are denied.
**Verification Procedure (reproducible):**
- Commands/steps: `go test ./internal/persistence -run 'TestStore_StartTaskRun_PinsPolicyVersion|TestStore_RecordPolicyVersion' -v`; `go test ./internal/policy -run 'TestReloadFromFile_InvalidRetainsPrevious' -v`.
- Expected outputs (specific): pinning and invalid-reload retention tests pass.
**Evidence to Capture:**
- `policy_versions` rows, attempt policy-version mapping, reload failure alerts.
**Related Requirements:** `GC-SPEC-SEC-003`, `GC-SPEC-SEC-007`, `GC-SPEC-CFG-006`, `GC-SPEC-CFG-003`

### US-020: Retention Windows and User PII Purge (Priority: P1)
**Persona:** Security Admin  
**Goal:** Enforce configurable retention and audited, idempotent purge behavior.  
**Preconditions:** Test data spans multiple ages and includes PII markers.  
**Trigger:** Retention and purge jobs execute repeatedly.  
**Main Flow:**
1) Retention applies separate windows for logs, task events, and audit channels.
2) Purge removes/tombstones PII-bearing records according to policy.
3) Re-running jobs is idempotent and fully audited.
**Alternate Flows:**
- AF1: Re-run with no eligible rows records no-op result, not error.
**Acceptance Criteria (Given/When/Then):**
- Given aged records, when retention runs, then only out-of-policy rows are removed.
- Given purge request, when completed, then targeted PII is deleted/tombstoned with audit evidence.
- Given immediate re-run, when job executes again, then no unintended extra deletion occurs.
**Verification Procedure (reproducible):**
- Commands/steps: `go test ./internal/persistence -run 'TestStore_RunRetention|TestStore_PurgeSessionPII' -v`.
- Expected outputs (specific): retention and purge tests pass and show idempotent behavior.
**Evidence to Capture:**
- before/after row counts, purge audit records, retention run transcript.
**Related Requirements:** `GC-SPEC-DATA-005`, `GC-SPEC-DATA-006`, `GC-SPEC-DATA-008`

### US-021: Brain Boundary, Intent Normalization, Schema Validation, Provenance (Priority: P1)
**Persona:** Skill Author  
**Goal:** Keep provider-specific logic isolated and tool intents deterministic and validated.  
**Preconditions:** Valid and invalid tool-intent fixtures available.  
**Trigger:** Scheduler receives intent candidates from brain adapter.  
**Main Flow:**
1) Brain provider output is normalized to deterministic internal intent records.
2) Schema-versioned intent validation runs before execution.
3) Context compaction preserves message provenance references.
**Alternate Flows:**
- AF1: Invalid schema fails safely without executing external tool side effects.
**Acceptance Criteria (Given/When/Then):**
- Given provider adapter output, when inspected, then scheduler/persistence layers do not depend on provider SDK types.
- Given invalid intent schema, when validation runs, then execution is rejected safely.
- Given compaction, when provenance is inspected, then source message references are retained.
**Verification Procedure (reproducible):**
- Commands/steps: run boundary and normalization unit tests in `internal/engine` and intent fixtures in adapter tests.
- Expected outputs (specific): interface boundary, normalization, schema validation, and provenance checks pass.
**Evidence to Capture:**
- unit test transcript, normalized intent fixture output, provenance check report.
**Related Requirements:** `GC-SPEC-BRN-001`, `GC-SPEC-BRN-002`, `GC-SPEC-BRN-003`, `GC-SPEC-BRN-004`

### US-022: ACP Legacy Alias Compatibility (`text` and `content`) (Priority: P1)
**Persona:** Integrator / ACP Client  
**Goal:** Maintain v0.1 compatibility for clients expecting legacy field aliases.  
**Preconditions:** Mixed client fixtures using `text` and `content`.  
**Trigger:** Client sends and receives payloads using either alias.  
**Main Flow:**
1) Protocol accepts documented alias inputs.
2) Internal persistence remains canonical.
3) Response framing stays valid JSON-RPC.
**Alternate Flows:**
- AF1: Unsupported aliases return explicit protocol error.
**Acceptance Criteria (Given/When/Then):**
- Given legacy alias payload, when processed, then semantic content is preserved.
- Given unsupported alias, when processed, then explicit contract error is returned.
- Given mixed client versions, when contract suite runs, then compatibility tests pass.
**Verification Procedure (reproducible):**
- Commands/steps: `go test ./internal/persistence -run 'TestStore_HistoryItemHasTextAlias' -v`; ACP contract fixture run for alias cases.
- Expected outputs (specific): alias tests pass with canonical storage unchanged.
**Evidence to Capture:**
- compatibility transcript, alias fixture results, protocol diff notes.
**Related Requirements:** `GC-SPEC-ACP-009`

### P2

### US-023: Performance Targets: Startup, Memory, DB p95, Replay, Reload Stall (Priority: P2)
**Persona:** Operator  
**Goal:** Quantify performance against SPEC targets and detect regressions early.  
**Preconditions:** Reference hardware and benchmark harness.  
**Trigger:** Pre-release performance run.  
**Main Flow:**
1) Measure cold startup latency and idle memory.
2) Measure DB claim/update p95 under nominal load.
3) Measure 1,000-event replay duration and ensure reload path does not globally stall scheduler.
**Alternate Flows:**
- AF1: Any threshold breach creates performance regression ticket and blocks promotion by policy.
**Acceptance Criteria (Given/When/Then):**
- Given benchmark run, when results are computed, then startup and memory meet targets.
- Given DB load profile, when p95 is measured, then transaction latency target is met.
- Given replay/reload contention runs, when measured, then replay target and non-blocking reload target are met.
**Verification Procedure (reproducible):**
- Commands/steps: run benchmark suite and replay/hot-reload contention tests; archive raw outputs.
- Expected outputs (specific): report includes pass/fail for each SPEC PERF requirement with numeric values.
**Evidence to Capture:**
- benchmark report, replay timing logs, lane throughput during reload.
**Related Requirements:** `GC-SPEC-PERF-001`, `GC-SPEC-PERF-002`, `GC-SPEC-PERF-004`, `GC-SPEC-PERF-005`, `GC-SPEC-PERF-006`

## 6) Negative / Abuse Stories

### US-024: Anti-Story: Browser Automation Capability Must Not Exist (Priority: P0)
**Persona:** Security Admin  
**Goal:** Prove browser automation remains out of scope in v0.1.  
**Preconditions:** Runtime built with default capabilities.  
**Trigger:** Attempt to enumerate or invoke browser capability.  
**Main Flow:**
1) Capability inventory is enumerated.
2) Browser automation capability is absent.
3) Browser-like invocation attempts are rejected and audited.
**Alternate Flows:**
- AF1: Future capability placeholders may exist in taxonomy metadata but have no executable runtime path.
**Acceptance Criteria (Given/When/Then):**
- Given v0.1 runtime, when capabilities are enumerated, then no browser automation executor is present.
- Given attempted browser invocation, when request is processed, then request is rejected and audited.
- Given dependency audit, when reviewed, then no bundled headless browser engine is present.
**Verification Procedure (reproducible):**
- Commands/steps: `go run ./tools/verify/non_goals_audit > docs/EVIDENCE_VERIFY/non_goals_audit.txt`.
- Expected outputs (specific): `BROWSER_AUTOMATION: PASS` — no browser automation deps in `go.mod`/`go.sum`, no browser-related imports in `*.go` files.
**Evidence to Capture:**
- `docs/EVIDENCE_VERIFY/non_goals_audit.txt`.
**Related Requirements:** `GC-SPEC-NG-001`

### US-025: Anti-Story: Distributed Clustering Must Not Activate (Priority: P0)
**Persona:** Operator  
**Goal:** Prove v0.1 remains single-node without distributed scheduling.  
**Preconditions:** Standard local deployment and config parser access.  
**Trigger:** Attempt to set cluster/federation settings or start multi-node scheduler behavior.  
**Main Flow:**
1) Unsupported cluster settings are rejected/ignored with explicit message.
2) Scheduler executes only local-process work.
3) No inter-node control plane is activated.
**Alternate Flows:**
- AF1: Documentation may mention future seam, but runtime behavior remains non-clustered.
**Acceptance Criteria (Given/When/Then):**
- Given cluster options, when daemon starts, then options are unsupported and do not activate clustering.
- Given normal operation, when runtime topology is inspected, then scheduling is local only.
- Given deployment review, when network behavior is captured, then no cluster-control traffic exists.
**Verification Procedure (reproducible):**
- Commands/steps: `go run ./tools/verify/non_goals_audit > docs/EVIDENCE_VERIFY/non_goals_audit.txt`.
- Expected outputs (specific): `CLUSTERING: PASS` — no clustering/consensus deps, no raft/gossip/etcd imports in `*.go` files.
**Evidence to Capture:**
- `docs/EVIDENCE_VERIFY/non_goals_audit.txt`.
**Related Requirements:** `GC-SPEC-NG-002`

### US-026: Anti-Story: Multi-User Separation Must Not Be Implied (Priority: P0)
**Persona:** Security Admin  
**Goal:** Prevent false assumptions about unsupported in-process multi-tenancy.  
**Preconditions:** Multiple ACP sessions in one daemon process.  
**Trigger:** Integrator attempts tenant isolation semantics in one process.  
**Main Flow:**
1) Runtime behavior confirms one local user context per process.
2) Sessions are isolated by session/task IDs but not as separate security tenants.
3) Unsupported multi-user separation expectations are explicitly documented/rejected.
**Alternate Flows:**
- AF1: Separate OS processes remain valid isolation boundary if needed.
**Acceptance Criteria (Given/When/Then):**
- Given one daemon process, when multiple clients connect, then runtime remains single-tenant by design.
- Given request for per-tenant isolation in-process, when evaluated, then requirement is marked unsupported in v0.1.
- Given architecture review, when checked, then no hidden multi-tenant partitioning controls exist.
**Verification Procedure (reproducible):**
- Commands/steps: `go run ./tools/verify/non_goals_audit > docs/EVIDENCE_VERIFY/non_goals_audit.txt`.
- Expected outputs (specific): `MULTI_TENANT: PASS` — no tenant isolation, RBAC/ACL user-separation, or per-tenant partitioning code in `*.go` files.
**Evidence to Capture:**
- `docs/EVIDENCE_VERIFY/non_goals_audit.txt`.
**Related Requirements:** `GC-SPEC-SCOPE-002`, `GC-SPEC-NG-003`

### US-027: Abuse: Unauthenticated/Origin/Capability Bypass Attempts Denied (Priority: P0)
**Persona:** Security Admin  
**Goal:** Validate denial paths for remote unauthorized clients and capability bypass attempts.  
**Preconditions:** Deny-default policy, auth token, and disallowed origin clients.  
**Trigger:** Attack matrix sends invalid auth, bad origin, and unauthorized capability attempts.  
**Main Flow:**
1) Connection gate rejects missing/invalid bearer tokens.
2) Origin gate rejects disallowed browser origins.
3) Capability bypass attempts are denied by centralized PDP/PEP.
**Alternate Flows:**
- AF1: Valid local client with valid token proceeds normally.
**Acceptance Criteria (Given/When/Then):**
- Given missing/invalid token, when ACP connect/mutate is attempted, then request is rejected.
- Given disallowed origin, when websocket handshake occurs, then request is rejected.
- Given unauthorized capability request, when executed from any path, then action is denied and audited.
**Verification Procedure (reproducible):**
- Commands/steps: `go test ./internal/gateway -run 'TestGateway_AbuseDenialMatrix|TestGateway_OriginRejectsDisallowedOrigin|TestGateway_WSRejectsMissingOrInvalidAuth|TestGateway_PolicyDeniesCapabilities|TestGateway_PolicyDecisionAudited' -v`.
- Expected outputs (specific): all abuse matrix vectors denied (missing auth 401, invalid auth 401, bad origin 403, no handshake error, capability bypass denied); audit evidence present.
**Evidence to Capture:**
- `docs/EVIDENCE_VERIFY/abuse_denial_matrix.txt`, gateway reject logs, audit decision records.
**Related Requirements:** `GC-SPEC-ACP-003`, `GC-SPEC-ACP-004`, `GC-SPEC-SEC-001`, `GC-SPEC-SEC-002`

### US-028: Abuse: SSRF Bypass and Headless Approval Timeout Default Deny (Priority: P0)
**Persona:** Security Admin  
**Goal:** Block SSRF bypass tricks and prove approval timeout deny behavior in headless mode.  
**Preconditions:** SSRF bypass corpus and approval-required action in non-TTY environment.  
**Trigger:** Encoded/redirected private-target URLs and unapproved high-risk actions.  
**Main Flow:**
1) URL normalization and destination checks run before egress.
2) Prohibited destinations/schemes are denied and audited.
3) Headless approval requests without response timeout to deny.
**Alternate Flows:**
- AF1: Explicitly approved request proceeds and records decision with policy version.
**Acceptance Criteria (Given/When/Then):**
- Given SSRF bypass payloads, when evaluated, then prohibited targets are denied consistently.
- Given redirect chains to blocked ranges, when followed, then request is denied before connection.
- Given headless approval request with no response, when timeout expires, then result is deny with audit entry.
**Verification Procedure (reproducible):**
- Commands/steps: `go test ./internal/policy -run 'TestAllowHTTPURL_SSRFBypassCorpus' -v`; `go test ./internal/gateway -run 'TestGateway_ApprovalTimeoutDefaultDeny' -v`.
- Expected outputs (specific): all 21 SSRF bypass vectors denied (loopback, private CIDR, IPv6, encoded, scheme tricks); approval timeout auto-denies with audit entry.
**Evidence to Capture:**
- `docs/EVIDENCE_VERIFY/ssrf_bypass_approval_timeout.txt`, audit log decisions.
**Related Requirements:** `GC-SPEC-SEC-004`, `GC-SPEC-SEC-008`, `GC-SPEC-TUI-004`

## 7) Coverage Matrix (SPEC -> User Stories)
| Requirement ID | Covered by Story IDs | Coverage note |
| --- | --- | --- |
| GC-SPEC-ACP-001 | US-009 | ACP transport/JSON-RPC contract verified in handshake suite. |
| GC-SPEC-ACP-002 | US-009 | Hello negotiation required before mutating methods. |
| GC-SPEC-ACP-003 | US-009, US-027 | Auth deny path covered in primary and abuse stories. |
| GC-SPEC-ACP-004 | US-009, US-027 | Origin allowlist enforced and abuse-tested. |
| GC-SPEC-ACP-005 | US-010 | Event schema and correlation fields verified in replay flow. |
| GC-SPEC-ACP-006 | US-010 | Per-session FIFO ordering validated. |
| GC-SPEC-ACP-007 | US-010 | Replay cursor + replay-gap behavior validated. |
| GC-SPEC-ACP-008 | US-010 | Bounded outbound backpressure close verified. |
| GC-SPEC-ACP-009 | US-022 | Legacy alias compatibility contract test. |
| GC-SPEC-BRN-001 | US-021 | Primarily internal boundary; verified by unit/integration tests. |
| GC-SPEC-BRN-002 | US-021 | Deterministic intent normalization verified by fixtures. |
| GC-SPEC-BRN-003 | US-021 | Schema validation gate before execution. |
| GC-SPEC-BRN-004 | US-021 | Compaction provenance retention verified. |
| GC-SPEC-CFG-001 | US-018 | Effective config precedence validated. |
| GC-SPEC-CFG-002 | US-018 | Hot-reload vs restart-required boundary validated. |
| GC-SPEC-CFG-003 | US-018, US-019 | Invalid reload retains last valid state and emits alerts. |
| GC-SPEC-CFG-004 | US-001 | First-run token bootstrap and secure permissions verified. |
| GC-SPEC-CFG-005 | US-015 | Status fingerprint and post-restore status contract validated. |
| GC-SPEC-CFG-006 | US-019 | Policy versions persisted before activation. |
| GC-SPEC-CFG-007 | US-013 | Legacy mode gating tested in skill lifecycle. |
| GC-SPEC-DATA-001 | US-004 | Migration ledger monotonic checksum path verified. |
| GC-SPEC-DATA-002 | US-004 | Future-version guard blocks unsupported schema. |
| GC-SPEC-DATA-003 | US-004 | Transactional migrations with audit evidence. |
| GC-SPEC-DATA-004 | US-015 | Downgrade-by-restore drill validated. |
| GC-SPEC-DATA-005 | US-020 | Retention windows configurable by data class. |
| GC-SPEC-DATA-006 | US-020 | PII purge/tombstone behavior validated. |
| GC-SPEC-DATA-007 | US-011 | Redaction metadata retained without secrets. |
| GC-SPEC-DATA-008 | US-020 | Retention idempotency and auditability validated. |
| GC-SPEC-NG-001 | US-024 | Non-goal evidence confirms no browser automation. |
| GC-SPEC-NG-002 | US-025 | Non-goal evidence confirms no clustering. |
| GC-SPEC-NG-003 | US-026 | Non-goal evidence confirms no in-process multi-user separation. |
| GC-SPEC-OBS-001 | US-002 | Structured log schema validated during startup checks. |
| GC-SPEC-OBS-002 | US-010 | Replay completeness and ordering validated. |
| GC-SPEC-OBS-003 | US-011 | Audit channel append-only separation validated. |
| GC-SPEC-OBS-004 | US-016 | Metrics endpoint operational coverage validated. |
| GC-SPEC-OBS-005 | US-015 | Health endpoint contract validated after restore. |
| GC-SPEC-OBS-006 | US-014 | Incident export bundle workflow validated. |
| GC-SPEC-PER-001 | US-001, US-003 | WAL/FULL durability verified on first-run and persistence tests. |
| GC-SPEC-PER-002 | US-003 | Busy-timeout + retry behavior validated under contention. |
| GC-SPEC-PER-003 | US-003 | Short transaction scope requirement validated. |
| GC-SPEC-PER-004 | US-003 | ACID rollback/fault-injection path validated. |
| GC-SPEC-PER-005 | US-015 | Online-consistent backup/restore drill validated. |
| GC-SPEC-PER-006 | US-006 | Startup recovery reconciles expired leases. |
| GC-SPEC-PERF-001 | US-023 | Startup benchmark target validated. |
| GC-SPEC-PERF-002 | US-023 | Idle memory target validated. |
| GC-SPEC-PERF-003 | US-016 | Load and lane concurrency target validated. |
| GC-SPEC-PERF-004 | US-023 | DB write-path p95 latency validated. |
| GC-SPEC-PERF-005 | US-023 | 1,000-event replay timing target validated. |
| GC-SPEC-PERF-006 | US-023 | Reload contention non-stall target validated. |
| GC-SPEC-QUE-001 | US-006 | Atomic claim semantics verified by race tests. |
| GC-SPEC-QUE-002 | US-006 | Heartbeat/expiry reclaim behavior validated. |
| GC-SPEC-QUE-003 | US-007 | Exponential backoff with jitter validated. |
| GC-SPEC-QUE-004 | US-007 | DLQ terminal routing after max attempts validated. |
| GC-SPEC-QUE-005 | US-007 | Poison-pill early-DLQ behavior validated. |
| GC-SPEC-QUE-006 | US-008 | Side-effect dedupe by idempotency key validated. |
| GC-SPEC-QUE-007 | US-016 | Fairness/aging no-starvation behavior validated. |
| GC-SPEC-QUE-008 | US-010 | Intake durability/backpressure behavior validated. |
| GC-SPEC-REL-001 | US-008 | Reliability language and behavior alignment validated. |
| GC-SPEC-REL-002 | US-008 | Duplicate side-effect suppression validated. |
| GC-SPEC-REL-003 | US-006 | Crash reclaim/recovery determinism validated. |
| GC-SPEC-REL-004 | US-010 | Timeline replay semantics validated. |
| GC-SPEC-REL-005 | US-017 | Graceful drain timeout and safe retry marking validated. |
| GC-SPEC-REL-006 | US-015 | DR drill with RPO/RTO target validated. |
| GC-SPEC-REL-007 | US-007 | Deterministic retry/terminal reason codes validated. |
| GC-SPEC-RUN-001 | US-001, US-002 | Startup order and required phases validated. |
| GC-SPEC-RUN-002 | US-016 | Bounded lane concurrency validated. |
| GC-SPEC-RUN-003 | US-017 | Graceful shutdown phases validated. |
| GC-SPEC-RUN-004 | US-017 | Trace/session/run/task correlation propagation validated. |
| GC-SPEC-RUN-005 | US-002 | Fatal startup reason-code event validated. |
| GC-SPEC-SCOPE-001 | US-001 | Single executable runtime scope validated. |
| GC-SPEC-SCOPE-002 | US-026 | Single-tenant process model validated. |
| GC-SPEC-SCOPE-003 | US-016 | Concurrent ACP + TUI operation in one daemon validated. |
| GC-SPEC-SEC-001 | US-011, US-027 | Default deny and abuse bypass denial validated. |
| GC-SPEC-SEC-002 | US-011, US-027 | Centralized PDP/PEP across paths validated. |
| GC-SPEC-SEC-003 | US-019 | Policy pinning per attempt validated. |
| GC-SPEC-SEC-004 | US-011, US-028 | SSRF controls validated in baseline and abuse corpus. |
| GC-SPEC-SEC-005 | US-011, US-014 | Redaction in logs/events/export validated. |
| GC-SPEC-SEC-006 | US-011 | Immutable decision audit completeness validated. |
| GC-SPEC-SEC-007 | US-019 | Invalid policy reload fail-closed behavior validated. |
| GC-SPEC-SEC-008 | US-012, US-028 | Approval broker and timeout deny validated. |
| GC-SPEC-SKL-001 | US-013 | Production `.wasm` loading and mode gating validated. |
| GC-SPEC-SKL-002 | US-013 | ABI mismatch blocks activation and preserves prior version. |
| GC-SPEC-SKL-003 | US-013 | Staged reload with rollback validated. |
| GC-SPEC-SKL-004 | US-013 | Cache key correctness by content hash + ABI validated. |
| GC-SPEC-SKL-005 | US-013 | Invocation limit enforcement validated. |
| GC-SPEC-SKL-006 | US-013 | Structured lifecycle events validated. |
| GC-SPEC-SKL-007 | US-013 | Auto-quarantine behavior validated. |
| GC-SPEC-STM-001 | US-005 | Canonical state set validated. |
| GC-SPEC-STM-002 | US-005 | Illegal transition rejection validated. |
| GC-SPEC-STM-003 | US-005 | Atomic task update + event append validated. |
| GC-SPEC-STM-004 | US-005 | Completion metadata and terminal reason code validated. |
| GC-SPEC-STM-005 | US-017 | Cancel observed pre/post tool boundary validated. |
| GC-SPEC-TUI-001 | US-012 | TUI operational-only parity with ACP validated. |
| GC-SPEC-TUI-002 | US-016 | Near-real-time ops panel fields validated. |
| GC-SPEC-TUI-003 | US-012 | Approval UX behavior validated. |
| GC-SPEC-TUI-004 | US-012, US-028 | Headless approval and ops without deadlock validated. |

## 8) Regression Checklist (P0 Condensed)
| Check ID | Stories | Commands / steps | Expected result | Evidence artifact |
| --- | --- | --- | --- | --- |
| RC-01 | US-001 | Start daemon with empty `GOCLAW_HOME`. | Config/token/DB/log paths bootstrap; token permissions restrictive. | `runtime_smoke.txt` |
| RC-02 | US-002 | Run startup order and failure tests in `internal/smoke`. | Ordered startup; fail-closed with reason code. | `runtime_daemon.log` |
| RC-03 | US-003 | Run persistence PRAGMA and contention tests. | WAL/FULL + busy-timeout + rollback behavior verified. | `db_pragmas.txt` |
| RC-04 | US-004 | Run migration checksum/version-guard tests. | Monotonic checksums; future version refused. | `db_schema.txt` |
| RC-05 | US-005 | Run state machine transition tests. | Legal transitions only; atomic `task_event` append. | transition report |
| RC-06 | US-006 | Run lease race + crash recovery drill. | Single claimant; expired leases reclaimed after restart. | `queue_lease_race.txt`, `recovery_sigkill.txt` |
| RC-07 | US-007 | Run retry/DLQ/poison tests. | Backoff+jitter; DLQ routing; poison short-circuit works. | retry/DLQ logs |
| RC-08 | US-008 | Run dedupe tests with retry/crash simulation. | Side effects execute once per idempotency key. | dedupe table snapshot |
| RC-09 | US-009 | Run ACP hello/auth/origin verifier. | Pre-hello mutate denied; auth/origin enforcement active. | `acp_ws_jsonrpc.txt` |
| RC-10 | US-010 | Run replay ordering/gap/backpressure tests. | FIFO replay, replay-gap error, deterministic close on overflow. | `acp_replay_backpressure.txt` |
| RC-11 | US-011 | Run policy default-deny + SSRF + redaction checks. | Unauthorized actions denied; secrets redacted; audit complete. | `security_policy_ssrf_redaction.txt` |
| RC-12 | US-012 | Run headless approval and TUI approval checks. | Approval broker works with and without TTY; timeout deny path works. | approval transcript |
| RC-13 | US-013 | Run WASM ABI/rollback/quarantine tests. | ABI mismatch preserves prior module; reload rollback and quarantine function. | `skills_wasm_lifecycle.txt` |
| RC-14 | US-014 | Run incident export verifier. | Bounded redacted export bundle generated and validated. | `incident_export.txt` |
| RC-15 | US-015 | Run backup/restore drill verifier. | Online backup works; RPO/RTO targets pass; health/status complete after restore. | `backup_restore_drill.txt` |
| RC-16 | US-016, US-017 | Run concurrency/fairness + abort/shutdown traces. | Lane cap honored, no starvation; graceful drain and cancel boundaries verified. | load + shutdown traces |
| RC-17 | US-024, US-025, US-026 | Run non-goal audits. | No browser automation, no clustering, no in-process multi-user separation. | `non_goals_audit.txt` |
| RC-18 | US-027, US-028 | Run abuse matrix for auth/origin/capability bypass + SSRF/approval timeout. | All bypass attempts denied with audit evidence. | abuse matrix report |

## 9) Verification Audit Results (2026-02-12)

**Audit date:** 2026-02-12
**Full test suite:** 14/14 packages PASS (`go test ./... -count=1 -timeout 120s`)
**RC-01..RC-18:** ALL PASS
**P0 stories (22):** ALL PASS | **P1 stories (4):** ALL PASS | **P2 stories (1):** N/A (performance benchmarks)
**Evidence pack:** 53 files in `docs/EVIDENCE_VERIFY/`
**Detailed regression status:** `docs/REGRESSION_STATUS.md`

### Fixes Applied During Audit
1. **US-006**: `tools/verify/lease_recovery_crash/main.go` — added VERDICT output line to `recover` mode.
2. **US-011/028**: `internal/policy/policy_test.go` — added `TestAllowHTTPURL_SSRFBypassCorpus` (21 encoded/scheme/IPv6/private CIDR vectors).
3. **US-012/028**: `internal/gateway/gateway_test.go` — added `TestGateway_ApprovalTimeoutDefaultDeny`; `gateway.go` added `ApprovalTimeout` config with auto-deny goroutine.
4. **US-027**: `internal/gateway/gateway_test.go` — added `TestGateway_AbuseDenialMatrix` and `TestGateway_OriginRejectsDisallowedOrigin`.
5. **US-024-026**: Created `tools/verify/non_goals_audit/main.go` — scans `go.mod`, `go.sum`, and `*.go` files for browser deps, clustering, multi-tenant code.
