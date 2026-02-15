#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════════════════════════
# PDR v5 Phase-Gated Autonomous Executor
# ═══════════════════════════════════════════════════════════════════════════
#
# Executes all 5 phases of PDR v5 without human intervention.
# Each phase gets a fresh Claude Code invocation with full context budget.
# Gates verified externally in bash — not trusted to the LLM.
#
# Usage:
#   ./run-pdr-v5.sh                    # Full run, Phase 1→5
#   ./run-pdr-v5.sh --start-phase 3    # Resume from Phase 3
#   ./run-pdr-v5.sh --dry-run          # Show plan without executing
#   ./run-pdr-v5.sh --max-retries 3    # More retries per phase
#
# Requirements:
#   - claude CLI (latest, authenticated)
#   - go, git, just
#   - PDR-v5.md in repo root

set -euo pipefail

# ─── Configuration ───────────────────────────────────────────────────────────

PDR_PATH="./PDR-v5.md"
PREFLIGHT_CACHE=".pdr-v5-preflight.txt"
LOG_DIR=".pdr-v5-logs"
MAX_RETRIES=2
START_PHASE=1
DRY_RUN=false
TOTAL_PHASES=5

# ─── Argument Parsing ────────────────────────────────────────────────────────

while [[ $# -gt 0 ]]; do
    case "$1" in
        --start-phase) START_PHASE="$2"; shift 2 ;;
        --max-retries) MAX_RETRIES="$2"; shift 2 ;;
        --dry-run)     DRY_RUN=true; shift ;;
        -h|--help)
            echo "Usage: $0 [--start-phase N] [--max-retries N] [--dry-run]"
            exit 0 ;;
        *) echo "Unknown arg: $1"; exit 1 ;;
    esac
done

# ─── Colors ──────────────────────────────────────────────────────────────────

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'

