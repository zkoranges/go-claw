# PDR: The Happy Path (v0.2)

**Status**: READY FOR IMPLEMENTATION
**Version**: 5.0 (final)
**Date**: 2026-02-15
**Depends on**: PDR v3 + v4 complete
**Target**: go-claw v0.2
**Phases**: 5 sequential phases, each with hard verification gates

---

## What This PDR Does

v0.1 built the engine. v0.2 builds the car. After this PDR, a new user installs GoClaw, sees three working agents, types `@coder fix this bug`, and gets value immediately. No YAML editing required on first run.

**What ships at the end:**
- `@agent` routes a single message, `@@agent` switches sticky context
- 3 starter agents (`@coder`, `@researcher`, `@writer`) generated on first run
- `Ctrl+N` or `/agent new` opens agent creation modal with model dropdown
- `goclaw pull <url>` fetches and installs agent configs from any URL
- Collapsible activity feed shows delegation and plan progress
- Error messages rewritten for humans
- `/help` updated with all new syntax
- README updated to reflect v0.2 UX
- Version bumped to v0.2-dev

---

## Execution Protocol

Same rules as previous PDRs. Non-negotiable:

1. **Read before writing.** Run every context-gathering command. Codebase is truth, not PDR.
2. **One step at a time.** Complete each step fully before starting the next.
3. **Compilation after every edit.** `go build ./...` after every file change.
4. **Hard gate between phases.** Every gate command must pass.
5. **Commit after each gate.** `git add -A && git commit -m "PDR v5 Phase N: <description>"`
6. **Rollback on catastrophic failure.** `git reset --hard HEAD~1` and retry phase.
7. **Match existing code style.** Read 2-3 files in same package before writing.

---

## Pre-Flight

### Build Verification

```bash
git status
just check
go test -race ./...
```

All must pass before proceeding.

### Context Gathering

Run ALL of these. Record output. Every phase references these results.

```bash
# === TUI Architecture ===

# Main model struct — field names, types
grep -B2 -A30 "type Model struct\|type model struct" internal/tui/*.go

# Update function — how tea.Msg is dispatched
grep -B2 -A30 "func.*Update.*tea.Msg\|func.*Update.*Msg" internal/tui/*.go | head -80

# View function — how screen is composed
grep -B2 -A15 "func.*View()" internal/tui/*.go | head -40

# Input submission — what happens on Enter
grep -B5 -A30 "KeyEnter\|Submit\|handleInput\|sendMessage\|processInput" internal/tui/*.go | head -80

# Command dispatch — how /agent, /model, /help are routed
grep -B3 -A20 'strings.HasPrefix.*"/"\|case "/\|handleCommand\|processCommand' internal/tui/*.go | head -80

# All TUI source files
ls -la internal/tui/*.go

# TUI constructor — what dependencies are injected
grep -B5 -A20 "func New\|func InitialModel\|tui.New\|tui.Model{" internal/tui/*.go cmd/goclaw/main.go | head -50

# TUI interfaces
grep -B2 -A10 "interface {" internal/tui/*.go

# Does TUI already have any modal/overlay?
grep -rn "modal\|overlay\|popup\|dialog\|form" internal/tui/*.go | head -20

# What Bubbletea components are in use
grep -n "textinput\|textarea\|list\|viewport\|spinner\|table" internal/tui/*.go | head -20

# Key bindings — existing shortcuts
grep -n "KeyMap\|key.Binding\|KeyCtrl\|ctrl+" internal/tui/*.go | head -20

# Init function — existing tea.Cmd batch
grep -B5 -A15 "func.*Init()" internal/tui/*.go | head -30

# === Agent Switcher ===

# AgentSwitcher interface — all methods
grep -B2 -A20 "AgentSwitcher" internal/tui/*.go | head -40

# AgentInfo struct
grep -B2 -A10 "AgentInfo" internal/tui/*.go | head -20

# How SwitchAgent works — what does it return/set?
grep -B5 -A20 "SwitchAgent\|switchAgent" internal/tui/*.go cmd/goclaw/main.go | head -40

# Where active agent ID is stored in TUI model
grep -n "activeAgent\|currentAgent\|agentID\|selectedAgent\|brain\|Brain" internal/tui/*.go | head -20

# tuiAgentSwitcher in main.go — full implementation
grep -B5 -A40 "tuiAgentSwitcher" cmd/goclaw/main.go | head -60

# CreateAgent signature
grep -B2 -A10 "func.*CreateAgent" cmd/goclaw/main.go internal/tui/*.go | head -20

# === Message Sending Path (critical for @mention routing) ===

# How a chat message is sent — does it accept agent ID override?
grep -B5 -A15 "CreateChatTask\|QueueTask\|sendChat\|submitMessage" internal/tui/*.go internal/engine/*.go | head -40

# Can tasks be created with a specific agent ID?
grep -B5 -A10 "AgentID\|agent_id\|agentID" internal/persistence/store.go | head -20

# === Config ===

# Config struct — all fields
grep -B2 -A40 "type Config struct" internal/config/config.go

# AgentConfigEntry — exact field names (CRITICAL for starters and pull)
grep -B2 -A30 "type AgentConfigEntry struct\|type AgentEntry struct" internal/config/config.go

# Does Capabilities field exist in config?
grep -n "Capabilities\|capabilities" internal/config/config.go

# How config is loaded — Load function signature
grep -B2 -A10 "func Load\|func ReadConfig\|func ParseConfig" internal/config/config.go | head -20

# How config is saved/written (if method exists)
grep -n "Marshal\|WriteFile\|SaveConfig\|writeConfig\|Save" internal/config/config.go | head -10

# First-run config generation — where does it happen?
grep -B10 -A30 "initConfig\|generateDefault\|defaultConfig\|firstRun\|createDefault\|exist\|NotExist" internal/config/config.go cmd/goclaw/main.go | head -80

# Default config template — is it string literal, embedded, or struct?
grep -B5 -A30 "defaultConfig\|sampleConfig\|templateConfig\|DefaultConfig" internal/config/*.go | head -60

# YAML tags on AgentConfigEntry — field name mapping
grep "yaml:" internal/config/config.go | head -20

# === Agent Registry ===

# All public methods
grep -n "func.*Registry.*)" internal/agent/registry.go | head -20

# How agents register at runtime
grep -B3 -A10 "func.*Register\|func.*Add\|func.*Create" internal/agent/registry.go | head -30

# === Bus Events ===

# All event types published
grep -rn 'Publish(' internal/ --include="*.go" | head -30

# Bus Subscribe — callback or channel pattern?
grep -B5 -A15 "func.*Subscribe" internal/bus/*.go

# Bus Event struct
grep -B2 -A10 "type Event struct\|type Message struct" internal/bus/*.go | head -20

# === CLI Subcommands ===

# How subcommands dispatch in main.go
grep -B10 -A25 'os.Args\|case.*"doctor"\|case.*"daemon"' cmd/goclaw/main.go | head -50

# === Help Text ===

# Current help/command reference
grep -B5 -A40 "helpText\|/help\|commandHelp\|availableCommands" internal/tui/*.go | head -80

# === Error Display ===

# How errors are shown to user in TUI
grep -B5 -A10 "Error\|error\|errMsg\|ErrorMsg" internal/tui/*.go | head -30

# === Model Strings ===

# What model strings the brain accepts
grep -rn "gemini\|claude\|gpt-4\|model" internal/engine/brain*.go internal/config/config.go | grep -i "string\|const\|default\|valid" | head -30
```

---

# PHASE 1: @Mentions

**Goal**: `@agent` and `@@agent` route messages to named agents. Replaces `/agent switch` as primary interaction.
**Files created**: `internal/tui/mention.go`, `internal/tui/mention_test.go`
**Files modified**: TUI input handler file (identified in pre-flight)
**Risk**: Medium. TUI input handling is central.

## Step 1.1: Understand Current Input Flow

Before writing any code, trace exactly how user input flows from keypress to agent:

