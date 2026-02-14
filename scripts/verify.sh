#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

GATE="${1:-gate0}"
EVID_DIR="$ROOT/docs/EVIDENCE"
DIST_DIR="$ROOT/dist"

mkdir -p "$EVID_DIR" "$DIST_DIR"

BUILD_EVID="$EVID_DIR/${GATE}_build.txt"
TEST_JSON="$EVID_DIR/${GATE}_test_report.json"

{
  echo "== go version =="
  go version
  echo
  echo "== go env (selected) =="
  go env GOOS GOARCH GOMOD GOPATH GOCACHE
  echo
  echo "== go vet =="
} >"$BUILD_EVID"

go vet ./... >>"$BUILD_EVID" 2>&1 || true

{
  echo
  echo "== gofmt =="
} >>"$BUILD_EVID"

GOFILES="$(find . -name '*.go' -not -path './dist/*' -not -path './docs/EVIDENCE/*' -print)"
if [[ -n "${GOFILES}" ]]; then
  # shellcheck disable=SC2086
  gofmt -w ${GOFILES} >>"$BUILD_EVID" 2>&1 || true
else
  echo "(no go files)" >>"$BUILD_EVID"
fi

{
  echo
  echo "== go build =="
  echo "go build -trimpath -o \"$DIST_DIR/goclaw\" ./cmd/goclaw"
} >>"$BUILD_EVID"

go build -trimpath -o "$DIST_DIR/goclaw" ./cmd/goclaw >>"$BUILD_EVID" 2>&1

{
  echo
  echo "== binary info =="
  ls -la "$DIST_DIR/goclaw"
  (command -v file >/dev/null 2>&1 && file "$DIST_DIR/goclaw") || true
} >>"$BUILD_EVID"

go test -json ./... >"$TEST_JSON"

