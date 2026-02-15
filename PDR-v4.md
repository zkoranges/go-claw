# PDR: Multi-Agent Coordination — Functional Layer

**Status**: READY FOR IMPLEMENTATION
**Version**: 4.0
**Date**: 2026-02-15
**Depends on**: PDR v3.0 (all 5 phases complete and verified)
**Target**: go-claw v0.2-dev
**Phases**: 5 sequential phases, each with hard verification gates

---

## What This PDR Does

PDR v3.0 shipped the plumbing: schema tables, pricing engine, bus wiring, event publishing, delegation types, DAG structure, analytics queries. All verified passing.

This PDR connects the plumbing into working features. After completion, Patterns 2 (Delegation) and 3 (Team Workflows) from VISION.md will actually work end-to-end.

**What ships at the end:**
- Delegating agent sets parent_task_id (task trees work)
- Agents can route delegation by capability, not just agent name
- Agents can read/write shared context within a task tree
- Delegation and DAG completion tracked via bus events (not polling)
- DAG executor waits for real task completion before advancing
- Step outputs flow into subsequent step prompts via template substitution
- Plans defined in config.yaml, validated at load, hot-reloaded
- /plan TUI command triggers named workflows
- POST /api/plans/{name}/run API endpoint
- TUI shows delegation status and plan progress in real time

---

## Execution Protocol

Identical to PDR v3.0. These rules are repeated here because they are non-negotiable:

1. **Read before writing.** Before each phase, run every context-gathering command. The codebase is the source of truth. If this PDR contradicts the code, follow the code and adapt.

2. **One step at a time.** Complete each numbered step fully before starting the next. Run verification immediately.

3. **Compilation after every edit.** `go build ./...` after every file change. If it fails, fix before moving on. Use `grep -rn "FunctionName" internal/` to find all callers when changing signatures.

4. **Hard gate between phases.** Every gate command must pass before proceeding.

5. **Commit after each gate.** `git add -A && git commit -m "PDR v4 Phase N complete"` after each passing gate.

6. **Rollback on catastrophic failure.** `git reset --hard HEAD~1` to return to last gate.

7. **Match existing code style.** Before writing new code, read 2-3 existing files in the same package. Match error handling, logging, naming, and test patterns exactly.

---

## Pre-Flight

Before starting any phase:

```bash
git status          # Clean working tree
just check          # Build + vet + test all pass
go test -race ./... # Zero races
```

Then verify PDR v3.0 artifacts exist:

```bash
# Tables from v3.0 must exist
rm -f ~/.goclaw/goclaw.db
just build && timeout 3s ./dist/goclaw 2>/dev/null || true
sqlite3 ~/.goclaw/goclaw.db ".tables" | grep task_metrics
sqlite3 ~/.goclaw/goclaw.db ".tables" | grep task_context
sqlite3 ~/.goclaw/goclaw.db ".tables" | grep plan_executions

# Packages from v3.0 must compile
go build ./internal/pricing/
go build ./internal/coordinator/
go build ./internal/analytics/

# Bus wired into Store
grep -n "bus" internal/persistence/store.go | head -10
```

If any pre-flight check fails, fix it before starting. Do not build v4.0 on a broken v3.0 foundation.

Then gather context for the entire PDR:

```bash
# Current Store struct and constructor
grep -B2 -A15 "type Store struct" internal/persistence/store.go
grep -B2 -A15 "func New" internal/persistence/store.go | head -25

# Current bus type and Publish signature
grep -B2 -A10 "type Bus struct" internal/bus/*.go
grep -B2 -A10 "func.*Publish" internal/bus/*.go
grep -B2 -A10 "func.*Subscribe" internal/bus/*.go

# Current delegation implementation
cat internal/tools/delegate.go

# Current coordinator types
cat internal/coordinator/plan.go
cat internal/coordinator/executor.go

# Current config struct
grep -B2 -A30 "type Config struct" internal/config/config.go
grep -B2 -A20 "type AgentConfigEntry struct\|type AgentEntry struct" internal/config/config.go

# Current task struct and status constants
grep -B2 -A30 "type Task struct" internal/persistence/store.go
grep -n "TaskStatus\|= \"SUCCEEDED\"\|= \"FAILED\"\|= \"QUEUED\"" internal/persistence/store.go | head -20

# Current TUI model
grep -B2 -A20 "type Model struct\|type model struct" internal/tui/*.go | head -40

# How tools are registered
grep -n "delegate_task\|AddTool\|register" internal/tools/tools.go | head -20

# How events currently publish
grep -n "bus.Publish\|Publish(" internal/persistence/store.go | head -10

# Context helpers
grep -n "func.*TaskID\|func.*AgentID\|func.*SessionID" internal/shared/*.go

# How config watches for changes
grep -n "fsnotify\|Watch\|OnChange\|reload" internal/config/config.go | head -10

# Gateway route registration pattern
grep -n "HandleFunc\|Handle(" internal/gateway/gateway.go | head -20

# CreateChatTask signature
grep -B2 -A10 "func.*CreateChatTask" internal/agent/registry.go
```

Record ALL output. You will reference it throughout every phase.

---

# PHASE 1: Wire the Foundation

**Goal**: Connect v3.0's dead-code artifacts into working functionality. After Phase 1: parent_task_id is set during delegation, capabilities route to agents, and shared context has read/write methods.
**Files modified**: `internal/persistence/store.go`, `internal/tools/delegate.go`, `internal/agent/registry.go`
**Risk**: Low. Purely additive — no signature changes, no callers broken.

## Step 1.1: Add Context Store Methods

v3.0 created the `task_context` table but added no Go methods to access it.

**File**: `internal/persistence/store.go`

First verify the table schema:
```bash
grep -A10 "task_context" internal/persistence/store.go
```

Add these methods:

```go
// SetTaskContext writes a key-value pair to the shared context for a task tree.
func (s *Store) SetTaskContext(ctx context.Context, taskRootID, key, value string) error {
    _, err := s.db.ExecContext(ctx, `
        INSERT INTO task_context (task_root_id, key, value, updated_at)
        VALUES (?, ?, ?, CURRENT_TIMESTAMP)
        ON CONFLICT(task_root_id, key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
        taskRootID, key, value,
    )
    if err != nil {
        return fmt.Errorf("set task context %s/%s: %w", taskRootID, key, err)
    }
    return nil
}

// GetTaskContext reads a value from the shared context for a task tree.
// Returns empty string and no error if the key does not exist.
func (s *Store) GetTaskContext(ctx context.Context, taskRootID, key string) (string, error) {
    var value string
    err := s.db.QueryRowContext(ctx, `
        SELECT value FROM task_context WHERE task_root_id = ? AND key = ?`,
        taskRootID, key,
    ).Scan(&value)
    if err == sql.ErrNoRows {
        return "", nil
    }
    if err != nil {
        return "", fmt.Errorf("get task context %s/%s: %w", taskRootID, key, err)
    }
    return value, nil
}

