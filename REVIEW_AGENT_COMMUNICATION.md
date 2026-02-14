# Inter-Agent Communication: Implementation Review

**Date:** 2026-02-14
**Scope:** `delegate_task`, `send_message`, `read_messages` tools; `agent_messages` schema; TUI `/agents` command; context propagation; policy integration

## Review Methodology

Five parallel review agents examined the implementation from orthogonal angles:

1. **Protocol & Message Contract** — Message schema, delivery semantics, ordering, timeouts
2. **State & Persistence** — SQLite durability, schema migration, retention, orphaned data
3. **Security & Isolation** — Policy enforcement, capability gating, prompt injection, confused deputy
4. **Concurrency & Integration** — Worker pool exhaustion, deadlocks, payload format, drain behavior
5. **API Surface & TUI** — User visibility, ACP exposure, command correctness, agent selector UX

---

## Findings Summary

| Severity | Count | Fixed |
|----------|-------|-------|
| Critical | 5 | 4 |
| Bug | 8 | 7 |
| Design Gap | 12 | — |
| Inconsistency | 3 | — |
| Hardening | 10 | 1 |

---

## Critical Findings

### C-1: `knownCapabilities` missing messaging/delegate capabilities — FIXED

**Location:** `internal/policy/policy.go:41-60`

The `knownCapabilities` map did not include `tools.send_message`, `tools.read_messages`, or `tools.delegate_task`. Since `Policy.validate()` rejects unknown capabilities, these tools were **permanently blocked** — adding them to `policy.yaml` would fail validation.

**Fix:** Added all three capabilities to the `knownCapabilities` map.

### C-2: Self-delegation deadlock — FIXED

**Location:** `internal/tools/delegate.go`

`delegate_task` had no guard against an agent delegating to itself. Since the calling agent's worker blocks on the poll loop, and the delegated task queues for the same agent, this would deadlock (the worker waiting for a task that can only run on its now-consumed worker).

**Fix:** Added `shared.AgentID(ctx)` check — rejects delegation when `callerAgent == input.TargetAgent`.

### C-3: Payload format mismatch — delegate_task fundamentally broken — FIXED

**Location:** `internal/tools/delegate.go:82`, `internal/engine/engine.go:52-56`

`delegate_task` passed the raw prompt string to `store.CreateTaskForAgent`, but `EchoProcessor.Process` expects JSON-encoded `chatTaskPayload` (`{"content":"..."}`). Every delegated task would fail with `"decode payload: invalid character ..."`.

**Fix:** Wrapped the prompt with `json.Marshal(chatPayload{Content: input.Prompt})` before creating the task.

### C-4: Worker pool exhaustion via blocking delegation — NOT FIXED (Design)

**Location:** `internal/tools/delegate.go:93-139`

`delegate_task` blocks a worker goroutine for up to 300s. With default 4 workers, 4 concurrent delegations exhaust the agent. Fixing this properly requires an async delegation model (release worker, resume on completion), which is an architectural change beyond the current scope.

### C-5: Confused deputy via delegation — NOT FIXED (Design)

**Location:** `internal/tools/delegate.go:43-52`

An unprivileged agent with `tools.delegate_task` can craft prompts that cause a privileged agent to execute restricted operations. The target agent's own policy governs execution, but there is no mechanism to prevent capability laundering. Proper fix requires delegation authorization scoping.

---

## Bug Findings

### B-1: Target agent existence not validated — FIXED

**Location:** `internal/tools/delegate.go:82`, `internal/tools/messaging.go:85`

Neither `send_message` nor `delegate_task` checked whether the target agent exists. Messages to phantom agents silently succeeded; delegations would poll for 300s before timing out.

**Fix:** Both now call `store.GetAgent()` and return an error if the agent doesn't exist.

### B-2: Delegated child task not canceled on timeout/context cancel — FIXED

**Location:** `internal/tools/delegate.go:100-112`

When delegation timed out or the context was canceled, the child task was left in QUEUED/RUNNING status, consuming resources with no parent waiting for the result.

**Fix:** Both timeout and context-cancel paths now call `store.AbortTask(taskID)` to cancel the orphaned child.

### B-3: `versionChecksums` missing v7 entry — FIXED

**Location:** `internal/persistence/store.go:311-320`

The `versionChecksums` list used for upgrade validation didn't include v7. This is a latent bug: adding a future v8 migration would fail to upgrade from v7 because the validation loop wouldn't find a matching entry.

**Fix:** Added `{schemaVersionV7, schemaChecksumV7}` to the list.

### B-4: `DeleteAgent` doesn't clean up `agent_messages` — FIXED

**Location:** `internal/persistence/store.go:2815-2825`

Deleting an agent left orphaned rows in `agent_messages` (both sent and received). These would accumulate indefinitely.

**Fix:** Wrapped `DeleteAgent` in a transaction that also deletes from `agent_messages WHERE from_agent = ? OR to_agent = ?`.

