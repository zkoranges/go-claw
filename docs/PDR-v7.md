# PDR-v7: Tools & Reach (v0.4)

**Version**: v0.4
**Theme**: Agents that touch real systems and are reachable everywhere
**Predecessor**: PDR-v6 (v0.3 — Context and Memory)
**Duration**: 3-4 weeks
**Status**: Planning

---

## Table of Contents

1. [Overview](#1-overview)
2. [Existing Code Inventory](#2-existing-code-inventory)
3. [Feature 1: MCP Client — Full Implementation](#3-feature-1-mcp-client--full-implementation)
4. [Feature 2: True Async Delegation](#4-feature-2-true-async-delegation)
5. [Feature 3: Telegram Deep Integration](#5-feature-3-telegram-deep-integration)
6. [Feature 4: A2A Protocol (Experimental)](#6-feature-4-a2a-protocol-experimental)
7. [Implementation Order](#7-implementation-order)
8. [Verification Protocol](#8-verification-protocol)
9. [Self-Verification Script](#9-self-verification-script)
10. [CLAUDE.md Additions](#10-claudemd-additions)
11. [Risk Register](#11-risk-register)
12. [Definition of Done](#12-definition-of-done)

---

## 1. Overview

v0.4 transforms GoClaw from a local-only tool into one that touches real external systems. The
existing minimal MCP client gets a full rewrite with per-agent scoping, auto-discovery,
reconnection, and policy governance. Delegation becomes truly async. Telegram gains plan
progress, HITL approval gates, and the `/plan` command. A read-only A2A agent card provides
experimental interop.

### Success Criteria

All of the following MUST be true at completion:

- [ ] MCP servers configurable per-agent in `config.yaml`; tools auto-discovered on connect
- [ ] Policy engine governs MCP tool access per-agent with default-deny
- [ ] MCP connections reconnect on failure with exponential backoff
- [ ] MCP config hot-reloads via existing fsnotify watcher
- [ ] `delegate_task_async` tool returns immediately; result injected on next agent turn
- [ ] Delegation state persisted in SQLite; survives crash/restart
- [ ] Telegram pushes plan progress updates and supports HITL inline keyboards
- [ ] Telegram `/plan` command triggers named plan execution
- [ ] `GET /.well-known/agent.json` returns valid A2A agent card
- [ ] All new code has unit tests
- [ ] `just check` passes (build + vet + test)
- [ ] Zero data races under `go test -race ./...`
- [ ] All pre-existing tests continue to pass

---

## 2. Existing Code Inventory

**CRITICAL**: Read this section first. The PDR builds ON TOP of existing code. Do not
replace working subsystems — extend them.

### 2.1 MCP Client (Minimal — Needs Full Rewrite)

**Files**: `internal/mcp/client.go`, `manager.go`, `transport.go`, `client_test.go`

**Current state** (from `main.go`):

```go
// Config comes from cfg.MCP.Servers — a GLOBAL list, not per-agent
var mcpConfigs []mcp.ServerConfig
for _, s := range cfg.MCP.Servers {
    mcpConfigs = append(mcpConfigs, mcp.ServerConfig{
        Name: s.Name, Command: s.Command, Args: s.Args,
        Env: s.Env, Enabled: s.Enabled,
    })
}
mcpManager := mcp.NewManager(mcpConfigs, pol, logger)
mcpManager.Start(ctx)

// Tools registered on ALL agents identically — no per-agent scoping
for _, ra := range registry.ListRunningAgents() {
    if ra.Brain != nil && ra.Brain.Genkit() != nil {
        mcpTools := tools.RegisterMCPTools(ra.Brain.Genkit(), ra.Brain.Registry(), mcpManager)
        ...
    }
}
```

**What's missing** (the v0.4 work):

| Gap | Description |
|-----|-------------|
| Per-agent server config | All agents get same MCP servers; need per-agent `mcp_servers` in config |
| Auto-discovery | No `tools/list` call; tools hardcoded or manually registered |
| Reconnection | No retry on server crash; connection dies silently |
| Hot-reload | Config change doesn't affect running MCP connections |
| Per-agent policy | `pol.AllowCapability(cap)` is a flat check; no agent×server×tool granularity |
| SSE transport config | Only stdio transport in current config schema (`Command`, `Args`) |

### 2.2 Config System

**File**: `internal/config/config.go` (NOT `models.go` — that file only has `AvailableModels()`)

The config struct already has:

```go
// Existing — in cfg.MCP.Servers
type MCPServerEntry struct {
    Name    string   `yaml:"name"`
    Command string   `yaml:"command"`
    Args    []string `yaml:"args"`
    Env     []string `yaml:"env"`     // Note: currently []string, not map
    Enabled bool     `yaml:"enabled"`
}
```

And the MCP section in config:

```go
type MCPConfig struct {
    Servers []MCPServerEntry `yaml:"servers"`
}
```

Agent config already exists:

```go
type AgentConfigEntry struct {
    AgentID            string   `yaml:"agent_id"`
    DisplayName        string   `yaml:"display_name"`
    Provider           string   `yaml:"provider"`
    Model              string   `yaml:"model"`
    APIKeyEnv          string   `yaml:"api_key_env"`
    Soul               string   `yaml:"soul"`
    SoulFile           string   `yaml:"soul_file"`
    WorkerCount        int      `yaml:"worker_count"`
    TaskTimeoutSeconds int      `yaml:"task_timeout_seconds"`
    MaxQueueDepth      int      `yaml:"max_queue_depth"`
    SkillsFilter       []string `yaml:"skills_filter"`
    PreferredSearch    string   `yaml:"preferred_search"`
}
```

### 2.3 Policy Engine

**File**: `internal/policy/policy.go`

Current API surface:

```go
pol.AllowCapability(cap string) bool    // flat string check: "shell.exec", "web.search", etc.
pol.AllowDomain(domain string) bool     // domain allowlist for HTTP
```

This is a **flat model** — no agent-scoping. v0.4 needs to extend this without breaking
existing callers.

### 2.4 Telegram Channel

**File**: `internal/channels/telegram.go`, `channel.go`, `channel_test.go`

Already receives `eventBus` in constructor:

```go
tg := channels.NewTelegramChannel(token, allowedIDs, registry, store, logger, eventBus)
```

Handles basic message routing with `@agent` prefix parsing. No plan awareness,
no HITL, no progress updates.

### 2.5 Coordinator (Plans/DAG)

**Files**: `internal/coordinator/executor.go`, `plan.go`, `loader.go`, `retry.go`, `waiter.go`

Already loaded in `main.go`:

```go
plans, err := coordinator.LoadPlansFromConfig(cfg.Plans, agentIDs)
```

Executor runs DAG steps with dependency resolution. Has retry support (`retry.go`)
and waiters (`waiter.go`). **Does NOT** have HITL approval gates — that's new in v0.4.

### 2.6 Delegation (Sync Only)

**File**: `internal/tools/delegate.go`, `delegate_test.go`

Current delegation blocks the calling agent until the delegatee finishes. No persistence
of delegation state. No result injection.

### 2.7 Event Bus

**File**: `internal/bus/bus.go`, `bus_test.go`

In-process pub/sub. Already used by persistence store and passed to Telegram.
Event constants are defined where they're published (not centralized).

### 2.8 Schema Version

**CLAUDE.md states**: `schemaVersionV8`, checksum `gc-v8-2026-02-14-agent-history`.

Any new tables in v0.4 must use schema **v9**. Update both the constant and checksum
in `internal/persistence/store.go`.

### 2.9 OnAgentCreated Hook

Already in `main.go`:

```go
registry.SetOnAgentCreated(func(ra *agent.RunningAgent) {
    // Provisions: skills, WASM modules, shell executor, MCP tools
    ...
    mcpTools := tools.RegisterMCPTools(ra.Brain.Genkit(), ra.Brain.Registry(), mcpManager)
    ...
})
```

**v0.4 must update this hook** to use the new per-agent MCP registration instead of
the current global approach.

### 2.10 Gateway

**File**: `internal/gateway/gateway.go`

HTTP mux with routes for `/ws`, `/healthz`, `/metrics`, REST API endpoints,
and OpenAI-compat handler. New routes (A2A) register here.

---

## 3. Feature 1: MCP Client — Full Implementation

### 3.1 Goal

Transform the minimal global MCP integration into a production-quality per-agent system
with auto-discovery, reconnection, policy governance, and hot-reload.

### 3.2 Config Changes

**File**: `internal/config/config.go`

Add per-agent MCP server references to `AgentConfigEntry`:

```go
// Add to AgentConfigEntry:
type AgentConfigEntry struct {
    // ... all existing fields unchanged ...
    MCPServers []AgentMCPRef `yaml:"mcp_servers,omitempty"`
}

// AgentMCPRef references a globally-defined MCP server or defines an inline one.
type AgentMCPRef struct {
    // Name references a server from the global mcp.servers list.
    // If Command is also set, this is an inline definition (name used as identifier).
    Name string `yaml:"name"`
    // Inline server definition (optional — overrides global if set)
    Command   string            `yaml:"command,omitempty"`
    Args      []string          `yaml:"args,omitempty"`
    URL       string            `yaml:"url,omitempty"`       // SSE transport
    Transport string            `yaml:"transport,omitempty"` // "stdio" (default) or "sse"
    Env       map[string]string `yaml:"env,omitempty"`
    Timeout   string            `yaml:"timeout,omitempty"`   // e.g. "30s", parsed as time.Duration
}
```

Extend global `MCPServerEntry` to support SSE:

```go
type MCPServerEntry struct {
    Name      string            `yaml:"name"`
    Command   string            `yaml:"command,omitempty"`
    Args      []string          `yaml:"args,omitempty"`
    URL       string            `yaml:"url,omitempty"`       // NEW: SSE endpoint
    Transport string            `yaml:"transport,omitempty"` // NEW: "stdio" (default) or "sse"
    Env       map[string]string `yaml:"env,omitempty"`       // CHANGED: was []string, now map
    Enabled   bool              `yaml:"enabled"`
    Timeout   string            `yaml:"timeout,omitempty"`   // NEW
}
```

> **Migration note**: The `Env` field type changes from `[]string` to `map[string]string`.
> Add a `UnmarshalYAML` method or a post-load normalizer that accepts both formats for
> backward compatibility. Existing configs use `[]string` format like `["KEY=VALUE"]`.

**Example config.yaml**:

```yaml
mcp:
  servers:
    - name: github
      command: npx
      args: ["-y", "@modelcontextprotocol/server-github"]
      env:
        GITHUB_PERSONAL_ACCESS_TOKEN: "${GITHUB_TOKEN}"
      enabled: true
      timeout: "30s"
    - name: filesystem
      command: npx
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/home/user/projects"]
      enabled: true
    - name: postgres
      transport: sse
      url: "http://localhost:3001/sse"
      enabled: true

agents:
  - agent_id: coder
    model: gemini-2.5-pro
    mcp_servers:
      - name: github        # references global server
      - name: filesystem     # references global server
  - agent_id: researcher
    model: gemini-2.5-pro
    mcp_servers: []          # explicitly no MCP access
  - agent_id: devops
    model: gemini-2.5-pro
    mcp_servers:
      - name: postgres       # references global server
      - name: private-api    # inline definition
        transport: sse
        url: "http://localhost:4000/mcp"
        timeout: "60s"
```

**Resolution rules** (implement in config loading):

1. If `AgentMCPRef` has only `Name` → look up in global `mcp.servers` by name
2. If `AgentMCPRef` has `Name` + `Command` or `URL` → inline definition, ignore global
3. If an agent has no `mcp_servers` field → **inherit all enabled global servers** (backward compat)
4. If an agent has `mcp_servers: []` (explicit empty) → no MCP servers

### 3.3 MCP Manager Rewrite

**File**: `internal/mcp/manager.go`

The existing `Manager` struct and its `NewManager`, `Start`, `Stop` methods need significant
extension. Preserve the existing constructor signature as deprecated (or adapt callers in
`main.go`) and add the new per-agent lifecycle.

**New/modified API surface**:

```go
// ManagerConfig replaces the old positional constructor args.
type ManagerConfig struct {
    GlobalServers []ServerConfig     // from cfg.MCP.Servers (resolved)
    Policy        policy.Checker     // for AllowMCPTool checks
    Logger        *slog.Logger
}

// Manager orchestrates MCP server connections.
// It manages both global servers and per-agent server instances.
type Manager struct {
    mu          sync.RWMutex
    global      map[string]*connection     // name -> connection (shared)
    perAgent    map[string]map[string]*connection // agentID -> name -> connection
    policy      policy.Checker
    logger      *slog.Logger
}

// connection wraps an MCP client with lifecycle.
type connection struct {
    config      ServerConfig
    client      *Client
    tools       []DiscoveredTool
    healthy     atomic.Bool
    cancel      context.CancelFunc
    mu          sync.RWMutex
}

// DiscoveredTool represents a tool enumerated from an MCP server.
type DiscoveredTool struct {
    Name        string
    Description string
    InputSchema json.RawMessage
    ServerName  string
}

// Public API:

// NewManager creates a manager. Does not start connections.
func NewManager(cfg ManagerConfig) *Manager

// Start connects all enabled global servers.
func (m *Manager) Start(ctx context.Context) error

// Stop disconnects all servers (global and per-agent).
func (m *Manager) Stop() error

// ConnectAgentServers starts MCP servers for a specific agent.
// configs are the resolved AgentMCPRef entries for this agent.
// If an entry references a global server (Name-only), the global connection is shared.
// If it's an inline definition, a new connection is created scoped to this agent.
func (m *Manager) ConnectAgentServers(ctx context.Context, agentID string, configs []ServerConfig) error

// DisconnectAgent stops all per-agent connections for this agent.
// Does NOT stop shared global connections.
func (m *Manager) DisconnectAgent(agentID string) error

// DiscoverTools enumerates tools from all MCP servers accessible to an agent.
// Calls tools/list on each connected server. Caches results.
// Returns only tools allowed by policy.
func (m *Manager) DiscoverTools(ctx context.Context, agentID string) ([]DiscoveredTool, error)

// InvokeTool calls a tool on behalf of an agent.
// Checks policy before invocation. Enforces timeout from server config.
func (m *Manager) InvokeTool(ctx context.Context, agentID, serverName, toolName string, input json.RawMessage) (json.RawMessage, error)

// ServerNames returns server names accessible to an agent.
func (m *Manager) ServerNames(agentID string) []string

// Healthy reports whether a specific server is connected and responsive.
func (m *Manager) Healthy(agentID, serverName string) bool

// ReloadAgent diffs current vs new config for an agent.
// Disconnects removed servers, connects new ones, reconnects changed ones.
func (m *Manager) ReloadAgent(ctx context.Context, agentID string, newConfigs []ServerConfig) error
```

### 3.4 Auto-Discovery

When a connection is established (in `connectServer` or equivalent internal method):

1. Call MCP `tools/list` method on the server
2. Parse response into `[]DiscoveredTool`
3. Cache tools on the `connection` struct (protected by `connection.mu`)
4. Log discovered tools at INFO level: `"mcp tools discovered", "server", name, "count", len(tools)`

If `tools/list` fails, log a warning and set `tools` to empty (server is connected but
tool-less). Retry discovery on next `DiscoverTools` call if cache is empty.

### 3.5 Reconnection

When a connection drops (detected via read/write error or explicit EOF):

1. Mark connection as unhealthy (`healthy.Store(false)`)
2. Start background goroutine with exponential backoff: 1s, 2s, 4s, 8s, 16s, 32s, max 60s
3. On successful reconnect: re-run `tools/list`, restore `healthy` flag
4. Log each attempt: `"mcp reconnect attempt", "server", name, "attempt", n, "backoff", dur`
5. Publish bus event `mcp.server.reconnected` on success
6. Give up after 10 consecutive failures; log ERROR and leave unhealthy

### 3.6 Policy Integration

**File**: `internal/policy/policy.go`

Extend the policy engine with MCP-specific rules. The existing `AllowCapability` stays
unchanged — this is additive.

**New policy.yaml schema** (additive — existing `capabilities` and `domains` sections unchanged):

```yaml
# policy.yaml — new section
mcp:
  # Default for any agent/server/tool not matched below.
  # Must be "deny" or "allow". Omitted = "deny" (default-deny).
  default: deny
  rules:
    - agent: coder
      server: github
      tools: ["*"]         # all tools from github server
    - agent: coder
      server: filesystem
      tools: ["read_file", "list_directory"]  # read-only subset
    - agent: devops
      server: "*"
      tools: ["*"]         # devops gets everything
```

**New method on policy engine**:

```go
// AllowMCPTool checks whether agent may invoke tool on server.
// Evaluation: most-specific matching rule wins. If no rule matches, use mcp.default
// (which itself defaults to "deny").
//
// Specificity order (highest to lowest):
//   1. Exact agent + exact server + exact tool
//   2. Exact agent + exact server + wildcard tool
//   3. Exact agent + wildcard server + wildcard tool
//   4. Wildcard agent + exact server + exact tool
//   ... etc, standard most-specific-match
//
// This method is ADDITIVE — it does not change AllowCapability behavior.
func (p *LivePolicy) AllowMCPTool(agentID, serverName, toolName string) bool
```

**Data model** (internal to policy package):

```go
type MCPRule struct {
    Agent  string   `yaml:"agent"`   // agent_id or "*"
    Server string   `yaml:"server"`  // server name or "*"
    Tools  []string `yaml:"tools"`   // tool names or ["*"]
}

type MCPPolicyConfig struct {
    Default string    `yaml:"default"` // "deny" or "allow"
    Rules   []MCPRule `yaml:"rules"`
}
```

Policy hot-reload already works via fsnotify. The MCP section will be parsed during
`policy.Load()` / `policy.ReloadFromFile()`. If the MCP section is missing or malformed,
retain previous policy (existing fail-closed behavior).

### 3.7 Brain Integration

**File**: `internal/engine/brain.go`

When an agent's brain is initialized, MCP tools must be registered as Genkit tools.
This replaces the current global `tools.RegisterMCPTools` approach.

```go
// Replace the current global MCP registration with per-agent registration.
// Called during brain setup AND by the OnAgentCreated hook.
func (b *Brain) RegisterMCPTools(ctx context.Context, agentID string, mgr *mcp.Manager) error {
    tools, err := mgr.DiscoverTools(ctx, agentID)
    if err != nil {
        b.logger.Warn("mcp tool discovery failed", "agent", agentID, "error", err)
        // Non-fatal: agent works without MCP tools
        return nil
    }
    for _, tool := range tools {
        // Policy check already happened in DiscoverTools, but we log it here
        b.logger.Debug("registering mcp tool", "agent", agentID,
            "server", tool.ServerName, "tool", tool.Name)
        b.registerGenkitTool(tool) // wrap as Genkit action
    }
    b.logger.Info("mcp tools registered", "agent", agentID, "count", len(tools))
    return nil
}
```

### 3.8 MCP Bridge Update

**File**: `internal/tools/mcp_bridge.go`

The existing bridge registers MCP tools into Genkit. Update it to:

1. Accept `agentID` parameter in registration functions
2. Route invocations through `Manager.InvokeTool` (which enforces policy + timeout)
3. Log every invocation to audit: `audit.Record("mcp_tool_call", serverName, toolName, agentID, input_summary)`
4. Wrap errors with server/tool context: `fmt.Errorf("mcp tool %s/%s: %w", server, tool, err)`

### 3.9 main.go Changes

The startup sequence in `main.go` must change:

**Before** (current):
```go
// Global MCP config → global manager → register on all agents
mcpManager := mcp.NewManager(mcpConfigs, pol, logger)
mcpManager.Start(ctx)
for _, ra := range registry.ListRunningAgents() {
    tools.RegisterMCPTools(...)
}
```

**After** (v0.4):
```go
// Global MCP config → manager with global servers
mcpManager := mcp.NewManager(mcp.ManagerConfig{
    GlobalServers: mcpConfigs,
    Policy:        pol,
    Logger:        logger,
})
mcpManager.Start(ctx) // connects global servers only

// Per-agent MCP connections — during agent creation loop
for _, acfg := range cfg.Agents {
    // ... existing agent creation ...
    if ra := registry.GetAgent(acfg.AgentID); ra != nil && ra.Brain != nil {
        resolved := resolveMCPRefs(acfg.MCPServers, cfg.MCP.Servers)
        mcpManager.ConnectAgentServers(ctx, acfg.AgentID, resolved)
        ra.Brain.RegisterMCPTools(ctx, acfg.AgentID, mcpManager)
    }
}
```

**Update `OnAgentCreated` hook** to use per-agent MCP:
```go
registry.SetOnAgentCreated(func(ra *agent.RunningAgent) {
    // ... existing skill/WASM/shell provisioning unchanged ...

    // MCP: per-agent (replaces old global RegisterMCPTools)
    if ra.Brain != nil {
        acfg := findAgentConfig(cfg, ra.Config.AgentID)
        if acfg != nil {
            resolved := resolveMCPRefs(acfg.MCPServers, cfg.MCP.Servers)
            mcpManager.ConnectAgentServers(ctx, ra.Config.AgentID, resolved)
            ra.Brain.RegisterMCPTools(ctx, ra.Config.AgentID, mcpManager)
        }
    }
})
```

**Update config hot-reload** (`reconcileAgents`):
```go
// In the "changed agents" branch of reconcileAgents:
mcpManager.ReloadAgent(ctx, id, newResolvedConfigs)
```

### 3.10 Tests Required

All tests MUST run offline, zero API credits (per CLAUDE.md testing patterns).
Use mock MCP servers that implement the protocol over stdio in-process.

| Test File | What It Verifies | Min Tests |
|-----------|------------------|-----------|
| `internal/mcp/manager_test.go` | Connect/disconnect lifecycle, per-agent isolation, shared global reuse, discovery, invoke, reconnect backoff, hot-reload diff, health reporting, timeout enforcement | 10 |
| `internal/policy/policy_test.go` | `AllowMCPTool` rule matching: exact, wildcard, specificity, default-deny, default-allow, missing section, hot-reload | 7 |
| `internal/config/config_test.go` | Parse `mcp_servers` on agents, global server refs, inline defs, env expansion, backward-compat `[]string` Env, explicit empty list, SSE transport | 6 |
| `internal/tools/mcp_bridge_test.go` | End-to-end tool call through bridge, audit logging, error wrapping, policy denial | 4 |
| `internal/engine/brain_test.go` | `RegisterMCPTools` registers allowed tools, skips denied, handles discovery failure gracefully | 3 |

**Minimum: 30 new tests for Feature 1.**

### 3.11 Verification Commands

```bash
go test ./internal/mcp/... -v -count=1
go test ./internal/policy/... -v -count=1
go test ./internal/config/... -v -count=1
go test ./internal/tools/... -v -count=1 -run "MCP|Mcp|mcp"
go test ./internal/engine/... -v -count=1 -run "MCP|Mcp|mcp"
go test -race ./internal/mcp/... -count=1
```

---

## 4. Feature 2: True Async Delegation

### 4.1 Goal

Add `delegate_task_async` alongside the existing sync `delegate_task` tool. The async
variant returns immediately; the result is injected into the calling agent's conversation
on their next turn.

### 4.2 Design Decisions

**Both tools coexist.** The LLM sees both `delegate_task` (sync, blocking) and
`delegate_task_async` (async, non-blocking) in its tool list. The system prompt
should guide the agent:
- Use sync for quick lookups where the agent needs the answer immediately
- Use async for long-running reviews, analysis, research

**Persistence.** Delegation state is stored in SQLite so it survives crashes.

**Injection timing.** Before each agent turn (in the brain's message-building phase),
check for completed delegations and prepend them as system messages.

### 4.3 Schema Migration (v8 → v9)

**File**: `internal/persistence/store.go`

Add migration to `migrateSchema`:

```go
const schemaVersionV9 = 9
const schemaChecksumV9 = "gc-v9-2026-XX-XX-delegations" // set actual date

// In migrateSchema, after v8 block:
if version < schemaVersionV9 {
    _, err := tx.ExecContext(ctx, `
        CREATE TABLE IF NOT EXISTS delegations (
            id            TEXT PRIMARY KEY,
            task_id       TEXT,
            parent_agent  TEXT NOT NULL,
            child_agent   TEXT NOT NULL,
            prompt        TEXT NOT NULL,
            status        TEXT NOT NULL DEFAULT 'queued',
            result        TEXT,
            error_msg     TEXT,
            created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
            completed_at  DATETIME,
            injected      INTEGER NOT NULL DEFAULT 0
        );
        CREATE INDEX IF NOT EXISTS idx_deleg_parent_pending
            ON delegations(parent_agent, injected) WHERE status IN ('completed','failed') AND injected = 0;
        CREATE INDEX IF NOT EXISTS idx_deleg_task
            ON delegations(task_id);
    `)
    // update schema_version to v9
}
```

**IMPORTANT**: Update BOTH `schemaVersionCurrent` AND the checksum constant. Follow the
existing pattern in `store.go` — look at how v7→v8 was done and replicate the structure.

### 4.4 Delegation Store

**File**: `internal/persistence/delegations.go` (new file)

```go
package persistence

type Delegation struct {
    ID          string
    TaskID      string    // links to tasks table (set when task is created)
    ParentAgent string    // agent that requested delegation
    ChildAgent  string    // agent that executes
    Prompt      string    // what was delegated
    Status      string    // "queued", "running", "completed", "failed"
    Result      string    // output from child agent
    ErrorMsg    string    // error message if failed
    CreatedAt   time.Time
    CompletedAt *time.Time
    Injected    bool      // true once result has been injected into parent's conversation
}

// Methods on *Store:

func (s *Store) CreateDelegation(ctx context.Context, d *Delegation) error
func (s *Store) GetDelegation(ctx context.Context, id string) (*Delegation, error)
func (s *Store) CompleteDelegation(ctx context.Context, id, result string) error
func (s *Store) FailDelegation(ctx context.Context, id, errMsg string) error
func (s *Store) PendingDelegationsForAgent(ctx context.Context, agentID string) ([]*Delegation, error)
func (s *Store) MarkDelegationInjected(ctx context.Context, id string) error
func (s *Store) GetDelegationByTaskID(ctx context.Context, taskID string) (*Delegation, error)
```

`PendingDelegationsForAgent` returns rows where `parent_agent = agentID AND injected = 0
AND status IN ('completed', 'failed')`.

### 4.5 Async Delegate Tool

**File**: `internal/tools/delegate.go` (add alongside existing sync tool)

```go
// Register as a new Genkit tool alongside the existing delegate_task tool.

type AsyncDelegateInput struct {
    AgentID string `json:"agent_id"`
    Task    string `json:"task"`
}

type AsyncDelegateOutput struct {
    DelegationID string `json:"delegation_id"`
    Status       string `json:"status"` // always "queued"
    Message      string `json:"message"`
}
```

Implementation:

1. Validate target agent exists in registry
2. Create `Delegation` record in store (status "queued")
3. Create a task for the child agent via `ChatTaskRouter` (same as sync delegation)
4. Store the task ID on the delegation record
5. Return immediately with delegation ID

The tool description should clearly differentiate it from sync:
```
"delegate_task_async: Delegate a task to another agent without waiting for the result.
Returns immediately with a delegation ID. The result will be delivered to you automatically
when it's ready. Use this for long-running tasks like code reviews, research, or analysis.
For quick lookups where you need the answer right now, use delegate_task instead."
```

### 4.6 Brain Context Injection

**File**: `internal/engine/brain.go`

Before each agent turn (in the method that builds the message list for the LLM call),
inject completed delegation results:

```go
func (b *Brain) injectPendingDelegations(ctx context.Context, agentID string, messages []Message) []Message {
    pending, err := b.store.PendingDelegationsForAgent(ctx, agentID)
    if err != nil {
        b.logger.Warn("failed to check pending delegations", "agent", agentID, "error", err)
        return messages
    }

    var injections []Message
    for _, d := range pending {
        var content string
        if d.Status == "completed" {
            content = fmt.Sprintf("[Delegation result from @%s (id: %s)]:\n%s",
                d.ChildAgent, d.ID, d.Result)
        } else { // failed
            content = fmt.Sprintf("[Delegation to @%s FAILED (id: %s)]: %s",
                d.ChildAgent, d.ID, d.ErrorMsg)
        }
        injections = append(injections, Message{Role: "system", Content: content})

        if err := b.store.MarkDelegationInjected(ctx, d.ID); err != nil {
            b.logger.Warn("failed to mark delegation injected", "id", d.ID, "error", err)
        }
    }

    if len(injections) > 0 {
        // Prepend injections before the latest user message
        return append(injections, messages...)
    }
    return messages
}
```

**Where to call this**: Find the method in `brain.go` that assembles the message list
before calling the LLM (likely in the `Chat` or `Run` method). Insert `injectPendingDelegations`
call there, BEFORE context compaction runs (so delegation results are treated as fresh content).

### 4.7 Engine Event Wiring

When a task completes in `internal/engine/engine.go`, check if it's linked to a delegation:

```go
// In the task completion handler (after status transitions to SUCCEEDED):
func (e *Engine) onTaskSucceeded(ctx context.Context, taskID string, result string) {
    // ... existing success handling ...

    // Check if this task is a delegation
    d, err := e.store.GetDelegationByTaskID(ctx, taskID)
    if err != nil || d == nil {
        return // not a delegation
    }
    if err := e.store.CompleteDelegation(ctx, d.ID, result); err != nil {
        e.logger.Error("failed to complete delegation", "id", d.ID, "error", err)
    }
    e.bus.Publish("delegation.completed", map[string]string{
        "delegation_id": d.ID,
        "parent_agent":  d.ParentAgent,
        "child_agent":   d.ChildAgent,
    })
}

// Similarly for task failure:
func (e *Engine) onTaskFailed(ctx context.Context, taskID string, errMsg string) {
    d, err := e.store.GetDelegationByTaskID(ctx, taskID)
    if err != nil || d == nil {
        return
    }
    if err := e.store.FailDelegation(ctx, d.ID, errMsg); err != nil {
        e.logger.Error("failed to fail delegation", "id", d.ID, "error", err)
    }
    e.bus.Publish("delegation.failed", map[string]string{
        "delegation_id": d.ID,
        "parent_agent":  d.ParentAgent,
        "child_agent":   d.ChildAgent,
    })
}
```

**Finding the right hook**: Look in `engine.go` for where task status transitions to
`TaskStatusSucceeded` or `TaskStatusFailed`. That's where to add the delegation check.
The bus is already available on the engine struct (or injected — check constructor).

### 4.8 TUI Activity Feed

**File**: `internal/tui/activity.go`

Subscribe to delegation bus events to show status:

```
🔄 @security reviewing auth.go (del_abc123) — 12s
✅ @security completed review (del_abc123) — 2 issues found
❌ @writer failed draft (del_def456) — context limit exceeded
```

### 4.9 Tests Required

| Test File | What It Verifies | Min Tests |
|-----------|------------------|-----------|
| `internal/persistence/delegations_test.go` | CRUD, pending query, mark injected, get by task ID, schema migration | 7 |
| `internal/tools/delegate_test.go` | Async tool returns immediately, creates delegation + task, invalid agent rejected, coexists with sync tool | 4 |
| `internal/engine/brain_test.go` | Injection of completed results, failed results, no double-injection, empty pending is no-op | 4 |
| `internal/engine/engine_test.go` | Task completion triggers delegation completion, task failure triggers delegation failure, non-delegation tasks unaffected | 3 |
| `internal/tui/activity_test.go` | Delegation events rendered correctly | 2 |

**Minimum: 20 new tests for Feature 2.**

### 4.10 Verification Commands

```bash
go test ./internal/persistence/... -v -count=1 -run -i "(?i)deleg"
go test ./internal/tools/... -v -count=1 -run -i "(?i)async"
go test ./internal/engine/... -v -count=1 -run -i "(?i)deleg|inject"
go test -race ./internal/persistence/... ./internal/engine/... -count=1
```

---

## 5. Feature 3: Telegram Deep Integration

### 5.1 Goal

Extend the existing Telegram channel with plan progress updates, HITL approval gates via
inline keyboards, `/plan` command, proactive agent alerts, and rich formatting.

### 5.2 Plan Progress Updates

**File**: `internal/channels/telegram.go`

The channel already has `eventBus`. Subscribe to coordinator events on init:

```go
func (t *TelegramChannel) subscribeToEvents() {
    // These event names must match what coordinator/executor.go publishes.
    // If the coordinator doesn't publish these yet, add publishing there first.
    t.bus.Subscribe("plan.step.started", t.onPlanStepStarted)
    t.bus.Subscribe("plan.step.completed", t.onPlanStepCompleted)
    t.bus.Subscribe("plan.step.failed", t.onPlanStepFailed)
    t.bus.Subscribe("plan.completed", t.onPlanCompleted)
    t.bus.Subscribe("delegation.completed", t.onDelegationCompleted)
    t.bus.Subscribe("delegation.failed", t.onDelegationFailed)
}
```

**Coordinator event publishing**: Check whether `internal/coordinator/executor.go` already
publishes these events. If not, add `bus.Publish(...)` calls at the appropriate state
transitions. The executor may need the bus injected — check its constructor and add it
if missing.

Format plan progress as Telegram MarkdownV2:

```go
func formatPlanProgress(planName string, steps []StepStatus) string {
    // Output:
    // 📋 *Plan: content\-pipeline*
    //   ✅ research    \(Researcher\)   4\.1s
    //   🔄 write       \(Writer\)       running\.\.\.
    //   ⏳ review      \(Editor\)       waiting
    //
    // IMPORTANT: Escape all MarkdownV2 special chars: _ * [ ] ( ) ~ > # + - = | { } . !
}
```

**Debounce**: Batch progress updates to max 1 message per 2 seconds per chat to avoid
Telegram rate limits (30 messages/second per bot, but per-chat limits are stricter).

### 5.3 HITL Approval Gates

#### 5.3.1 Coordinator Changes

**File**: `internal/coordinator/executor.go`

Add HITL support to plan steps. Extend the plan step model:

```yaml
# Plan step with approval gate
plans:
  deploy-pipeline:
    steps:
      - id: deploy
        agent: devops
        input: "Deploy to production"
        depends_on: [test]
        approval: required    # NEW: "required", "optional", or absent
        approval_timeout: 1h  # NEW: timeout before auto-reject
```

**File**: `internal/coordinator/plan.go`

Add fields to the step struct (check existing field names and adapt):

```go
// Add to whatever the existing step struct is called:
type Step struct {
    // ... existing fields ...
    Approval        string        `yaml:"approval,omitempty" json:"approval,omitempty"`
    ApprovalTimeout time.Duration `yaml:"approval_timeout,omitempty" json:"approval_timeout,omitempty"`
}
```

When the executor reaches a step with `approval: required`:

1. Pause execution of this step
2. Publish `hitl.approval.requested` event with plan ID, step ID, question
3. Wait for response via bus event `hitl.approval.response`
4. On approve → continue step execution
5. On reject → fail step with "Rejected by human"
6. On timeout → fail step with "Approval timed out"

```go
type HITLRequest struct {
    PlanID    string   `json:"plan_id"`
    StepID    string   `json:"step_id"`
    AgentID   string   `json:"agent_id"`
    Question  string   `json:"question"`
    Options   []string `json:"options"`  // ["Approve", "Reject", "Skip"]
    Timeout   time.Duration
}

type HITLResponse struct {
    PlanID   string `json:"plan_id"`
    StepID   string `json:"step_id"`
    Action   string `json:"action"`  // "approve", "reject", "skip"
    UserID   string `json:"user_id"` // Telegram user ID or "tui"
}
```

#### 5.3.2 Telegram HITL UI

When `hitl.approval.requested` fires, Telegram sends an inline keyboard:

```go
func (t *TelegramChannel) onHITLRequest(data interface{}) {
    req := data.(HITLRequest)
    msg := tgbotapi.NewMessage(t.chatID, formatHITLMessage(req))
    msg.ParseMode = "MarkdownV2"
    msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("✅ Approve", fmt.Sprintf("hitl:%s:%s:approve", req.PlanID, req.StepID)),
            tgbotapi.NewInlineKeyboardButtonData("❌ Reject", fmt.Sprintf("hitl:%s:%s:reject", req.PlanID, req.StepID)),
            tgbotapi.NewInlineKeyboardButtonData("⏭ Skip", fmt.Sprintf("hitl:%s:%s:skip", req.PlanID, req.StepID)),
        ),
    )
    t.bot.Send(msg)
}
```

Callback handler:

```go
func (t *TelegramChannel) handleCallbackQuery(query *tgbotapi.CallbackQuery) {
    // Parse: "hitl:<planID>:<stepID>:<action>"
    parts := strings.SplitN(query.Data, ":", 4)
    if len(parts) != 4 || parts[0] != "hitl" {
        return
    }
    resp := HITLResponse{
        PlanID: parts[1],
        StepID: parts[2],
        Action: parts[3],
        UserID: fmt.Sprintf("telegram:%d", query.From.ID),
    }
    t.bus.Publish("hitl.approval.response", resp)

    // Answer callback to dismiss loading spinner
    t.bot.Request(tgbotapi.NewCallback(query.ID, "Response recorded"))
}
```

### 5.4 /plan Command

```go
func (t *TelegramChannel) handleMessage(msg *tgbotapi.Message) {
    text := strings.TrimSpace(msg.Text)

    // /plan <name> <input>
    if strings.HasPrefix(text, "/plan ") {
        t.handlePlanCommand(msg)
        return
    }
    // ... existing @agent routing ...
}

func (t *TelegramChannel) handlePlanCommand(msg *tgbotapi.Message) {
    // Parse: "/plan content-pipeline Write about our new auth system"
    parts := strings.SplitN(strings.TrimPrefix(msg.Text, "/plan "), " ", 2)
    planName := parts[0]
    input := ""
    if len(parts) > 1 {
        input = parts[1]
    }

    // Validate plan exists (need access to coordinator or plan registry)
    // Trigger execution
    // Send confirmation message
}
```

**Dependency**: Telegram needs access to the coordinator/plan registry to validate
and execute plans. Pass this as a dependency during construction or via an interface.
Check what `TelegramChannel` already receives in its constructor and add what's needed.

### 5.5 Alert Tool

**File**: `internal/tools/alert.go` (new file)

A Genkit tool that agents can call to send proactive alerts:

```go
type AlertInput struct {
    Severity string `json:"severity"` // "info", "warning", "critical"
    Title    string `json:"title"`
    Body     string `json:"body"`
}

type AlertOutput struct {
    Delivered bool   `json:"delivered"`
    Message   string `json:"message"`
}
```

When invoked:
1. Validate severity (must be info/warning/critical)
2. Publish `agent.alert` event to bus with agent ID, severity, title, body
3. All channels (Telegram, TUI activity feed) that subscribe will display it

Register in `internal/tools/catalog.go` alongside other built-in tools.

### 5.6 Tests Required

| Test File | What It Verifies | Min Tests |
|-----------|------------------|-----------|
| `internal/channels/telegram_test.go` | Plan progress formatting, HITL keyboard rendering, callback parsing, /plan parsing, alert display, MarkdownV2 escaping, event subscriptions wired | 8 |
| `internal/tools/alert_test.go` | Alert publishes to bus, severity validation, registered in catalog | 3 |
| `internal/coordinator/executor_test.go` | HITL gate pauses execution, approve continues, reject fails, timeout fails | 4 |

**Minimum: 15 new tests for Feature 3.**

### 5.7 Verification Commands

```bash
go test ./internal/channels/... -v -count=1
go test ./internal/tools/... -v -count=1 -run "(?i)alert"
go test ./internal/coordinator/... -v -count=1 -run "(?i)hitl|approval"
go test -race ./internal/channels/... -count=1
```

---

## 6. Feature 4: A2A Protocol (Experimental)

### 6.1 Goal

Serve a read-only Agent Card at `GET /.well-known/agent.json`. Passive discoverability
only — no inbound A2A task execution. Minimal surface, minimal maintenance.

### 6.2 Implementation

**File**: `internal/gateway/a2a.go` (new file)

```go
package gateway

// AgentCard follows the A2A agent card schema.
// Reference: https://google.github.io/A2A/#/documentation?id=agent-card
// NOTE: The A2A spec is evolving. This implements the schema as of early 2025.
// Before implementing, verify the current spec at the URL above and adjust
// field names/types if the spec has changed.
type AgentCard struct {
    Name               string       `json:"name"`
    Description        string       `json:"description"`
    URL                string       `json:"url"`
    Version            string       `json:"version"`
    Capabilities       Capabilities `json:"capabilities"`
    DefaultInputModes  []string     `json:"defaultInputModes"`
    DefaultOutputModes []string     `json:"defaultOutputModes"`
    Skills             []A2ASkill   `json:"skills"`
}

type Capabilities struct {
    Streaming              bool `json:"streaming"`
    PushNotifications      bool `json:"pushNotifications"`
    StateTransitionHistory bool `json:"stateTransitionHistory"`
}

type A2ASkill struct {
    ID          string   `json:"id"`
    Name        string   `json:"name"`
    Description string   `json:"description"`
    Tags        []string `json:"tags,omitempty"`
}

func (g *Gateway) handleAgentCard(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }

    if !g.a2aEnabled {
        http.NotFound(w, r)
        return
    }

    agents := g.registry.ListAgents()
    skills := make([]A2ASkill, 0, len(agents))
    for _, a := range agents {
        skills = append(skills, A2ASkill{
            ID:          a.AgentID,
            Name:        a.DisplayName,
            Description: "", // Keep minimal — don't leak soul prompts
            Tags:        a.SkillsFilter,
        })
    }

    card := AgentCard{
        Name:               "GoClaw",
        Description:        "Multi-agent runtime with durable task execution",
        URL:                fmt.Sprintf("http://localhost:%d", g.port),
        Version:            Version,
        Capabilities:       Capabilities{StateTransitionHistory: true},
        DefaultInputModes:  []string{"text"},
        DefaultOutputModes: []string{"text"},
        Skills:             skills,
    }

    w.Header().Set("Content-Type", "application/json")
    w.Header().Set("Cache-Control", "public, max-age=300")
    json.NewEncoder(w).Encode(card)
}
```

**Route registration** in `internal/gateway/gateway.go`:

```go
// In the mux/router setup (find where other routes are registered):
mux.HandleFunc("/.well-known/agent.json", g.handleAgentCard)
```

**Add to Gateway struct**: `a2aEnabled bool` field, populated from config.

**Config addition** in `internal/config/config.go`:

```go
type A2AConfig struct {
    Enabled *bool `yaml:"enabled,omitempty"` // pointer to distinguish unset (default true) from false
}

// In the main Config struct:
type Config struct {
    // ... existing fields ...
    A2A A2AConfig `yaml:"a2a,omitempty"`
}
```

Default to enabled. Use pointer-to-bool so `omitempty` doesn't treat `false` as empty.

### 6.3 Tests Required

| Test File | What It Verifies | Min Tests |
|-----------|------------------|-----------|
| `internal/gateway/a2a_test.go` | GET returns valid JSON with required fields, agents listed as skills, POST returns 405, disabled returns 404, Content-Type correct, empty agent list handled | 6 |

**Minimum: 6 new tests for Feature 4.**

### 6.4 Verification Commands

```bash
go test ./internal/gateway/... -v -count=1 -run "(?i)a2a|agentcard"
```

---

## 7. Implementation Order

Implement in this exact order. Each phase is a gate — all its tests must pass before
proceeding.

```
Phase 1 (Week 1): MCP Client — Full Implementation
  ├── 1a. Config changes (config.go): AgentMCPRef, SSE transport, Env map migration
  ├── 1b. Policy MCP rules (policy.go): AllowMCPTool, rule parsing, specificity
  ├── 1c. MCP Manager rewrite (manager.go): per-agent, discovery, reconnect, hot-reload
  ├── 1d. MCP Bridge update (mcp_bridge.go): per-agent routing, audit, errors
  ├── 1e. Brain integration (brain.go): RegisterMCPTools per-agent
  ├── 1f. main.go: new startup sequence, OnAgentCreated update, reconcile update
  └── 1g. Tests for all above — GATE: go test passes for mcp, policy, config, tools, engine

Phase 2 (Week 2): True Async Delegation
  ├── 2a. Schema migration v8→v9 (store.go): delegations table
  ├── 2b. Delegation store (delegations.go): CRUD operations
  ├── 2c. Async delegate tool (delegate.go): add alongside sync
  ├── 2d. Brain injection (brain.go): injectPendingDelegations
  ├── 2e. Engine wiring (engine.go): task completion → delegation completion
  ├── 2f. TUI activity feed (activity.go): delegation status display
  └── 2g. Tests — GATE: go test passes for persistence, tools, engine, tui

Phase 3 (Week 3): Telegram Deep Integration
  ├── 3a. Coordinator: bus event publishing for plan steps (if not already present)
  ├── 3b. Coordinator: HITL approval gate in executor
  ├── 3c. Telegram: event subscriptions, progress formatting
  ├── 3d. Telegram: HITL inline keyboards + callback handler
  ├── 3e. Telegram: /plan command handler (needs coordinator/plan registry access)
  ├── 3f. Alert tool (alert.go): new tool + catalog registration
  ├── 3g. Telegram: alert display
  └── 3h. Tests — GATE: go test passes for channels, tools, coordinator

Phase 4 (Week 3-4): A2A Protocol
  ├── 4a. Gateway: a2a.go handler + route registration
  ├── 4b. Config: A2AConfig struct + parsing
  └── 4c. Tests — GATE: go test passes for gateway, config
```

### Dependency Graph

```
MCP Client ────────── (start first, independent)
     │
     ├──► Async Delegation (independent of MCP, but starts after to avoid merge conflicts)
     │         │
     │         └──► Telegram Deep (uses delegation events + coordinator HITL)
     │
     └──► A2A Protocol (independent, can start anytime after Phase 1)
```

---

## 8. Verification Protocol

### 8.1 Per-Phase Gate

After completing each phase, run:

```bash
# Phase-specific tests
go test ./internal/<packages-in-phase>/... -v -count=1

# Then full suite
just check
go test -race ./... -count=1
```

Do NOT proceed to the next phase until the gate passes.

### 8.2 Full Suite Gate (After All Phases)

```bash
# 1. Compile
go build ./...

# 2. Vet
go vet ./...

# 3. Full test suite
go test ./... -count=1

# 4. Race detector
go test -race ./... -count=1

# 5. Test count delta
BASELINE=$(cat .test-count-baseline 2>/dev/null || echo 0)
CURRENT=$(grep -r "func Test" internal/ cmd/ tools/ 2>/dev/null | wc -l | tr -d ' ')
DELTA=$((CURRENT - BASELINE))
echo "Tests: $CURRENT (baseline: $BASELINE, delta: +$DELTA)"
[ "$DELTA" -ge 70 ] || echo "WARN: Expected >=70 new tests, got $DELTA"

# 6. New file existence
for f in \
    "internal/persistence/delegations.go" \
    "internal/persistence/delegations_test.go" \
    "internal/tools/alert.go" \
    "internal/tools/alert_test.go" \
    "internal/gateway/a2a.go" \
    "internal/gateway/a2a_test.go"; do
    [ -f "$f" ] && echo "OK: $f" || echo "MISSING: $f"
done

# 7. Schema version check
grep -q "schemaVersionV9\|schemaVersion.*= 9" internal/persistence/store.go && \
    echo "OK: schema v9" || echo "FAIL: schema not bumped to v9"
```

### 8.3 Acceptance Criteria Checklist

```
Feature 1: MCP Client
  [ ] Per-agent mcp_servers field parses in config
  [ ] Global server references resolved by name
  [ ] Inline server definitions work
  [ ] SSE transport configurable
  [ ] Env field accepts both map and legacy []string format
  [ ] Manager.ConnectAgentServers creates per-agent connections
  [ ] Manager.DisconnectAgent stops only that agent's connections
  [ ] Auto-discovery calls tools/list and caches results
  [ ] Reconnect with exponential backoff on server failure
  [ ] Config hot-reload triggers ReloadAgent
  [ ] AllowMCPTool added to policy engine
  [ ] Policy default-deny for MCP tools
  [ ] Policy wildcard matching works
  [ ] Brain registers only policy-allowed tools
  [ ] main.go updated: per-agent MCP startup
  [ ] OnAgentCreated hook updated for per-agent MCP
  [ ] Audit log records MCP tool invocations
  [ ] 30+ new tests pass

Feature 2: Async Delegation
  [ ] Schema migrated to v9 with delegations table
  [ ] delegate_task_async tool registered alongside delegate_task
  [ ] Async tool returns immediately with delegation ID
  [ ] Delegation record persisted in SQLite
  [ ] Task completion triggers delegation completion
  [ ] Task failure triggers delegation failure
  [ ] Brain injects completed delegation results as system messages
  [ ] No double-injection of already-injected results
  [ ] TUI activity feed shows delegation status
  [ ] Delegation survives process restart (persistence)
  [ ] 20+ new tests pass

Feature 3: Telegram Deep Integration
  [ ] Coordinator publishes plan step events to bus
  [ ] Coordinator pauses at HITL steps, resumes on approval
  [ ] HITL timeout causes step failure
  [ ] Telegram subscribes to plan events on init
  [ ] Plan progress formatted as MarkdownV2
  [ ] HITL renders inline keyboard with Approve/Reject/Skip
  [ ] Callback parsed and published as hitl.approval.response
  [ ] /plan command parsed and triggers plan execution
  [ ] Alert tool publishes to bus
  [ ] Telegram displays alerts
  [ ] Progress updates debounced (max 1/2s per chat)
  [ ] 15+ new tests pass

Feature 4: A2A Protocol
  [ ] GET /.well-known/agent.json returns valid JSON
  [ ] All registered agents listed as skills
  [ ] POST/PUT/DELETE return 405
  [ ] Disabled via a2a.enabled=false returns 404
  [ ] Content-Type is application/json
  [ ] 6+ new tests pass

Global
  [ ] just check passes
  [ ] go test -race ./... clean
  [ ] go vet ./... clean
  [ ] No new compilation warnings
  [ ] Schema version is v9
  [ ] Version string updated to v0.4-dev in main.go
```

---

## 9. Self-Verification Script

Save as `tools/verify/v04_verify.sh`. This is the single source of truth for completion.

**IMPORTANT**: Before starting implementation, capture the baseline:
```bash
grep -r "func Test" internal/ cmd/ tools/ 2>/dev/null | wc -l > .test-count-baseline
```

```bash
#!/usr/bin/env bash
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

PASS=0
FAIL=0
WARN=0

pass() { echo -e "${GREEN}✅ PASS${NC}: $1"; ((PASS++)); }
fail() { echo -e "${RED}❌ FAIL${NC}: $1"; ((FAIL++)); }
warn() { echo -e "${YELLOW}⚠️  WARN${NC}: $1"; ((WARN++)); }

echo "=========================================="
echo " GoClaw v0.4 Verification"
echo "=========================================="
echo ""

# ── Step 0: Baseline ───────────────────────
BASELINE=0
if [ -f .test-count-baseline ]; then
    BASELINE=$(cat .test-count-baseline | tr -d ' ')
fi

# ── Pre-flight ──────────────────────────────
echo "── Pre-flight ──"

if go version &>/dev/null; then pass "Go compiler available"
else fail "Go compiler not found"; fi

if go build ./... 2>/dev/null; then pass "Project compiles"
else fail "Compilation failed"; fi

if go vet ./... 2>/dev/null; then pass "go vet clean"
else fail "go vet has issues"; fi

echo ""

# ── Feature 1: MCP Client ──────────────────
echo "── Feature 1: MCP Client ──"

# Config: per-agent MCP (case-insensitive grep for flexibility)
grep -qi "mcpservers\|mcp_servers" internal/config/config.go 2>/dev/null && \
    pass "Per-agent MCP config field present" || fail "Per-agent MCP config missing"

grep -qi "sse\|url.*yaml.*url" internal/config/config.go 2>/dev/null && \
    pass "SSE transport in config" || fail "SSE transport missing"

# Manager: per-agent methods (flexible name matching)
grep -qi "connectagent\|connect.*agent" internal/mcp/manager.go 2>/dev/null && \
    pass "Per-agent connect in manager" || fail "Per-agent connect missing"

grep -qi "disconnectagent\|disconnect.*agent" internal/mcp/manager.go 2>/dev/null && \
    pass "Per-agent disconnect in manager" || fail "Per-agent disconnect missing"

grep -qi "discovertools\|discover.*tools\|tools.*list\|toolslist" internal/mcp/manager.go 2>/dev/null && \
    pass "Tool discovery in manager" || fail "Tool discovery missing"

grep -qi "invoketool\|invoke.*tool\|calltool\|call.*tool" internal/mcp/manager.go 2>/dev/null && \
    pass "Tool invocation in manager" || fail "Tool invocation missing"

grep -qi "reconnect\|backoff\|retry" internal/mcp/manager.go 2>/dev/null && \
    pass "Reconnection logic present" || fail "Reconnection logic missing"

# Policy: MCP checks (flexible)
grep -qi "allowmcptool\|mcp.*rule\|mcprule\|mcppolicy" internal/policy/policy.go 2>/dev/null && \
    pass "MCP policy in engine" || fail "MCP policy missing"

# Tests exist and pass
MCP_TEST_COUNT=$(grep -c "func Test" internal/mcp/manager_test.go 2>/dev/null || echo 0)
[ "$MCP_TEST_COUNT" -ge 8 ] && pass "MCP manager tests: $MCP_TEST_COUNT (≥8)" || \
    fail "MCP manager tests: $MCP_TEST_COUNT (<8)"

if go test ./internal/mcp/... -count=1 -timeout 120s 2>/dev/null; then
    pass "MCP package tests pass"
else fail "MCP package tests fail"; fi

if go test ./internal/policy/... -count=1 -timeout 120s 2>/dev/null; then
    pass "Policy package tests pass"
else fail "Policy package tests fail"; fi

echo ""

# ── Feature 2: Async Delegation ─────────────
echo "── Feature 2: Async Delegation ──"

# Schema v9
grep -q "schemaVersionV9\|schemaVersion.*=.*9\|schema_version.*9" internal/persistence/store.go 2>/dev/null && \
    pass "Schema bumped to v9" || fail "Schema not v9"

[ -f internal/persistence/delegations.go ] && pass "delegations.go exists" || fail "delegations.go missing"
[ -f internal/persistence/delegations_test.go ] && pass "delegations_test.go exists" || fail "delegations_test.go missing"

grep -qi "delegation" internal/persistence/delegations.go 2>/dev/null && \
    pass "Delegation store implemented" || fail "Delegation store empty"

# Async tool
grep -qi "async.*delegate\|delegate.*async\|asyncdelegate\|delegate_task_async" internal/tools/delegate.go 2>/dev/null && \
    pass "Async delegate tool exists" || fail "Async delegate tool missing"

# Brain injection
grep -qi "inject.*deleg\|deleg.*inject\|pendingdeleg" internal/engine/brain.go 2>/dev/null && \
    pass "Brain delegation injection exists" || fail "Brain delegation injection missing"

DELEG_TEST_COUNT=$(grep -c "func Test" internal/persistence/delegations_test.go 2>/dev/null || echo 0)
[ "$DELEG_TEST_COUNT" -ge 5 ] && pass "Delegation tests: $DELEG_TEST_COUNT (≥5)" || \
    fail "Delegation tests: $DELEG_TEST_COUNT (<5)"

if go test ./internal/persistence/... -count=1 -timeout 120s 2>/dev/null; then
    pass "Persistence tests pass"
else fail "Persistence tests fail"; fi

echo ""

# ── Feature 3: Telegram Deep ────────────────
echo "── Feature 3: Telegram Deep Integration ──"

[ -f internal/tools/alert.go ] && pass "alert.go exists" || fail "alert.go missing"
[ -f internal/tools/alert_test.go ] && pass "alert_test.go exists" || fail "alert_test.go missing"

grep -qi "hitl\|approval\|inlinekeyboard\|callbackquery" internal/channels/telegram.go 2>/dev/null && \
    pass "HITL in Telegram" || fail "HITL missing from Telegram"

grep -qi "handleplan\|/plan\|plancommand\|plan.*command" internal/channels/telegram.go 2>/dev/null && \
    pass "/plan handler exists" || fail "/plan handler missing"

grep -qi "formatprogress\|formatplan\|markdownv2\|markdown.*v2" internal/channels/telegram.go 2>/dev/null && \
    pass "Progress formatting exists" || fail "Progress formatting missing"

grep -qi "hitl\|approval.*gate\|approval.*request" internal/coordinator/executor.go 2>/dev/null && \
    pass "HITL gate in coordinator" || fail "HITL gate missing"

if go test ./internal/channels/... -count=1 -timeout 120s 2>/dev/null; then
    pass "Channels tests pass"
else fail "Channels tests fail"; fi

if go test ./internal/coordinator/... -count=1 -timeout 120s 2>/dev/null; then
    pass "Coordinator tests pass"
else fail "Coordinator tests fail"; fi

echo ""

# ── Feature 4: A2A Protocol ────────────────
echo "── Feature 4: A2A Protocol ──"

[ -f internal/gateway/a2a.go ] && pass "a2a.go exists" || fail "a2a.go missing"
[ -f internal/gateway/a2a_test.go ] && pass "a2a_test.go exists" || fail "a2a_test.go missing"

grep -qi "well-known\|agent\.json\|agentcard" internal/gateway/a2a.go 2>/dev/null && \
    pass "Agent card handler implemented" || fail "Agent card handler missing"

grep -qi "well-known\|agent\.json" internal/gateway/gateway.go 2>/dev/null && \
    pass "A2A route registered" || fail "A2A route not registered"

A2A_TEST_COUNT=$(grep -c "func Test" internal/gateway/a2a_test.go 2>/dev/null || echo 0)
[ "$A2A_TEST_COUNT" -ge 4 ] && pass "A2A tests: $A2A_TEST_COUNT (≥4)" || \
    fail "A2A tests: $A2A_TEST_COUNT (<4)"

if go test ./internal/gateway/... -count=1 -timeout 120s 2>/dev/null; then
    pass "Gateway tests pass"
else fail "Gateway tests fail"; fi

echo ""

# ── Global Checks ──────────────────────────
echo "── Global Checks ──"

if go test ./... -count=1 -timeout 300s 2>/dev/null; then
    pass "Full test suite passes"
else fail "Full test suite fails"; fi

if go test -race ./... -count=1 -timeout 300s 2>/dev/null; then
    pass "Race detector clean"
else fail "Race conditions detected"; fi

# Test count delta
CURRENT=$(grep -r "func Test" internal/ cmd/ tools/ 2>/dev/null | wc -l | tr -d ' ')
if [ "$BASELINE" -gt 0 ]; then
    DELTA=$((CURRENT - BASELINE))
    echo "  Tests: $CURRENT (baseline: $BASELINE, delta: +$DELTA)"
    [ "$DELTA" -ge 70 ] && pass "Test delta: +$DELTA (≥70)" || \
        warn "Test delta: +$DELTA (target ≥70)"
else
    echo "  Tests: $CURRENT (no baseline captured — run baseline step first)"
    warn "No test baseline — cannot verify delta"
fi

grep -q "v0\.4" cmd/goclaw/main.go 2>/dev/null && \
    pass "Version string updated" || warn "Version string not updated"

echo ""
echo "=========================================="
echo " Results"
echo "=========================================="
echo -e "  ${GREEN}Passed${NC}: $PASS"
echo -e "  ${RED}Failed${NC}: $FAIL"
echo -e "  ${YELLOW}Warnings${NC}: $WARN"
echo ""

if [ "$FAIL" -eq 0 ]; then
    echo -e "${GREEN}🎉 v0.4 VERIFICATION PASSED${NC}"
    exit 0
else
    echo -e "${RED}💀 v0.4 VERIFICATION FAILED — $FAIL issue(s)${NC}"
    exit 1
fi
```

---

## 10. CLAUDE.md Additions

Append the following to the project's `CLAUDE.md`:

```markdown
## v0.4 Implementation Notes

### Current milestone: v0.4 (Tools & Reach)
### PDR: docs/PDR-v7.md
### Verify: tools/verify/v04_verify.sh

### Before starting:
1. Read §2 of PDR-v7.md (Existing Code Inventory) — understand what already exists
2. Capture test baseline: `grep -r "func Test" internal/ cmd/ tools/ | wc -l > .test-count-baseline`
3. Run `just check` to confirm clean starting state

### Implementation order (strict):
1. MCP Client Full Implementation (internal/mcp/, internal/policy/, internal/config/, internal/tools/, internal/engine/, main.go)
2. Async Delegation (internal/persistence/, internal/tools/, internal/engine/, internal/tui/)
3. Telegram Deep Integration (internal/channels/, internal/tools/, internal/coordinator/)
4. A2A Protocol (internal/gateway/, internal/config/)

### Schema version:
- Current: v8 (gc-v8-2026-02-14-agent-history)
- Target: v9 (add delegations table)
- Update BOTH schemaVersionCurrent AND checksum in persistence/store.go

### Key existing code to extend (do NOT replace):
- MCP Manager Start/Stop lifecycle in main.go (extend with per-agent)
- OnAgentCreated hook in main.go (update MCP section, keep skills/WASM/shell)
- policy.AllowCapability (keep unchanged; AllowMCPTool is additive)
- Coordinator executor, plan loader, retry (extend with HITL, don't rewrite)
- Telegram constructor and basic @agent routing (extend with events)
- Sync delegate_task tool (keep; async is a new separate tool)
- MCPServerEntry.Env is currently []string — support both []string and map[string]string

### Key patterns:
- All new tables: SQLite, WAL mode, migration in store.go, bump schema version
- All new tools: register in internal/tools/catalog.go
- Bus events: define constants near publisher, not centralized
- Tests: table-driven, OFFLINE (zero API credits), -count=1, guard Brain nil
- All new files: follow conventions of neighboring files in same package

### Do NOT:
- Replace existing MCP Manager — extend it
- Break the OnAgentCreated provisioning hook — update it
- Add cloud dependencies (local-first is non-negotiable)
- Modify the 8-state task machine
- Make real API calls in tests (GEMINI_API_KEY="" in test setup)
- Forget backward compat for MCPServerEntry.Env []string format

### Verify after each phase:
Run phase-specific tests, then `just check`, then `go test -race ./...`

### Verify at end:
./tools/verify/v04_verify.sh
```

---

## 11. Risk Register

| Risk | Impact | Mitigation |
|------|--------|------------|
| MCP server crashes during discovery | Agent starts without tools | Cache last-known tool list; non-fatal on discovery failure |
| Env field type migration breaks existing configs | Config load fails | `UnmarshalYAML` accepts both `[]string` and `map[string]string` |
| Telegram API rate limits on progress updates | Updates dropped | Debounce: max 1 msg/2s per chat; batch step updates |
| Async delegation result arrives during compaction | Result lost | Inject before compaction; system messages exempt from compaction |
| HITL approval timeout — user never responds | Plan hangs forever | Configurable timeout (default 1h); auto-reject on expiry |
| Schema migration v8→v9 fails on existing DB | Startup crash | Migration is additive only (new table); test with real v0.3 databases |
| A2A spec changes after implementation | Stale schema | Minimal surface (read-only); trivial to update; version-stamp in code |
| Per-agent MCP doubles memory for shared servers | Resource waste | Share global connections; per-agent only for inline definitions |
| Reconnection backoff floods logs | Log noise | Cap at 10 attempts; log at DEBUG level after first 3 |
| Both sync and async delegate confuse LLM | Wrong tool chosen | Clear tool descriptions; system prompt guidance |
| Coordinator bus not injected | Events never published | Check executor constructor; add bus if missing |

---

## 12. Definition of Done

v0.4 is **done** when:

1. `./tools/verify/v04_verify.sh` exits with code 0 and 0 failures
2. All items in §8.3 Acceptance Criteria Checklist are checked
3. `just check` passes
4. `go test -race ./... -count=1` is clean
5. `go vet ./...` is clean
6. Schema version is v9 in `internal/persistence/store.go`
7. Version string is `v0.4-dev` in `cmd/goclaw/main.go`
8. README.md status table updated (MCP Client → Stable)
9. PROGRESS.md updated with v0.4 completion entry
10. Git tag `v0.4-dev` created