```bash
# The input handler — what happens when user presses Enter
grep -B10 -A30 "KeyEnter\|Submit" internal/tui/*.go

# How the message gets to the agent brain
grep -B5 -A20 "sendMessage\|SendChat\|processInput\|handleSubmit" internal/tui/*.go | head -60

# Where the "active agent" is tracked in the TUI model
grep -n "activeAgent\|currentAgent\|agentID\|selectedAgent" internal/tui/*.go | head -20

# CRITICAL: Can a message be sent to a specific agent without switching?
# Look for an agent ID parameter in the message-sending path
grep -B5 -A10 "CreateChatTask\|QueueTask\|sendChat" internal/tui/*.go internal/engine/*.go | head -40
```

**Map the flow**: keypress → input string → command check → agent routing → brain call.
Your @mention parsing inserts between "command check" and "agent routing."

**Determine routing approach**: Check if the message-sending path accepts an agent ID override:
- If YES (agent ID is a parameter): implement single-message routing via override (Approach A — preferred)
- If NO (agent is implicit from TUI state): `@agent` becomes equivalent to `@@agent` (always sticky) for v0.2. Add a TODO for single-message routing in v0.2.1.

Record which approach you are using. This affects Step 1.4.

## Step 1.2: Add @Mention Parser

**File**: `internal/tui/mention.go` (new file)

```go
package tui

import "strings"

// MentionResult holds the parsed result of an @mention in user input.
type MentionResult struct {
	AgentID string // Target agent ID (empty if no mention)
	Message string // Message with @mention stripped
	Sticky  bool   // True if @@ was used or @agent with no message
}

// ParseMention extracts @agent or @@agent from the beginning of input.
//
// Rules:
//   - /commands are never treated as mentions
//   - @agent <msg> routes single message (Sticky=false)
//   - @agent with no message = sticky switch (same as @@agent)
//   - @@agent switches permanently (Sticky=true)
//   - @@agent <msg> switches permanently and sends message
//   - Agent IDs: a-z, A-Z, 0-9, hyphens. Must start alphanumeric. No trailing hyphen.
func ParseMention(input string) MentionResult {
	input = strings.TrimSpace(input)

	// Commands take priority
	if strings.HasPrefix(input, "/") {
		return MentionResult{Message: input}
	}

	// Check for @@ (sticky switch)
	if strings.HasPrefix(input, "@@") {
		rest := input[2:]
		agentID, message := extractAgentID(rest)
		if agentID != "" {
			return MentionResult{
				AgentID: agentID,
				Message: strings.TrimSpace(message),
				Sticky:  true,
			}
		}
		return MentionResult{Message: input}
	}

	// Check for @ (single message or sticky if no message)
	if strings.HasPrefix(input, "@") {
		rest := input[1:]
		agentID, message := extractAgentID(rest)
		if agentID != "" {
			msg := strings.TrimSpace(message)
			return MentionResult{
				AgentID: agentID,
				Message: msg,
				Sticky:  msg == "", // no message = sticky
			}
		}
		return MentionResult{Message: input}
	}

	return MentionResult{Message: input}
}

// extractAgentID pulls a valid agent ID token from the start of text.
func extractAgentID(text string) (agentID string, rest string) {
	if len(text) == 0 {
		return "", text
	}
	if !isAgentIDStart(rune(text[0])) {
		return "", text
	}

	for i, r := range text {
		if r == ' ' || r == '\t' || r == '\n' {
			id := text[:i]
			if id[len(id)-1] == '-' {
				return "", text
			}
			return id, text[i:]
		}
		if !isAgentIDChar(r) {
			return "", text
		}
	}

	id := text
	if id[len(id)-1] == '-' {
		return "", text
	}
	return id, ""
}

func isAgentIDStart(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func isAgentIDChar(r rune) bool {
	return isAgentIDStart(r) || r == '-'
}
```

**Verify**: `go build ./internal/tui/`

## Step 1.3: Test @Mention Parser

**File**: `internal/tui/mention_test.go`

```go
package tui

import "testing"

func TestParseMention(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		agentID string
		message string
		sticky  bool
	}{
		// Basic routing
		{"single message", "@coder fix this bug", "coder", "fix this bug", false},
		{"sticky @@", "@@coder", "coder", "", true},
		{"sticky @@ with message", "@@researcher find papers on RAG", "researcher", "find papers on RAG", true},

		// @agent with no message = sticky
		{"bare @agent is sticky", "@coder", "coder", "", true},

		// No mention
		{"plain message", "hello world", "", "hello world", false},
		{"empty input", "", "", "", false},
		{"whitespace only", "   ", "", "", false},

		// Commands take priority
		{"slash command", "/help", "", "/help", false},
		{"slash agent command", "/agent switch coder", "", "/agent switch coder", false},

		// Agent ID validation
		{"hyphenated ID", "@code-reviewer check PR", "code-reviewer", "check PR", false},
		{"invalid chars", "@agent! foo", "", "@agent! foo", false},
		{"bare @", "@", "", "@", false},
		{"bare @@", "@@", "", "@@", false},
		{"@ space", "@ hello", "", "@ hello", false},
		{"trailing hyphen", "@coder- fix this", "", "@coder- fix this", false},

		// Whitespace handling
		{"leading whitespace", "  @coder fix", "coder", "fix", false},

		// Case and numbers
		{"uppercase", "@Coder fix this", "Coder", "fix this", false},
		{"numeric", "@agent1 do stuff", "agent1", "do stuff", false},

		// Long message
		{"long message", "@writer write a blog post about distributed systems and why they fail",
			"writer", "write a blog post about distributed systems and why they fail", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := ParseMention(tt.input)
			if r.AgentID != tt.agentID {
				t.Errorf("AgentID: got %q, want %q", r.AgentID, tt.agentID)
			}
			if r.Message != tt.message {
				t.Errorf("Message: got %q, want %q", r.Message, tt.message)
			}
			if r.Sticky != tt.sticky {
				t.Errorf("Sticky: got %v, want %v", r.Sticky, tt.sticky)
			}
		})
	}
}
```

**Verify**: `go test ./internal/tui/ -v -run "ParseMention" -count=1`

## Step 1.4: Wire @Mention into TUI Input Handler

**File**: The TUI file that handles input submission (identified in Step 1.1)

Find the exact location where user input is processed after pressing Enter:

```bash
grep -n "KeyEnter\|handleSubmit\|processInput\|sendMessage" internal/tui/*.go
```

Insert mention parsing BEFORE the existing command check and agent routing.

**Two implementation paths based on Step 1.1 findings:**

### Path A: Message Path Accepts Agent ID Override

If `CreateChatTask` or equivalent accepts an agent ID parameter:

```go
input := /* however the TUI captures input text — ADAPT */

mention := ParseMention(input)

if mention.AgentID != "" {
    // Validate agent exists
    agentIDs := m.agentSwitcher.ListAgentIDs() // ADAPT
    found := false
    for _, id := range agentIDs {
        if id == mention.AgentID {
            found = true
            break
        }
    }
    if !found {
        available := strings.Join(agentIDs, ", @")
        // ADAPT: show error using existing TUI pattern
        // Message: "Unknown agent: @X. Available: @coder, @researcher, @writer"
        return m, nil
    }

    if mention.Sticky {
        // Permanent switch — same as /agent switch
        // ADAPT: call existing SwitchAgent, update model fields
        if mention.Message != "" {
            // Send message to new agent
        }
    } else {
        // Single-message route: send to target agent without switching
        // ADAPT: pass mention.AgentID as override to CreateChatTask
    }
    return m, cmd
}
// EXISTING: command handling, normal message sending
```

### Path B: Sticky-Only Fallback

If the message path does NOT support an agent ID override:

```go
input := /* ADAPT */
mention := ParseMention(input)

if mention.AgentID != "" {
    // Validate agent exists (same as Path A)
    // ...

    // ALL mentions become sticky (single-message routing deferred)
    // ADAPT: call existing SwitchAgent
    m.agentSwitcher.SwitchAgent(mention.AgentID)
    if mention.Message != "" {
        // Send message to now-active agent
    }
    // TODO(v0.2.1): single-message routing without context switch
    return m, cmd
}
```

**CRITICAL ADAPT checklist** (verify all before writing):
- [ ] TUI model field for active agent ID (name?)
- [ ] SwitchAgent return signature (brain, name, prompt, error?)
- [ ] Which model fields to update after switch
- [ ] Message sending: tea.Cmd (async) or synchronous?
- [ ] Pointer vs value receiver on Update

