#!/usr/bin/env bash
set -euo pipefail

echo "=========================================="
echo " GoClaw v0.5 Verification"
echo "=========================================="
echo ""

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

PASS=0
FAIL=0
WARN=0

pass() { echo -e "  ${GREEN}✓${NC} $1"; PASS=$((PASS + 1)); }
fail() { echo -e "  ${RED}✗${NC} $1"; FAIL=$((FAIL + 1)); }
warn() { echo -e "  ${YELLOW}!${NC} $1"; WARN=$((WARN + 1)); }

# Capture baseline
BASELINE=$(cat .test-count-baseline 2>/dev/null || echo 0)

echo "── Build & Vet ──"

if go build ./... 2>/dev/null; then
    pass "Build succeeds"
else fail "Build fails"; fi

if go vet ./... 2>/dev/null; then
    pass "Vet clean"
else fail "Vet errors"; fi

echo ""

# ── Feature 1: Streaming ────────────────────
echo "── Feature 1: Streaming Responses ──"

[ -f internal/gateway/stream.go ] && pass "stream.go exists" || fail "stream.go missing"
[ -f internal/gateway/stream_test.go ] && pass "stream_test.go exists" || fail "stream_test.go missing"

grep -qi "GenerateStream\|StreamChunk" internal/engine/brain.go 2>/dev/null && \
    pass "GenerateStream in brain" || fail "GenerateStream missing"

grep -qi "text/event-stream" internal/gateway/stream.go 2>/dev/null && \
    pass "SSE content type in stream handler" || fail "SSE content type missing"

grep -qi "EventStreamToken\|stream\.token" internal/engine/engine.go 2>/dev/null && \
    pass "Stream token bus event in engine" || fail "Stream token event missing"

# Check openai_handler.go (actual filename, not openai.go)
grep -qi "stream.*true\|handleOpenAIStream\|openai.*stream" internal/gateway/openai_handler.go 2>/dev/null && \
    pass "OpenAI streaming mode" || fail "OpenAI streaming missing"

grep -qi "editMessageText\|editMessage\|progressive" internal/channels/telegram.go 2>/dev/null && \
    pass "Telegram progressive editing" || fail "Telegram progressive editing missing"

STREAM_TEST_COUNT=$(grep -c "func Test" internal/gateway/stream_test.go 2>/dev/null || echo 0)
[ "$STREAM_TEST_COUNT" -ge 4 ] && pass "Stream tests: $STREAM_TEST_COUNT (≥4)" || \
    fail "Stream tests: $STREAM_TEST_COUNT (<4)"

if go test ./internal/engine/... -count=1 -timeout 120s -run "(?i)stream" 2>/dev/null; then
    pass "Engine stream tests pass"
else fail "Engine stream tests fail"; fi

if go test ./internal/gateway/... -count=1 -timeout 120s -run "(?i)stream|sse" 2>/dev/null; then
    pass "Gateway stream tests pass"
else fail "Gateway stream tests fail"; fi

echo ""

# ── Feature 2: Agent Loops ───────────────────
echo "── Feature 2: Agent Loops ──"

[ -f internal/engine/loop.go ] && pass "loop.go exists" || fail "loop.go missing"
[ -f internal/engine/loop_test.go ] && pass "loop_test.go exists" || fail "loop_test.go missing"
[ -f internal/persistence/loops.go ] && pass "loops.go (persistence) exists" || fail "loops.go missing"
[ -f internal/persistence/loops_test.go ] && pass "loops_test.go exists" || fail "loops_test.go missing"
[ -f internal/tools/loop_control.go ] && pass "loop_control.go exists" || fail "loop_control.go missing"

grep -qi "LoopRunner\|LoopState\|LoopStatus" internal/engine/loop.go 2>/dev/null && \
    pass "LoopRunner implemented" || fail "LoopRunner missing"

