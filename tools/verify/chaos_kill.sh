#!/usr/bin/env bash
set -euo pipefail

# Chaos SIGKILL recovery drill:
# - prepare a queued task
# - claim + start it in a helper process that sleeps forever
# - SIGKILL the helper (simulating a crash mid-execution)
# - run recovery and assert the task is re-queued (QUEUED)
#
# This is intentionally hermetic and does not require network access.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT="${1:-}"

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT

db="$tmpdir/goclaw.db"
bin="$tmpdir/lease_recovery_crash"

cd "$ROOT"

echo "ROOT=$ROOT"
echo "TMPDIR=$tmpdir"
echo "DB=$db"

echo "BUILD lease_recovery_crash..."
go build -o "$bin" ./tools/verify/lease_recovery_crash

echo "PREPARE task..."
"$bin" -mode prepare -db "$db" | tee "$tmpdir/prepare.txt"

echo "CLAIM+SLEEP (background)..."
"$bin" -mode claim-sleep -db "$db" >"$tmpdir/claim_sleep.txt" 2>&1 &
pid="$!"

claimed=""
deadline="$((SECONDS + 5))"
while [[ -z "$claimed" && "$SECONDS" -lt "$deadline" ]]; do
  if [[ -f "$tmpdir/claim_sleep.txt" ]]; then
    claimed="$(grep -Eo 'CLAIMED_TASK_ID=[a-zA-Z0-9-]+' "$tmpdir/claim_sleep.txt" || true)"
  fi
  sleep 0.1
done

if [[ -z "$claimed" ]]; then
  echo "ERROR: helper did not claim task in time" >&2
  echo "claim_sleep_log:"
  cat "$tmpdir/claim_sleep.txt" || true
  exit 1
fi
echo "$claimed"

echo "SIGKILL pid=$pid"
kill -9 "$pid" || true

echo "RECOVER..."
recover_out="$tmpdir/recover.txt"
"$bin" -mode recover -db "$db" | tee "$recover_out"

grep -q "VERDICT PASS" "$recover_out"
grep -q "status=QUEUED" "$recover_out"

echo "VERDICT PASS (SIGKILL recovery)"

if [[ -n "$OUT" ]]; then
  mkdir -p "$(dirname "$OUT")"
  {
    echo "# chaos_kill.sh output"
    echo "# date_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo
    echo "## prepare"
    cat "$tmpdir/prepare.txt"
    echo
    echo "## claim_sleep"
    cat "$tmpdir/claim_sleep.txt"
    echo
    echo "## recover"
    cat "$tmpdir/recover.txt"
  } >"$OUT"
  echo "WROTE $OUT"
fi