// GetAllTaskContext returns all key-value pairs for a task tree.
func (s *Store) GetAllTaskContext(ctx context.Context, taskRootID string) (map[string]string, error) {
    rows, err := s.db.QueryContext(ctx, `
        SELECT key, value FROM task_context WHERE task_root_id = ?`, taskRootID)
    if err != nil {
        return nil, fmt.Errorf("get all task context %s: %w", taskRootID, err)
    }
    defer rows.Close()

    result := make(map[string]string)
    for rows.Next() {
        var k, v string
        if err := rows.Scan(&k, &v); err != nil {
            return nil, fmt.Errorf("scan task context: %w", err)
        }
        result[k] = v
    }
    return result, rows.Err()
}
```

**Adapt**: Match the error wrapping pattern used by other Store methods (check if they use `fmt.Errorf("...: %w")` or a different pattern). Use the same `sql.ErrNoRows` import path.

**Verify**:
```bash
go build ./internal/persistence/
```

## Step 1.2: Test Context Store Methods

**File**: `internal/persistence/store_test.go` (or a new file `internal/persistence/context_test.go`)

Check existing test patterns first:
```bash
head -30 internal/persistence/store_test.go
grep -n "func Test" internal/persistence/store_test.go | head -10
```

Write tests matching the existing pattern:

```go
func TestSetGetTaskContext(t *testing.T) {
    // Use the same test setup pattern as other tests in this file
    // Create store, set context, get it back, verify value
    // Then overwrite same key, verify new value
}

func TestGetTaskContext_NotFound(t *testing.T) {
    // Create store, get nonexistent key
    // Must return empty string and nil error (NOT an error)
}

func TestGetAllTaskContext(t *testing.T) {
    // Set 3 keys, get all, verify map has exactly 3 entries
}
```

**Adapt**: Copy the exact test setup function used by existing tests (helper to create temp DB, Store instance, cleanup). Do not invent a new pattern.

**Verify**:
```bash
go test ./internal/persistence/ -v -run "TaskContext" -count=1
```

## Step 1.3: Add SetParentTask Method

**File**: `internal/persistence/store.go`

```go
// SetParentTask links a child task to its parent in the task tree.
func (s *Store) SetParentTask(ctx context.Context, childTaskID, parentTaskID string) error {
    _, err := s.db.ExecContext(ctx, `
        UPDATE tasks SET parent_task_id = ? WHERE id = ?`,
        parentTaskID, childTaskID,
    )
    if err != nil {
        return fmt.Errorf("set parent task %s -> %s: %w", childTaskID, parentTaskID, err)
    }
    return nil
}
```

**Verify**: `go build ./internal/persistence/`

## Step 1.4: Wire parent_task_id into Delegation

**File**: `internal/tools/delegate.go`

Find the existing `delegate_task_async` function (or the sync `delegate_task` if async wasn't implemented in v3.0):

```bash
grep -B5 -A30 "func delegate" internal/tools/delegate.go
```

Identify how the calling agent's task ID is available. Check:
```bash
grep -n "TaskID\|taskID\|task_id" internal/shared/*.go
grep -n "context.Value\|ctx.Value" internal/tools/delegate.go
```

After the line that calls `CreateChatTask` (which returns the new task ID), add:

```go
// Set parent-child relationship for task tree
callerTaskID := shared.TaskID(ctx) // or however the calling task's ID is stored in context
if callerTaskID != "" {
    // Best-effort: don't fail the delegation if this fails
    if store != nil {
        _ = store.SetParentTask(ctx, taskID, callerTaskID)
    }
}
```

**Adapt**:
- `shared.TaskID(ctx)` may be named differently. Check context helpers output from pre-flight.
- Store access may come from context, or may need to be passed differently. Check how other tools access the Store.
- If the calling agent's task ID is not in context, you need to find where tasks are executed and add it. Check: `grep -n "context.WithValue\|WithTaskID" internal/engine/` — if a `WithTaskID` helper doesn't exist, you may need to create one in `internal/shared/`.

**Verify**:
```bash
go build ./internal/tools/
go test ./internal/tools/ -count=1
```

## Step 1.5: Wire Capability Routing into Delegation

The current `delegate_task_async` (or `delegate_task`) takes a `target_agent` string. If the caller doesn't know the specific agent, they should be able to request by capability instead.

**File**: `internal/tools/delegate.go`

Find how the Registry is accessed in tools:
```bash
grep -n "Registry\|registry\|agent.Registry" internal/tools/*.go
```

Modify the delegation function to support capability-based routing. The input should accept EITHER `target_agent` (specific) OR `capability` (routed):

```go
// Inside the delegation function, before CreateChatTask:
targetAgent, _ := input["target_agent"].(string)
capability, _ := input["capability"].(string)

if targetAgent == "" && capability == "" {
    return "", fmt.Errorf("either target_agent or capability is required")
}

// Route by capability if no specific agent given
if targetAgent == "" && capability != "" {
    registry := /* get registry from context — adapt to match existing pattern */
    matches := registry.FindAgentsByCapability(ctx, capability)
    if len(matches) == 0 {
        return "", fmt.Errorf("no agent found with capability: %s", capability)
    }
    targetAgent = matches[0] // Use first match (simplest routing)
}
```

**Adapt**: The registry access pattern varies wildly by codebase. Options:
- Context key: `ctx.Value(registryKey).(*agent.Registry)`
- Closure: The tool function may be a closure that captures the registry
- Global: There may be a package-level registry

Check how other tools access shared resources and match the pattern.

**Verify**:
```bash
go build ./internal/tools/
```

## Step 1.6: Update Tool Schema for Capability Parameter

Find where tool schemas/descriptions are defined:
```bash
grep -B5 -A15 "delegate_task" internal/tools/tools.go
```

Update the delegation tool's parameter schema to include `capability` as an optional parameter. The LLM needs to know it can route by capability.

**Adapt**: Tool schema format depends on how tools are defined (JSON schema struct, map of strings, etc.). Match exactly.

**Verify**: `go build ./internal/tools/`

## GATE 1

```bash
just check
go test -race ./...
go test ./internal/persistence/ -v -run "TaskContext"

# Verify context store works end-to-end
grep -n "SetTaskContext\|GetTaskContext\|GetAllTaskContext" internal/persistence/store.go

# Verify parent_task_id is set during delegation
grep -n "SetParentTask\|parent_task_id" internal/tools/delegate.go

