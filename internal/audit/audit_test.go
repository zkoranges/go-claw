package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordWritesAuditEntry(t *testing.T) {
	home := t.TempDir()
	if err := Init(home); err != nil {
		t.Fatalf("init audit: %v", err)
	}
	t.Cleanup(func() { _ = Close() })

	Record("deny", "acp.mutate", "missing_capability", "policy-abc", "agent.chat")
	Record("allow", "acp.read", "capability_granted", "policy-abc", "system.status")

	path := filepath.Join(home, "logs", "audit.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least two audit entries, got %d", len(lines))
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("unmarshal first audit entry: %v", err)
	}
	if first["decision"] != "deny" {
		t.Fatalf("expected deny decision, got %#v", first["decision"])
	}
	if first["capability"] != "acp.mutate" {
		t.Fatalf("expected capability acp.mutate, got %#v", first["capability"])
	}
	if first["reason"] == "" || first["policy_version"] == "" {
		t.Fatalf("expected reason and policy_version in audit entry: %#v", first)
	}
}

func TestAuditAppendOnly(t *testing.T) {
	// V-OBS-003: Audit logs MUST be append-only at application layer.
	home := t.TempDir()
	if err := Init(home); err != nil {
		t.Fatalf("init audit: %v", err)
	}
	t.Cleanup(func() { _ = Close() })

	// Write two entries.
	Record("allow", "test.op1", "test", "pol-v1", "subject1")
	Record("deny", "test.op2", "test2", "pol-v1", "subject2")

	path := filepath.Join(home, "logs", "audit.jsonl")

	// Capture file size after writes.
	info1, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat audit file: %v", err)
	}
	size1 := info1.Size()

	// Write a third entry.
	Record("allow", "test.op3", "test3", "pol-v1", "subject3")

	// File size must grow (append-only).
	info2, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat audit file after append: %v", err)
	}
	size2 := info2.Size()
	if size2 <= size1 {
		t.Fatalf("expected file to grow (append-only), size before=%d after=%d", size1, size2)
	}

	// Verify all three entries are present and in order.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}

	// Verify each line is valid JSON with expected fields.
	for i, line := range lines {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", i, err)
		}
		if _, ok := e["timestamp"]; !ok {
			t.Fatalf("line %d missing timestamp", i)
		}
		if _, ok := e["decision"]; !ok {
			t.Fatalf("line %d missing decision", i)
		}
	}
}
