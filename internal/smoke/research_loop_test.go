package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/persistence"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type historyResult struct {
	Items []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"items"`
}

func TestSmoke_E2EResearchLoopCreatesToolSubTasks(t *testing.T) {
	// [Testing Gap T-10] Deterministic E2E research loop without external network:
	// - local search endpoint via GOCLAW_SEARCH_ENDPOINT
	// - local read_url pages
	// - verify tool calls recorded as tasks with type=tool

	// Local HTTP server provides a DDG-compatible HTML response plus content pages.
	mux := http.NewServeMux()
	s := httptest.NewServer(mux)
	defer s.Close()

	u, err := url.Parse(s.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	host := u.Hostname()

	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		q := strings.ToLower(r.URL.Query().Get("q"))
		target := s.URL + "/p4090"
		title := "RTX 4090 price"
		snippet := "RTX 4090 is $1599"
		if strings.Contains(q, "5090") {
			target = s.URL + "/p5090"
			title = "RTX 5090 price"
			snippet = "RTX 5090 is $9999"
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`
<html><body>
<a class="result__a" href="` + target + `">` + title + `</a>
<a class="result__snippet">` + snippet + `</a>
</body></html>
`))
	})
	mux.HandleFunc("/p5090", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("RTX 5090 price is $9999\n"))
	})
	mux.HandleFunc("/p4090", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("RTX 4090 price is $1599\n"))
	})

	bin := buildGoclawBinary(t)
	home := t.TempDir()
	addr := pickFreeAddr(t)
	token := "smoke-research-token"
	sessionID := "c5c60d2a-5f31-4f1f-8b5e-8b053b9d54f9"

	policyData := strings.Join([]string{
		"allow_capabilities:",
		"  - acp.read",
		"  - acp.mutate",
		"  - tools.web_search",
		"  - tools.read_url",
		"",
		"allow_domains:",
		"  - " + host,
		"",
		"allow_loopback: true",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(home, "policy.yaml"), []byte(policyData), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.token"), []byte(token+"\n"), 0o600); err != nil {
		t.Fatalf("write auth token: %v", err)
	}

	cmd := exec.Command(bin, "-daemon")
	cmd.Env = append(os.Environ(),
		"GOCLAW_HOME="+home,
		"GOCLAW_BIND_ADDR="+addr,
		"GOCLAW_NO_TUI=1",
		"GOCLAW_SEARCH_ENDPOINT="+s.URL+"/search",
	)
	var daemonOut bytes.Buffer
	cmd.Stdout = &daemonOut
	cmd.Stderr = &daemonOut
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}

	stopDaemon := func() {
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
	}
	t.Cleanup(stopDaemon)

	// Wait for daemon WS readiness.
	var conn *websocket.Conn
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		c, _, err := websocket.Dial(ctx, wsURL(addr), &websocket.DialOptions{
			HTTPHeader: http.Header{
				"Authorization": []string{"Bearer " + token},
			},
		})
		cancel()
		if err == nil {
			conn = c
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if conn == nil {
		t.Fatalf("daemon did not become ready\noutput=%s", daemonOut.String())
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	sendHello(t, conn)

	// Start chat task that triggers the deterministic research workflow.
	ctx := context.Background()
	if err := wsjson.Write(ctx, conn, rpcReq{
		JSONRPC: "2.0",
		ID:      10,
		Method:  "agent.chat",
		Params: map[string]any{
			"session_id": sessionID,
			"content":    "Compare price for RTX 5090 and RTX 4090",
		},
	}); err != nil {
		t.Fatalf("write agent.chat: %v", err)
	}
	var chatResp rpcResp
	if err := wsjson.Read(ctx, conn, &chatResp); err != nil {
		t.Fatalf("read agent.chat: %v", err)
	}
	if chatResp.Error != nil {
		t.Fatalf("agent.chat error: %+v", chatResp.Error)
	}
	var chatResult struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(chatResp.Result, &chatResult); err != nil || chatResult.TaskID == "" {
		t.Fatalf("unmarshal chat result: %v (%s)", err, string(chatResp.Result))
	}

	// Poll session.history until the assistant reply appears (task completed).
	var assistant string
	histDeadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(histDeadline) {
		if err := wsjson.Write(ctx, conn, rpcReq{
			JSONRPC: "2.0",
			ID:      11,
			Method:  "session.history",
			Params: map[string]any{
				"session_id": sessionID,
				"limit":      50,
			},
		}); err != nil {
			t.Fatalf("write session.history: %v", err)
		}
		var histResp rpcResp
		if err := wsjson.Read(ctx, conn, &histResp); err != nil {
			t.Fatalf("read session.history: %v", err)
		}
		if histResp.Error != nil {
			t.Fatalf("session.history error: %+v", histResp.Error)
		}
		var hr historyResult
		_ = json.Unmarshal(histResp.Result, &hr)
		for _, item := range hr.Items {
			if item.Role == "assistant" {
				assistant = item.Content
			}
		}
		if strings.Contains(assistant, "$9999") && strings.Contains(assistant, "$1599") {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if assistant == "" {
		t.Fatalf("did not observe assistant response for research loop (task_id=%s)\noutput=%s", chatResult.TaskID, daemonOut.String())
	}
	if !strings.Contains(assistant, "$9999") || !strings.Contains(assistant, "$1599") {
		t.Fatalf("unexpected assistant response: %q", assistant)
	}

	// Stop daemon to safely inspect DB artifacts.
	stopDaemon()

	store, err := persistence.Open(filepath.Join(home, "goclaw.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	tasks, err := store.ListTasksBySession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	chatCount := 0
	toolCount := 0
	for _, task := range tasks {
		switch task.Type {
		case "chat":
			chatCount++
		case "tool":
			toolCount++
		}
	}
	if chatCount < 1 {
		t.Fatalf("expected at least 1 chat task, got %d (tasks=%#v)", chatCount, tasks)
	}
	if toolCount < 2 {
		t.Fatalf("expected at least 2 tool tasks, got %d (tasks=%#v)", toolCount, tasks)
	}
}
