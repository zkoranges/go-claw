# SPEC Traceability Matrix (v2.0.0 Contract)

Legend:
- Status: PENDING / PASS / FAIL
- Latest Step: last gated step that produced evidence for the requirement

| Requirement ID | Verification Hook(s) | Latest Step | Evidence Path(s) | Status |
| --- | --- | --- | --- | --- |
| GC-SPEC-ACP-001 | V-ACP-001 (Contract test suite) | ITER-02 | docs/EVIDENCE_VERIFY/acp_contract.txt | PASS |
| GC-SPEC-ACP-002 | V-ACP-002 (Handshake protocol test) | ITER-02 | docs/EVIDENCE_VERIFY/acp_contract.txt | PASS |
| GC-SPEC-ACP-003 | V-ACP-003 (Auth rejection test) | ITER-02 | docs/EVIDENCE_VERIFY/acp_contract.txt | PASS |
| GC-SPEC-ACP-004 | V-ACP-004 (Origin policy test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-ACP-005 | V-ACP-005 (Event schema/order test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-ACP-006 | V-ACP-006 (Ordering semantics test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-ACP-007 | V-ACP-007 (Reconnect/replay gap test) | ITER-02 | docs/EVIDENCE_VERIFY/acp_replay_backpressure.txt | PASS |
| GC-SPEC-ACP-008 | V-ACP-008 (Backpressure saturation test) | ITER-02 | docs/EVIDENCE_VERIFY/acp_replay_backpressure.txt | PASS |
| GC-SPEC-ACP-009 | V-ACP-009 (Compatibility contract test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-BRN-001 | V-BRN-001 (Architecture dependency test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-BRN-002 | V-BRN-002 (Tool-intent normalization test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-BRN-003 | V-BRN-003 (Schema validation test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-BRN-004 | V-BRN-004 (Compaction provenance test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-CFG-001 | V-CFG-001 (Config precedence test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-CFG-002 | V-CFG-002 (Reload behavior test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-CFG-003 | V-CFG-003 (Invalid reload resilience test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-CFG-004 | V-CFG-004 (Token bootstrap test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-CFG-005 | V-CFG-005 (Status fingerprint test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-CFG-006 | V-CFG-006 (Policy versioning test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-CFG-007 | V-CFG-007 (Legacy mode default test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-DATA-001 | V-DATA-001 (Migration ledger test) | ITER-02 | docs/EVIDENCE_VERIFY/db_schema.txt | PASS |
| GC-SPEC-DATA-002 | V-DATA-002 (Version guard test) | S-002 | docs/EVIDENCE/S-002/db.txt; docs/EVIDENCE/S-002/migrations.txt | PASS |
| GC-SPEC-DATA-003 | V-DATA-003 (Migration atomicity test) | S-002 | docs/EVIDENCE/S-002/db.txt; docs/EVIDENCE/S-002/migrations.txt | PASS |
| GC-SPEC-DATA-004 | V-DATA-004 (Restore rollback drill) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-DATA-005 | V-DATA-005 (Retention config test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-DATA-006 | V-DATA-006 (PII purge test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-DATA-007 | V-DATA-007 (Redaction metadata test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-DATA-008 | V-DATA-008 (Retention idempotency test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-NG-001 | V-NG-001 (Dependency and capability audit) | ITER-02 | docs/EVIDENCE_VERIFY/non_goals_audit.txt | PASS |
| GC-SPEC-NG-002 | V-NG-002 (Architecture review + deployment test) | ITER-02 | docs/EVIDENCE_VERIFY/non_goals_audit.txt | PASS |
| GC-SPEC-NG-003 | V-NG-003 (Architecture review) | ITER-02 | docs/EVIDENCE_VERIFY/non_goals_audit.txt | PASS |
| GC-SPEC-OBS-001 | V-OBS-001 (Log schema test) | S-007 | docs/EVIDENCE/S-007/log_schema.txt; docs/EVIDENCE/S-007/startup_order.txt | PASS |
| GC-SPEC-OBS-002 | V-OBS-002 (Replay completeness test) | ITER-02 | docs/EVIDENCE_VERIFY/acp_replay_backpressure.txt | PASS |
| GC-SPEC-OBS-003 | V-OBS-003 (Audit append-only test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-OBS-004 | V-OBS-004 (Metrics coverage test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-OBS-005 | V-OBS-005 (Health contract test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-OBS-006 | V-OBS-006 (Incident export test) | ITER-02 | docs/EVIDENCE_VERIFY/incident_export.txt | PASS |
| GC-SPEC-PER-001 | V-PER-001 (PRAGMA assertion test) | ITER-02 | docs/EVIDENCE_VERIFY/db_pragmas.txt | PASS |
| GC-SPEC-PER-002 | V-PER-002 (Lock contention test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-PER-003 | V-PER-003 (Transaction scope instrumentation) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-PER-004 | V-PER-004 (Fault injection test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-PER-005 | V-PER-005 (Backup/restore drill) | ITER-02 | docs/EVIDENCE_VERIFY/backup_restore_drill.txt | PASS |
| GC-SPEC-PER-006 | V-PER-006 (Crash recovery test) | ITER-02 | docs/EVIDENCE_VERIFY/recovery_sigkill.txt | PASS |
| GC-SPEC-PERF-001 | V-PERF-001 (Startup benchmark) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-PERF-002 | V-PERF-002 (Memory profile report) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-PERF-003 | V-PERF-003 (Load test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-PERF-004 | V-PERF-004 (DB latency profiling) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-PERF-005 | V-PERF-005 (Replay performance test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-PERF-006 | V-PERF-006 (Hot-reload contention test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-QUE-001 | V-QUE-001 (Concurrent claim race test) | ITER-02 | docs/EVIDENCE_VERIFY/queue_lease_race.txt | PASS |
| GC-SPEC-QUE-002 | V-QUE-002 (Lease expiry/heartbeat test) | S-004 | docs/EVIDENCE/S-004/lease_race.txt; docs/EVIDENCE/S-004/crash_recovery.txt | PASS |
| GC-SPEC-QUE-003 | V-QUE-003 (Retry timing test) | ITER-02 | docs/EVIDENCE_VERIFY/queue_lease_race.txt | PASS |
| GC-SPEC-QUE-004 | V-QUE-004 (DLQ transition test) | ITER-02 | docs/EVIDENCE_VERIFY/queue_lease_race.txt | PASS |
| GC-SPEC-QUE-005 | V-QUE-005 (Poison-pill heuristic test) | ITER-02 | docs/EVIDENCE_VERIFY/queue_lease_race.txt | PASS |
| GC-SPEC-QUE-006 | V-QUE-006 (Idempotency dedupe test) | ITER-02 | docs/EVIDENCE_VERIFY/queue_lease_race.txt | PASS |
| GC-SPEC-QUE-007 | V-QUE-007 (Fairness simulation test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-QUE-008 | V-QUE-008 (Load/backpressure integration test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-REL-001 | V-REL-001 (Doc + behavior conformance review) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-REL-002 | V-REL-002 (Duplicate side-effect chaos test) | ITER-02 | docs/EVIDENCE_VERIFY/queue_lease_race.txt | PASS |
| GC-SPEC-REL-003 | V-REL-003 (SIGKILL recovery test) | ITER-02 | docs/EVIDENCE_VERIFY/recovery_sigkill.txt | PASS |
| GC-SPEC-REL-004 | V-REL-004 (Timeline reconstruction test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-REL-005 | V-REL-005 (Shutdown-drain test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-REL-006 | V-REL-006 (Disaster recovery drill) | ITER-02 | docs/EVIDENCE_VERIFY/backup_restore_drill.txt | PASS |
| GC-SPEC-REL-007 | V-REL-007 (Retry reason-code test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-RUN-001 | V-RUN-001 (Integration startup-order test) | S-007 | docs/EVIDENCE/S-007/log_schema.txt; docs/EVIDENCE/S-007/startup_order.txt | PASS |
| GC-SPEC-RUN-002 | V-RUN-002 (Concurrency stress test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-RUN-003 | V-RUN-003 (Signal/termination integration test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-RUN-004 | V-RUN-004 (Trace propagation test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-RUN-005 | V-RUN-005 (Failure injection test) | S-007 | docs/EVIDENCE/S-007/log_schema.txt; docs/EVIDENCE/S-007/startup_order.txt | PASS |
| GC-SPEC-SCOPE-001 | V-SCOPE-001 (Build/packaging evidence) | ITER-02 | docs/EVIDENCE_VERIFY/build.txt | PASS |
| GC-SPEC-SCOPE-002 | V-SCOPE-002 (Integration + config isolation test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-SCOPE-003 | V-SCOPE-003 (Integration multi-client test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-SEC-001 | V-SEC-001 (Policy default-deny test) | S-006 | docs/EVIDENCE/S-006/policy.txt; docs/EVIDENCE/S-006/policy_reload.txt | PASS |
| GC-SPEC-SEC-002 | V-SEC-002 (Cross-path enforcement test) | S-006 | docs/EVIDENCE/S-006/policy.txt; docs/EVIDENCE/S-006/policy_reload.txt | PASS |
| GC-SPEC-SEC-003 | V-SEC-003 (Policy pinning test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-SEC-004 | V-SEC-004 (SSRF defense test) | ITER-02 | docs/EVIDENCE_VERIFY/security_policy_ssrf_redaction.txt | PASS |
| GC-SPEC-SEC-005 | V-SEC-005 (Redaction test) | ITER-02 | docs/EVIDENCE_VERIFY/security_policy_ssrf_redaction.txt | PASS |
| GC-SPEC-SEC-006 | V-SEC-006 (Audit completeness test) | ITER-02 | docs/EVIDENCE_VERIFY/security_policy_ssrf_redaction.txt | PASS |
| GC-SPEC-SEC-007 | V-SEC-007 (Invalid policy reload test) | S-006 | docs/EVIDENCE/S-006/policy_reload.txt | PASS |
| GC-SPEC-SEC-008 | V-SEC-008 (Headless approval test) | ITER-02 | docs/EVIDENCE_VERIFY/acp_replay_backpressure.txt | PASS |
| GC-SPEC-SKL-001 | V-SKL-001 (Mode-gating test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-SKL-002 | V-SKL-002 (ABI mismatch test) | ITER-02 | docs/EVIDENCE_VERIFY/skills_wasm_lifecycle.txt | PASS |
| GC-SPEC-SKL-003 | V-SKL-003 (Atomic reload rollback test) | ITER-02 | docs/EVIDENCE_VERIFY/skills_wasm_lifecycle.txt | PASS |
| GC-SPEC-SKL-004 | V-SKL-004 (Cache correctness test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-SKL-005 | V-SKL-005 (Resource-limit test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-SKL-006 | V-SKL-006 (Lifecycle event test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-SKL-007 | V-SKL-007 (Auto-quarantine test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-STM-001 | V-STM-001 (State enum + transition tests) | S-003 | docs/EVIDENCE/S-003/state_machine.txt; docs/EVIDENCE/S-003/transition_negative.txt | PASS |
| GC-SPEC-STM-002 | V-STM-002 (Negative transition tests) | S-003 | docs/EVIDENCE/S-003/state_machine.txt; docs/EVIDENCE/S-003/transition_negative.txt | PASS |
| GC-SPEC-STM-003 | V-STM-003 (Atomicity integration test) | S-003 | docs/EVIDENCE/S-003/state_machine.txt; docs/EVIDENCE/S-003/transition_negative.txt | PASS |
| GC-SPEC-STM-004 | V-STM-004 (Completion metadata test) | S-003 | docs/EVIDENCE/S-003/state_machine.txt; docs/EVIDENCE/S-003/transition_negative.txt | PASS |
| GC-SPEC-STM-005 | V-STM-005 (Abort race test) | S-003 | docs/EVIDENCE/S-003/state_machine.txt; docs/EVIDENCE/S-003/transition_negative.txt | PASS |
| GC-SPEC-TUI-001 | V-TUI-001 (Feature parity review) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-TUI-002 | V-TUI-002 (Manual operational check) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-TUI-003 | V-TUI-003 (Approval UX test) | S-001 | docs/EVIDENCE/S-001/README.md | PENDING |
| GC-SPEC-TUI-004 | V-TUI-004 (Headless integration test) | ITER-02 | docs/EVIDENCE_VERIFY/acp_replay_backpressure.txt | PASS |