**Verify**: `go build ./...`

## Step 1.5: Visual Feedback

### Chat History Highlighting

Find message rendering:
```bash
grep -B5 -A15 "renderMessage\|formatMessage\|messageStyle\|chatView\|contentView" internal/tui/*.go | head -40
```

Add: when a user message starts with `@agent`, render the @token in cyan:

```go
func highlightMention(content string) string {
    if !strings.HasPrefix(content, "@") {
        return content
    }
    spaceIdx := strings.IndexByte(content, ' ')
    if spaceIdx == -1 {
        return lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Render(content)
    }
    mention := content[:spaceIdx]
    rest := content[spaceIdx:]
    return lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Render(mention) + rest
}
```

Call in user message rendering path.

### Input Prompt

Update the prompt to show active agent:
```bash
grep -n "placeholder\|prompt\|Prompt\|cursor\|inputStyle" internal/tui/*.go | head -20
```

Change from `> ` to `coder> ` (showing active agent name).

**Verify**: `go build ./...`

## GATE 1

```bash
just check
go test -race ./...
go test ./internal/tui/ -v -run "ParseMention" -count=1

# Parser exists
grep -n "func ParseMention" internal/tui/mention.go
grep -n "type MentionResult" internal/tui/mention.go

# Tests are comprehensive
grep -c "t.Run\|func Test" internal/tui/mention_test.go
# Should be >= 15 cases

# Wired into input handler
grep -n "ParseMention\|mention" internal/tui/*.go | grep -v _test.go | grep -v mention.go

# Agent validation
grep -n "Unknown agent\|unknown agent\|not found" internal/tui/*.go | grep -v _test.go

# Mention highlighting
grep -n "highlightMention\|Color.*6" internal/tui/*.go | grep -v _test.go
```

If all pass: `git add -A && git commit -m "PDR v5 Phase 1: @mentions"`

---

# PHASE 2: Starter Agents

**Goal**: First run generates 3 high-quality agents in config.yaml. No YAML editing needed.
**Files created**: `internal/config/starters.go`, `internal/config/starters_test.go`
**Files modified**: config generation path (identified in pre-flight)
**Risk**: Low. Purely additive to first-run config generation.

## Step 2.1: Understand First-Run Config Generation

```bash
grep -B10 -A30 "initConfig\|generateDefault\|createDefault\|firstRun\|NotExist" internal/config/config.go cmd/goclaw/main.go | head -80
grep -rn "os.WriteFile\|os.Create\|ioutil.WriteFile\|config.yaml" internal/config/config.go cmd/goclaw/main.go | head -10
```

Map: where is the default config template? Is it string literal, embedded, or struct?

## Step 2.2: Define Starter Agent Configs

**File**: `internal/config/starters.go` (new file)

**BEFORE writing this file**, get exact struct fields:
```bash
grep -B2 -A30 "type AgentConfigEntry struct" internal/config/config.go
grep -n "Capabilities\|capabilities" internal/config/config.go
```

If `Capabilities` is missing from `AgentConfigEntry`, add it:
```go
Capabilities []string `yaml:"capabilities,omitempty"`
```

Then write the starters file. All field names MUST match the actual struct:

```go
package config

// StarterAgents returns default agents for first-run setup.
// Generated into config.yaml only when no agents are configured.
func StarterAgents() []AgentConfigEntry {
    return []AgentConfigEntry{
        {
            ID:          "coder",
            DisplayName: "Coder",
            // ADAPT: Soul field may be named SystemPrompt, Prompt, etc.
            Soul: `You are a senior software engineer. You write clean, idiomatic code with clear error handling. When asked to fix bugs, you first reproduce the issue, then explain the root cause, then provide a minimal fix. You prefer simple solutions over clever ones. When reviewing code, you check for: correctness, edge cases, error handling, naming clarity, and unnecessary complexity. You know Go, Python, TypeScript, Rust, and shell scripting well. You always explain your reasoning.`,
            Capabilities: []string{"coding", "debugging", "code-review", "refactoring"},
        },
        {
            ID:          "researcher",
            DisplayName: "Researcher",
            Soul: `You are a thorough research assistant. When asked to investigate a topic, you search for primary sources, cross-reference claims, and clearly distinguish between established facts and speculation. You cite your sources. You present findings in a structured way: summary first, then details, then open questions. When comparing options, you use tables and clear criteria. You flag when information might be outdated.`,
            Capabilities: []string{"research", "search", "analysis", "comparison"},
        },
        {
            ID:          "writer",
            DisplayName: "Writer",
            Soul: `You are a skilled technical writer. You write clear, concise documentation that respects the reader's time. You adapt your style to the format: READMEs are scannable with examples, API docs are precise with types, blog posts have personality and narrative flow, commit messages are imperative and specific. You ask about the target audience when it's unclear. You avoid jargon unless writing for specialists.`,
            Capabilities: []string{"writing", "documentation", "editing", "communication"},
        },
    }
}
```

**Verify**: `go build ./internal/config/`

## Step 2.3: Test Starter Agents

**File**: `internal/config/starters_test.go`

```go
package config

import "testing"

func TestStarterAgents_Count(t *testing.T) {
    agents := StarterAgents()
    if len(agents) != 3 {
        t.Fatalf("expected 3 starter agents, got %d", len(agents))
    }
}

func TestStarterAgents_ExpectedIDs(t *testing.T) {
    agents := StarterAgents()
    expected := map[string]bool{"coder": true, "researcher": true, "writer": true}
    for _, a := range agents {
        if !expected[a.ID] {
            t.Errorf("unexpected agent ID: %q", a.ID)
        }
        delete(expected, a.ID)
    }
    for missing := range expected {
        t.Errorf("missing expected agent: %q", missing)
    }
}

func TestStarterAgents_FieldsNonEmpty(t *testing.T) {
    for _, a := range StarterAgents() {
        if a.ID == "" {
            t.Error("agent has empty ID")
        }
        if a.DisplayName == "" {
            t.Errorf("agent %s: empty DisplayName", a.ID)
        }
        // ADAPT: field name for soul/prompt
        if a.Soul == "" {
            t.Errorf("agent %s: empty Soul", a.ID)
        }
        if len(a.Capabilities) == 0 {
            t.Errorf("agent %s: no capabilities", a.ID)
        }
    }
}

func TestStarterAgents_UniqueIDs(t *testing.T) {
    seen := make(map[string]bool)
    for _, a := range StarterAgents() {
        if seen[a.ID] {
            t.Errorf("duplicate agent ID: %q", a.ID)
        }
        seen[a.ID] = true
    }
}
```

**Verify**: `go test ./internal/config/ -v -run "StarterAgent" -count=1`

## Step 2.4: Wire Starter Agents into First-Run

Modify the first-run config path to include starters:

```go
// ADAPT to match actual first-run code structure:
if len(cfg.Agents) == 0 {
    cfg.Agents = StarterAgents()
}
```

**Verify** (using isolated directory — NEVER use ~/.goclaw in tests):
```bash
go build ./...
TEST_HOME=/tmp/test-goclaw-starters-$$
rm -rf "$TEST_HOME"
GOCLAW_HOME="$TEST_HOME" just build
GOCLAW_HOME="$TEST_HOME" timeout 3s ./dist/goclaw 2>/dev/null || true
grep "coder" "$TEST_HOME/config.yaml" && echo "PASS" || echo "FAIL"
grep "researcher" "$TEST_HOME/config.yaml" && echo "PASS" || echo "FAIL"
grep "writer" "$TEST_HOME/config.yaml" && echo "PASS" || echo "FAIL"
rm -rf "$TEST_HOME"
```

## GATE 2

```bash
just check
go test -race ./...
go test ./internal/config/ -v -run "StarterAgent"

grep -n "func StarterAgents" internal/config/starters.go
grep -rn "StarterAgents" cmd/goclaw/main.go internal/config/config.go

# Functional (isolated)
TEST_HOME=/tmp/test-goclaw-gate2-$$; rm -rf "$TEST_HOME"
GOCLAW_HOME="$TEST_HOME" just build
GOCLAW_HOME="$TEST_HOME" timeout 3s ./dist/goclaw 2>/dev/null || true
grep -c "coder\|researcher\|writer" "$TEST_HOME/config.yaml"
rm -rf "$TEST_HOME"
```

