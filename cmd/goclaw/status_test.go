package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestRunStatusCommand_ExtraArgs(t *testing.T) {
	code := runStatusCommand(context.Background(), []string{"extra"})
	if code != 2 {
		t.Fatalf("got exit code %d, want 2", code)
	}
}

func TestRunStatusCommand_HealthyServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer ts.Close()

	setTestConfig(t, ts.Listener.Addr().String())

	code := runStatusCommand(context.Background(), nil)
	if code != 0 {
		t.Fatalf("got exit code %d, want 0", code)
	}
}

func TestRunStatusCommand_UnhealthyServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"status":"unhealthy"}`))
	}))
	defer ts.Close()

	setTestConfig(t, ts.Listener.Addr().String())

	code := runStatusCommand(context.Background(), nil)
	if code != 1 {
		t.Fatalf("got exit code %d, want 1", code)
	}
}

func TestRunStatusCommand_ConnectionRefused(t *testing.T) {
	setTestConfig(t, "127.0.0.1:1")

	code := runStatusCommand(context.Background(), nil)
	if code != 1 {
		t.Fatalf("got exit code %d, want 1 for connection refused", code)
	}
}

func TestRunStatusCommand_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	setTestConfig(t, "127.0.0.1:18789")

	code := runStatusCommand(ctx, nil)
	if code != 1 {
		t.Fatalf("got exit code %d, want 1 for cancelled context", code)
	}
}

// setTestConfig writes a minimal config.yaml to a temp dir and sets GOCLAW_HOME.
func setTestConfig(t *testing.T, addr string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GOCLAW_HOME", home)
	yaml := `bind_addr: "` + addr + `"`
	if err := os.WriteFile(home+"/config.yaml", []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
