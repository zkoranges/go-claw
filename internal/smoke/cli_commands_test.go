package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestSmoke_CLIStatusOutputsHealthzJSON(t *testing.T) {
	bin := buildIronclawBinary(t)
	home := t.TempDir()
	addr := pickFreeAddr(t)

	// Minimal policy to allow daemon startup; /healthz does not require ACP capabilities.
	policyData := "allow_capabilities:\n  - acp.read\n  - acp.mutate\n"
	if err := os.WriteFile(filepath.Join(home, "policy.yaml"), []byte(policyData), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
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
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	// Poll until status succeeds.
	deadline := time.Now().Add(8 * time.Second)
	var statusOut string
	for time.Now().Before(deadline) {
		s := exec.Command(bin, "status")
		s.Env = append(os.Environ(),
			"GOCLAW_HOME="+home,
			"GOCLAW_BIND_ADDR="+addr,
		)
		var buf bytes.Buffer
		s.Stdout = &buf
		s.Stderr = &buf
		err := s.Run()
		if err == nil {
			statusOut = buf.String()
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if strings.TrimSpace(statusOut) == "" {
		t.Fatalf("status did not become ready in time\noutput=%s", out.String())
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(statusOut), &body); err != nil {
		t.Fatalf("status output not JSON: %v\nout=%s", err, statusOut)
	}
	if _, ok := body["healthy"]; !ok {
		t.Fatalf("expected healthy field in status output: %#v", body)
	}
}

func TestSmoke_CLIImportWritesConfigYAML(t *testing.T) {
	bin := buildIronclawBinary(t)
	home := t.TempDir()
	work := t.TempDir()

	if err := os.WriteFile(filepath.Join(work, ".env"), []byte(strings.Join([]string{
		"GEMINI_API_KEY=test-gemini-key",
		"GEMINI_MODEL=gemini-test-model",
		"BRAVE_API_KEY=test-brave-key",
		"",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, bin, "import")
	c.Dir = work
	c.Env = append(os.Environ(),
		"GOCLAW_HOME="+home,
		"GOCLAW_NO_TUI=1",
	)
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = &out
	if err := c.Run(); err != nil {
		t.Fatalf("import failed: %v\n%s", err, out.String())
	}

	raw, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	cfg := make(map[string]any)
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse config.yaml: %v", err)
	}
	if cfg["gemini_api_key"] != "test-gemini-key" {
		t.Fatalf("expected gemini_api_key to be imported")
	}
	if cfg["gemini_model"] != "gemini-test-model" {
		t.Fatalf("expected gemini_model to be imported")
	}
	apiKeys, _ := cfg["api_keys"].(map[string]any)
	if apiKeys == nil || apiKeys["brave_search"] != "test-brave-key" {
		t.Fatalf("expected api_keys.brave_search to be imported; got %#v", apiKeys)
	}
}