If all pass: `git add -A && git commit -m "PDR v5 Phase 2: starter agents"`

---

# PHASE 3: Agent Creation Modal

**Goal**: `Ctrl+N` or `/agent new` opens a Bubbletea form overlay. Created agent optionally persisted to config.yaml.
**Files created**: `internal/tui/modal.go`, `internal/tui/modal_test.go`
**Files modified**: TUI model (struct, Update, View)
**Risk**: Medium-high. Bubbletea overlay rendering.

## Step 3.0: Pre-Flight

```bash
grep -rn "modal\|overlay\|popup\|dialog\|form" internal/tui/*.go | head -20
cat internal/tui/*.go | grep -B5 -A10 "func.*View()" | head -40
grep -n "textinput\|textarea\|list\|viewport" internal/tui/*.go | head -20
grep -n "ctrl+\|Ctrl\|KeyCtrl" internal/tui/*.go | head -20
grep -n "WindowSizeMsg\|width\|height\|Width\|Height" internal/tui/*.go | head -15
```

## Step 3.1: Create Modal Model

**File**: `internal/tui/modal.go`

```go
package tui

import (
    "strings"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
)

type ModalState int

const (
    ModalClosed ModalState = iota
    ModalOpen
)

const modalFieldCount = 5 // ID, Model, Soul, Checkbox, Button

type AgentModal struct {
    state        ModalState
    focusIndex   int
    idField      string
    soulField    string
    modelOptions []string
    modelIndex   int
    saveToConfig bool
    err          string
}

func NewAgentModal(modelOptions []string) AgentModal {
    if len(modelOptions) == 0 {
        modelOptions = []string{"default"}
    }
    return AgentModal{state: ModalClosed, modelOptions: modelOptions, saveToConfig: true}
}

func (m *AgentModal) Open() {
    m.state = ModalOpen
    m.focusIndex = 0
    m.idField = ""
    m.soulField = ""
    m.modelIndex = 0
    m.saveToConfig = true
    m.err = ""
}

func (m *AgentModal) Close()             { m.state = ModalClosed }
func (m AgentModal) IsOpen() bool        { return m.state == ModalOpen }
func (m AgentModal) FocusIndex() int     { return m.focusIndex }
func (m AgentModal) IDField() string     { return m.idField }
func (m AgentModal) SoulField() string   { return m.soulField }
func (m AgentModal) Err() string         { return m.err }

type AgentCreatedMsg struct {
    ID, Model, Soul string
    SaveToConfig    bool
}
type ModalCancelledMsg struct{}

func (m *AgentModal) Update(msg tea.KeyMsg) tea.Cmd {
    switch msg.String() {
    case "esc":
        m.Close()
        return func() tea.Msg { return ModalCancelledMsg{} }
    case "tab", "down":
        m.focusIndex = (m.focusIndex + 1) % modalFieldCount
        return nil
    case "shift+tab", "up":
        m.focusIndex = (m.focusIndex + modalFieldCount - 1) % modalFieldCount
        return nil
    case "enter":
        switch m.focusIndex {
        case 3:
            m.saveToConfig = !m.saveToConfig
            return nil
        case 4:
            return m.submit()
        default:
            m.focusIndex = (m.focusIndex + 1) % modalFieldCount
            return nil
        }
    case "left", "right":
        if m.focusIndex == 1 {
            if msg.String() == "left" {
                m.modelIndex = (m.modelIndex - 1 + len(m.modelOptions)) % len(m.modelOptions)
            } else {
                m.modelIndex = (m.modelIndex + 1) % len(m.modelOptions)
            }
            return nil
        }
    case "backspace":
        switch m.focusIndex {
        case 0:
            if len(m.idField) > 0 {
                m.idField = m.idField[:len(m.idField)-1]
            }
        case 2:
            if len(m.soulField) > 0 {
                m.soulField = m.soulField[:len(m.soulField)-1]
            }
        }
        return nil
    default:
        switch m.focusIndex {
        case 0:
            for _, r := range msg.String() {
                if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
                    m.idField += string(r)
                }
            }
        case 2:
            m.soulField += msg.String()
        }
    }
    return nil
}

func (m *AgentModal) submit() tea.Cmd {
    id := strings.TrimSpace(m.idField)
    if id == "" {
        m.err = "Agent ID is required"
        return nil
    }
    model := m.modelOptions[m.modelIndex]
    soul := strings.TrimSpace(m.soulField)
    m.Close()
    return func() tea.Msg {
        return AgentCreatedMsg{ID: id, Model: model, Soul: soul, SaveToConfig: m.saveToConfig}
    }
}

func (m AgentModal) View() string {
    if !m.IsOpen() {
        return ""
    }

    border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
        BorderForeground(lipgloss.Color("62")).Padding(1, 2).Width(54)
    title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("62"))
    focus := lipgloss.NewStyle().Foreground(lipgloss.Color("86"))
    dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
    errS := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

    mk := func(idx int) string {
        if m.focusIndex == idx {
            return focus.Render("▸ ")
        }
        return "  "
    }

    var b strings.Builder
    b.WriteString(title.Render("Create New Agent") + "\n\n")
    b.WriteString(mk(0) + "ID:    [ " + m.idField + " ]\n")
    b.WriteString(mk(1) + "Model: [ ◀ " + m.modelOptions[m.modelIndex] + " ▶ ]\n")
    soulPreview := m.soulField
    if len(soulPreview) > 35 {
        soulPreview = soulPreview[:35] + "..."
    }
    b.WriteString(mk(2) + "Soul:  [ " + soulPreview + " ]\n\n")
    check := "[ ]"
    if m.saveToConfig {
        check = "[x]"
    }
    b.WriteString(mk(3) + check + " Save to config.yaml\n\n")
    btn := "[ Create ]"
    if m.focusIndex == 4 {
        btn = focus.Render("[ Create ]")
    }
    b.WriteString("  " + btn + dim.Render("  (Esc to cancel)") + "\n")
    if m.err != "" {
        b.WriteString("\n" + errS.Render("  ⚠ "+m.err))
    }
    return border.Render(b.String())
}
```

**Verify**: `go build ./internal/tui/`

## Step 3.2: Test Modal

**File**: `internal/tui/modal_test.go`