# Verify capability routing exists
grep -n "capability\|FindAgentsByCapability" internal/tools/delegate.go
```

If all pass: `git add -A && git commit -m "PDR v4 Phase 1: wire foundation"`

---

# PHASE 2: Event-Driven Completion Tracking

**Goal**: Build a reusable TaskCompletionWaiter that uses bus events instead of polling. This is the critical enabler for both delegation and DAG execution.
**Files created**: `internal/coordinator/waiter.go`, `internal/coordinator/waiter_test.go` (added to existing package)
**Files modified**: `internal/tools/delegate.go`
**Risk**: Medium. Depends on Phase 2 of v3.0 (bus event publishing) actually working.

## Step 2.0: Pre-Flight — Verify Bus Events Fire

**CRITICAL**: Before writing ANY code in this phase, verify that v3.0's bus wiring works.

```bash
# Check that task completion publishes to bus
grep -n "bus.Publish\|Publish(" internal/persistence/store.go
```

You should see Publish calls in task completion paths (CompleteTask, FailTask, etc.). If you don't see any, v3.0's Phase 3 (Event Publishing) was incomplete. Fix it before proceeding.

Next, verify the bus Subscribe mechanism:
```bash
grep -B2 -A15 "func.*Subscribe" internal/bus/*.go
```

Record:
- What does Subscribe return? (channel? callback? subscription object?)
- Can you filter by topic/event type?
- Is there an Unsubscribe mechanism?

**If bus events don't fire on task completion, STOP. Go back to v3.0 Phase 2-3 and fix the event publishing first. Do not build Phase 2 on broken bus infrastructure.**

## Step 2.1: Design TaskCompletionWaiter

The waiter subscribes to bus events for specific task IDs and blocks until they reach a terminal state. It is used by both `await_delegation` and the DAG executor.

**File**: `internal/coordinator/waiter.go` (new package)

```go
// waiter.go adds task completion tracking to the coordinator package.
package coordinator

import (
    "context"
    "fmt"
    "sync"
    "time"

    "github.com/basket/go-claw/internal/bus"
    "github.com/basket/go-claw/internal/persistence"
)

// TaskResult holds the outcome of a completed task.
type TaskResult struct {
    TaskID           string
    Status           string
    Output           string
    PromptTokens     int
    CompletionTokens int
    CostUSD          float64
    DurationMs       int64
    Error            string
}

// Waiter tracks task completion via bus events.
type Waiter struct {
    eventBus *bus.Bus
    store    *persistence.Store
}

// NewWaiter creates a task completion waiter.
func NewWaiter(eventBus *bus.Bus, store *persistence.Store) *Waiter {
    return &Waiter{eventBus: eventBus, store: store}
}
```

**Adapt**: Import paths must match go.mod module path. Bus type must match what you found in Step 2.0.

**Verify**: `go build ./internal/coordinator/`

## Step 2.2: Implement WaitForTask

**File**: `internal/coordinator/waiter.go`

```go
// WaitForTask blocks until the given task reaches a terminal state or the context expires.
// Uses bus event subscription — does not poll.
func (w *Waiter) WaitForTask(ctx context.Context, taskID string, timeout time.Duration) (*TaskResult, error) {
    ctx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()

    // Subscribe to task events
    // ADAPT: This depends entirely on the bus.Subscribe API you found in Step 2.0
    // Option A: channel-based
    //   ch := w.eventBus.Subscribe("task.*")
    //   defer w.eventBus.Unsubscribe(ch)
    // Option B: callback-based
    //   sub := w.eventBus.Subscribe("task.*", func(event bus.Event) { ... })
    //   defer sub.Cancel()

    // Check if already terminal before waiting (race condition guard)
    result, err := w.checkTerminal(ctx, taskID)
    if err != nil {
        return nil, err
    }
    if result != nil {
        return result, nil
    }

    // Wait for bus event or timeout
    for {
        select {
        case <-ctx.Done():
            return nil, fmt.Errorf("timeout waiting for task %s: %w", taskID, ctx.Err())

        // ADAPT to bus subscription mechanism:
        // case event := <-ch:
        //     if extractTaskID(event) != taskID { continue }
        //     result, err := w.checkTerminal(ctx, taskID)
        //     if err != nil { return nil, err }
        //     if result != nil { return result, nil }
        }
    }
}

// checkTerminal checks if a task is in a terminal state and returns its result.
// Returns (nil, nil) if the task is still in progress.
func (w *Waiter) checkTerminal(ctx context.Context, taskID string) (*TaskResult, error) {
    task, err := w.store.GetTask(ctx, taskID)
    if err != nil {
        return nil, fmt.Errorf("get task %s: %w", taskID, err)
    }
    if task == nil {
        return nil, fmt.Errorf("task %s not found", taskID)
    }

    if !isTerminalStatus(task.Status) {
        return nil, nil
    }

    return &TaskResult{
        TaskID:           task.ID,
        Status:           task.Status,
        // ADAPT: Use actual Task struct field names from pre-flight
        // Output:        task.Result or task.Output
        // PromptTokens:  task.PromptTokens
        // CompletionTokens: task.CompletionTokens
        // CostUSD:       task.EstimatedCostUSD
        // DurationMs:    task.DurationMs
        // Error:         task.ErrorMessage or task.Error
    }, nil
}

func isTerminalStatus(status string) bool {
    // ADAPT: Use actual status constants from pre-flight
    switch status {
    case "SUCCEEDED", "FAILED", "CANCELED", "DEAD_LETTER":
        return true
    }
    return false
}
```

**CRITICAL ADAPTATION**: The entire event subscription block depends on the bus API. Read the bus package carefully. The code above is a template — you must rewrite the subscription logic to match the actual bus.Subscribe/Unsubscribe API.

**Verify**: `go build ./internal/coordinator/`

## Step 2.3: Implement WaitForAll (Parallel Waiter)

**File**: `internal/coordinator/waiter.go`

```go
// WaitForAll waits for multiple tasks to complete. Returns results for all tasks.
// If any task fails, the others still complete (no early abort).
func (w *Waiter) WaitForAll(ctx context.Context, taskIDs []string, timeout time.Duration) (map[string]*TaskResult, error) {
    results := make(map[string]*TaskResult)
    var mu sync.Mutex
    var wg sync.WaitGroup
    errCh := make(chan error, len(taskIDs))

    for _, id := range taskIDs {
        wg.Add(1)
        go func(taskID string) {
            defer wg.Done()
            result, err := w.WaitForTask(ctx, taskID, timeout)
            if err != nil {
                errCh <- fmt.Errorf("task %s: %w", taskID, err)
                return
            }
            mu.Lock()
            results[taskID] = result
            mu.Unlock()
        }(id)
    }

    wg.Wait()
    close(errCh)

    // Collect errors
    var errs []error
    for err := range errCh {
        errs = append(errs, err)
    }
    if len(errs) > 0 {
        return results, fmt.Errorf("%d tasks failed: %v", len(errs), errs[0])
    }
    return results, nil
}
```

**Verify**: `go build ./internal/coordinator/`

## Step 2.4: Test the Waiter

**File**: `internal/coordinator/waiter_test.go`

```go
package coordinator

import (
    "context"
    "testing"
    "time"
)

func TestWaitForTask_AlreadyTerminal(t *testing.T) {
    // Create a real store with a task already in SUCCEEDED state
    // Create a real bus
    // Call WaitForTask — should return immediately without waiting
}

func TestWaitForTask_Timeout(t *testing.T) {
    // Create a store with a task in RUNNING state
    // Call WaitForTask with 100ms timeout
    // Should return timeout error
}

func TestWaitForAll_Parallel(t *testing.T) {
    // Create 3 tasks, all SUCCEEDED
    // Call WaitForAll — should return 3 results
}
```

**Adapt**: Use the same test setup patterns as `internal/persistence/store_test.go` for creating temp databases and stores.

**Verify**: `go test ./internal/coordinator/ -v -count=1`

## Step 2.5: Refactor await_delegation to Use Waiter

**File**: `internal/tools/delegate.go`

Replace the polling loop in `await_delegation` with the Waiter:

```bash
grep -B5 -A30 "await\|Await\|poll" internal/tools/delegate.go
```

The current implementation likely has a `for i := 0; i < 60; i++ { time.Sleep(1 * time.Second) }` loop. Replace it:

```go
func awaitDelegation(ctx context.Context, input map[string]interface{}) (string, error) {
    taskID, ok := input["task_id"].(string)
    if !ok {
        return "", fmt.Errorf("task_id required")
    }

    // Get waiter from context
    waiter := /* adapt: get from context or closure */

    result, err := waiter.WaitForTask(ctx, taskID, 60*time.Second)
    if err != nil {
        return "", err
    }

    b, _ := json.Marshal(result)
    return string(b), nil
}
```

**Note**: This is still blocking from the LLM's perspective — the tool call doesn't return until the task completes or times out. True async (fire-and-resume) requires engine-level changes deferred to v0.3. This is explicitly acknowledged and intentional.

**Verify**:
```bash
go build ./internal/tools/
go test ./internal/tools/ -count=1
```

## GATE 2

```bash
just check
go test -race ./...
go test ./internal/coordinator/ -v

