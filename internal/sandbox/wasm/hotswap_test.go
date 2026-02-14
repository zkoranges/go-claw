package wasm_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
	"github.com/basket/go-claw/internal/sandbox/wasm"
)

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}

func TestWatcher_NotificationsForCompileAndLoad(t *testing.T) {
	// [SPEC: SPEC-GOAL-G4, SPEC-HOTSWAP-WATCH-2, SPEC-HOTSWAP-WATCH-3] [PDR: V-26]
	binDir := t.TempDir()
	fakeTinyGo := filepath.Join(binDir, "tinygo")
	writeExecutable(t, fakeTinyGo, `#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    out="$1"
  fi
  shift
done
printf '\0asm\1\0\0\0' > "$out"
exit 0
`)

	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	host, err := wasm.NewHost(context.Background(), wasm.Config{
		Store:  store,
		Policy: policy.Default(),
	})
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	defer func() { _ = host.Close(context.Background()) }()

	skillDir := t.TempDir()
	w := wasm.NewWatcher(skillDir, host, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatalf("start watcher: %v", err)
	}

	if err := os.WriteFile(filepath.Join(skillDir, "random.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	var sawCompile, sawLoaded, sawUpdated bool
	deadline := time.After(3 * time.Second)
	for !(sawCompile && sawLoaded && sawUpdated) {
		select {
		case msg := <-w.Notifications():
			if strings.Contains(msg.Message, "Compiling random") {
				sawCompile = true
			}
			if strings.Contains(msg.Message, "Skill Loaded: random") {
				sawLoaded = true
			}
		case updated := <-w.ToolsUpdated():
			if updated == "random.go" {
				sawUpdated = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for notifications: compile=%t loaded=%t updated=%t", sawCompile, sawLoaded, sawUpdated)
		}
	}
}

func TestWatcher_SyntaxErrorEmitsErrorNotification(t *testing.T) {
	// [SPEC: SPEC-GOAL-G4] [PDR: V-26]
	binDir := t.TempDir()
	fakeTinyGo := filepath.Join(binDir, "tinygo")
	writeExecutable(t, fakeTinyGo, `#!/bin/sh
echo "syntax error: unexpected token" 1>&2
exit 1
`)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	host, err := wasm.NewHost(context.Background(), wasm.Config{
		Store:  store,
		Policy: policy.Default(),
	})
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	defer func() { _ = host.Close(context.Background()) }()

	skillDir := t.TempDir()
	w := wasm.NewWatcher(skillDir, host, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatalf("start watcher: %v", err)
	}

	if err := os.WriteFile(filepath.Join(skillDir, "broken.go"), []byte("package main\nfunc {\n"), 0o644); err != nil {
		t.Fatalf("write broken skill: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case msg := <-w.Notifications():
			if strings.Contains(msg.Message, "Skill compile error (broken)") {
				return
			}
		case <-deadline:
			t.Fatalf("expected syntax-error notification")
		}
	}
}

func TestWatcher_ABIMismatchPreventsActivation(t *testing.T) {
	binDir := t.TempDir()
	fakeTinyGo := filepath.Join(binDir, "tinygo")
	writeExecutable(t, fakeTinyGo, `#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    out="$1"
  fi
  shift
done
printf '\0asm\1\0\0\0' > "$out"
exit 0
`)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	host, err := wasm.NewHost(context.Background(), wasm.Config{
		Store:  store,
		Policy: policy.Default(),
	})
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	defer func() { _ = host.Close(context.Background()) }()

	skillDir := t.TempDir()
	w := wasm.NewWatcher(skillDir, host, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatalf("start watcher: %v", err)
	}

	if err := os.WriteFile(filepath.Join(skillDir, "random.abi"), []byte("v2\n"), 0o644); err != nil {
		t.Fatalf("write abi: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "random.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case msg := <-w.Notifications():
			if strings.Contains(msg.Message, "ABI mismatch") {
				if host.HasModule("random") {
					t.Fatalf("expected ABI mismatch to preserve no active random module")
				}
				return
			}
		case <-deadline:
			t.Fatalf("expected ABI mismatch notification")
		}
	}
}

func TestWatcher_ReloadFailureRollsBackPreviousModule(t *testing.T) {
	binDir := t.TempDir()
	fakeTinyGo := filepath.Join(binDir, "tinygo")
	writeExecutable(t, fakeTinyGo, `#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    out="$1"
  fi
  shift
done
printf '\0asm\1\0\0\0' > "$out"
exit 0
`)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	host, err := wasm.NewHost(context.Background(), wasm.Config{
		Store:  store,
		Policy: policy.Default(),
	})
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	defer func() { _ = host.Close(context.Background()) }()

	skillDir := t.TempDir()
	w := wasm.NewWatcher(skillDir, host, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatalf("start watcher: %v", err)
	}

	src := filepath.Join(skillDir, "random.go")
	if err := os.WriteFile(src, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write initial skill: %v", err)
	}
	loaded := false
	deadline := time.After(3 * time.Second)
	for !loaded {
		select {
		case msg := <-w.Notifications():
			if strings.Contains(msg.Message, "Skill Loaded: random") {
				loaded = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for initial load")
		}
	}
	if !host.HasModule("random") {
		t.Fatalf("expected initial random module to be active")
	}

	// Rewrite tinygo stub to emit invalid bytes so next reload fails loading.
	writeExecutable(t, fakeTinyGo, `#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    out="$1"
  fi
  shift
done
echo "not a wasm module" > "$out"
exit 0
`)
	if err := os.WriteFile(src, []byte("package main // trigger reload\n"), 0o644); err != nil {
		t.Fatalf("rewrite skill source: %v", err)
	}

	failDeadline := time.After(3 * time.Second)
	for {
		select {
		case msg := <-w.Notifications():
			if strings.Contains(msg.Message, "Skill load error (random)") {
				if !host.HasModule("random") {
					t.Fatalf("expected previous module to remain active after failed reload")
				}
				return
			}
		case <-failDeadline:
			t.Fatalf("expected skill load error notification")
		}
	}
}
