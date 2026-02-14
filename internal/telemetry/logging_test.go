package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewLogger_EmitsStructuredSchema(t *testing.T) {
	home := t.TempDir()
	logger, closer, err := NewLogger(home, "debug", true)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	defer closer.Close()

	logger.Info("startup phase", "phase", "config_loaded", "task_id", "task-1")

	logPath := filepath.Join(home, "logs", "system.jsonl")
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		t.Fatalf("expected at least one log line")
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("unmarshal log json: %v", err)
	}

	required := []string{"timestamp", "level", "msg", "component", "trace_id"}
	for _, key := range required {
		if _, ok := entry[key]; !ok {
			t.Fatalf("missing required key %q in log entry: %#v", key, entry)
		}
	}
	if entry["component"] != "runtime" {
		t.Fatalf("expected component=runtime, got %#v", entry["component"])
	}
	if entry["trace_id"] != "-" {
		t.Fatalf("expected trace_id='-', got %#v", entry["trace_id"])
	}
	if entry["task_id"] != "task-1" {
		t.Fatalf("expected task_id propagation, got %#v", entry["task_id"])
	}
}

func TestNewLogger_RedactsSensitiveFields(t *testing.T) {
	home := t.TempDir()
	logger, closer, err := NewLogger(home, "info", true)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	defer closer.Close()

	logger.Info("security check",
		"api_key", "abc123",
		"auth_header", "Authorization: Bearer super-secret-token",
	)

	logPath := filepath.Join(home, "logs", "system.jsonl")
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 0 {
		t.Fatalf("expected log line")
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &entry); err != nil {
		t.Fatalf("unmarshal log: %v", err)
	}
	if entry["api_key"] != "[REDACTED]" {
		t.Fatalf("expected api_key redaction, got %#v", entry["api_key"])
	}
	if entry["auth_header"] != "[REDACTED]" {
		t.Fatalf("expected auth_header redaction, got %#v", entry["auth_header"])
	}
}
