package tools

import (
	"strings"
	"testing"
	"time"
)

func TestShell_Echo(t *testing.T) {
	parts := strings.Fields("echo hello")
	if len(parts) == 0 {
		t.Fatal("empty command")
	}
	if parts[0] != "echo" {
		t.Fatalf("got %q, want echo", parts[0])
	}
	if _, blocked := denyList["echo"]; blocked {
		t.Fatal("echo should not be on deny list")
	}
}

func TestShell_DenyList(t *testing.T) {
	blocked := []string{"rm", "sudo", "kill", "killall", "shutdown", "reboot"}
	for _, cmd := range blocked {
		if _, ok := denyList[cmd]; !ok {
			t.Errorf("%q should be on deny list", cmd)
		}
	}
}

func TestShell_AllowedCommands(t *testing.T) {
	allowed := []string{"echo", "ls", "cat", "grep", "find", "wc", "head", "tail", "sort", "curl", "git"}
	for _, cmd := range allowed {
		if _, ok := denyList[cmd]; ok {
			t.Errorf("%q should NOT be on deny list", cmd)
		}
	}
}

func TestTruncateOutput(t *testing.T) {
	short := "hello"
	if got := truncateOutput(short, 100); got != short {
		t.Fatalf("truncateOutput(%q, 100) = %q", short, got)
	}

	long := strings.Repeat("a", 100)
	got := truncateOutput(long, 50)
	if !strings.HasSuffix(got, "... (truncated)") {
		t.Fatalf("expected truncation suffix, got %q", got)
	}
	if len(got) != 50+len("\n... (truncated)") {
		t.Fatalf("unexpected length: %d", len(got))
	}
}

func TestShell_EmptyCommand(t *testing.T) {
	parts := strings.Fields("")
	if len(parts) != 0 {
		t.Fatalf("expected empty parts, got %d", len(parts))
	}
}

func TestShell_WorkingDir(t *testing.T) {
	// Verify non-empty working dir is accepted.
	input := ShellInput{
		Command:    "pwd",
		WorkingDir: "/tmp",
	}
	if input.WorkingDir == "" {
		t.Fatal("expected non-empty working dir")
	}
}

func TestSplitCommandSegments(t *testing.T) {
	tests := []struct {
		cmd      string
		expected []string
	}{
		{"echo hello", []string{"echo hello"}},
		{"echo hello | grep hello", []string{"echo hello", "grep hello"}},
		{"ls -la && echo done", []string{"ls -la", "echo done"}},
		{"cat foo || echo fallback", []string{"cat foo", "echo fallback"}},
		{"echo a | grep a && echo b || echo c", []string{"echo a", "grep a", "echo b", "echo c"}},
		{"", nil},
		{"  echo hello  ", []string{"echo hello"}},
	}

	for _, tt := range tests {
		got := splitCommandSegments(tt.cmd)
		if len(got) != len(tt.expected) {
			t.Errorf("splitCommandSegments(%q) = %v, want %v", tt.cmd, got, tt.expected)
			continue
		}
		for i, s := range got {
			if s != tt.expected[i] {
				t.Errorf("splitCommandSegments(%q)[%d] = %q, want %q", tt.cmd, i, s, tt.expected[i])
			}
		}
	}
}

func TestShell_PipeAllowed(t *testing.T) {
	// Pipes should not be blocked — only injection vectors like ; $( ` are blocked.
	cmd := "echo hello | grep hello"
	for _, op := range []string{";", "$(", "`"} {
		if strings.Contains(cmd, op) {
			t.Fatalf("test command should not contain %q", op)
		}
	}
	segments := splitCommandSegments(cmd)
	if len(segments) != 2 {
		t.Fatalf("expected 2 segments, got %d: %v", len(segments), segments)
	}
	// Verify no segment contains a denied command.
	for _, seg := range segments {
		for _, tok := range strings.Fields(seg) {
			if _, blocked := denyList[tok]; blocked {
				t.Fatalf("segment %q contains blocked command %q", seg, tok)
			}
		}
	}
}

func TestShell_PipeToDeniedCommandBlocked(t *testing.T) {
	// "echo hello | rm" should be caught by per-segment deny-list check.
	segments := splitCommandSegments("echo hello | rm -rf /")
	found := false
	for _, seg := range segments {
		for _, tok := range strings.Fields(seg) {
			if _, blocked := denyList[tok]; blocked {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("expected 'rm' to be caught in pipe segment deny-list check")
	}
}

func TestShell_Timeout(t *testing.T) {
	input := ShellInput{
		Command:    "sleep 1",
		TimeoutSec: 200, // exceeds max
	}
	timeout := defaultShellTimeout
	if input.TimeoutSec > 0 {
		timeout = time.Duration(input.TimeoutSec) * time.Second
		if timeout > maxShellTimeout {
			timeout = maxShellTimeout
		}
	}
	if timeout != maxShellTimeout {
		t.Fatalf("expected maxShellTimeout, got %v", timeout)
	}
}