# Verify waiter exists and compiles
grep -n "WaitForTask\|WaitForAll" internal/coordinator/waiter.go

# Verify await_delegation no longer polls
grep -c "time.Sleep" internal/tools/delegate.go
# Should be 0 (no more sleep-based polling)
```

If all pass: `git add -A && git commit -m "PDR v4 Phase 2: event-driven completion tracking"`

---

# PHASE 3: Working DAG Executor

**Goal**: The coordinator's DAG executor actually waits for task completion, pipes outputs between steps, and records execution in the database.
**Files modified**: `internal/coordinator/executor.go`, `internal/coordinator/plan.go`, `internal/persistence/store.go`
**Risk**: Medium-high. Core coordination logic. Depends on Phase 2 Waiter working correctly.

## Step 3.0: Pre-Flight — Verify Waiter Works

Before starting Phase 3, confirm that Phase 2's Waiter can track task completion:

```bash
go test ./internal/coordinator/ -v -run "WaitForTask"
```

If this fails, fix Phase 2 first.

## Step 3.1: Refactor Executor to Use Waiter

**File**: `internal/coordinator/executor.go`

First, read the current executor:
```bash
cat internal/coordinator/executor.go
```

The v3.0 executor has these problems:
1. `executeWave` calls `CreateChatTask` and immediately returns — never waits
2. `StepResult` is marked `Status: "PENDING"` — never updated
3. No output captured from completed tasks

Replace the executor to use the Waiter from Phase 2:

```go
// Executor runs DAG plans with real completion tracking.
type Executor struct {
    taskRouter ChatTaskRouter
    waiter     *coordinator.Waiter // NEW: from Phase 2
    store      *persistence.Store   // NEW: for recording plan execution
}

// New creates a DAG executor with completion tracking.
func New(router ChatTaskRouter, waiter *coordinator.Waiter, store *persistence.Store) *Executor {
    return &Executor{
        taskRouter: router,
        waiter:     waiter,
        store:      store,
    }
}
```

**Adapt**: The waiter lives in the existing `internal/coordinator/` package alongside plan.go and executor.go.

**CRITICAL**: Changing the `New` signature breaks all callers. Find them:
```bash
grep -rn "coordinator.New" --include='*.go' .
```
Update every call site. Test callers get nil for waiter and store if needed.

**Verify**: `go build ./...`

## Step 3.2: Implement Real executeWave

Replace `executeWave` with a version that waits for completion:

```go
func (e *Executor) executeWave(ctx context.Context, sessionID string, steps []PlanStep, result *ExecutionResult) error {
    // Create all tasks in this wave
    taskToStep := make(map[string]string) // taskID -> stepID
    var taskIDs []string

    for _, step := range steps {
        // Resolve prompt template (see Step 3.3)
        prompt := resolvePrompt(step.Prompt, result)

        taskID, err := e.taskRouter.CreateChatTask(ctx, step.AgentID, sessionID, prompt)
        if err != nil {
            result.StepResults[step.ID] = StepResult{
                Status: "FAILED",
                Error:  fmt.Sprintf("failed to create task: %v", err),
            }
            return fmt.Errorf("step %s: create task: %w", step.ID, err)
        }

        taskToStep[taskID] = step.ID
        taskIDs = append(taskIDs, taskID)

        // Record task mapping (in-progress status)
        result.StepResults[step.ID] = StepResult{
            TaskID: taskID,
            Status: "RUNNING",
        }
    }

    if e.waiter == nil {
        // No waiter (test mode) — return with PENDING status
        return nil
    }

    // Wait for ALL tasks in this wave to complete
    taskResults, err := e.waiter.WaitForAll(ctx, taskIDs, 5*time.Minute)

    // Update step results from task results
    for taskID, tr := range taskResults {
        stepID := taskToStep[taskID]
        result.StepResults[stepID] = StepResult{
            TaskID:     tr.TaskID,
            Status:     tr.Status,
            Output:     tr.Output,
            CostUSD:    tr.CostUSD,
            DurationMs: tr.DurationMs,
            Error:      tr.Error,
        }
    }

    if err != nil {
        return fmt.Errorf("wave execution: %w", err)
    }

    return nil
}
```

**Verify**: `go build ./internal/coordinator/`

## Step 3.3: Implement Prompt Template Substitution

**File**: `internal/coordinator/executor.go`

This enables `{research.output}` in step prompts to be replaced with actual output from earlier steps.

```go
// resolvePrompt replaces {step_id.output} references with actual results.
func resolvePrompt(template string, result *ExecutionResult) string {
    resolved := template
    for stepID, sr := range result.StepResults {
        placeholder := "{" + stepID + ".output}"
        resolved = strings.ReplaceAll(resolved, placeholder, sr.Output)
    }
    return resolved
}
```

Add `"strings"` to imports.

**Verify**: `go build ./internal/coordinator/`

## Step 3.4: Record Plan Execution in Database

**File**: `internal/persistence/store.go`

Add methods to track plan execution:

```go
// CreatePlanExecution records the start of a plan execution.
func (s *Store) CreatePlanExecution(ctx context.Context, id, planName, sessionID string, totalSteps int) error {
    _, err := s.db.ExecContext(ctx, `
        INSERT INTO plan_executions (id, plan_name, session_id, status, total_steps)
        VALUES (?, ?, ?, 'running', ?)`,
        id, planName, sessionID, totalSteps,
    )
    if err != nil {
        return fmt.Errorf("create plan execution: %w", err)
    }
    return nil
}

