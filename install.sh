#!/usr/bin/env bash
set -euo pipefail

REPO="zkoranges/go-claw"
MIN_GO_VERSION="1.24"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
GOCLAW_HOME="${GOCLAW_HOME:-$HOME/.goclaw}"

# Colors (disabled if not a terminal)
if [ -t 1 ]; then
  bold="\033[1m"  green="\033[32m"  red="\033[31m"  yellow="\033[33m"  reset="\033[0m"
else
  bold=""  green=""  red=""  yellow=""  reset=""
fi

info()  { printf "${bold}${green}==>${reset} %s\n" "$*"; }
warn()  { printf "${bold}${yellow}warning:${reset} %s\n" "$*"; }
fail()  { printf "${bold}${red}error:${reset} %s\n" "$*" >&2; exit 1; }

# --- Check Go ---
check_go() {
  command -v go >/dev/null 2>&1 || fail "Go is not installed. Install Go ${MIN_GO_VERSION}+ from https://go.dev/dl/"

  go_version=$(go version | grep -oE '[0-9]+\.[0-9]+' | head -1)
  go_major=$(echo "$go_version" | cut -d. -f1)
  go_minor=$(echo "$go_version" | cut -d. -f2)
  min_major=$(echo "$MIN_GO_VERSION" | cut -d. -f1)
  min_minor=$(echo "$MIN_GO_VERSION" | cut -d. -f2)

  if [ "$go_major" -lt "$min_major" ] || { [ "$go_major" -eq "$min_major" ] && [ "$go_minor" -lt "$min_minor" ]; }; then
    fail "Go ${go_version} found, but ${MIN_GO_VERSION}+ is required."
  fi
  info "Go ${go_version} found"
}

# --- Check git ---
check_git() {
  command -v git >/dev/null 2>&1 || fail "git is not installed."
}

# --- Detect install method ---
install_goclaw() {
  # If we're already inside the repo, build in place.
  if [ -f "go.mod" ] && grep -q "go-claw" go.mod 2>/dev/null; then
    info "Building from local source"
    go build -o goclaw ./cmd/goclaw
    binary="./goclaw"
  else
    # Clone to a temp directory and build.
    tmpdir=$(mktemp -d)
    trap 'rm -rf "$tmpdir"' EXIT
    info "Cloning github.com/${REPO}"
    git clone --depth 1 "https://github.com/${REPO}.git" "$tmpdir/go-claw" 2>&1 | tail -1
    cd "$tmpdir/go-claw"
    info "Building"
    go build -o goclaw ./cmd/goclaw
    binary="./goclaw"
  fi

  # Install binary
  if [ -w "$INSTALL_DIR" ]; then
    cp "$binary" "$INSTALL_DIR/goclaw"
  else
    info "Installing to ${INSTALL_DIR} (requires sudo)"
    sudo cp "$binary" "$INSTALL_DIR/goclaw"
  fi
  chmod +x "$INSTALL_DIR/goclaw"
  info "Installed to ${INSTALL_DIR}/goclaw"
}

# --- Create GOCLAW_HOME ---
setup_home() {
  if [ -d "$GOCLAW_HOME" ]; then
    info "GOCLAW_HOME already exists at ${GOCLAW_HOME}"
    return
  fi
  mkdir -p "$GOCLAW_HOME"
  info "Created ${GOCLAW_HOME}"
}

# --- Verify ---
verify() {
  if command -v goclaw >/dev/null 2>&1; then
    info "Verification: $(goclaw --help 2>&1 | head -1 || echo 'goclaw is on PATH')"
  elif [ -x "$INSTALL_DIR/goclaw" ]; then
    info "Installed successfully"
    if ! echo "$PATH" | tr ':' '\n' | grep -qx "$INSTALL_DIR"; then
      warn "${INSTALL_DIR} is not in your PATH. Add it with:"
      echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    fi
  fi
}

# --- Main ---
main() {
  printf "${bold}GoClaw Installer${reset}\n\n"
  check_git
  check_go
  install_goclaw
  setup_home
  verify
  printf "\n${bold}Done.${reset} Run ${green}goclaw${reset} to start (first run launches the setup wizard).\n"
}

main "$@"