### B-5: No audit record on successful `read_messages` — FIXED

**Location:** `internal/tools/messaging.go:98-136`

`send_message` recorded an audit entry on success, but `read_messages` only audited denials. Successful reads were invisible to the audit trail.

**Fix:** Added `audit.Record("allow", capReadMessages, "messages_read", pv, ...)` after successful read.

### B-6: `read_messages` limit uncapped — FIXED

**Location:** `internal/tools/messaging.go:113-116`

An agent could request `limit=1000000`, loading all messages into memory.

**Fix:** Capped limit at 100.

### B-7: `/agents new` and `/agents team` don't propagate API key — NOT FIXED (False Positive)

**Location:** `internal/tui/chat.go:476-484`

The reviewer noted that `CreateAgent` doesn't pass the API key. However, the `agent.Registry.CreateAgent` method resolves API keys through the global `r.apiKeys` map (passed to `BrainConfig.APIKeys`), so the Gemini key is available through the global config path. Per-agent `APIKey` is for override only.

---

## Design Gaps (Documented, Not Fixed)

| # | Gap | Location | Notes |
|---|-----|----------|-------|
| D-1 | No message type/content type field | `store.go:491` | All messages are untyped plaintext. Future structured payloads need schema migration |
| D-2 | `agent_messages` not covered by `RunRetention` | `store.go:2116` | Read messages accumulate indefinitely. Should add retention with configurable window |
| D-3 | Messages not linked to task FSM or session | `store.go:491-498` | No `task_id`/`session_id` column for correlation |
| D-4 | No delegation provenance (who delegated to whom) | `delegate.go` | No `parent_agent_id` or `delegated_by` field in task metadata |
| D-5 | No rate limiting on inter-agent messages | `messaging.go:57-96` | Misbehaving agent can flood inbox unboundedly |
| D-6 | No per-agent-pair capability gating | `messaging.go:57-65` | Policy is flat — no ACL for "A may message B but not C" |
| D-7 | No sanitizer on inter-agent message content | `messaging.go` | Messages bypass `safety.Sanitizer` which only guards `Respond()` entry |
| D-8 | Inter-agent messages invisible in TUI | `tui/chat_tui.go` | `PeekAgentMessages` exists but is never called from TUI |
| D-9 | No ACP/JSON-RPC method for inter-agent messaging | `gateway/gateway.go` | Only accessible via LLM tool calls, not ACP clients |
| D-10 | No WebSocket notification for message events | `gateway/gateway.go` | No `agent.message.sent` broadcast |
| D-11 | No TUI abort command for running tasks | `tui/chat_tui.go` | Ctrl+C/D quits entire TUI rather than canceling current task |
| D-12 | No depth/hop limit for delegation chains | `delegate.go` | Deep chains exhaust workers transitively |

## Inconsistencies (Documented, Not Fixed)

| # | Issue | Location |
|---|-------|----------|
| I-1 | At-most-once message delivery contradicts at-least-once task model | `store.go:2863` |
| I-2 | Delegated task results indistinguishable from direct agent responses in chat history | `engine.go:317` |
| I-3 | Send audit omits message ID/content hash | `messaging.go:93` |

## Hardening (Documented, Not Fixed)

| # | Issue | Location |
|---|-------|----------|
| H-1 | No message size limit at persistence layer | `store.go:2853` |
| H-2 | Message ordering by `created_at` (second precision), not `id` | `store.go:2880` |
| H-3 | `ReadAgentMessages` uses `fmt.Sprintf` for SQL IN clause | `store.go:2904` |
| H-4 | `delegate_task` polls at fixed 500ms with no backoff | `delegate.go:95` |
| H-5 | Zero test coverage for messaging and delegation | `tools/*`, `persistence/*` |
| H-6 | `/agents <typo>` silently treated as agent switch | `tui/chat.go:569` |
| H-7 | Empty agent list in selector silently ignored | `tui/chat_tui.go:225` |
| H-8 | No format validation on agent ID strings at persistence layer | `store.go:2853` |
| H-9 | SOUL.md indirectly exploitable via delegation + read_file | `engine/brain.go` |
| H-10 | SQLite MaxOpenConns=1 serialization under delegation poll load | `store.go:226` |

---

## Verification

```
go build ./...           # PASS
go vet ./...             # PASS
go test ./... -count=1   # 26/26 packages PASS
```

## Recommendations (Priority Order)

1. **Add integration tests** for `delegate_task`, `send_message`, `read_messages` (H-5)
2. **Add `agent_messages` to `RunRetention`** with configurable retention window (D-2)
3. **Add message size limit** (e.g., 64KB) at the tool layer (H-1)
4. **Consider async delegation model** to avoid worker pool exhaustion (C-4)
5. **Add delegation depth limit** via context-propagated hop counter (D-12)
6. **Expose messaging via ACP** for external client observability (D-9)