// CompletePlanExecution marks a plan execution as finished.
func (s *Store) CompletePlanExecution(ctx context.Context, id, status string, totalCostUSD float64) error {
    _, err := s.db.ExecContext(ctx, `
        UPDATE plan_executions
        SET status = ?, total_cost_usd = ?, completed_at = CURRENT_TIMESTAMP
        WHERE id = ?`,
        status, totalCostUSD, id,
    )
    if err != nil {
        return fmt.Errorf("complete plan execution: %w", err)
    }
    return nil
}

// GetPlanExecution retrieves a plan execution by ID.
func (s *Store) GetPlanExecution(ctx context.Context, id string) (*PlanExecution, error) {
    var pe PlanExecution
    err := s.db.QueryRowContext(ctx, `
        SELECT id, plan_name, session_id, status, total_steps, completed_steps,
               total_cost_usd, created_at, completed_at
        FROM plan_executions WHERE id = ?`, id,
    ).Scan(&pe.ID, &pe.PlanName, &pe.SessionID, &pe.Status, &pe.TotalSteps,
        &pe.CompletedSteps, &pe.TotalCostUSD, &pe.CreatedAt, &pe.CompletedAt)
    if err == sql.ErrNoRows {
        return nil, nil
    }
    if err != nil {
        return nil, fmt.Errorf("get plan execution: %w", err)
    }
    return &pe, nil
}
```

Add the PlanExecution struct:

```go
// PlanExecution represents a running or completed plan.
type PlanExecution struct {
    ID             string
    PlanName       string
    SessionID      string
    Status         string
    TotalSteps     int
    CompletedSteps int
    TotalCostUSD   float64
    CreatedAt      time.Time
    CompletedAt    *time.Time
}
```

**Verify**: `go build ./internal/persistence/`

## Step 3.5: Wire Plan Recording into Executor

**File**: `internal/coordinator/executor.go`

Update the Execute method to record plan execution:

```go
func (e *Executor) Execute(ctx context.Context, sessionID string, plan Plan) (*ExecutionResult, error) {
    if err := plan.Validate(); err != nil {
        return nil, fmt.Errorf("invalid plan: %w", err)
    }

    // Generate execution ID
    execID := uuid.New().String()

    // Record plan start
    if e.store != nil {
        _ = e.store.CreatePlanExecution(ctx, execID, plan.Name, sessionID, len(plan.Steps))
    }

    result := &ExecutionResult{
        ExecutionID: execID,
        StepResults: make(map[string]StepResult),
    }

    order, err := topoSort(plan.Steps)
    if err != nil {
        if e.store != nil {
            _ = e.store.CompletePlanExecution(ctx, execID, "failed", 0)
        }
        return nil, fmt.Errorf("invalid plan: %w", err)
    }

    for waveNum, wave := range order {
        if len(wave) == 0 {
            continue
        }
        if err := e.executeWave(ctx, sessionID, wave, result); err != nil {
            if e.store != nil {
                _ = e.store.CompletePlanExecution(ctx, execID, "failed", result.TotalCost())
            }
            return result, fmt.Errorf("wave %d failed: %w", waveNum, err)
        }
    }

    // Record completion
    if e.store != nil {
        _ = e.store.CompletePlanExecution(ctx, execID, "succeeded", result.TotalCost())
    }

    return result, nil
}
```

Add `ExecutionID` to `ExecutionResult` and a `TotalCost` helper:

```go
type ExecutionResult struct {
    ExecutionID string
    StepResults map[string]StepResult
}

func (r *ExecutionResult) TotalCost() float64 {
    var total float64
    for _, sr := range r.StepResults {
        total += sr.CostUSD
    }
    return total
}
```

Add `"github.com/google/uuid"` to imports.

**Verify**: `go build ./internal/coordinator/`

## Step 3.6: Implement Plan.Validate Properly

**File**: `internal/coordinator/plan.go`

The v3.0 Validate was a stub returning nil. Replace with real validation:

```go
func (p *Plan) Validate() error {
    if len(p.Steps) == 0 {
        return fmt.Errorf("plan has no steps")
    }

    // Check unique IDs
    seen := make(map[string]bool)
    for _, s := range p.Steps {
        if s.ID == "" {
            return fmt.Errorf("step has empty ID")
        }
        if seen[s.ID] {
            return fmt.Errorf("duplicate step ID: %s", s.ID)
        }
        seen[s.ID] = true
    }

    // Check all dependencies reference existing steps
    for _, s := range p.Steps {
        for _, dep := range s.DependsOn {
            if !seen[dep] {
                return fmt.Errorf("step %s depends on nonexistent step %s", s.ID, dep)
            }
        }
    }

    // Check for cycles via topological sort
    _, err := topoSort(p.Steps)
    return err
}
```

**Verify**:
```bash
go build ./internal/coordinator/
go test ./internal/coordinator/ -v -count=1
```

## Step 3.7: Update Executor Tests

**File**: `internal/coordinator/executor_test.go`

The v3.0 tests use a mockRouter. Update them to also work with nil waiter (test mode):

```bash
cat internal/coordinator/executor_test.go
```

Update `New` calls to pass nil waiter and nil store:
```go
exec := New(&mockRouter{}, nil, nil)
```

Add a new test for template substitution:

```go
func TestResolvePrompt(t *testing.T) {
    result := &ExecutionResult{
        StepResults: map[string]StepResult{
            "research": {Output: "The sun is a star."},
        },
    }
    got := resolvePrompt("Based on: {research.output}", result)
    want := "Based on: The sun is a star."
    if got != want {
        t.Fatalf("got %q, want %q", got, want)
    }
}

func TestResolvePrompt_NoMatch(t *testing.T) {
    result := &ExecutionResult{StepResults: make(map[string]StepResult)}
    got := resolvePrompt("No references here", result)
    if got != "No references here" {
        t.Fatalf("prompt should be unchanged, got %q", got)
    }
}

func TestValidate_EmptyPlan(t *testing.T) {
    p := Plan{Steps: nil}
    if err := p.Validate(); err == nil {
        t.Fatal("expected error for empty plan")
    }
}

func TestValidate_DuplicateID(t *testing.T) {
    p := Plan{Steps: []PlanStep{
        {ID: "a", AgentID: "x", Prompt: "1"},
        {ID: "a", AgentID: "y", Prompt: "2"},
    }}
    if err := p.Validate(); err == nil {
        t.Fatal("expected error for duplicate ID")
    }
}

func TestValidate_MissingDependency(t *testing.T) {
    p := Plan{Steps: []PlanStep{
        {ID: "a", AgentID: "x", Prompt: "1", DependsOn: []string{"nonexistent"}},
    }}
    if err := p.Validate(); err == nil {
        t.Fatal("expected error for missing dependency")
    }
}
```

**Verify**: `go test ./internal/coordinator/ -v -count=1`

## GATE 3

```bash
just check
go test -race ./...
go test ./internal/coordinator/ -v

# Verify real completion tracking
grep -n "WaitForAll\|waiter" internal/coordinator/executor.go