log()  { echo -e "${CYAN}[PDR]${NC} $*"; }
pass() { echo -e "${GREEN}[PASS]${NC} $*"; }
fail() { echo -e "${RED}[FAIL]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
hdr()  { echo -e "\n${BOLD}═══════════════════════════════════════════════════${NC}"; echo -e "${BOLD}  $*${NC}"; echo -e "${BOLD}═══════════════════════════════════════════════════${NC}\n"; }

mkdir -p "$LOG_DIR"

# ─── Pre-Checks ─────────────────────────────────────────────────────────────

hdr "PRE-FLIGHT CHECKS"

if [[ ! -f "$PDR_PATH" ]]; then
    fail "PDR not found at $PDR_PATH — copy it to repo root"
    exit 1
fi
pass "PDR found: $PDR_PATH"

if ! command -v claude &>/dev/null; then
    fail "claude CLI not found. Install: npm install -g @anthropic-ai/claude-code"
    exit 1
fi
CLAUDE_VERSION=$(claude -v 2>/dev/null || echo "unknown")
pass "claude CLI: $CLAUDE_VERSION"

for tool in go git; do
    if ! command -v $tool &>/dev/null; then
        fail "$tool not found"
        exit 1
    fi
done
pass "go and git available"

if command -v just &>/dev/null; then
    pass "just available"
else
    warn "just not found — will use 'go build ./...' + 'go vet ./...' instead"
fi

# ─── Suppress Bypass Permissions Dialog ──────────────────────────────────────

# The --dangerously-skip-permissions flag shows a confirmation dialog on every
# launch unless this setting is persisted. Set it once to avoid stalling.
CLAUDE_SETTINGS_DIR="${HOME}/.claude"
CLAUDE_SETTINGS_FILE="${CLAUDE_SETTINGS_DIR}/settings.json"

if [[ -f "$CLAUDE_SETTINGS_FILE" ]]; then
    if ! grep -q "skipDangerousModePermissionPrompt" "$CLAUDE_SETTINGS_FILE" 2>/dev/null; then
        # Add the setting — merge into existing JSON
        log "Adding skipDangerousModePermissionPrompt to claude settings..."
        TMP_SETTINGS=$(mktemp)
        if command -v python3 &>/dev/null; then
            python3 -c "
import json, sys
with open('$CLAUDE_SETTINGS_FILE') as f:
    d = json.load(f)
d['skipDangerousModePermissionPrompt'] = True
with open('$TMP_SETTINGS', 'w') as f:
    json.dump(d, f, indent=2)
" 2>/dev/null && mv "$TMP_SETTINGS" "$CLAUDE_SETTINGS_FILE" || rm -f "$TMP_SETTINGS"
        fi
    fi
else
    mkdir -p "$CLAUDE_SETTINGS_DIR"
    echo '{"skipDangerousModePermissionPrompt": true}' > "$CLAUDE_SETTINGS_FILE"
fi
pass "Bypass permissions dialog suppressed"

# ─── Git State ───────────────────────────────────────────────────────────────

if [[ -n "$(git status --porcelain 2>/dev/null)" ]]; then
    warn "Dirty git state — committing checkpoint"
    git add -A && git commit -m "checkpoint: pre-PDR-v5 $(date +%Y%m%d-%H%M%S)" || true
fi

PRE_PDR_COMMIT=$(git rev-parse HEAD)
log "Pre-PDR commit: ${PRE_PDR_COMMIT:0:10}"
log "Rollback all: git reset --hard $PRE_PDR_COMMIT"

# ─── Pre-Flight Context Gathering ────────────────────────────────────────────

run_preflight() {
    hdr "GATHERING CODEBASE CONTEXT"

    {
        echo "=== PRE-FLIGHT OUTPUT — $(date) ==="
        echo ""

        local -A sections=(
            ["TUI Model Struct"]='grep -B2 -A30 "type Model struct\|type model struct" internal/tui/*.go'
            ["TUI Update Function"]='grep -B2 -A30 "func.*Update.*tea.Msg\|func.*Update.*Msg" internal/tui/*.go | head -80'
            ["TUI View Function"]='grep -B2 -A15 "func.*View()" internal/tui/*.go | head -40'
            ["Input Handler"]='grep -B5 -A30 "KeyEnter\|Submit\|handleInput\|sendMessage\|processInput" internal/tui/*.go | head -80'
            ["Command Dispatch"]="grep -B3 -A20 'strings.HasPrefix.*\"/\"\|handleCommand\|processCommand' internal/tui/*.go | head -80"
            ["TUI Files"]='ls -la internal/tui/*.go'
            ["TUI Constructor"]='grep -B5 -A20 "func New\|func InitialModel\|tui.New" internal/tui/*.go cmd/goclaw/main.go | head -50'
            ["TUI Interfaces"]='grep -B2 -A10 "interface {" internal/tui/*.go'
            ["Existing Modals"]='grep -rn "modal\|overlay\|popup\|dialog\|form" internal/tui/*.go | head -20'
            ["Bubbletea Components"]='grep -n "textinput\|textarea\|list\|viewport\|spinner\|table" internal/tui/*.go | head -20'
            ["Key Bindings"]='grep -n "KeyMap\|key.Binding\|KeyCtrl\|ctrl+" internal/tui/*.go | head -20'
            ["Init Function"]='grep -B5 -A15 "func.*Init()" internal/tui/*.go | head -30'
            ["AgentSwitcher"]='grep -B2 -A20 "AgentSwitcher" internal/tui/*.go | head -40'
            ["SwitchAgent"]='grep -B5 -A20 "SwitchAgent\|switchAgent" internal/tui/*.go cmd/goclaw/main.go | head -40'
            ["Active Agent Field"]='grep -n "activeAgent\|currentAgent\|agentID\|selectedAgent\|brain\|Brain" internal/tui/*.go | head -20'
            ["tuiAgentSwitcher"]='grep -B5 -A40 "tuiAgentSwitcher" cmd/goclaw/main.go | head -60'
            ["CreateAgent Signature"]='grep -B2 -A10 "func.*CreateAgent" cmd/goclaw/main.go internal/tui/*.go | head -20'
            ["Message Sending Path"]='grep -B5 -A15 "CreateChatTask\|QueueTask\|sendChat\|submitMessage" internal/tui/*.go internal/engine/*.go | head -40'
            ["Config Struct"]='grep -B2 -A40 "type Config struct" internal/config/config.go'
            ["AgentConfigEntry"]='grep -B2 -A30 "type AgentConfigEntry struct\|type AgentEntry struct" internal/config/config.go'
            ["Config Capabilities"]='grep -n "Capabilities\|capabilities" internal/config/config.go'
            ["Config Load"]='grep -B2 -A10 "func Load\|func ReadConfig\|func ParseConfig" internal/config/config.go | head -20'
            ["Config Write"]='grep -n "Marshal\|WriteFile\|SaveConfig\|writeConfig\|Save" internal/config/config.go | head -10'
            ["First-Run Config"]='grep -B10 -A30 "initConfig\|generateDefault\|defaultConfig\|firstRun\|NotExist" internal/config/config.go cmd/goclaw/main.go | head -80'
            ["YAML Tags"]='grep "yaml:" internal/config/config.go | head -20'
            ["Agent Registry"]='grep -n "func.*Registry.*)" internal/agent/registry.go | head -20'
            ["Bus Events Published"]="grep -rn 'Publish(' internal/ --include='*.go' | head -30"
            ["Bus Subscribe"]='grep -B5 -A15 "func.*Subscribe" internal/bus/*.go'
            ["Bus Event Types"]='grep -B2 -A10 "type Event struct\|type Message struct" internal/bus/*.go | head -20'
            ["CLI Subcommands"]="grep -B10 -A25 'os.Args\|case.*\"doctor\"\|case.*\"daemon\"' cmd/goclaw/main.go | head -50"
            ["Help Text"]='grep -B5 -A40 "helpText\|/help\|commandHelp\|availableCommands" internal/tui/*.go | head -80'
            ["Error Display"]='grep -B5 -A10 "Error\|error\|errMsg\|ErrorMsg" internal/tui/*.go | head -30'
            ["Model Strings"]="grep -rn 'gemini\|claude\|gpt-4\|model' internal/engine/brain*.go internal/config/config.go | grep -i 'string\|const\|default\|valid' | head -30"
            ["Version String"]='grep -n "Version\|version" cmd/goclaw/main.go | head -5'
        )

        for name in "${!sections[@]}"; do
            echo "=== $name ==="
            eval "${sections[$name]}" 2>/dev/null || echo "(not found)"
            echo ""
        done

        echo "=== END PRE-FLIGHT ==="
    } > "$PREFLIGHT_CACHE"

    pass "Pre-flight cached: $PREFLIGHT_CACHE ($(wc -l < "$PREFLIGHT_CACHE") lines)"
}

# ─── Gate Functions ──────────────────────────────────────────────────────────

gate_build() {
    log "Gate: build + test..."
    if command -v just &>/dev/null; then
        just check 2>&1 || return 1
    else
        go build ./... 2>&1 || return 1
        go vet ./... 2>&1 || return 1
    fi
    go test -race -count=1 ./... 2>&1 || return 1
    pass "Build + tests pass"
}

gate_1() {
    log "Gate 1: @Mentions..."
    local f=0

    grep -q "func ParseMention" internal/tui/mention.go 2>/dev/null                || { fail "ParseMention missing";       ((f++)); }
    grep -q "type MentionResult" internal/tui/mention.go 2>/dev/null                || { fail "MentionResult missing";      ((f++)); }

    local tc; tc=$(grep -c "t.Run\|func Test" internal/tui/mention_test.go 2>/dev/null || echo 0)
    [[ "$tc" -ge 10 ]] || { fail "Mention tests: $tc (need ≥10)"; ((f++)); }

    grep -rl "ParseMention" internal/tui/*.go 2>/dev/null | grep -v _test.go | grep -v mention.go | grep -q . \
        || { fail "ParseMention not wired into TUI"; ((f++)); }

    go test ./internal/tui/ -run "ParseMention" -count=1 2>&1 || { fail "ParseMention tests fail"; ((f++)); }

    [[ $f -eq 0 ]] && pass "Gate 1 ✓" || fail "Gate 1: $f failures"
    return $f
}

gate_2() {
    log "Gate 2: Starter Agents..."
    local f=0

    grep -q "func StarterAgents" internal/config/starters.go 2>/dev/null           || { fail "StarterAgents missing";      ((f++)); }
    grep -rq "StarterAgents" cmd/goclaw/main.go internal/config/config.go 2>/dev/null || { fail "StarterAgents not wired"; ((f++)); }

    go test ./internal/config/ -run "StarterAgent" -count=1 2>&1 || { fail "StarterAgent tests fail"; ((f++)); }

    # Functional (isolated — never ~/.goclaw)
    local th="/tmp/test-goclaw-gate2-$$"; rm -rf "$th"
    if command -v just &>/dev/null; then
        GOCLAW_HOME="$th" just build 2>/dev/null
    else
        GOCLAW_HOME="$th" go build -o ./dist/goclaw ./cmd/goclaw/ 2>/dev/null
    fi
    GOCLAW_HOME="$th" timeout 3s ./dist/goclaw 2>/dev/null || true
    for a in coder researcher writer; do
        grep -q "$a" "$th/config.yaml" 2>/dev/null || { fail "Starter '$a' missing from config"; ((f++)); }
    done
    rm -rf "$th"

    [[ $f -eq 0 ]] && pass "Gate 2 ✓" || fail "Gate 2: $f failures"
    return $f
}

gate_3() {
    log "Gate 3: Agent Creation Modal..."
    local f=0

    [[ -f internal/tui/modal.go ]]      || { fail "modal.go missing";              ((f++)); }
    [[ -f internal/tui/modal_test.go ]]  || { fail "modal_test.go missing";         ((f++)); }

    local tc; tc=$(grep -c "func Test" internal/tui/modal_test.go 2>/dev/null || echo 0)
    [[ "$tc" -ge 8 ]] || { fail "Modal tests: $tc (need ≥8)"; ((f++)); }

    grep -rl "agentModal\|AgentModal" internal/tui/*.go 2>/dev/null | grep -v _test.go | grep -v modal.go | grep -q . \
        || { fail "Modal not wired into TUI"; ((f++)); }

    grep -q "ctrl+n\|Ctrl+N\|ctrl+N" internal/tui/*.go 2>/dev/null || { fail "Ctrl+N missing"; ((f++)); }

    # SaveToConfig must call AppendAgent
    grep -q "AppendAgent" internal/tui/*.go 2>/dev/null || { fail "SaveToConfig persistence missing"; ((f++)); }

    grep -q "func AvailableModels" internal/config/*.go 2>/dev/null || { fail "AvailableModels missing"; ((f++)); }

    go test ./internal/tui/ -run "AgentModal" -count=1 2>&1 || { fail "AgentModal tests fail"; ((f++)); }

    [[ $f -eq 0 ]] && pass "Gate 3 ✓" || fail "Gate 3: $f failures"
    return $f
}

gate_4() {
    log "Gate 4: goclaw pull..."
    local f=0

    grep -q "func runPullCommand" cmd/goclaw/pull.go 2>/dev/null                   || { fail "runPullCommand missing";     ((f++)); }
    grep -q '"pull"' cmd/goclaw/main.go 2>/dev/null                                || { fail "pull not in main.go";        ((f++)); }
    grep -q "func AppendAgent" internal/config/config.go 2>/dev/null               || { fail "AppendAgent missing";        ((f++)); }
    grep -q "already exists" internal/config/config.go 2>/dev/null                 || { fail "Duplicate ID check missing"; ((f++)); }

    local tc; tc=$(grep -c "func Test.*Pull" cmd/goclaw/pull_test.go 2>/dev/null || echo 0)
    [[ "$tc" -ge 8 ]] || { fail "Pull tests: $tc (need ≥8)"; ((f++)); }

    go test ./cmd/goclaw/ -run "Pull" -count=1 2>&1 || { fail "Pull tests fail"; ((f++)); }

    ./dist/goclaw pull 2>&1 | grep -qi "usage" || { fail "pull usage broken"; ((f++)); }

    [[ $f -eq 0 ]] && pass "Gate 4 ✓" || fail "Gate 4: $f failures"
    return $f
}

gate_5() {
    log "Gate 5: Activity Feed + Polish..."
    local f=0

    [[ -f internal/tui/activity.go ]]      || { fail "activity.go missing";         ((f++)); }
    [[ -f internal/tui/activity_test.go ]]  || { fail "activity_test.go missing";    ((f++)); }

    local tc; tc=$(grep -c "func Test" internal/tui/activity_test.go 2>/dev/null || echo 0)
    [[ "$tc" -ge 8 ]] || { fail "Activity tests: $tc (need ≥8)"; ((f++)); }

    grep -rl "activityFeed" internal/tui/*.go 2>/dev/null | grep -v activity.go | grep -v _test.go | grep -q . \
        || { fail "Activity feed not wired"; ((f++)); }

    grep -q "ctrl+a\|Ctrl+A\|ctrl+A" internal/tui/*.go 2>/dev/null || { fail "Ctrl+A missing"; ((f++)); }
    grep -q "func humanError"   internal/tui/errors.go 2>/dev/null || { fail "humanError missing"; ((f++)); }
    grep -q "v0.2"              cmd/goclaw/main.go 2>/dev/null     || { fail "Version not bumped"; ((f++)); }
    grep -qE "@coder|@mention|goclaw pull" README.md 2>/dev/null   || { fail "README not updated"; ((f++)); }

    go test ./internal/tui/ -run "ActivityFeed" -count=1 2>&1 || { fail "ActivityFeed tests fail"; ((f++)); }

    [[ $f -eq 0 ]] && pass "Gate 5 ✓" || fail "Gate 5: $f failures"
    return $f
}

# ─── Claude Code Invocation ─────────────────────────────────────────────────

phase_names=(
    ""  # 0 unused
    "@Mentions"
    "Starter Agents"
    "Agent Creation Modal"
    "goclaw pull"
    "Activity Feed + Polish"
)

phase_hints=(
    ""  # 0 unused
    "You are implementing @mentions. The parser goes in internal/tui/mention.go. The hardest part is wiring into the TUI input handler. Check the pre-flight file for the exact input flow. If CreateChatTask does NOT accept an agent ID override, use Path B (sticky-only). Record which path you chose in a code comment."
    "You are adding starter agents. Low-risk, purely additive. The CRITICAL thing is matching AgentConfigEntry field names EXACTLY. Check the pre-flight file for the struct definition. All functional tests MUST use GOCLAW_HOME=/tmp/test-* — NEVER touch ~/.goclaw."
    "You are building the agent creation modal. Riskiest phase. Read the existing TUI View() composition carefully before adding overlay rendering. The modal MUST have tests (>= 8 test functions). The SaveToConfig handler MUST call config.AppendAgent explicitly — do NOT assume config watchers exist."
    "You are adding goclaw pull. Low risk, new subcommand. Match the existing subcommand dispatch pattern in main.go. The pull_test.go must cover: valid, missing id, missing soul, duplicate id, 404, HTML response, invalid YAML, no args, invalid URL (>= 9 tests). Use strings.Contains not a custom helper."
    "You are adding activity feed, help text, error messages, README update, and version bump. Many small changes. Activity feed needs tests (>= 8). For bus subscription: check if bus events from PDR v4 exist yet. If not, add a TODO comment and skip the wiring — the ActivityFeed model and UI are still useful. Update README with @mention syntax and goclaw pull. Bump version to v0.2-dev in main.go."
)

run_phase() {
    local phase=$1
    local attempt=$2
    local log_file="$LOG_DIR/phase-${phase}-attempt-${attempt}.log"

    hdr "Phase $phase: ${phase_names[$phase]} (attempt $attempt/$MAX_RETRIES)"

    if $DRY_RUN; then
        warn "DRY RUN — skipping claude invocation"
        return 0
    fi

    # Build the phase prompt
    local prompt
    prompt=$(cat <<PROMPT
You are executing Phase $phase of PDR v5 for the go-claw project.

## Your Task

1. Read the PDR file at: $PDR_PATH — specifically the "PHASE $phase" section
2. Read the pre-flight context cache at: $PREFLIGHT_CACHE
3. Execute every step in Phase $phase sequentially
4. After EACH file edit, run: go build ./...
5. After all steps complete, run the GATE $phase verification commands from the PDR
6. If all gate checks pass: git add -A && git commit -m "PDR v5 Phase $phase: ${phase_names[$phase]}"

## Critical Rules

- Read the PDR from disk. It is the source of truth.
- The pre-flight cache has codebase context from before you started. Use it to resolve ADAPT markers.
- ADAPT means: the code template needs codebase-specific modifications. Before writing ANY code with an ADAPT marker, grep the codebase to find the real struct names, function signatures, and patterns.
- If go build fails, fix the error immediately.
- If a test fails, fix it before proceeding.
- Do NOT modify files outside Phase $phase scope.
- Do NOT skip steps. Execute in order.
- Match existing code style — read 2-3 files in the same package first.
- For functional tests: NEVER use ~/.goclaw. Always use GOCLAW_HOME=/tmp/test-*

## Phase-Specific Guidance

${phase_hints[$phase]}

Begin now. Read the PDR and pre-flight cache, then execute Phase $phase.
PROMPT
)

    # Invoke Claude Code autonomously
    echo "$prompt" | claude \
        -p \
        --dangerously-skip-permissions \
        --max-turns 80 \
        --verbose \
        2>&1 | tee "$log_file"

    local exit_code=${PIPESTATUS[1]:-$?}

    if [[ $exit_code -ne 0 ]]; then
        fail "Claude Code exited with code $exit_code"
        return 1
    fi

    return 0
}

# ─── Main Loop ───────────────────────────────────────────────────────────────

hdr "PDR v5 AUTONOMOUS EXECUTOR"
log "Phases: $START_PHASE → $TOTAL_PHASES"
log "Max retries: $MAX_RETRIES per phase"
log "Pre-PDR commit: ${PRE_PDR_COMMIT:0:10}"

# Pre-flight
if [[ ! -f "$PREFLIGHT_CACHE" ]] || [[ "$START_PHASE" -eq 1 ]]; then
    run_preflight
else
    log "Using cached pre-flight: $PREFLIGHT_CACHE"
fi

# Verify build
log "Verifying clean build..."
if ! gate_build 2>&1 | tee "$LOG_DIR/preflight-build.log"; then
    fail "Build fails before starting. Fix manually."
    exit 1
fi

# Execute phases
for phase in $(seq "$START_PHASE" "$TOTAL_PHASES"); do
    succeeded=false

    for attempt in $(seq 1 "$MAX_RETRIES"); do

        # Run Claude Code for this phase
        if ! run_phase "$phase" "$attempt"; then
            fail "Claude Code failed for Phase $phase"
            if [[ $attempt -lt $MAX_RETRIES ]]; then
                warn "Rolling back attempt $attempt, retrying..."
                git checkout -- . 2>/dev/null || true
                git clean -fd 2>/dev/null || true
                continue
            fi
        fi

        # Build gate
        if ! gate_build 2>&1 | tee "$LOG_DIR/gate-${phase}-build-${attempt}.log"; then
            fail "Build broken after Phase $phase"
            if [[ $attempt -lt $MAX_RETRIES ]]; then
                warn "Rolling back, retrying..."
                git checkout -- . 2>/dev/null || true
                git clean -fd 2>/dev/null || true
                continue
            fi
            fail "Phase $phase FAILED after $MAX_RETRIES attempts (build)"
            exit 1
        fi

        # Phase-specific gate
        gate_func="gate_$phase"
        if $gate_func 2>&1 | tee "$LOG_DIR/gate-${phase}-specific-${attempt}.log"; then
            succeeded=true
            break
        else
            fail "Gate $phase failed"
            if [[ $attempt -lt $MAX_RETRIES ]]; then
                warn "Retrying phase $phase..."
                git checkout -- . 2>/dev/null || true
                git clean -fd 2>/dev/null || true
            fi
        fi
    done

    if ! $succeeded; then
        hdr "PHASE $phase FAILED"
        fail "After $MAX_RETRIES attempts"
        fail "Logs: $LOG_DIR/"
        fail "Last good: $(git log --oneline -1)"
        fail ""
        fail "Resume:   $0 --start-phase $phase"
        fail "Rollback: git reset --hard $PRE_PDR_COMMIT"
        exit 1
    fi

    hdr "PHASE $phase COMPLETE ✓"
done

# ─── Post-Implementation ────────────────────────────────────────────────────

hdr "POST-IMPLEMENTATION VERIFICATION"

gate_build || { fail "Final build broken"; exit 1; }

# First-run
TH="/tmp/test-goclaw-final-$$"; rm -rf "$TH"
if command -v just &>/dev/null; then
    GOCLAW_HOME="$TH" just build 2>/dev/null
else
    GOCLAW_HOME="$TH" go build -o ./dist/goclaw ./cmd/goclaw/ 2>/dev/null
fi
GOCLAW_HOME="$TH" timeout 3s ./dist/goclaw 2>/dev/null || true
for a in coder researcher writer; do
    grep -q "$a" "$TH/config.yaml" 2>/dev/null && pass "Starter: $a" || fail "Starter: $a"
done
rm -rf "$TH"

# Pull
./dist/goclaw pull 2>&1 | grep -qi "usage" && pass "Pull: usage" || fail "Pull: usage"

# Version
./dist/goclaw --version 2>&1 | grep -q "v0.2" && pass "Version: v0.2" || warn "Version: check manually"

# Git
commits=$(git log --oneline "$PRE_PDR_COMMIT"..HEAD 2>/dev/null | grep -c "PDR v5" || echo 0)
pass "Git: $commits PDR v5 commits"

# Test count
total_tests=$(go test ./... -count=1 2>&1 | grep -c "^ok " || echo "?")
pass "Packages passing: $total_tests"

hdr "PDR v5 COMPLETE — go-claw v0.2 ready 🚀"
echo ""
log "Commits:"
git log --oneline "$PRE_PDR_COMMIT"..HEAD
echo ""
log "Rollback all: git reset --hard $PRE_PDR_COMMIT"