```go
package tui

import (
    "testing"

    tea "github.com/charmbracelet/bubbletea"
)

func specialKey(k string) tea.KeyMsg {
    // ADAPT: verify Bubbletea key type constructors match installed version
    switch k {
    case "esc":
        return tea.KeyMsg{Type: tea.KeyEscape}
    case "tab":
        return tea.KeyMsg{Type: tea.KeyTab}
    case "enter":
        return tea.KeyMsg{Type: tea.KeyEnter}
    case "backspace":
        return tea.KeyMsg{Type: tea.KeyBackspace}
    default:
        return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
    }
}

func keyMsg(k string) tea.KeyMsg {
    return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

func TestAgentModal_OpenClose(t *testing.T) {
    m := NewAgentModal([]string{"a", "b"})
    if m.IsOpen() {
        t.Fatal("should start closed")
    }
    m.Open()
    if !m.IsOpen() {
        t.Fatal("should be open")
    }
    m.Close()
    if m.IsOpen() {
        t.Fatal("should be closed")
    }
}

func TestAgentModal_OpenResets(t *testing.T) {
    m := NewAgentModal([]string{"a"})
    m.Open()
    m.idField = "old"
    m.soulField = "old"
    m.focusIndex = 3
    m.Open()
    if m.idField != "" || m.soulField != "" || m.focusIndex != 0 {
        t.Fatal("Open() should reset state")
    }
}

func TestAgentModal_EscCancels(t *testing.T) {
    m := NewAgentModal([]string{"a"})
    m.Open()
    cmd := m.Update(specialKey("esc"))
    if m.IsOpen() {
        t.Fatal("Esc should close")
    }
    if cmd == nil {
        t.Fatal("should return ModalCancelledMsg cmd")
    }
    if _, ok := cmd().(ModalCancelledMsg); !ok {
        t.Fatal("cmd should produce ModalCancelledMsg")
    }
}

func TestAgentModal_TabCycles(t *testing.T) {
    m := NewAgentModal([]string{"a"})
    m.Open()
    for i := 0; i < modalFieldCount; i++ {
        if m.FocusIndex() != i {
            t.Fatalf("step %d: expected focus %d, got %d", i, i, m.FocusIndex())
        }
        m.Update(specialKey("tab"))
    }
    if m.FocusIndex() != 0 {
        t.Fatal("should wrap to 0")
    }
}

func TestAgentModal_TypeID(t *testing.T) {
    m := NewAgentModal([]string{"a"})
    m.Open()
    m.Update(keyMsg("m"))
    m.Update(keyMsg("y"))
    m.Update(keyMsg("-"))
    m.Update(keyMsg("a"))
    if m.IDField() != "my-a" {
        t.Fatalf("got %q", m.IDField())
    }
}

func TestAgentModal_IDRejectsInvalid(t *testing.T) {
    m := NewAgentModal([]string{"a"})
    m.Open()
    m.Update(keyMsg("a"))
    m.Update(keyMsg("!"))
    m.Update(keyMsg("B")) // uppercase rejected
    m.Update(keyMsg("c"))
    if m.IDField() != "ac" {
        t.Fatalf("got %q", m.IDField())
    }
}

func TestAgentModal_SubmitEmptyID(t *testing.T) {
    m := NewAgentModal([]string{"a"})
    m.Open()
    for i := 0; i < 4; i++ {
        m.Update(specialKey("tab"))
    }
    cmd := m.Update(specialKey("enter"))
    if cmd != nil {
        t.Fatal("empty ID should not produce cmd")
    }
    if m.Err() == "" {
        t.Fatal("should set error")
    }
    if !m.IsOpen() {
        t.Fatal("should stay open on error")
    }
}

func TestAgentModal_SubmitValid(t *testing.T) {
    m := NewAgentModal([]string{"model-a", "model-b"})
    m.Open()
    m.Update(keyMsg("t"))
    m.Update(keyMsg("e"))
    m.Update(keyMsg("s"))
    m.Update(keyMsg("t"))
    for i := 0; i < 4; i++ {
        m.Update(specialKey("tab"))
    }
    cmd := m.Update(specialKey("enter"))
    if cmd == nil {
        t.Fatal("valid submit should produce cmd")
    }
    if m.IsOpen() {
        t.Fatal("should close on valid submit")
    }
    msg, ok := cmd().(AgentCreatedMsg)
    if !ok {
        t.Fatal("should produce AgentCreatedMsg")
    }
    if msg.ID != "test" {
        t.Fatalf("ID: got %q", msg.ID)
    }
    if msg.Model != "model-a" {
        t.Fatalf("Model: got %q", msg.Model)
    }
    if !msg.SaveToConfig {
        t.Fatal("SaveToConfig should default true")
    }
}

func TestAgentModal_NilModelOptions(t *testing.T) {
    m := NewAgentModal(nil)
    if len(m.modelOptions) != 1 || m.modelOptions[0] != "default" {
        t.Fatal("nil options should default to ['default']")
    }
}

func TestAgentModal_ViewClosed(t *testing.T) {
    m := NewAgentModal([]string{"a"})
    if m.View() != "" {
        t.Fatal("closed modal should render empty")
    }
}
```

**Verify**: `go test ./internal/tui/ -v -run "AgentModal" -count=1`

## Step 3.3: Wire Modal into TUI

Add `agentModal AgentModal` and `configPath string` to model struct.

Initialize with `AvailableModels()` from config package.

In Update:
- Modal intercepts all keys when open
- `ctrl+n` → `m.agentModal.Open()`
- `AgentCreatedMsg` → call `CreateAgent` AND `config.AppendAgent` if `SaveToConfig`
- `/agent new` → `m.agentModal.Open()`

In View:
- If modal open, overlay modal view

**SaveToConfig persistence** (CRITICAL — do not skip):
```go
case AgentCreatedMsg:
    // 1. Create runtime agent
    err := m.agentSwitcher.CreateAgent(ctx, msg.ID, msg.ID, "", msg.Model, msg.Soul)
    if err != nil { /* show error */ }

    // 2. Persist to config.yaml if requested
    if msg.SaveToConfig {
        entry := config.AgentConfigEntry{
            ID:   msg.ID,
            Soul: msg.Soul,
            // ADAPT: set Model, Provider, DisplayName fields
        }
        if err := config.AppendAgent(m.configPath, entry); err != nil {
            // Non-fatal: agent runs, just not persisted across restart
            // ADAPT: show warning
        }
    }
```

## Step 3.4: AvailableModels Helper

**File**: `internal/config/models.go` (new file)

```go
package config

import "os"

// AvailableModels returns models based on configured API keys.
func AvailableModels() []string {
    // ADAPT: model strings MUST match what brain accepts
    var models []string
    if os.Getenv("GEMINI_API_KEY") != "" {
        models = append(models, "gemini-2.5-pro", "gemini-2.5-flash")
    }
    if os.Getenv("ANTHROPIC_API_KEY") != "" {
        models = append(models, "claude-sonnet-4-5", "claude-haiku-4-5")
    }
    if os.Getenv("OPENAI_API_KEY") != "" {
        models = append(models, "gpt-4o", "gpt-4o-mini")
    }
    if os.Getenv("OPENROUTER_API_KEY") != "" {
        models = append(models, "openrouter/auto")
    }
    if len(models) == 0 {
        models = []string{"default"}
    }
    return models
}
```

**Verify**: `go build ./...`

## GATE 3

```bash
just check
go test -race ./...
go test ./internal/tui/ -v -run "AgentModal"

ls internal/tui/modal.go
grep -c "func Test" internal/tui/modal_test.go   # >= 10
grep -n "agentModal\|AgentModal" internal/tui/*.go | grep -v _test.go | grep -v modal.go
grep -n "ctrl+n" internal/tui/*.go | grep -v _test.go
grep -n '"new"\|"create"' internal/tui/*.go | grep -v _test.go
grep -n "AppendAgent\|SaveToConfig" internal/tui/*.go | grep -v _test.go
grep -n "func AvailableModels" internal/config/*.go
```

If all pass: `git add -A && git commit -m "PDR v5 Phase 3: agent creation modal"`

---

# PHASE 4: `goclaw pull`

**Goal**: `goclaw pull <url>` fetches agent config from any HTTPS URL, validates, adds to config.yaml.
**Files created**: `cmd/goclaw/pull.go`, `cmd/goclaw/pull_test.go`
**Files modified**: `cmd/goclaw/main.go`, `internal/config/config.go`
**Risk**: Low. New CLI subcommand.

### Shareable Agent YAML Format

Community members publish agent configs like this:

```yaml
# Example: https://gist.github.com/.../senior-go-dev.yaml
id: senior-go-dev
display_name: Senior Go Developer
model: gemini-2.5-pro
soul: |
  You are a senior Go engineer who prefers clean interfaces,
  strict typing, and table-driven tests. You review code for
  correctness, readability, and idiomatic patterns.
capabilities:
  - coding
  - go
  - code-review
```

Required: `id`, `soul`. Everything else optional.

## Step 4.1: Add Config AppendAgent

```bash
grep -n "SaveConfig\|WriteConfig\|Marshal\|WriteFile\|AppendAgent" internal/config/config.go | head -10
```

If no append method exists, add to `internal/config/config.go`:

```go
// AppendAgent adds an agent to config.yaml.
// WARNING: Round-trips through yaml.Marshal — strips comments, may reorder fields.
// TODO(v0.3): preserve user formatting via YAML-aware append.
func AppendAgent(configPath string, agent AgentConfigEntry) error {
    cfg, err := Load(configPath) // ADAPT: match Load signature
    if err != nil {
        return fmt.Errorf("load config: %w", err)
    }
    for _, existing := range cfg.Agents {
        if existing.ID == agent.ID {
            return fmt.Errorf("agent @%s already exists — use a different ID or remove the existing agent first", agent.ID)
        }
    }
    cfg.Agents = append(cfg.Agents, agent)
    data, err := yaml.Marshal(cfg)
    if err != nil {
        return fmt.Errorf("marshal config: %w", err)
    }
    return os.WriteFile(configPath, data, 0644)
}
```

