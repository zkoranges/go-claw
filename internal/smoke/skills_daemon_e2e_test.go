package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type rpcReq struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type skillInfo struct {
	Name      string `json:"Name"`
	Type      string `json:"Type"`
	Source    string `json:"Source"`
	SourceURL string `json:"SourceURL"`
}

type skillStatus struct {
	Info     skillInfo `json:"Info"`
	Enabled  bool      `json:"Enabled"`
	Eligible bool      `json:"Eligible"`
	Missing  []string  `json:"Missing"`
}

type statusResult struct {
	Skills []skillStatus `json:"skills"`
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("copy tree mkdir: %v", err)
	}
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		outPath := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(outPath, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			// Fail closed; fixtures should not contain symlinks.
			return nil
		}
		srcF, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcF.Close()

		mode := info.Mode() & 0o777
		if mode == 0 {
			mode = 0o644
		}
		dstF, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		defer dstF.Close()
		if _, err := io.Copy(dstF, srcF); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("copy tree: %v", err)
	}
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, strings.TrimSpace(string(out)))
	}
}

func initRepoFromFixture(t *testing.T, fixtureDir string) string {
	t.Helper()
	repoDir := t.TempDir()
	copyTree(t, fixtureDir, repoDir)
	gitCmd(t, repoDir, "init", "-b", "main")
	gitCmd(t, repoDir, "config", "user.email", "test@example.com")
	gitCmd(t, repoDir, "config", "user.name", "Test")
	gitCmd(t, repoDir, "add", ".")
	gitCmd(t, repoDir, "commit", "-m", "initial")
	return repoDir
}

func wsURL(addr string) string {
	if strings.HasPrefix(addr, "http://") {
		return "ws" + addr[len("http"):] + "/ws"
	}
	if strings.HasPrefix(addr, "https://") {
		return "wss" + addr[len("https"):] + "/ws"
	}
	if strings.HasPrefix(addr, "ws://") || strings.HasPrefix(addr, "wss://") {
		return addr + "/ws"
	}
	return "ws://" + addr + "/ws"
}

func dialWS(t *testing.T, addr, token string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(addr), &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + token},
		},
	})
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "test done") })
	return conn
}

func sendHello(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, conn, rpcReq{JSONRPC: "2.0", ID: 1, Method: "system.hello", Params: map[string]any{"version": "1.0"}}); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	var resp rpcResp
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatalf("read hello: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("hello error: %+v", resp.Error)
	}
}

func getStatus(t *testing.T, conn *websocket.Conn) statusResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, conn, rpcReq{JSONRPC: "2.0", ID: 2, Method: "system.status"}); err != nil {
		t.Fatalf("write status: %v", err)
	}
	var resp rpcResp
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("status error: %+v", resp.Error)
	}
	var out statusResult
	_ = json.Unmarshal(resp.Result, &out)
	return out
}

func findSkill(status statusResult, name string) *skillStatus {
	for i := range status.Skills {
		if status.Skills[i].Info.Name == name {
			return &status.Skills[i]
		}
	}
	return nil
}

func waitForSkill(t *testing.T, conn *websocket.Conn, name string, wantPresent bool, timeout time.Duration) *skillStatus {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s := getStatus(t, conn)
		got := findSkill(s, name)
		if wantPresent && got != nil {
			return got
		}
		if !wantPresent && got == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if wantPresent {
		t.Fatalf("timed out waiting for skill %q to appear", name)
	} else {
		t.Fatalf("timed out waiting for skill %q to disappear", name)
	}
	return nil
}