grep -qi "loop_checkpoints" internal/persistence/store.go 2>/dev/null && \
    pass "loop_checkpoints migration" || fail "loop_checkpoints table missing"

grep -qi "SaveLoopCheckpoint\|LoadLoopCheckpoint" internal/persistence/loops.go 2>/dev/null && \
    pass "Loop checkpoint CRUD" || fail "Loop checkpoint CRUD missing"

grep -qi "checkpoint_now\|set_loop_status" internal/tools/loop_control.go 2>/dev/null && \
    pass "Loop control tools" || fail "Loop control tools missing"

grep -qi "LoopConfig\|loop.*enabled\|max_steps" internal/config/config.go 2>/dev/null && \
    pass "LoopConfig in config" || fail "LoopConfig missing"

LOOP_TEST_COUNT=$(grep -c "func Test" internal/engine/loop_test.go 2>/dev/null || echo 0)
[ "$LOOP_TEST_COUNT" -ge 8 ] && pass "Loop tests: $LOOP_TEST_COUNT (≥8)" || \
    fail "Loop tests: $LOOP_TEST_COUNT (<8)"

if go test ./internal/engine/... -count=1 -timeout 120s -run "(?i)loop" 2>/dev/null; then
    pass "Loop tests pass"
else fail "Loop tests fail"; fi

if go test ./internal/persistence/... -count=1 -timeout 120s -run "(?i)loop|checkpoint" 2>/dev/null; then
    pass "Loop persistence tests pass"
else fail "Loop persistence tests fail"; fi

echo ""

# ── Feature 3: Structured Output ─────────────
echo "── Feature 3: Structured Output ──"

[ -f internal/engine/structured.go ] && pass "structured.go exists" || fail "structured.go missing"
[ -f internal/engine/structured_test.go ] && pass "structured_test.go exists" || fail "structured_test.go missing"

grep -qi "StructuredValidator\|ValidateResponse\|extractJSON" internal/engine/structured.go 2>/dev/null && \
    pass "StructuredValidator implemented" || fail "StructuredValidator missing"

grep -qi "StructuredOutput\|structured_output" internal/config/config.go 2>/dev/null && \
    pass "StructuredOutputConfig in config" || fail "StructuredOutputConfig missing"

grep -qi "validateAndRetry\|SetValidator\|validator" internal/engine/brain.go 2>/dev/null && \
    pass "Validation in brain" || fail "Validation missing from brain"

STRUCT_TEST_COUNT=$(grep -c "func Test" internal/engine/structured_test.go 2>/dev/null || echo 0)
[ "$STRUCT_TEST_COUNT" -ge 8 ] && pass "Structured tests: $STRUCT_TEST_COUNT (≥8)" || \
    fail "Structured tests: $STRUCT_TEST_COUNT (<8)"

if go test ./internal/engine/... -count=1 -timeout 120s -run "(?i)struct|valid|schema|extract" 2>/dev/null; then
    pass "Structured output tests pass"
else fail "Structured output tests fail"; fi

echo ""

# ── Feature 4: OpenTelemetry ─────────────────
echo "── Feature 4: OpenTelemetry ──"

[ -d internal/otel ] && pass "otel package exists" || fail "otel package missing"
[ -f internal/otel/otel.go ] && pass "otel.go exists" || fail "otel.go missing"
[ -f internal/otel/spans.go ] && pass "spans.go exists" || fail "spans.go missing"
[ -f internal/otel/metrics.go ] && pass "metrics.go exists" || fail "metrics.go missing"
[ -f internal/otel/otel_test.go ] && pass "otel_test.go exists" || fail "otel_test.go missing"

grep -q "go.opentelemetry.io/otel" go.mod 2>/dev/null && \
    pass "OTel dependency in go.mod" || fail "OTel dependency missing"

grep -qi "Telemetry\|telemetry" internal/config/config.go 2>/dev/null && \
    pass "Telemetry config" || fail "Telemetry config missing"