# Verify template substitution
grep -n "resolvePrompt" internal/coordinator/executor.go

# Verify plan recording
grep -n "CreatePlanExecution\|CompletePlanExecution" internal/coordinator/executor.go
grep -n "CreatePlanExecution\|CompletePlanExecution" internal/persistence/store.go
```

If all pass: `git add -A && git commit -m "PDR v4 Phase 3: working DAG executor"`

---

# PHASE 4: Plan System

**Goal**: Plans load from config.yaml, validate at startup, hot-reload, and can be triggered via TUI command and REST API.
**Files modified**: `internal/config/config.go`, `internal/coordinator/loader.go` (new), `internal/tui/commands.go`, `internal/gateway/gateway.go`, `cmd/goclaw/main.go`
**Risk**: Medium. Config schema change, TUI modification, gateway modification.

## Step 4.1: Add Plans to Config Schema

**File**: `internal/config/config.go`

First read the current Config struct:
```bash
grep -B2 -A40 "type Config struct" internal/config/config.go
```

Add plans field:

```go
type Config struct {
    // ... existing fields ...
    Plans []PlanConfig `yaml:"plans"` // NEW
}

// PlanConfig defines a named workflow in config.yaml.
type PlanConfig struct {
    Name  string           `yaml:"name"`
    Steps []PlanStepConfig `yaml:"steps"`
}

// PlanStepConfig defines a step within a plan.
type PlanStepConfig struct {
    ID        string   `yaml:"id"`
    AgentID   string   `yaml:"agent_id"`
    Prompt    string   `yaml:"prompt"`
    DependsOn []string `yaml:"depends_on"`
}
```

**Verify**:
```bash
go build ./internal/config/
go test ./internal/config/ -count=1
```

## Step 4.2: Create Plan Loader

**File**: `internal/coordinator/loader.go` (new)

```go
package coordinator

import (
    "fmt"

    "github.com/basket/go-claw/internal/config"
)

// LoadPlansFromConfig converts config plan definitions into validated Plan objects.
func LoadPlansFromConfig(configs []config.PlanConfig, knownAgents []string) (map[string]Plan, error) {
    plans := make(map[string]Plan)
    agentSet := make(map[string]bool)
    for _, a := range knownAgents {
        agentSet[a] = true
    }

    for _, pc := range configs {
        if pc.Name == "" {
            return nil, fmt.Errorf("plan has empty name")
        }
        if _, exists := plans[pc.Name]; exists {
            return nil, fmt.Errorf("duplicate plan name: %s", pc.Name)
        }

        plan := Plan{
            Name:  pc.Name,
            Steps: make([]PlanStep, len(pc.Steps)),
        }

        for i, sc := range pc.Steps {
            // Validate agent exists
            if !agentSet[sc.AgentID] {
                return nil, fmt.Errorf("plan %s step %s: unknown agent %s", pc.Name, sc.ID, sc.AgentID)
            }

            plan.Steps[i] = PlanStep{
                ID:        sc.ID,
                AgentID:   sc.AgentID,
                Prompt:    sc.Prompt,
                DependsOn: sc.DependsOn,
            }
        }

        if err := plan.Validate(); err != nil {
            return nil, fmt.Errorf("plan %s: %w", pc.Name, err)
        }

        plans[pc.Name] = plan
    }

    return plans, nil
}
```

**Verify**: `go build ./internal/coordinator/`

## Step 4.3: Test Plan Loader

**File**: `internal/coordinator/loader_test.go`

```go
package coordinator

import (
    "testing"

    "github.com/basket/go-claw/internal/config"
)

func TestLoadPlansFromConfig_Valid(t *testing.T) {
    configs := []config.PlanConfig{
        {
            Name: "test-pipeline",
            Steps: []config.PlanStepConfig{
                {ID: "research", AgentID: "researcher", Prompt: "do research"},
                {ID: "write", AgentID: "writer", Prompt: "{research.output}", DependsOn: []string{"research"}},
            },
        },
    }
    plans, err := LoadPlansFromConfig(configs, []string{"researcher", "writer"})
    if err != nil {
        t.Fatal(err)
    }
    if len(plans) != 1 {
        t.Fatalf("expected 1 plan, got %d", len(plans))
    }
    if _, ok := plans["test-pipeline"]; !ok {
        t.Fatal("plan test-pipeline not found")
    }
}

func TestLoadPlansFromConfig_UnknownAgent(t *testing.T) {
    configs := []config.PlanConfig{
        {
            Name: "bad-plan",
            Steps: []config.PlanStepConfig{
                {ID: "step1", AgentID: "nonexistent", Prompt: "hello"},
            },
        },
    }
    _, err := LoadPlansFromConfig(configs, []string{"researcher"})
    if err == nil {
        t.Fatal("expected error for unknown agent")
    }
}