**Verify**: `go build ./internal/config/`

## Step 4.2: Create Pull Subcommand

**File**: `cmd/goclaw/pull.go`

```go
package main

import (
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "strings"
    "time"

    "github.com/basket/go-claw/internal/config"
    "gopkg.in/yaml.v3"
)

type PullableAgent struct {
    ID           string   `yaml:"id"`
    DisplayName  string   `yaml:"display_name"`
    Soul         string   `yaml:"soul"`
    Model        string   `yaml:"model"`
    Provider     string   `yaml:"provider"`
    Capabilities []string `yaml:"capabilities"`
}

func runPullCommand(args []string) int {
    if len(args) < 1 {
        fmt.Fprintln(os.Stderr, `Usage: goclaw pull <url>

Fetches an agent configuration from a URL and adds it to config.yaml.

Examples:
  goclaw pull https://gist.githubusercontent.com/user/abc/raw/agent.yaml
  goclaw pull https://raw.githubusercontent.com/user/repo/main/agents/sre.yaml

Agent YAML format (minimum required: id and soul):
  id: my-agent
  soul: |
    You are a helpful agent.
  display_name: My Agent     # optional
  model: gemini-2.5-pro      # optional
  capabilities: [coding]     # optional`)
        return 1
    }

    url := args[0]
    if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
        fmt.Fprintln(os.Stderr, "Error: URL must start with http:// or https://")
        return 1
    }

    fmt.Printf("Fetching %s...\n", url)
    client := &http.Client{Timeout: 15 * time.Second}
    resp, err := client.Get(url)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error: failed to fetch: %v\n", err)
        return 1
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        fmt.Fprintf(os.Stderr, "Error: server returned %d %s\n", resp.StatusCode, http.StatusText(resp.StatusCode))
        return 1
    }

    ct := resp.Header.Get("Content-Type")
    if strings.Contains(ct, "text/html") {
        fmt.Fprintln(os.Stderr, "Error: URL returned HTML, not YAML. If using GitHub, use the 'Raw' URL.")
        fmt.Fprintln(os.Stderr, "  Tip: https://raw.githubusercontent.com/user/repo/main/file.yaml")
        return 1
    }

    body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error: failed to read response: %v\n", err)
        return 1
    }

    var agent PullableAgent
    if err := yaml.Unmarshal(body, &agent); err != nil {
        fmt.Fprintf(os.Stderr, "Error: invalid YAML: %v\n", err)
        return 1
    }

    if agent.ID == "" {
        fmt.Fprintln(os.Stderr, "Error: agent config missing required 'id' field")
        return 1
    }
    if agent.Soul == "" {
        fmt.Fprintln(os.Stderr, "Error: agent config missing required 'soul' field")
        return 1
    }

    // ADAPT: map all fields that exist in AgentConfigEntry
    entry := config.AgentConfigEntry{
        ID:          agent.ID,
        DisplayName: agent.DisplayName,
        Soul:        agent.Soul,
    }
    if entry.DisplayName == "" {
        entry.DisplayName = agent.ID
    }

    homeDir := os.Getenv("GOCLAW_HOME")
    if homeDir == "" {
        home, _ := os.UserHomeDir()
        homeDir = filepath.Join(home, ".goclaw")
    }
    configPath := filepath.Join(homeDir, "config.yaml")

    if err := config.AppendAgent(configPath, entry); err != nil {
        fmt.Fprintf(os.Stderr, "Error: %v\n", err)
        return 1
    }

    fmt.Printf("✓ Installed agent @%s\n", agent.ID)
    fmt.Printf("  Source: %s\n", url)
    preview := agent.Soul
    if len(preview) > 80 {
        preview = preview[:80] + "..."
    }
    fmt.Printf("  Soul: %s\n", preview)
    fmt.Println()
    fmt.Printf("Restart GoClaw or use @%s in the TUI to start chatting.\n", agent.ID)
    return 0
}
```

**Verify**: `go build ./cmd/goclaw/`

## Step 4.3: Register Subcommand

Add to main.go subcommand dispatch:
```go
case "pull":
    os.Exit(runPullCommand(os.Args[2:]))
```

**Verify**: `go build ./cmd/goclaw/`

## Step 4.4: Test Pull Command

**File**: `cmd/goclaw/pull_test.go`

```go
package main

import (
    "net/http"
    "net/http/httptest"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

const validAgentYAML = `id: test-agent
display_name: Test Agent
soul: You are a test agent.
capabilities: [testing]
`

func setupPullTest(t *testing.T) string {
    t.Helper()
    tmpDir := t.TempDir()
    // ADAPT: minimal config that config.Load can parse
    configPath := filepath.Join(tmpDir, "config.yaml")
    os.WriteFile(configPath, []byte("agents: []\n"), 0644)
    t.Setenv("GOCLAW_HOME", tmpDir)
    return tmpDir
}

func TestRunPullCommand_Valid(t *testing.T) {
    tmpDir := setupPullTest(t)
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/plain")
        w.Write([]byte(validAgentYAML))
    }))
    defer srv.Close()

    if code := runPullCommand([]string{srv.URL}); code != 0 {
        t.Fatalf("exit %d", code)
    }
    data, _ := os.ReadFile(filepath.Join(tmpDir, "config.yaml"))
    if !strings.Contains(string(data), "test-agent") {
        t.Fatal("config missing test-agent")
    }
}

func TestRunPullCommand_MissingID(t *testing.T) {
    setupPullTest(t)
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("soul: no id\n"))
    }))
    defer srv.Close()
    if code := runPullCommand([]string{srv.URL}); code == 0 {
        t.Fatal("should fail for missing id")
    }
}

func TestRunPullCommand_MissingSoul(t *testing.T) {
    setupPullTest(t)
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("id: soulless\n"))
    }))
    defer srv.Close()
    if code := runPullCommand([]string{srv.URL}); code == 0 {
        t.Fatal("should fail for missing soul")
    }
}

func TestRunPullCommand_DuplicateID(t *testing.T) {
    tmpDir := setupPullTest(t)
    os.WriteFile(filepath.Join(tmpDir, "config.yaml"),
        []byte("agents:\n- id: existing\n  soul: here\n"), 0644)
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("id: existing\nsoul: dup\n"))
    }))
    defer srv.Close()
    if code := runPullCommand([]string{srv.URL}); code == 0 {
        t.Fatal("should fail for duplicate ID")
    }
}

func TestRunPullCommand_HTTP404(t *testing.T) {
    setupPullTest(t)
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(404)
    }))
    defer srv.Close()
    if code := runPullCommand([]string{srv.URL}); code == 0 {
        t.Fatal("should fail for 404")
    }
}

func TestRunPullCommand_HTMLResponse(t *testing.T) {
    setupPullTest(t)
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/html")
        w.Write([]byte("<html>not yaml</html>"))
    }))
    defer srv.Close()
    if code := runPullCommand([]string{srv.URL}); code == 0 {
        t.Fatal("should fail for HTML")
    }
}

func TestRunPullCommand_InvalidYAML(t *testing.T) {
    setupPullTest(t)
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("{{{{not yaml"))
    }))
    defer srv.Close()
    if code := runPullCommand([]string{srv.URL}); code == 0 {
        t.Fatal("should fail for invalid YAML")
    }
}

func TestRunPullCommand_NoArgs(t *testing.T) {
    if code := runPullCommand(nil); code == 0 {
        t.Fatal("should fail with no args")
    }
}

func TestRunPullCommand_InvalidURL(t *testing.T) {
    setupPullTest(t)
    if code := runPullCommand([]string{"not-a-url"}); code == 0 {
        t.Fatal("should fail for invalid URL")
    }
}
```

**Verify**: `go test ./cmd/goclaw/ -v -run "Pull" -count=1`

## GATE 4

```bash
just check
go test -race ./...
go test ./cmd/goclaw/ -v -run "Pull"

grep -n "func runPullCommand" cmd/goclaw/pull.go
grep -n '"pull"' cmd/goclaw/main.go
grep -n "func AppendAgent" internal/config/config.go
grep -n "already exists" internal/config/config.go
grep -c "func Test.*Pull" cmd/goclaw/pull_test.go   # >= 9

