package smoke

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func buildIronclawBinary(t *testing.T) string {
	t.Helper()
	root := moduleRoot(t)
	outPath := filepath.Join(t.TempDir(), "goclaw")
	cmd := exec.Command("go", "build", "-o", outPath, "./cmd/goclaw")
	cmd.Dir = root
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, buf.String())
	}
	return outPath
}

func pickFreeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick free addr: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func TestSmoke_StartupPhasesFollowRequiredOrder(t *testing.T) {
	bin := buildIronclawBinary(t)
	home := t.TempDir()
	addr := pickFreeAddr(t)

	policyData := "allow_capabilities:\n  - acp.read\n  - acp.mutate\n"
	if err := os.WriteFile(filepath.Join(home, "policy.yaml"), []byte(policyData), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.token"), []byte("smoke-token\n"), 0o600); err != nil {
		t.Fatalf("write auth token: %v", err)
	}

	cmd := exec.Command(bin, "-daemon")
	cmd.Env = append(os.Environ(),
		"GOCLAW_HOME="+home,
		"GOCLAW_BIND_ADDR="+addr,
		"GOCLAW_NO_TUI=1",
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}

	logPath := filepath.Join(home, "logs", "system.jsonl")
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(logPath)
		if strings.Contains(string(data), `"phase":"scheduler_started"`) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	_ = cmd.Process.Signal(os.Interrupt)
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("daemon did not exit after signal")
	case <-waitDone:
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read logs: %v", err)
	}

	phases := map[string]int{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		phase, _ := entry["phase"].(string)
		if phase == "" {
			continue
		}
		if _, exists := phases[phase]; !exists {
			phases[phase] = lineNo
		}
	}
	required := []string{
		"config_loaded",
		"schema_migrated",
		"recovery_scan_completed",
		"policy_loaded",
		"acp_listener_bound",
		"scheduler_started",
	}
	for _, phase := range required {
		if _, ok := phases[phase]; !ok {
			t.Fatalf("missing startup phase %q in logs\noutput=%s", phase, out.String())
		}
	}
	for i := 1; i < len(required); i++ {
		prev := required[i-1]
		cur := required[i]
		if phases[prev] >= phases[cur] {
			t.Fatalf("phase ordering invalid: %s(%d) >= %s(%d)", prev, phases[prev], cur, phases[cur])
		}
	}
}

func TestSmoke_StartupFailureEmitsReasonCode(t *testing.T) {
	bin := buildIronclawBinary(t)
	home := t.TempDir()

	invalidPolicy := "allow_capabilities:\n  - acp.invalid\n"
	if err := os.WriteFile(filepath.Join(home, "policy.yaml"), []byte(invalidPolicy), 0o644); err != nil {
		t.Fatalf("write invalid policy: %v", err)
	}

	cmd := exec.Command(bin, "-daemon")
	cmd.Env = append(os.Environ(),
		"GOCLAW_HOME="+home,
		"GOCLAW_BIND_ADDR="+pickFreeAddr(t),
		"GOCLAW_NO_TUI=1",
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected startup failure for invalid policy")
	}

	logData, _ := os.ReadFile(filepath.Join(home, "logs", "system.jsonl"))
	combined := string(logData) + "\n" + out.String()
	if !strings.Contains(combined, `"reason_code":"E_POLICY_LOAD"`) {
		t.Fatalf("expected structured startup reason_code in output/logs\ncombined=%s", combined)
	}
	if !strings.Contains(combined, `"msg":"startup failure"`) {
		t.Fatalf("expected startup failure log message\ncombined=%s", combined)
	}
	if !strings.Contains(combined, `"component":"runtime"`) {
		t.Fatalf("expected runtime component field\ncombined=%s", combined)
	}
	if !strings.Contains(combined, fmt.Sprintf(`"level":"%s"`, "ERROR")) &&
		!strings.Contains(combined, fmt.Sprintf(`"level":"%s"`, "error")) {
		t.Fatalf("expected error level in output/logs\ncombined=%s", combined)
	}
}