func TestLoadPlansFromConfig_DuplicateName(t *testing.T) {
    configs := []config.PlanConfig{
        {Name: "dup", Steps: []config.PlanStepConfig{{ID: "1", AgentID: "a", Prompt: "x"}}},
        {Name: "dup", Steps: []config.PlanStepConfig{{ID: "1", AgentID: "a", Prompt: "y"}}},
    }
    _, err := LoadPlansFromConfig(configs, []string{"a"})
    if err == nil {
        t.Fatal("expected error for duplicate plan name")
    }
}
```

**Verify**: `go test ./internal/coordinator/ -v -run "LoadPlans" -count=1`

## Step 4.4: Wire Plan Loading into Main

**File**: `cmd/goclaw/main.go`

Find where config is loaded and agents are initialized:
```bash
grep -n "config.Load\|reconcileAgents\|cfg\." cmd/goclaw/main.go | head -20
```

After agents are initialized, load plans:

```go
// Load plans from config (after agents are initialized)
agentIDs := registry.ListAgentIDs() // or however you get the list
plans, err := coordinator.LoadPlansFromConfig(cfg.Plans, agentIDs)
if err != nil {
    logger.Warn("failed to load plans from config", "error", err)
    // Don't fatal — plans are optional
} else if len(plans) > 0 {
    logger.Info("loaded plans from config", "count", len(plans))
}
```

**Adapt**: Find the exact variable names for config, registry, and logger from main.go.

Store the plans somewhere accessible (package-level var, passed to gateway, or stored in a coordinator manager). The simplest approach:

```go
// Near the top of main or as a field in an app struct
var loadedPlans map[string]coordinator.Plan
```

**For hot-reload**: Find where config changes trigger agent reconciliation:
```bash
grep -n "fsnotify\|OnChange\|reconcile" cmd/goclaw/main.go | head -10
```

Add plan reloading in the same callback:

```go
// In the config change callback, after agent reconciliation:
newPlans, planErr := coordinator.LoadPlansFromConfig(newCfg.Plans, registry.ListAgentIDs())
if planErr != nil {
    logger.Warn("plan reload failed, keeping previous plans", "error", planErr)
} else {
    loadedPlans = newPlans
    logger.Info("plans reloaded", "count", len(newPlans))
}
```

**Verify**: `go build ./...`

## Step 4.5: Add /plan TUI Command

**File**: Find where TUI commands are handled:
```bash
grep -rn "handleCommand\|/agents\|/model\|/help" internal/tui/*.go | head -20
```

Read the command handling pattern, then add:

```go
// In the command handler switch/if chain:
case strings.HasPrefix(input, "/plan"):
    args := strings.TrimPrefix(input, "/plan")
    args = strings.TrimSpace(args)

    if args == "" {
        // List available plans
        var names []string
        for name := range loadedPlans {
            names = append(names, name)
        }
        if len(names) == 0 {
            return "No plans configured. Add plans: section to config.yaml"
        }
        return "Available plans: " + strings.Join(names, ", ")
    }

    // Parse: /plan <name> <input>
    parts := strings.SplitN(args, " ", 2)
    planName := parts[0]
    planInput := ""
    if len(parts) > 1 {
        planInput = parts[1]
    }

    plan, ok := loadedPlans[planName]
    if !ok {
        return fmt.Sprintf("Unknown plan: %s", planName)
    }

    // Replace {user_input} in all step prompts
    for i := range plan.Steps {
        plan.Steps[i].Prompt = strings.ReplaceAll(plan.Steps[i].Prompt, "{user_input}", planInput)
    }

    // Execute in background goroutine
    go func() {
        result, err := executor.Execute(ctx, sessionID, plan)
        if err != nil {
            // Send error to TUI via bus or callback
        }
        // Send result to TUI
    }()

    return fmt.Sprintf("Plan '%s' started with %d steps", planName, len(plan.Steps))
```

**CRITICAL ADAPT**: The TUI command handling pattern varies enormously. You MUST read the existing code to understand:
- How commands are dispatched (switch statement, map lookup, if chain?)
- How the TUI model accesses external state (executor, plans)
- How background operations report back to the TUI (channels, bus events, callbacks?)
- What the return type is (string, tea.Msg, error?)

The code above is conceptual. Rewrite it entirely to match the TUI's actual architecture.

**Verify**: `go build ./...`

## Step 4.6: Add REST API Endpoint

**File**: `internal/gateway/gateway.go`

Add endpoint to trigger a plan:

```go
func (g *Gateway) handleRunPlan(w http.ResponseWriter, r *http.Request) {
    // Extract plan name from URL path
    // ADAPT: Use the URL routing pattern already in use
    planName := /* extract from r.URL.Path or r.PathValue("name") */

    plan, ok := g.plans[planName]
    if !ok {
        http.Error(w, "plan not found: "+planName, http.StatusNotFound)
        return
    }

    // Parse request body for user input
    var req struct {
        Input     string `json:"input"`
        SessionID string `json:"session_id"`
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "invalid request body", http.StatusBadRequest)
        return
    }

    if req.SessionID == "" {
        req.SessionID = uuid.New().String()
    }

    // Replace {user_input} in prompts
    execPlan := plan // copy
    execPlan.Steps = make([]coordinator.PlanStep, len(plan.Steps))
    copy(execPlan.Steps, plan.Steps)
    for i := range execPlan.Steps {
        execPlan.Steps[i].Prompt = strings.ReplaceAll(execPlan.Steps[i].Prompt, "{user_input}", req.Input)
    }

    // Execute asynchronously
    go func() {
        _, _ = g.executor.Execute(context.Background(), req.SessionID, execPlan)
    }()

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]string{
        "status":     "started",
        "plan":       planName,
        "session_id": req.SessionID,
    })
}
```

Register the route:
```go
mux.HandleFunc("POST /api/plans/{name}/run", g.handleRunPlan)
```

**Adapt**: The Gateway struct needs access to plans and executor. Add fields and update constructor. This will break test callers — fix them all (same pattern as v3.0 Phase 2 Step 2.4).

**Verify**: `go build ./...`

## Step 4.7: Add GET /api/plans Endpoint

```go
func (g *Gateway) handleListPlans(w http.ResponseWriter, r *http.Request) {
    type planSummary struct {
        Name      string `json:"name"`
        StepCount int    `json:"step_count"`
    }

    var summaries []planSummary
    for name, plan := range g.plans {
        summaries = append(summaries, planSummary{
            Name:      name,
            StepCount: len(plan.Steps),
        })
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(summaries)
}
```

Register: `mux.HandleFunc("GET /api/plans", g.handleListPlans)`

**Verify**: `go build ./...`

## GATE 4

```bash
just check
go test -race ./...
go test ./internal/coordinator/ -v

# Config loads plans
grep -n "PlanConfig\|Plans" internal/config/config.go

# Loader exists and validates
grep -n "LoadPlansFromConfig" internal/coordinator/loader.go

# TUI command exists
grep -rn "/plan" internal/tui/*.go

# API endpoints exist
grep -n "plans" internal/gateway/gateway.go

# Functional: config with plans loads
cat > /tmp/test-plans.yaml <<'EOF'
agents:
  - id: researcher
    display_name: Researcher
    capabilities: [search]
  - id: writer
    display_name: Writer
    capabilities: [writing]
plans:
  - name: content-pipeline
    steps:
      - id: research
        agent_id: researcher
        prompt: "Research: {user_input}"
      - id: write
        agent_id: writer
        prompt: "Write based on: {research.output}"
        depends_on: [research]
EOF
echo "Config with plans parses without error"
```

If all pass: `git add -A && git commit -m "PDR v4 Phase 4: plan system"`

---

# PHASE 5: TUI Visibility

**Goal**: The user can see delegation status and plan progress in the TUI. Without this, Patterns 2-3 are invisible and the app appears frozen during multi-agent work.
**Files modified**: `internal/tui/*.go`
**Risk**: Medium. TUI code is complex. Read carefully before editing.

## Step 5.0: Understand TUI Architecture

**CRITICAL**: Read the TUI code thoroughly before making any changes.

```bash
# Main TUI model
grep -B2 -A30 "type Model struct\|type model struct" internal/tui/*.go

# Update function (Bubbletea)
grep -B2 -A20 "func.*Update.*tea.Msg\|func.*Update.*Msg" internal/tui/*.go | head -40

# View function
grep -B2 -A10 "func.*View()" internal/tui/*.go | head -20

# How messages are received (channels, subscriptions)
grep -n "tea.Cmd\|tea.Batch\|Tick\|Sub" internal/tui/*.go | head -20

# How the TUI currently handles long-running operations
grep -n "spinner\|loading\|waiting\|progress" internal/tui/*.go | head -20
```

Record:
- What is the TUI model struct? What fields does it have?
- How does Update handle messages?
- Is there already a status bar or notification area?
- How are background operations reported to the TUI?

## Step 5.1: Add Delegation Status to TUI

The TUI needs to show when a delegation is active. Minimum viable UX:

When the current agent delegates to another agent, the TUI displays:
```
⏳ Delegated to security-agent — waiting (12s)
```

This requires:
1. Subscribe to bus delegation events in the TUI
2. Track active delegations in the TUI model
3. Render delegation status in the view

**File**: The main TUI model file (identify from Step 5.0)

Add to the model struct:

```go
type model struct {
    // ... existing fields ...
    activeDelegations map[string]delegationStatus // NEW: taskID -> status
}

type delegationStatus struct {
    TargetAgent string
    StartedAt   time.Time
}
```

Subscribe to delegation events. How you do this depends on the TUI architecture:

**If the TUI uses tea.Cmd for async operations** (most Bubbletea apps):

```go
// Add a new Msg type
type delegationStartedMsg struct {
    TaskID      string
    TargetAgent string
}

type delegationCompletedMsg struct {
    TaskID string
    Status string
}

// In Update, handle these messages
case delegationStartedMsg:
    m.activeDelegations[msg.TaskID] = delegationStatus{
        TargetAgent: msg.TargetAgent,
        StartedAt:   time.Now(),
    }
case delegationCompletedMsg:
    delete(m.activeDelegations, msg.TaskID)
```

**In View**, render active delegations in a status area:

```go
// In the View function, add before or after the main chat area:
if len(m.activeDelegations) > 0 {
    for _, d := range m.activeDelegations {
        elapsed := time.Since(d.StartedAt).Truncate(time.Second)
        // Render: "⏳ Delegated to security-agent — waiting (12s)"
        statusLine := fmt.Sprintf("⏳ Delegated to %s — waiting (%s)", d.TargetAgent, elapsed)
        // Append to view output
    }
}
```

**CRITICAL ADAPT**: Bubbletea's Update/View cycle is specific to each app's architecture. The code above is conceptual. You must understand the existing model, message flow, and rendering before adding to it. Read at least 3 existing handlers in Update before writing new ones.

**Verify**: `go build ./...`

## Step 5.2: Add Plan Progress to TUI

When a plan is running, show progress:

```
Plan: content-pipeline
  ✅ research    (Researcher)  3.2s  $0.003
  🔄 write       (Writer)      running...
  ⏳ review      (Editor)      waiting for: write
```

Add to the model:

```go
type activePlan struct {
    Name    string
    Steps   []planStepStatus
}

type planStepStatus struct {
    ID        string
    AgentID   string
    Status    string // waiting, running, succeeded, failed
    Duration  time.Duration
    CostUSD   float64
}
```

Subscribe to plan execution events via the bus. The executor should publish events when steps start and complete. If it doesn't, add event publishing to the executor:

**File**: `internal/coordinator/executor.go`

In `executeWave`, before creating tasks:
```go
if e.bus != nil {
    e.bus.Publish("plan.step.started", map[string]string{
        "plan_name": plan.Name,
        "step_id":   step.ID,
        "agent_id":  step.AgentID,
    })
}
```

After getting results:
```go
if e.bus != nil {
    e.bus.Publish("plan.step.completed", map[string]string{
        "plan_name": plan.Name,
        "step_id":   stepID,
        "status":    sr.Status,
    })
}
```

**Adapt**: The executor needs bus access. Add it to the Executor struct and New function. Update all callers.

**Verify**: `go build ./...`

## Step 5.3: Add /plan Help to TUI

In the `/help` command handler, add plan-related commands:

```
/plan                  List available plans
/plan <name> <input>   Run a named plan
```

Find where help text is defined:
```bash
grep -B5 -A20 "/help\|helpText\|commandHelp" internal/tui/*.go | head -40
```

Add the plan commands to the help output.

**Verify**: `go build ./...`

## Step 5.4: Publish Delegation Events from Tools

**File**: `internal/tools/delegate.go`

When `delegate_task_async` creates a task, publish a bus event so the TUI can track it:

```go
// After CreateChatTask succeeds:
if eventBus != nil {
    eventBus.Publish("delegation.started", map[string]string{
        "task_id":      taskID,
        "target_agent": targetAgent,
        "source_agent": shared.AgentID(ctx),
    })
}
```

When `await_delegation` gets a result:
```go
// After WaitForTask returns:
if eventBus != nil {
    eventBus.Publish("delegation.completed", map[string]string{
        "task_id": taskID,
        "status":  result.Status,
    })
}
```

**Adapt**: The bus may need to be passed to delegation tools via context or closure. Check how other tools access shared resources.

**Verify**: `go build ./...`

## GATE 5

```bash
just check
go test -race ./...

# TUI compiles with new features
go build ./internal/tui/

# Delegation events published
grep -n "delegation.started\|delegation.completed" internal/tools/delegate.go

# Plan events published
grep -n "plan.step.started\|plan.step.completed" internal/coordinator/executor.go

# TUI handles delegation status
grep -n "activeDelegation\|delegationStatus" internal/tui/*.go

# TUI handles plan progress
grep -n "activePlan\|planStepStatus" internal/tui/*.go

# /plan in help
grep -n "/plan" internal/tui/*.go
```

If all pass: `git add -A && git commit -m "PDR v4 Phase 5: TUI visibility"`

---

# Post-Implementation Checklist

After all 5 phases, run full verification:

```bash
# All tests pass
just check
go test -race ./...

# Functional smoke test
rm -f ~/.goclaw/goclaw.db

# Create config with plans
cat > ~/.goclaw/config.yaml <<'EOF'
# ... existing config ...
plans:
  - name: test-pipeline
    steps:
      - id: step1
        agent_id: default
        prompt: "Say hello"
EOF

just build && just run-headless &
sleep 3

# Plans API works
curl -s http://127.0.0.1:18789/api/plans | python3 -m json.tool
# Must return JSON array with test-pipeline

# Analytics still works (from v3.0)
curl -s http://127.0.0.1:18789/api/analytics/metrics | python3 -m json.tool

# Context store works
sqlite3 ~/.goclaw/goclaw.db "INSERT INTO task_context VALUES ('root1', 'key1', 'val1', datetime('now'));"
sqlite3 ~/.goclaw/goclaw.db "SELECT * FROM task_context;"
# Must return the inserted row

kill %1
```

---

# What This PDR Does NOT Cover (Deferred)

These are explicitly out of scope. Do not implement them:

- **True async delegation** (fire-and-resume without blocking the LLM). Requires engine-level changes to support mid-conversation context injection. Deferred to v0.3.
- **MCP client support**. Already partially exists. Expansion is a separate PDR.
- **A2A protocol support**. Cross-framework agent communication. Separate PDR.
- **LLM-generated plans** (Pattern 4). The runtime now supports it — an LLM with a `create_plan` tool could generate Plan JSON and feed it to the executor. But the planning prompt and tool definition are a separate effort.
- **Web UI**. The TUI is the primary interface. A web UI is a separate project.
- **Distributed execution**. Single-machine only for now.
- **Agent marketplace / template library**. Community feature, not infrastructure.

---

# Rollback Reference

| Phase | Rollback command |
|-------|-----------------|
| Any   | `git reset --hard HEAD~1` |
| All   | `git reset --hard <pre-v4-commit>` |
| DB    | `rm ~/.goclaw/goclaw.db` then restart |