just build
./dist/goclaw pull 2>&1 | grep -i "usage" && echo "PASS" || echo "FAIL"
./dist/goclaw pull not-a-url 2>&1 | grep -i "http" && echo "PASS" || echo "FAIL"
```

If all pass: `git add -A && git commit -m "PDR v5 Phase 4: goclaw pull"`

---

# PHASE 5: Activity Feed, Help, Errors, README, Version

**Goal**: Finish the UX. Activity feed, help text, error messages, README, version bump.
**Files created**: `internal/tui/activity.go`, `internal/tui/activity_test.go`, `internal/tui/errors.go`
**Files modified**: TUI model, README.md, `cmd/goclaw/main.go`
**Risk**: Medium. Multiple small changes.

## Step 5.1: Activity Feed Model

**File**: `internal/tui/activity.go`

```go
package tui

import (
    "fmt"
    "sync"
    "time"

    "github.com/charmbracelet/lipgloss"
)

type ActivityItem struct {
    ID        string
    Icon      string
    Message   string
    StartedAt time.Time
    DoneAt    *time.Time
    Cost      float64
}

type ActivityFeed struct {
    mu        sync.Mutex
    items     []ActivityItem
    collapsed bool
    maxItems  int
}

func NewActivityFeed() *ActivityFeed {
    return &ActivityFeed{maxItems: 10, collapsed: true}
}

func (f *ActivityFeed) Add(item ActivityItem) {
    f.mu.Lock()
    defer f.mu.Unlock()
    f.items = append(f.items, item)
    if len(f.items) > f.maxItems {
        f.items = f.items[1:]
    }
    f.collapsed = false // auto-expand
}

func (f *ActivityFeed) Complete(id, icon string, cost float64) {
    f.mu.Lock()
    defer f.mu.Unlock()
    now := time.Now()
    for i := range f.items {
        if f.items[i].ID == id {
            f.items[i].Icon = icon
            f.items[i].DoneAt = &now
            f.items[i].Cost = cost
            return
        }
    }
}

func (f *ActivityFeed) Toggle() {
    f.mu.Lock()
    defer f.mu.Unlock()
    f.collapsed = !f.collapsed
}

func (f *ActivityFeed) HasActive() bool {
    f.mu.Lock()
    defer f.mu.Unlock()
    for _, it := range f.items {
        if it.DoneAt == nil {
            return true
        }
    }
    return false
}

func (f *ActivityFeed) Len() int {
    f.mu.Lock()
    defer f.mu.Unlock()
    return len(f.items)
}

func (f *ActivityFeed) CleanupOld(maxAge time.Duration) int {
    f.mu.Lock()
    defer f.mu.Unlock()
    now := time.Now()
    kept := f.items[:0]
    removed := 0
    for _, it := range f.items {
        if it.DoneAt != nil && now.Sub(*it.DoneAt) >= maxAge {
            removed++
            continue
        }
        kept = append(kept, it)
    }
    f.items = kept
    return removed
}

func (f *ActivityFeed) View() string {
    f.mu.Lock()
    defer f.mu.Unlock()

    if len(f.items) == 0 {
        return ""
    }

    dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

    if f.collapsed {
        active := 0
        for _, it := range f.items {
            if it.DoneAt == nil {
                active++
            }
        }
        if active == 0 {
            return ""
        }
        return dim.Render(fmt.Sprintf("── %d active tasks (Ctrl+A to expand) ──", active)) + "\n"
    }

    itemS := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
    costS := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

    out := dim.Render("── Activity (Ctrl+A to collapse) ──") + "\n"
    for _, it := range f.items {
        line := fmt.Sprintf("%s %s", it.Icon, it.Message)
        if it.DoneAt != nil {
            dur := it.DoneAt.Sub(it.StartedAt).Truncate(100 * time.Millisecond)
            line += fmt.Sprintf(" (%s)", dur)
            if it.Cost > 0 {
                line += costS.Render(fmt.Sprintf(" $%.4f", it.Cost))
            }
        } else {
            line += fmt.Sprintf(" (%s)", time.Since(it.StartedAt).Truncate(time.Second))
        }
        out += itemS.Render(line) + "\n"
    }
    return out
}
```

**Verify**: `go build ./internal/tui/`

## Step 5.2: Test Activity Feed

**File**: `internal/tui/activity_test.go`

```go
package tui

import (
    "fmt"
    "testing"
    "time"
)

func TestActivityFeed_AddAndLen(t *testing.T) {
    f := NewActivityFeed()
    if f.Len() != 0 {
        t.Fatal("new feed should be empty")
    }
    f.Add(ActivityItem{ID: "1", Icon: "⏳", Message: "test", StartedAt: time.Now()})
    if f.Len() != 1 {
        t.Fatal("len should be 1")
    }
}

func TestActivityFeed_MaxItems(t *testing.T) {
    f := NewActivityFeed()
    f.maxItems = 3
    for i := 0; i < 5; i++ {
        f.Add(ActivityItem{ID: fmt.Sprintf("%d", i), StartedAt: time.Now()})
    }
    if f.Len() != 3 {
        t.Fatalf("expected 3, got %d", f.Len())
    }
}

func TestActivityFeed_Complete(t *testing.T) {
    f := NewActivityFeed()
    f.Add(ActivityItem{ID: "t1", Icon: "⏳", StartedAt: time.Now()})
    if !f.HasActive() {
        t.Fatal("should have active")
    }
    f.Complete("t1", "✅", 0.005)
    if f.HasActive() {
        t.Fatal("should have no active")
    }
}

func TestActivityFeed_CompleteNonExistent(t *testing.T) {
    f := NewActivityFeed()
    f.Add(ActivityItem{ID: "t1", StartedAt: time.Now()})
    f.Complete("nope", "✅", 0)
    if !f.HasActive() {
        t.Fatal("original should still be active")
    }
}

func TestActivityFeed_CleanupOld(t *testing.T) {
    f := NewActivityFeed()
    past := time.Now().Add(-2 * time.Minute)
    done := past.Add(10 * time.Second)
    f.Add(ActivityItem{ID: "old", StartedAt: past, DoneAt: &done})
    f.Add(ActivityItem{ID: "active", StartedAt: time.Now()})
    removed := f.CleanupOld(30 * time.Second)
    if removed != 1 {
        t.Fatalf("removed %d", removed)
    }
    if f.Len() != 1 {
        t.Fatal("should have 1 remaining")
    }
}

func TestActivityFeed_CleanupKeepsRecent(t *testing.T) {
    f := NewActivityFeed()
    now := time.Now()
    recent := now.Add(-5 * time.Second)
    f.Add(ActivityItem{ID: "r", StartedAt: now.Add(-10 * time.Second), DoneAt: &recent})
    if f.CleanupOld(30*time.Second) != 0 {
        t.Fatal("should not remove recent")
    }
}

func TestActivityFeed_HasActiveEmpty(t *testing.T) {
    if NewActivityFeed().HasActive() {
        t.Fatal("empty feed not active")
    }
}

func TestActivityFeed_Toggle(t *testing.T) {
    f := NewActivityFeed()
    if !f.collapsed {
        t.Fatal("should start collapsed")
    }
    f.Toggle()
    if f.collapsed {
        t.Fatal("should be expanded")
    }
}

func TestActivityFeed_AutoExpand(t *testing.T) {
    f := NewActivityFeed()
    f.Add(ActivityItem{ID: "1", StartedAt: time.Now()})
    if f.collapsed {
        t.Fatal("should auto-expand on add")
    }
}