func TestSmoke_SkillsDaemonE2E_StatusInstallRemove(t *testing.T) {
	requireGit(t)
	bin := buildGoclawBinary(t)

	home := t.TempDir()
	addr := pickFreeAddr(t)
	token := "smoke-skill-token"

	// Policy must allow ACP reads for system.status; skill.inject is intentionally not granted.
	policyData := "allow_capabilities:\n  - acp.read\n  - acp.mutate\n"
	if err := os.WriteFile(filepath.Join(home, "policy.yaml"), []byte(policyData), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.token"), []byte(token+"\n"), 0o600); err != nil {
		t.Fatalf("write auth token: %v", err)
	}

	// Create a workspace with a project skill.
	workspace := t.TempDir()
	projectSkillName := "proj-skill"
	projectSkillDir := filepath.Join(workspace, "skills", projectSkillName)
	if err := os.MkdirAll(projectSkillDir, 0o755); err != nil {
		t.Fatalf("mkdir project skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectSkillDir, "SKILL.md"), []byte(`---
name: proj-skill
description: Project skill
---

Use this skill when asked about proj-skill.
`), 0o644); err != nil {
		t.Fatalf("write project SKILL.md: %v", err)
	}

	// Create a user skill.
	userSkillName := "user-skill"
	userSkillDir := filepath.Join(home, "skills", userSkillName)
	if err := os.MkdirAll(userSkillDir, 0o755); err != nil {
		t.Fatalf("mkdir user skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userSkillDir, "SKILL.md"), []byte(`---
name: user-skill
description: User skill
---

Use this skill when asked about user-skill.
`), 0o644); err != nil {
		t.Fatalf("write user SKILL.md: %v", err)
	}

	cmd := exec.Command(bin, "-daemon")
	cmd.Dir = workspace
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

	// Wait for daemon to accept WebSocket connections.
	var conn *websocket.Conn
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		try := func() (*websocket.Conn, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			c, _, err := websocket.Dial(ctx, wsURL(addr), &websocket.DialOptions{
				HTTPHeader: http.Header{
					"Authorization": []string{"Bearer " + token},
				},
			})
			return c, err
		}
		c, err := try()
		if err == nil {
			conn = c
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if conn == nil {
		t.Fatalf("daemon did not become ready in time\noutput=%s", out.String())
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	sendHello(t, conn)

	// Verify initial skill list includes project + user skills.
	if got := waitForSkill(t, conn, projectSkillName, true, 3*time.Second); got.Info.Source != "project" {
		t.Fatalf("expected project skill source=project, got %q", got.Info.Source)
	}
	if got := waitForSkill(t, conn, userSkillName, true, 3*time.Second); got.Info.Source != "user" {
		t.Fatalf("expected user skill source=user, got %q", got.Info.Source)
	}

	// Install a skill from a local git fixture (no network) via CLI.
	root := moduleRoot(t)
	repo := initRepoFromFixture(t, filepath.Join(root, "internal", "skills", "testdata", "valid_repo"))
	installName := "local-" + filepath.Base(repo)

	installCmd := exec.Command(bin, "skill", "install", repo)
	installCmd.Env = append(os.Environ(),
		"GOCLAW_HOME="+home,
		"GOCLAW_NO_TUI=1",
	)
	var installOut bytes.Buffer
	installCmd.Stdout = &installOut
	installCmd.Stderr = &installOut
	if err := installCmd.Run(); err != nil {
		t.Fatalf("skill install failed: %v\n%s", err, installOut.String())
	}

	// Verify it appears in system.status with provenance source=local and disabled by default.
	installedSkill := waitForSkill(t, conn, installName, true, 5*time.Second)
	if installedSkill.Info.Source != "local" {
		t.Fatalf("expected installed skill source=local, got %q", installedSkill.Info.Source)
	}
	if strings.TrimSpace(installedSkill.Info.SourceURL) == "" {
		t.Fatalf("expected installed skill to have non-empty SourceURL")
	}
	if installedSkill.Enabled {
		t.Fatalf("expected installed skill to be disabled by default (missing skill.inject)")
	}
	missingCap := false
	for _, m := range installedSkill.Missing {
		if strings.Contains(m, "skill.inject") {
			missingCap = true
		}
	}
	if !missingCap {
		t.Fatalf("expected Missing to include skill.inject; got %#v", installedSkill.Missing)
	}

	// Remove it via CLI and verify it is gone in status.
	removeCmd := exec.Command(bin, "skill", "remove", installName)
	removeCmd.Env = append(os.Environ(),
		"GOCLAW_HOME="+home,
		"GOCLAW_NO_TUI=1",
	)
	var removeOut bytes.Buffer
	removeCmd.Stdout = &removeOut
	removeCmd.Stderr = &removeOut
	if err := removeCmd.Run(); err != nil {
		t.Fatalf("skill remove failed: %v\n%s", err, removeOut.String())
	}
	_ = waitForSkill(t, conn, installName, false, 5*time.Second)
}
