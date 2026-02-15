package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validAgentYAML = `id: test-agent
display_name: Test Agent
soul: You are a test agent.
capabilities: [testing]
`

func setupPullTest(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	// Create minimal config that config.Load can parse
	configPath := filepath.Join(tmpDir, "config.yaml")
	os.WriteFile(configPath, []byte("agents: []\n"), 0o644)
	t.Setenv("GOCLAW_HOME", tmpDir)
	return tmpDir
}

func TestRunPullCommand_Valid(t *testing.T) {
	tmpDir := setupPullTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(validAgentYAML))
	}))
	defer srv.Close()

	if code := runPullCommand([]string{srv.URL}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	data, _ := os.ReadFile(filepath.Join(tmpDir, "config.yaml"))
	if !strings.Contains(string(data), "test-agent") {
		t.Fatal("config missing test-agent")
	}
}

func TestRunPullCommand_MissingID(t *testing.T) {
	setupPullTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("soul: no id\n"))
	}))
	defer srv.Close()
	if code := runPullCommand([]string{srv.URL}); code == 0 {
		t.Fatal("should fail for missing id")
	}
}

func TestRunPullCommand_MissingSoul(t *testing.T) {
	setupPullTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("id: soulless\n"))
	}))
	defer srv.Close()
	if code := runPullCommand([]string{srv.URL}); code == 0 {
		t.Fatal("should fail for missing soul")
	}
}

func TestRunPullCommand_DuplicateID(t *testing.T) {
	tmpDir := setupPullTest(t)
	os.WriteFile(filepath.Join(tmpDir, "config.yaml"),
		[]byte("agents:\n- agent_id: existing\n  soul: here\n"), 0o644)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("id: existing\nsoul: dup\n"))
	}))
	defer srv.Close()
	if code := runPullCommand([]string{srv.URL}); code == 0 {
		t.Fatal("should fail for duplicate ID")
	}
}

func TestRunPullCommand_HTTP404(t *testing.T) {
	setupPullTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()
	if code := runPullCommand([]string{srv.URL}); code == 0 {
		t.Fatal("should fail for 404")
	}
}

func TestRunPullCommand_HTMLResponse(t *testing.T) {
	setupPullTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>not yaml</html>"))
	}))
	defer srv.Close()
	if code := runPullCommand([]string{srv.URL}); code == 0 {
		t.Fatal("should fail for HTML")
	}
}

func TestRunPullCommand_InvalidYAML(t *testing.T) {
	setupPullTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{{{{not yaml"))
	}))
	defer srv.Close()
	if code := runPullCommand([]string{srv.URL}); code == 0 {
		t.Fatal("should fail for invalid YAML")
	}
}

func TestRunPullCommand_NoArgs(t *testing.T) {
	if code := runPullCommand(nil); code == 0 {
		t.Fatal("should fail with no args")
	}
}

func TestRunPullCommand_InvalidURL(t *testing.T) {
	setupPullTest(t)
	if code := runPullCommand([]string{"not-a-url"}); code == 0 {
		t.Fatal("should fail for invalid URL")
	}
}

func TestRunPullCommand_CapabilitiesPreserved(t *testing.T) {
	tmpDir := setupPullTest(t)
	yaml := `id: capable-agent
soul: I have capabilities.
capabilities: [coding, testing, review]
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(yaml))
	}))
	defer srv.Close()

	if code := runPullCommand([]string{srv.URL}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	data, _ := os.ReadFile(filepath.Join(tmpDir, "config.yaml"))
	content := string(data)
	if !strings.Contains(content, "coding") {
		t.Fatal("capabilities not preserved")
	}
}

func TestRunPullCommand_DisplayNameDefault(t *testing.T) {
	tmpDir := setupPullTest(t)
	yaml := `id: my-agent
soul: Test
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(yaml))
	}))
	defer srv.Close()

	if code := runPullCommand([]string{srv.URL}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	data, _ := os.ReadFile(filepath.Join(tmpDir, "config.yaml"))
	content := string(data)
	// DisplayName should default to ID
	if !strings.Contains(content, "my-agent") {
		t.Fatal("agent not added")
	}
}
