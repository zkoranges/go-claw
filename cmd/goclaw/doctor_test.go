package main

import (
	"context"
	"os"
	"testing"
)

func TestRunDoctorCommand_TextOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOCLAW_HOME", home)
	// Write minimal config so doctor doesn't fail on load.
	if err := os.WriteFile(home+"/config.yaml", []byte("worker_count: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code := runDoctorCommand(context.Background(), nil)
	// Doctor may return 0 or 1 depending on environment (e.g., no API key),
	// but it should not panic or return 2.
	if code == 2 {
		t.Fatalf("unexpected exit code 2 (parse error)")
	}
}

func TestRunDoctorCommand_JSONOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOCLAW_HOME", home)
	if err := os.WriteFile(home+"/config.yaml", []byte("worker_count: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// -json flag should produce parseable JSON output (exit 0).
	code := runDoctorCommand(context.Background(), []string{"-json"})
	if code != 0 {
		t.Fatalf("got exit code %d, want 0 for JSON output", code)
	}
}

func TestRunDoctorCommand_DoubleJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOCLAW_HOME", home)
	if err := os.WriteFile(home+"/config.yaml", []byte("worker_count: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// --json should also work.
	code := runDoctorCommand(context.Background(), []string{"--json"})
	if code != 0 {
		t.Fatalf("got exit code %d, want 0 for --json", code)
	}
}

func TestRunDoctorCommand_NeedsGenesis(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOCLAW_HOME", home)
	// No config.yaml at all — triggers NeedsGenesis path.

	code := runDoctorCommand(context.Background(), nil)
	// Should still complete (diagnoses the problem), not crash.
	if code < 0 {
		t.Fatalf("unexpected negative exit code: %d", code)
	}
}
