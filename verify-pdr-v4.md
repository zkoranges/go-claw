# PDR v4 Verification — Single Pass

You are independently verifying that PDR-v4.md was implemented correctly. Do not fix anything. Only observe, test, and report.

Read PDR-v4.md first to understand what should exist.

Run every section below. Record PASS or FAIL with evidence for each check. Do not stop until all sections complete.

## 1. Build and Tests

```
just check
go test -race ./...
```

Record: build status, vet status, total test count, any failures.

## 2. Phase 1 — Foundation Wiring

Context store methods:
- grep store.go for SetTaskContext, GetTaskContext, GetAllTaskContext
- Run: go test ./internal/persistence/ -v -run TaskContext
- Verify tests assert actual values not just err == nil

Parent task linkage:
- grep delegate.go for SetParentTask or parent_task_id
- Trace the call: does it read the caller task ID from context and set it on the child?

Capability routing:
- grep delegate.go for capability and FindAgentsByCapability
- grep tools.go or tool schema definitions for capability parameter
- Verify: if target_agent is empty but capability is set, does it route via registry?

## 3. Phase 2 — Completion Tracking

Waiter exists:
- ls internal/coordinator/waiter.go
- grep waiter.go for WaitForTask and WaitForAll
- Run: go test ./internal/coordinator/ -v -run Wait

Bus-driven not polling:
- grep waiter.go for Subscribe or event channel pattern
- grep waiter.go for time.Sleep — must be zero occurrences
- grep delegate.go for time.Sleep — must be zero occurrences (old poll loop removed)

Race condition guard:
- grep waiter.go for checkTerminal — must check terminal state before subscribing to avoid missed events

## 4. Phase 3 — Working DAG Executor

Completion tracking wired:
- grep executor.go for WaitForAll or waiter
- Verify executeWave actually blocks until tasks finish (not fire-and-forget)

Template substitution:
- grep executor.go for resolvePrompt
- Run: go test ./internal/coordinator/ -v -run ResolvePrompt
- Verify test checks actual string replacement not just no-error

Plan execution recording:
- grep executor.go for CreatePlanExecution and CompletePlanExecution
- grep store.go for PlanExecution struct
- grep store.go for CreatePlanExecution, CompletePlanExecution, GetPlanExecution methods

Validation:
- grep plan.go for func.*Validate
- Run: go test ./internal/coordinator/ -v -run Validate
- Must have tests for: empty plan, duplicate step ID, missing dependency, cycle detection

## 5. Phase 4 — Plan System

Config schema:
- grep config.go for PlanConfig and Plans
- Verify PlanConfig has Name, Steps fields
- Verify PlanStepConfig has ID, AgentID, Prompt, DependsOn fields

Loader:
- ls internal/coordinator/loader.go
- grep loader.go for LoadPlansFromConfig
- Run: go test ./internal/coordinator/ -v -run LoadPlans
- Verify loader validates: unknown agent, duplicate plan name, cycle in steps

Wired into main:
- grep main.go for LoadPlansFromConfig
- grep main.go for plan reload or plan hot-reload in config watcher callback

TUI command:
- grep -rn "/plan" internal/tui/
- Verify: /plan with no args lists available plans
- Verify: /plan name input triggers execution

API endpoint:
- grep gateway.go for plans
- Verify: POST /api/plans route exists
- Verify: GET /api/plans route exists

## 6. Phase 5 — TUI Visibility

Delegation visibility:
- grep -rn delegationStatus internal/tui/ or grep activeDelegation
- Verify TUI model tracks active delegations with target agent and start time

Plan progress:
- grep -rn planStepStatus internal/tui/ or grep activePlan
- Verify TUI model tracks running plan with per-step status

Events published:
- grep delegate.go for delegation.started and delegation.completed
- grep executor.go for plan.step.started and plan.step.completed

## 7. Functional Smoke Test

```
rm -f ~/.goclaw/goclaw.db
just build
./dist/goclaw --daemon &
DAEMON_PID=$!
sleep 3
curl -sf http://127.0.0.1:18789/healthz && echo "healthz: PASS" || echo "healthz: FAIL"
curl -sf http://127.0.0.1:18789/api/plans && echo "plans API: PASS" || echo "plans API: FAIL"
kill $DAEMON_PID 2>/dev/null
wait $DAEMON_PID 2>/dev/null
```

