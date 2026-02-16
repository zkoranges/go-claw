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
grep -qi "mcpservers\|mcp_servers" internal/config/config.go 2>/dev/null && 
    pass "Per-agent MCP config field present" || fail "Per-agent MCP config missing"

grep -qi "sse\|url.*yaml.*url" internal/config/config.go 2>/dev/null && 
    pass "SSE transport in config" || fail "SSE transport missing"

# Manager: per-agent methods (flexible name matching)
grep -qi "connectagent\|connect.*agent" internal/mcp/manager.go 2>/dev/null && 
    pass "Per-agent connect in manager" || fail "Per-agent connect missing"

grep -qi "disconnectagent\|disconnect.*agent" internal/mcp/manager.go 2>/dev/null && 
    pass "Per-agent disconnect in manager" || fail "Per-agent disconnect missing"

grep -qi "discovertools\|discover.*tools\|tools.*list\|toolslist" internal/mcp/manager.go 2>/dev/null && 
    pass "Tool discovery in manager" || fail "Tool discovery missing"

grep -qi "invoketool\|invoke.*tool\|calltool\|call.*tool" internal/mcp/manager.go 2>/dev/null && 
    pass "Tool invocation in manager" || fail "Tool invocation missing"

grep -qi "reconnect\|backoff\|retry" internal/mcp/manager.go 2>/dev/null && 
    pass "Reconnection logic present" || fail "Reconnection logic missing"

# Policy: MCP checks (flexible)
grep -qi "allowmcptool\|mcp.*rule\|mcprule\|mcppolicy" internal/policy/policy.go 2>/dev/null && 
    pass "MCP policy in engine" || fail "MCP policy missing"

# Tests exist and pass
MCP_TEST_COUNT=$(grep -c "func Test" internal/mcp/manager_test.go 2>/dev/null || echo 0)
[ "$MCP_TEST_COUNT" -ge 8 ] && pass "MCP manager tests: $MCP_TEST_COUNT (≥8)" || 
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
grep -q "schemaVersionV9\|schemaVersion.*=.*9\|schema_version.*9" internal/persistence/store.go 2>/dev/null && 
    pass "Schema bumped to v9" || fail "Schema not v9"

[ -f internal/persistence/delegations.go ] && pass "delegations.go exists" || fail "delegations.go missing"
[ -f internal/persistence/delegations_test.go ] && pass "delegations_test.go exists" || fail "delegations_test.go missing"

grep -qi "delegation" internal/persistence/delegations.go 2>/dev/null && 
    pass "Delegation store implemented" || fail "Delegation store empty"

# Async tool
grep -qi "async.*delegate\|delegate.*async\|asyncdelegate\|delegate_task_async" internal/tools/delegate.go 2>/dev/null && 
    pass "Async delegate tool exists" || fail "Async delegate tool missing"

# Brain injection
grep -qi "inject.*deleg\|deleg.*inject\|pendingdeleg" internal/engine/brain.go 2>/dev/null && 
    pass "Brain delegation injection exists" || fail "Brain delegation injection missing"

DELEG_TEST_COUNT=$(grep -c "func Test" internal/persistence/delegations_test.go 2>/dev/null || echo 0)
[ "$DELEG_TEST_COUNT" -ge 5 ] && pass "Delegation tests: $DELEG_TEST_COUNT (≥5)" || 
    fail "Delegation tests: $DELEG_TEST_COUNT (<5)"

if go test ./internal/persistence/... -count=1 -timeout 120s 2>/dev/null; then
    pass "Persistence tests pass"
else fail "Persistence tests fail"; fi

echo ""

# ── Feature 3: Telegram Deep ────────────────
echo "── Feature 3: Telegram Deep Integration ──"

[ -f internal/tools/alert.go ] && pass "alert.go exists" || fail "alert.go missing"
[ -f internal/tools/alert_test.go ] && pass "alert_test.go exists" || fail "alert_test.go missing"

grep -qi "hitl\|approval\|inlinekeyboard\|callbackquery" internal/channels/telegram.go 2>/dev/null && 
    pass "HITL in Telegram" || fail "HITL missing from Telegram"

grep -qi "handleplan\|/plan\|plancommand\|plan.*command" internal/channels/telegram.go 2>/dev/null && 
    pass "/plan handler exists" || fail "/plan handler missing"

grep -qi "formatprogress\|formatplan\|markdownv2\|markdown.*v2" internal/channels/telegram.go 2>/dev/null && 
    pass "Progress formatting exists" || fail "Progress formatting missing"

grep -qi "hitl\|approval.*gate\|approval.*request" internal/coordinator/executor.go 2>/dev/null && 
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

grep -qi "well-known\|agent\.json\|agentcard" internal/gateway/a2a.go 2>/dev/null && 
    pass "Agent card handler implemented" || fail "Agent card handler missing"

grep -qi "well-known\|agent\.json" internal/gateway/gateway.go 2>/dev/null && 
    pass "A2A route registered" || fail "A2A route not registered"

A2A_TEST_COUNT=$(grep -c "func Test" internal/gateway/a2a_test.go 2>/dev/null || echo 0)
[ "$A2A_TEST_COUNT" -ge 4 ] && pass "A2A tests: $A2A_TEST_COUNT (≥4)" || 
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
    [ "$DELTA" -ge 70 ] && pass "Test delta: +$DELTA (≥70)" || 
        warn "Test delta: +$DELTA (target ≥70)"
else
    echo "  Tests: $CURRENT (no baseline captured — run baseline step first)"
    warn "No test baseline — cannot verify delta"
fi

grep -q "v0\.4" cmd/goclaw/main.go 2>/dev/null && 
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