func TestActivityFeed_ViewEmpty(t *testing.T) {
    if NewActivityFeed().View() != "" {
        t.Fatal("empty view should be empty string")
    }
}
```

**Verify**: `go test ./internal/tui/ -v -run "ActivityFeed" -count=1`

## Step 5.3: Wire Activity Feed into TUI

Add `activityFeed *ActivityFeed` to model. Initialize in constructor.

`Ctrl+A` → `m.activityFeed.Toggle()`

View: insert `m.activityFeed.View()` between chat and input.

## Step 5.4: Bus Event Subscription

**Pre-flight**:
```bash
grep -B5 -A20 "func.*Subscribe" internal/bus/*.go
```

### If callback-based bus:

```go
type ActivityUpdateMsg struct {
    Action string
    Item   ActivityItem
}

// Bridge: bus callback → channel → tea.Cmd
func listenForActivity(eventBus *bus.Bus, ch chan<- ActivityUpdateMsg) {
    eventBus.Subscribe("delegation.started", func(e bus.Event) {
        ch <- ActivityUpdateMsg{Action: "add", Item: ActivityItem{
            ID: e.TaskID, Icon: "⏳", Message: "Delegating to @" + e.AgentID, StartedAt: time.Now(),
        }}
    })
    // ADAPT: subscription for delegation.completed, plan.step.started, plan.step.completed
}

func waitForActivity(ch <-chan ActivityUpdateMsg) tea.Cmd {
    return func() tea.Msg { return <-ch }
}
```

### If channel-based bus:

```go
func waitForActivity(ch <-chan bus.Event) tea.Cmd {
    return func() tea.Msg {
        e := <-ch
        // ADAPT: convert bus.Event to ActivityUpdateMsg
        return ActivityUpdateMsg{...}
    }
}
```

### If PDR v4 events don't exist yet:

```go
// TODO: wire to bus events once delegation.started/completed are published (PDR v4 Phase 5)
// ActivityFeed model and UI are ready; just needs event source
```

## Step 5.5: Cleanup Timer

```go
type activityCleanupMsg struct{}

func activityCleanupTick() tea.Cmd {
    return tea.Tick(10*time.Second, func(time.Time) tea.Msg { return activityCleanupMsg{} })
}

// In Update:
case activityCleanupMsg:
    m.activityFeed.CleanupOld(30 * time.Second)
    return m, activityCleanupTick()

// In Init: add activityCleanupTick() to tea.Batch
```

## Step 5.6: Error Messages

**File**: `internal/tui/errors.go`

```go
package tui

import "strings"

// humanError extracts the innermost error message from a Go error chain.
// "engine: brain: provider: connection refused" → "Connection refused"
func humanError(err error) string {
    if err == nil {
        return ""
    }
    msg := err.Error()
    if idx := strings.LastIndex(msg, ": "); idx != -1 && idx+2 < len(msg) {
        inner := msg[idx+2:]
        if len(inner) > 0 {
            inner = strings.ToUpper(inner[:1]) + inner[1:]
        }
        return inner
    }
    return msg
}
```

Find all TUI error display sites and wrap with `humanError`:
```bash
grep -rn "err.Error()\|fmt.Sprintf.*err\|errMsg" internal/tui/*.go | head -20
```

**Verify**: `go build ./internal/tui/`

## Step 5.7: Help Text

Add to existing help output:

```
Messaging:
  @agent <msg>       Send message to agent (single message)
  @agent             Switch to agent (same as @@agent)
  @@agent <msg>      Switch to agent and send message

Shortcuts:
  Ctrl+N             Create new agent (also: /agent new)
  Ctrl+A             Toggle activity feed

Note: Ctrl+N may be intercepted by some terminals. Use /agent new as fallback.
```

## Step 5.8: README Update

Update `README.md`:

1. Banner example → show `@coder` usage with v0.2-dev version
2. Features → add @mentions, starter agents, `goclaw pull`
3. Quick start → mention starter agents available immediately
4. Status table → v0.2-dev

## Step 5.9: Version Bump

```bash
grep -n "Version\|version" cmd/goclaw/main.go | head -5
```

Update: `var Version = "v0.2-dev"`

Check build scripts:
```bash
grep -rn "v0.1\|Version" Justfile Makefile .goreleaser* | head -10
```

**Verify**: `go build ./... && ./dist/goclaw --version 2>&1 | head -1`

## GATE 5

```bash
just check
go test -race ./...
go test ./internal/tui/ -v -run "ActivityFeed"

grep -n "func.*ActivityFeed" internal/tui/activity.go
grep -c "func Test" internal/tui/activity_test.go   # >= 10
grep -n "activityFeed" internal/tui/*.go | grep -v activity.go | grep -v _test.go
grep -n "ctrl+a" internal/tui/*.go | grep -v _test.go
grep -n "func humanError" internal/tui/errors.go
grep -n "@agent\|Ctrl+N\|Ctrl+A" internal/tui/*.go | grep -i "help\|Help" | head -5
grep -n "@coder\|goclaw pull\|v0.2" README.md | head -5
grep -n "v0.2" cmd/goclaw/main.go
```

If all pass: `git add -A && git commit -m "PDR v5 Phase 5: activity feed, polish, README, version bump"`

---

# Post-Implementation Verification

```bash
just check
go test -race ./...

# First-run (isolated)
TEST_HOME=/tmp/test-goclaw-final-$$; rm -rf "$TEST_HOME"
GOCLAW_HOME="$TEST_HOME" just build
GOCLAW_HOME="$TEST_HOME" timeout 3s ./dist/goclaw 2>/dev/null || true
echo "--- Starter agents ---"
grep "coder" "$TEST_HOME/config.yaml" && echo "PASS" || echo "FAIL"
grep "researcher" "$TEST_HOME/config.yaml" && echo "PASS" || echo "FAIL"
grep "writer" "$TEST_HOME/config.yaml" && echo "PASS" || echo "FAIL"
rm -rf "$TEST_HOME"

# Pull subcommand
./dist/goclaw pull 2>&1 | grep -i "usage" && echo "PASS: pull usage" || echo "FAIL"
./dist/goclaw pull not-a-url 2>&1 | grep -i "http" && echo "PASS: url validation" || echo "FAIL"

# Version
./dist/goclaw --version 2>&1 | grep "v0.2" && echo "PASS: version" || echo "FAIL"

# Git commits
git log --oneline | grep -c "PDR v5"
# Should output 5

# Test count
go test ./... -count=1 2>&1 | tail -5
```

---

# What This PDR Does NOT Cover

- **Context pinning / agent memory** — PDR v6 (v0.3)
- **MCP client expansion** — PDR v7 (v0.4)
- **Telegram deep integration** — PDR v7 (v0.4)
- **True async delegation** — PDR v7 (v0.4)
- **LLM-generated plans** — PDR v8 (v0.5)
- **Agent registry server** — not planned; `goclaw pull` uses URLs
- **Web UI** — TUI is primary
- **Config hot-reload for pulled agents** — restart required
- **Single-message routing** (if Path B used) — deferred to v0.2.1

---

# Rollback Reference

| Phase | Rollback |
|-------|----------|
| Any single phase | `git reset --hard HEAD~1` |
| All v0.2 work | `git reset --hard <pre-v5-hash>` (record before starting) |
| Corrupted config | `rm ~/.goclaw/config.yaml` then restart |

---

# Review Items Addressed

| # | Issue | Resolution |
|---|-------|-----------|
| 1 | Single-message routing underspecified | Path A/B decision framework with sticky fallback |
| 2 | @agent empty message undefined | Treated as sticky switch; test added |
| 3 | Gate tests use ~/.goclaw | All use `GOCLAW_HOME=/tmp/test-*-$$` |
| 4 | Modal has no tests | 10 test cases in modal_test.go |
| 5 | SaveToConfig not persisted | Explicit AppendAgent call in handler |
| 6 | Custom contains() reimplements stdlib | Uses strings.Contains |
| 7 | AppendAgent reformats config | WARNING comment + TODO for v0.3 |
| 8 | Activity feed has no tests | 10 test cases in activity_test.go |
| 9 | Bus subscription too vague | Pre-flight + callback/channel paths + graceful skip |
| 10 | humanError too naive | Innermost-error extraction via LastIndex |
| 11 | No duplicate ID test for pull | TestRunPullCommand_DuplicateID added |
| 12 | No HTTP error tests | 404, HTML, invalid YAML, invalid URL tests added |
| 13 | No version bump | Step 5.9 added |
| 14 | No README update | Step 5.8 added |
| 15 | Ctrl+N terminal conflict | Documented in help text; /agent new as fallback |
| 16 | No pull format spec | YAML example at top of Phase 4 |
| 17 | Phase ordering | Kept current (UX narrative); independence noted |