grep -qi "tracer\|trace\.Span\|span\.End" internal/engine/engine.go 2>/dev/null && \
    pass "Tracing in engine" || fail "Tracing missing from engine"

grep -qi "tracer\|trace\.Span\|span\.End" internal/engine/brain.go 2>/dev/null && \
    pass "Tracing in brain" || fail "Tracing missing from brain"

if go test ./internal/otel/... -count=1 -timeout 120s 2>/dev/null; then
    pass "OTel tests pass"
else fail "OTel tests fail"; fi

echo ""

# ── Feature 5: Gateway Security ──────────────
echo "── Feature 5: Gateway Security ──"

[ -f internal/gateway/auth.go ] && pass "auth.go exists" || fail "auth.go missing"
[ -f internal/gateway/auth_test.go ] && pass "auth_test.go exists" || fail "auth_test.go missing"
[ -f internal/gateway/ratelimit.go ] && pass "ratelimit.go exists" || fail "ratelimit.go missing"
[ -f internal/gateway/ratelimit_test.go ] && pass "ratelimit_test.go exists" || fail "ratelimit_test.go missing"
[ -f internal/gateway/cors.go ] && pass "cors.go exists" || fail "cors.go missing"
[ -f internal/gateway/cors_test.go ] && pass "cors_test.go exists" || fail "cors_test.go missing"

grep -qi "AuthMiddleware\|AuthConfig" internal/gateway/auth.go 2>/dev/null && \
    pass "Auth middleware implemented" || fail "Auth middleware missing"

grep -qi "TokenBucket\|RateLimitMiddleware" internal/gateway/ratelimit.go 2>/dev/null && \
    pass "Rate limiter implemented" || fail "Rate limiter missing"

grep -qi "CORSConfig\|Access-Control" internal/gateway/cors.go 2>/dev/null && \
    pass "CORS middleware implemented" || fail "CORS middleware missing"

grep -qi "buildHandler\|Wrap\|middleware" internal/gateway/gateway.go 2>/dev/null && \
    pass "Middleware wired in gateway" || fail "Middleware wiring missing"

AUTH_TEST_COUNT=$(grep -c "func Test" internal/gateway/auth_test.go 2>/dev/null || echo 0)
[ "$AUTH_TEST_COUNT" -ge 6 ] && pass "Auth tests: $AUTH_TEST_COUNT (≥6)" || \
    fail "Auth tests: $AUTH_TEST_COUNT (<6)"

RL_TEST_COUNT=$(grep -c "func Test" internal/gateway/ratelimit_test.go 2>/dev/null || echo 0)
[ "$RL_TEST_COUNT" -ge 4 ] && pass "Rate limit tests: $RL_TEST_COUNT (≥4)" || \
    fail "Rate limit tests: $RL_TEST_COUNT (<4)"

if go test ./internal/gateway/... -count=1 -timeout 120s -run "(?i)auth|rate|cors" 2>/dev/null; then
    pass "Security tests pass"
else fail "Security tests fail"; fi

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
    [ "$DELTA" -ge 80 ] && pass "Test delta: +$DELTA (≥80)" || \
        warn "Test delta: +$DELTA (target ≥80)"
else
    echo "  Tests: $CURRENT (no baseline captured — run baseline step first)"
    warn "No test baseline — cannot verify delta"
fi

# Schema version (v14 includes loop_checkpoints from v0.5)
grep -q "schemaVersionV14\|schemaVersion.*= 14\|SchemaVersion.*14" internal/persistence/store.go 2>/dev/null && \
    pass "Schema v14" || fail "Schema not at v14"

# Version string
grep -q "v0\.5" cmd/goclaw/main.go 2>/dev/null && \
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
    echo -e "${GREEN}🎉 v0.5 VERIFICATION PASSED${NC}"
    exit 0
else
    echo -e "${RED}💀 v0.5 VERIFICATION FAILED — $FAIL issue(s)${NC}"
    exit 1
fi
