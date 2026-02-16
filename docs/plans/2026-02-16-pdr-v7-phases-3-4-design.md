# PDR-v7 Phases 3-4 Design: Telegram Integration & A2A Protocol

**Date:** 2026-02-16
**Phases:** Phase 3 (Telegram Deep Integration) + Phase 4 (A2A Protocol)
**Status:** Design Approved, Ready for Implementation

---

## Overview

Phase 3 adds real-time plan execution tracking, human-in-the-loop (HITL) approval gates, and agent alerts to Telegram. Phase 4 provides passive A2A agent discovery via a standard agent card endpoint.

---

## Phase 3: Telegram Deep Integration

### Event Contract (Unified Event Types)

**Location:** `internal/bus/topics.go`

```go
// Plan execution lifecycle
const (
    TopicPlanExecutionStarted   = "plan.execution.started"
    TopicPlanExecutionCompleted = "plan.execution.completed"
    TopicPlanStepStarted        = "plan.step.started"
    TopicPlanStepCompleted      = "plan.step.completed"
    TopicPlanStepFailed         = "plan.step.failed"
)

// HITL approval gates
const (
    TopicHITLApprovalRequested  = "hitl.approval.requested"
    TopicHITLApprovalResponse   = "hitl.approval.response"
)

// Alerts from agents
const (
    TopicAgentAlert = "agent.alert"
)
```

Event payload structures in same file:
- `PlanStepEvent` (with Duration, Result, Error fields)
- `HITLApprovalRequest` (with RequestID UUID, timeout)
- `HITLApprovalResponse` (matches request via RequestID)
- `AgentAlert` (severity: info/warning/critical)

### Coordinator HITL Implementation

**File:** `internal/coordinator/executor.go`

When executing a plan step with `Approval == "required"`:
1. Publish `TopicHITLApprovalRequested` event
2. Pause execution and wait for `TopicHITLApprovalResponse` with timeout
3. Auto-reject on timeout (fail step, continue plan)
4. On approval: continue to step execution
5. On rejection: fail step, continue plan

Publish lifecycle events:
- `TopicPlanStepStarted` when step begins
- `TopicPlanStepCompleted` on success
- `TopicPlanStepFailed` on failure

### Alert Tool

**File:** `internal/tools/alert.go` (new)

Tool: `send_alert`
- Input: severity (info/warning/critical), title, body
- Validation: reject invalid severity
- Action: publish `TopicAgentAlert` with agent ID from context
- Output: confirmation with message ID

Register in `tools.Registry.RegisterAll()`.

### Telegram Event Integration

**File:** `internal/channels/telegram.go`

On init, subscribe to all events:
- `plan.step.*`: Format progress (✅ completed, ❌ failed) as MarkdownV2
- `hitl.approval.requested`: Send message with inline keyboard (Approve/Reject buttons)
- `agent.alert`: Display with severity emoji (ℹ️ info, ⚠️ warning, 🚨 critical)

Handle callback queries: Parse button data as `hitl:{requestID}:{action}`, publish `TopicHITLApprovalResponse`.

Handle `/plan <name> <input>` command: Call coordinator to execute named plan.

### main.go Integration

Pass to Telegram channel constructor:
- Event bus (for subscriptions)
- Coordinator (for `/plan` command)

Ensure Coordinator has bus reference for event publishing.

---

## Phase 4: A2A Protocol

### Agent Card Endpoint

**File:** `internal/gateway/a2a.go` (new)

Route: `GET /.well-known/agent.json`

Response: AgentCard JSON with:
- Basic metadata (name, description, version, URL)
- Capabilities (streaming, pushNotifications, stateTransitionHistory)
- Skills array (agents from registry with ID, name, tags)

Status codes:
- 200 OK: Card returned
- 404 Not Found: A2A disabled
- 405 Method Not Allowed: Non-GET methods

MarkdownV2 escaping: All special characters escaped in Telegram messages.

### Configuration

**File:** `internal/config/config.go`

Add `A2AConfig` struct with `Enabled *bool` field (default true).
Parse from `a2a.enabled` in config.yaml.

### Gateway Integration

**File:** `internal/gateway/gateway.go`

Store `a2aEnabled` bool in Gateway struct.
Register route handler in mux.
Pass config A2A flag to gateway constructor.

---

## Testing Strategy

**Phase 3 Tests (20+ tests):**
- `internal/coordinator/executor_test.go`: HITL gate pause/resume/timeout (4 tests)
- `internal/tools/alert_test.go`: Severity validation, event publishing (3 tests)
- `internal/channels/telegram_test.go`: Event rendering, callback parsing, /plan handler (8 tests)
- `internal/tui/activity_test.go`: Alert/delegation display in feed (3 tests)
- `internal/bus/topics_test.go`: Event struct marshaling (2 tests)

**Phase 4 Tests (6+ tests):**
- `internal/gateway/a2a_test.go`: Endpoint GET/POST/PUT/DELETE, enabled/disabled, schema validation (6 tests)

All tests use mocked/stubbed dependencies (no real API calls, no Telegram bot).

---

## Acceptance Criteria (PDR-v7 §8.3)

### Phase 3
- [ ] Coordinator publishes plan.step.* events
- [ ] HITL gate pauses execution at approval-required steps
- [ ] Approval continues, rejection fails step
- [ ] Timeout auto-rejects after deadline
- [ ] Alert tool registers with severity validation
- [ ] Telegram subscribes to plan/hitl/alert events
- [ ] Plan progress formatted as MarkdownV2
- [ ] HITL inline keyboard renders and callback parses
- [ ] /plan command validates and triggers execution
- [ ] 20+ new tests, all passing
- [ ] No regressions in existing tests

### Phase 4
- [ ] GET /.well-known/agent.json returns valid JSON
- [ ] Agents listed as skills in response
- [ ] POST/PUT/DELETE return 405
- [ ] Disabled via config returns 404
- [ ] Content-Type is application/json
- [ ] 6+ new tests, all passing

---

## Implementation Order

1. **Coordinator HITL** (foundation for Telegram)
2. **Alert Tool** (independent)
3. **Telegram Integration** (consumes coordinator + alert events)
4. **A2A Protocol** (independent of Phase 3)
5. **Integration in main.go** (wire bus/coordinator to Telegram)
6. **Comprehensive Testing** (verify all events, timeouts, edge cases)

---

## Verification

**Per-phase gates:**
- Phase 3 tests pass: `go test ./internal/coordinator/... ./internal/tools/... ./internal/channels/... -v -count=1`
- Phase 4 tests pass: `go test ./internal/gateway/... -v -count=1`

**Full verification:**
```bash
just check                    # build + vet + test
go test -race ./... -count=1  # race detector clean
```

**Test count:** Baseline 725 → Target 750+ (25+ new tests)