## 8. Git History

```
git log --oneline | grep -i "PDR v4 Phase"
```

Verify: at least 5 commits, one per phase.

## 9. Schema Verification

```
rm -f ~/.goclaw/goclaw.db
just build && timeout 3s ./dist/goclaw 2>/dev/null || true
sqlite3 ~/.goclaw/goclaw.db "PRAGMA table_info(tasks);" | grep parent_task_id
sqlite3 ~/.goclaw/goclaw.db ".tables" | grep task_context
sqlite3 ~/.goclaw/goclaw.db ".tables" | grep plan_executions
```

## 10. Write Report

Save to PDR_V4_VERIFICATION.md with this format:

```
# PDR v4 Verification Report
Date: <timestamp>

## Summary
Phases verified: X/5
Total checks: X passed, Y failed

## Results

| # | Check | Phase | Status | Evidence |
|---|-------|-------|--------|----------|
| 1 | Build passes | - | PASS/FAIL | ... |
| 2 | Tests pass (count: N) | - | PASS/FAIL | ... |
| 3 | Race detector clean | - | PASS/FAIL | ... |
| 4 | SetTaskContext exists | 1 | PASS/FAIL | ... |
| 5 | GetTaskContext exists | 1 | PASS/FAIL | ... |
| 6 | Context store tests pass | 1 | PASS/FAIL | ... |
| 7 | SetParentTask called in delegation | 1 | PASS/FAIL | ... |
| 8 | Capability routing works | 1 | PASS/FAIL | ... |
| 9 | Waiter exists with WaitForTask | 2 | PASS/FAIL | ... |
| 10 | Waiter uses bus not polling | 2 | PASS/FAIL | ... |
| 11 | No time.Sleep in delegation | 2 | PASS/FAIL | ... |
| 12 | Race condition guard (checkTerminal) | 2 | PASS/FAIL | ... |
| 13 | Executor uses WaitForAll | 3 | PASS/FAIL | ... |
| 14 | resolvePrompt implemented | 3 | PASS/FAIL | ... |
| 15 | Template substitution tests pass | 3 | PASS/FAIL | ... |
| 16 | Plan execution recorded in DB | 3 | PASS/FAIL | ... |
| 17 | Validate checks cycles/dupes/deps | 3 | PASS/FAIL | ... |
| 18 | PlanConfig in config schema | 4 | PASS/FAIL | ... |
| 19 | LoadPlansFromConfig exists | 4 | PASS/FAIL | ... |
| 20 | Loader tests pass | 4 | PASS/FAIL | ... |
| 21 | Plans loaded in main.go | 4 | PASS/FAIL | ... |
| 22 | Plan hot-reload wired | 4 | PASS/FAIL | ... |
| 23 | /plan TUI command exists | 4 | PASS/FAIL | ... |
| 24 | Plans API endpoints exist | 4 | PASS/FAIL | ... |
| 25 | Delegation status in TUI model | 5 | PASS/FAIL | ... |
| 26 | Plan progress in TUI model | 5 | PASS/FAIL | ... |
| 27 | Delegation bus events published | 5 | PASS/FAIL | ... |
| 28 | Plan step bus events published | 5 | PASS/FAIL | ... |
| 29 | healthz returns 200 | - | PASS/FAIL | ... |
| 30 | /api/plans returns JSON | - | PASS/FAIL | ... |
| 31 | 5 phase commits in git log | - | PASS/FAIL | ... |
| 32 | parent_task_id in schema | - | PASS/FAIL | ... |
| 33 | task_context table exists | - | PASS/FAIL | ... |
| 34 | plan_executions table exists | - | PASS/FAIL | ... |

## Critical Failures
<list any FAIL items that block Vision patterns 2 or 3 from working>

## Assessment
<one paragraph: is the implementation complete enough for v0.2-dev?>
```

Commit the report: git add PDR_V4_VERIFICATION.md && git commit -m "PDR v4: verification report"
