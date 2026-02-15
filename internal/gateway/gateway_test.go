package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/agent"
	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/coordinator"
	"github.com/basket/go-claw/internal/engine"
	"github.com/basket/go-claw/internal/gateway"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
)

func makeTestRegistry(store *persistence.Store, eng *engine.Engine) *agent.Registry {
	reg := agent.NewRegistry(store, nil, nil, nil, nil)
	reg.RegisterTestAgent("default", eng)
	return reg
}

func openStoreForGatewayTest(t *testing.T) *persistence.Store {
	t.Helper()
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

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

const gatewayTestAuthToken = "gateway-test-token"

var gatewayTestPolicy = policy.Policy{
	AllowCapabilities: []string{"acp.read", "acp.mutate"},
}

func connectWS(t *testing.T, serverURL string, token string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dialOpts := &websocket.DialOptions{}
	if token != "" {
		dialOpts.HTTPHeader = http.Header{
			"Authorization": []string{"Bearer " + token},
		}
	}
	conn, _, err := websocket.Dial(ctx, "ws"+serverURL[len("http"):]+"/ws", dialOpts)
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close(websocket.StatusNormalClosure, "test done")
	})
	return conn
}

func waitForTaskDone(t *testing.T, store *persistence.Store, taskID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, err := store.GetTask(context.Background(), taskID)
		if err == nil && (task.Status == persistence.TaskStatusSucceeded || task.Status == persistence.TaskStatusFailed || task.Status == persistence.TaskStatusCanceled) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s not finished in time", taskID)
}

func sendHello(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	ctx := context.Background()
	req := rpcReq{
		JSONRPC: "2.0",
		ID:      1000,
		Method:  "system.hello",
		Params:  map[string]any{"version": "1.0"},
	}
	if err := wsjson.Write(ctx, conn, req); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	var resp rpcResp
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatalf("read hello: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("system.hello returned error: %+v", resp.Error)
	}
}

func TestGateway_SystemStatusAndAgentChat(t *testing.T) {
	// [SPEC: SPEC-ACP-WS-1, SPEC-ACP-JSONRPC-1, SPEC-GOAL-G3] [PDR: V-10, V-11]
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, successfulGatewayProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	ctx := context.Background()
	sendHello(t, conn)

	reqStatus := rpcReq{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "system.status",
	}
	if err := wsjson.Write(ctx, conn, reqStatus); err != nil {
		t.Fatalf("write status req: %v", err)
	}
	var statusResp rpcResp
	if err := wsjson.Read(ctx, conn, &statusResp); err != nil {
		t.Fatalf("read status resp: %v", err)
	}
	if statusResp.Error != nil {
		t.Fatalf("system.status returned error: %+v", statusResp.Error)
	}

	reqChat := rpcReq{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "agent.chat",
		Params: map[string]any{
			"session_id": "7ced61c5-923f-41c2-ac40-d2137193a676",
			"content":    "hello",
		},
	}
	if err := wsjson.Write(ctx, conn, reqChat); err != nil {
		t.Fatalf("write chat req: %v", err)
	}
	var chatResp rpcResp
	if err := wsjson.Read(ctx, conn, &chatResp); err != nil {
		t.Fatalf("read chat resp: %v", err)
	}
	if chatResp.Error != nil {
		t.Fatalf("agent.chat returned error: %+v", chatResp.Error)
	}
	var chatResult struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(chatResp.Result, &chatResult); err != nil {
		t.Fatalf("unmarshal chat result: %v", err)
	}
	if chatResult.TaskID == "" {
		t.Fatalf("expected task_id in result")
	}

	waitForTaskDone(t, store, chatResult.TaskID)

	reqHistory := rpcReq{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "session.history",
		Params: map[string]any{
			"session_id": "7ced61c5-923f-41c2-ac40-d2137193a676",
			"limit":      10,
		},
	}
	if err := wsjson.Write(ctx, conn, reqHistory); err != nil {
		t.Fatalf("write history req: %v", err)
	}
	var historyResp rpcResp
	if err := wsjson.Read(ctx, conn, &historyResp); err != nil {
		t.Fatalf("read history resp: %v", err)
	}
	if historyResp.Error != nil {
		t.Fatalf("session.history returned error: %+v", historyResp.Error)
	}
}

func TestGateway_AgentAbortReturnsAborted(t *testing.T) {
	// [TODO #10 / T-11] agent.abort via WebSocket: chat → abort → verify FAILED.
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, blockingGatewayProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  5 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	ctx := context.Background()
	sendHello(t, conn)

	// Create a chat task.
	reqChat := rpcReq{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "agent.chat",
		Params: map[string]any{
			"session_id": "e0e0e0e0-1111-2222-3333-444444444444",
			"content":    "block_me",
		},
	}
	if err := wsjson.Write(ctx, conn, reqChat); err != nil {
		t.Fatalf("write chat: %v", err)
	}
	var chatResp rpcResp
	if err := wsjson.Read(ctx, conn, &chatResp); err != nil {
		t.Fatalf("read chat: %v", err)
	}
	if chatResp.Error != nil {
		t.Fatalf("agent.chat error: %+v", chatResp.Error)
	}
	var chatResult struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(chatResp.Result, &chatResult); err != nil {
		t.Fatalf("unmarshal chat result: %v", err)
	}

	// Wait for the task to be claimed (RUNNING).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		task, err := store.GetTask(context.Background(), chatResult.TaskID)
		if err == nil && task.Status == persistence.TaskStatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Abort the running task.
	reqAbort := rpcReq{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "agent.abort",
		Params:  map[string]any{"task_id": chatResult.TaskID},
	}
	if err := wsjson.Write(ctx, conn, reqAbort); err != nil {
		t.Fatalf("write abort: %v", err)
	}
	var abortResp rpcResp
	if err := wsjson.Read(ctx, conn, &abortResp); err != nil {
		t.Fatalf("read abort: %v", err)
	}
	if abortResp.Error != nil {
		t.Fatalf("agent.abort error: %+v", abortResp.Error)
	}
	var abortResult struct {
		Aborted bool `json:"aborted"`
	}
	if err := json.Unmarshal(abortResp.Result, &abortResult); err != nil {
		t.Fatalf("unmarshal abort result: %v", err)
	}
	if !abortResult.Aborted {
		t.Fatalf("expected aborted=true")
	}

	// Verify task is CANCELED.
	waitForTaskDone(t, store, chatResult.TaskID)
	task, err := store.GetTask(context.Background(), chatResult.TaskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Status != persistence.TaskStatusCanceled {
		t.Fatalf("expected CANCELED, got %s", task.Status)
	}
}

func TestGateway_SessionListReturnsData(t *testing.T) {
	// [T-11] session.list via WebSocket.
	store := openStoreForGatewayTest(t)
	ctx := context.Background()

	// Seed some sessions.
	for _, id := range []string{
		"f0f0f0f0-1111-2222-3333-444444444401",
		"f0f0f0f0-1111-2222-3333-444444444402",
	} {
		if err := store.EnsureSession(ctx, id); err != nil {
			t.Fatalf("ensure session: %v", err)
		}
	}

	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	sendHello(t, conn)

	reqList := rpcReq{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "session.list",
		Params:  map[string]any{"limit": 10},
	}
	if err := wsjson.Write(ctx, conn, reqList); err != nil {
		t.Fatalf("write session.list: %v", err)
	}
	var listResp rpcResp
	if err := wsjson.Read(ctx, conn, &listResp); err != nil {
		t.Fatalf("read session.list: %v", err)
	}
	if listResp.Error != nil {
		t.Fatalf("session.list error: %+v", listResp.Error)
	}

	var listResult struct {
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(listResp.Result, &listResult); err != nil {
		t.Fatalf("unmarshal list result: %v", err)
	}
	if len(listResult.Sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(listResult.Sessions))
	}
}

func TestGateway_ToolsUpdatedNotification(t *testing.T) {
	// [T-11] tools.updated notification broadcast.
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	toolEvents := make(chan string, 1)
	srv := gateway.New(gateway.Config{
		Store:        store,
		Registry:     makeTestRegistry(store, eng),
		Policy:       gatewayTestPolicy,
		AuthToken:    gatewayTestAuthToken,
		ToolsUpdated: toolEvents,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	sendHello(t, conn)

	// Send a tool event.
	toolEvents <- "my_skill.go"

	// Read the notification (should be a JSON-RPC notification with method "tools.updated").
	readCtx, readCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer readCancel()
	var notification rpcResp
	if err := wsjson.Read(readCtx, conn, &notification); err != nil {
		t.Fatalf("read notification: %v", err)
	}
	if notification.Method != "tools.updated" {
		t.Fatalf("expected method tools.updated, got %q", notification.Method)
	}
}

// blockingGatewayProcessor blocks until context is canceled (for abort tests).
type blockingGatewayProcessor struct{}

func (blockingGatewayProcessor) Process(ctx context.Context, task persistence.Task) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

type successfulGatewayProcessor struct{}

func (successfulGatewayProcessor) Process(ctx context.Context, task persistence.Task) (string, error) {
	return `{"reply":"ok"}`, nil
}

func TestGateway_InvalidRequestValidation(t *testing.T) {
	// [SPEC: SPEC-ACP-JSONRPC-1] [PDR: V-9, V-10]
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	ctx := context.Background()
	sendHello(t, conn)

	// Invalid UUID in params should map to a stable invalid-request error.
	reqBad := rpcReq{
		JSONRPC: "2.0",
		ID:      11,
		Method:  "agent.chat",
		Params: map[string]any{
			"session_id": "not-a-uuid",
			"content":    "hello",
		},
	}
	if err := wsjson.Write(ctx, conn, reqBad); err != nil {
		t.Fatalf("write bad req: %v", err)
	}
	var badResp rpcResp
	if err := wsjson.Read(ctx, conn, &badResp); err != nil {
		t.Fatalf("read bad resp: %v", err)
	}
	if badResp.Error == nil {
		t.Fatalf("expected error response")
	}
	if badResp.Error.Code != gateway.ErrCodeInvalid {
		t.Fatalf("expected invalid code %d, got %d", gateway.ErrCodeInvalid, badResp.Error.Code)
	}

	reqUnknown := rpcReq{
		JSONRPC: "2.0",
		ID:      12,
		Method:  "agent.unknown",
	}
	if err := wsjson.Write(ctx, conn, reqUnknown); err != nil {
		t.Fatalf("write unknown req: %v", err)
	}
	var unknownResp rpcResp
	if err := wsjson.Read(ctx, conn, &unknownResp); err != nil {
		t.Fatalf("read unknown resp: %v", err)
	}
	if unknownResp.Error == nil {
		t.Fatalf("expected unknown method error")
	}
	if unknownResp.Error.Code != gateway.ErrCodeMethodNotFound {
		t.Fatalf("expected method-not-found code %d, got %d", gateway.ErrCodeMethodNotFound, unknownResp.Error.Code)
	}
}

func TestGateway_WSRejectsMissingOrInvalidAuth(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelDial()

	_, resp, err := websocket.Dial(ctx, "ws"+ts.URL[len("http"):]+"/ws", nil)
	if err == nil {
		t.Fatalf("expected missing-auth dial to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing auth, got %+v", resp)
	}

	_, badResp, badErr := websocket.Dial(ctx, "ws"+ts.URL[len("http"):]+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer wrong-token"}},
	})
	if badErr == nil {
		t.Fatalf("expected invalid-auth dial to fail")
	}
	if badResp == nil || badResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid auth, got %+v", badResp)
	}
}

func TestGateway_MutatingRequiresHandshake(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	ctx := context.Background()

	reqChat := rpcReq{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "agent.chat",
		Params: map[string]any{
			"session_id": "7ced61c5-923f-41c2-ac40-d2137193a676",
			"content":    "hello",
		},
	}
	if err := wsjson.Write(ctx, conn, reqChat); err != nil {
		t.Fatalf("write chat: %v", err)
	}
	var resp rpcResp
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatalf("read chat resp: %v", err)
	}
	if resp.Error == nil {
		t.Fatalf("expected handshake-required error")
	}
	if resp.Error.Code != gateway.ErrCodeInvalidRequest {
		t.Fatalf("expected invalid-request error code %d, got %d", gateway.ErrCodeInvalidRequest, resp.Error.Code)
	}
	if !strings.Contains(resp.Error.Message, "system.hello required") {
		t.Fatalf("expected handshake-required message, got %q", resp.Error.Message)
	}
}

// US-027: Abuse matrix — origin rejection when AllowOrigins is configured.
func TestGateway_OriginRejectsDisallowedOrigin(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:        store,
		Registry:     makeTestRegistry(store, eng),
		Policy:       gatewayTestPolicy,
		AuthToken:    gatewayTestAuthToken,
		AllowOrigins: []string{"http://localhost:3000"},
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelDial()

	// Disallowed origin should be rejected with 403.
	_, resp, err := websocket.Dial(ctx, "ws"+ts.URL[len("http"):]+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + gatewayTestAuthToken},
			"Origin":        []string{"http://evil.example.com"},
		},
	})
	if err == nil {
		t.Fatalf("expected bad-origin dial to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for disallowed origin, got %+v", resp)
	}

	// Allowed origin should succeed.
	conn, _, err := websocket.Dial(ctx, "ws"+ts.URL[len("http"):]+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + gatewayTestAuthToken},
			"Origin":        []string{"http://localhost:3000"},
		},
	})
	if err != nil {
		t.Fatalf("expected allowed-origin dial to succeed: %v", err)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "test done")

	// No origin header (local CLI) should also succeed when AllowOrigins is set.
	conn2, _, err := websocket.Dial(ctx, "ws"+ts.URL[len("http"):]+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + gatewayTestAuthToken},
		},
	})
	if err != nil {
		t.Fatalf("expected no-origin dial to succeed: %v", err)
	}
	_ = conn2.Close(websocket.StatusNormalClosure, "test done")
}

// US-027: Comprehensive abuse denial matrix covering auth, origin, handshake, and capability bypass.
func TestGateway_AbuseDenialMatrix(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:        store,
		Registry:     makeTestRegistry(store, eng),
		Policy:       policy.Default(), // default-deny
		AuthToken:    gatewayTestAuthToken,
		AllowOrigins: []string{"http://localhost:3000"},
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancelDial := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelDial()

	// 1. Missing auth token -> 401
	_, resp, err := websocket.Dial(ctx, "ws"+ts.URL[len("http"):]+"/ws", nil)
	if err == nil {
		t.Fatalf("[no-auth] expected dial to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("[no-auth] expected 401, got %+v", resp)
	}
	t.Log("DENY: missing auth -> 401")

	// 2. Invalid auth token -> 401
	_, resp2, err := websocket.Dial(ctx, "ws"+ts.URL[len("http"):]+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer wrong-token"}},
	})
	if err == nil {
		t.Fatalf("[bad-auth] expected dial to fail")
	}
	if resp2 == nil || resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("[bad-auth] expected 401, got %+v", resp2)
	}
	t.Log("DENY: invalid auth -> 401")

	// 3. Disallowed origin -> 403
	_, resp3, err := websocket.Dial(ctx, "ws"+ts.URL[len("http"):]+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + gatewayTestAuthToken},
			"Origin":        []string{"http://evil.example.com"},
		},
	})
	if err == nil {
		t.Fatalf("[bad-origin] expected dial to fail")
	}
	if resp3 == nil || resp3.StatusCode != http.StatusForbidden {
		t.Fatalf("[bad-origin] expected 403, got %+v", resp3)
	}
	t.Log("DENY: disallowed origin -> 403")

	// 4. Valid auth, connect but mutate before handshake -> error
	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	chatReq := rpcReq{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "agent.chat",
		Params: map[string]any{
			"session_id": "7ced61c5-923f-41c2-ac40-d2137193a676",
			"content":    "hello",
		},
	}
	if err := wsjson.Write(ctx, conn, chatReq); err != nil {
		t.Fatalf("[no-hello] write: %v", err)
	}
	var noHelloResp rpcResp
	if err := wsjson.Read(ctx, conn, &noHelloResp); err != nil {
		t.Fatalf("[no-hello] read: %v", err)
	}
	if noHelloResp.Error == nil || noHelloResp.Error.Code != gateway.ErrCodeInvalidRequest {
		t.Fatalf("[no-hello] expected handshake-required error, got %+v", noHelloResp.Error)
	}
	t.Log("DENY: mutate before handshake -> invalid request")

	// 5. After handshake, capability bypass with default-deny policy -> denied
	conn2 := connectWS(t, ts.URL, gatewayTestAuthToken)
	sendHello(t, conn2)
	statusReq := rpcReq{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "system.status",
	}
	if err := wsjson.Write(ctx, conn2, statusReq); err != nil {
		t.Fatalf("[cap-bypass] write: %v", err)
	}
	var capResp rpcResp
	if err := wsjson.Read(ctx, conn2, &capResp); err != nil {
		t.Fatalf("[cap-bypass] read: %v", err)
	}
	if capResp.Error == nil || capResp.Error.Code != gateway.ErrCodeInvalid {
		t.Fatalf("[cap-bypass] expected policy denial, got %+v", capResp.Error)
	}
	t.Log("DENY: capability bypass (default-deny) -> policy denied")

	t.Log("ALL ABUSE MATRIX DENIALS VERIFIED")
}

// US-028: Headless approval timeout defaults to deny.
func TestGateway_ApprovalTimeoutDefaultDeny(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:           store,
		Registry:        makeTestRegistry(store, eng),
		Policy:          gatewayTestPolicy,
		AuthToken:       gatewayTestAuthToken,
		ApprovalTimeout: 100 * time.Millisecond, // short for test
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	sendHello(t, conn)

	ctx := context.Background()

	// Submit approval request.
	reqApproval := rpcReq{
		JSONRPC: "2.0",
		ID:      601,
		Method:  "approval.request",
		Params: map[string]any{
			"action":  "deploy-prod",
			"details": "timeout test",
		},
	}
	if err := wsjson.Write(ctx, conn, reqApproval); err != nil {
		t.Fatalf("write approval.request: %v", err)
	}

	// Read messages: expect RPC response + approval.required broadcast + approval.updated (timeout deny).
	// Messages can arrive in any order, so read in a loop and classify.
	readCtx, readCancel := context.WithTimeout(ctx, 3*time.Second)
	defer readCancel()

	gotResponse := false
	gotRequired := false
	gotTimeoutDeny := false

	for i := 0; i < 3; i++ {
		var msg rpcResp
		if err := wsjson.Read(readCtx, conn, &msg); err != nil {
			t.Fatalf("read message %d: %v", i, err)
		}
		switch {
		case msg.Method == "approval.required":
			gotRequired = true
		case msg.Method == "approval.updated":
			var params map[string]interface{}
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				t.Fatalf("unmarshal approval.updated params: %v", err)
			}
			if params["status"] != "DENIED" {
				t.Fatalf("expected status DENIED on timeout, got %v", params["status"])
			}
			gotTimeoutDeny = true
		case msg.ID != nil:
			gotResponse = true
			if msg.Error != nil {
				t.Fatalf("approval.request error: %+v", msg.Error)
			}
		}
	}

	if !gotResponse {
		t.Fatal("never received RPC response for approval.request")
	}
	if !gotRequired {
		t.Fatal("never received approval.required broadcast")
	}
	if !gotTimeoutDeny {
		t.Fatal("never received approval.updated (timeout deny) broadcast")
	}
	t.Log("PASS: approval auto-denied on timeout")
}

func TestGateway_PolicyDeniesCapabilities(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    policy.Default(),
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	sendHello(t, conn)

	ctx := context.Background()
	req := rpcReq{
		JSONRPC: "2.0",
		ID:      7,
		Method:  "system.status",
	}
	if err := wsjson.Write(ctx, conn, req); err != nil {
		t.Fatalf("write request: %v", err)
	}
	var resp rpcResp
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.Error == nil {
		t.Fatalf("expected policy denial error")
	}
	if resp.Error.Code != gateway.ErrCodeInvalid {
		t.Fatalf("expected invalid error code %d, got %d", gateway.ErrCodeInvalid, resp.Error.Code)
	}
	if !strings.Contains(resp.Error.Message, "acp.read") {
		t.Fatalf("expected denied capability message, got %q", resp.Error.Message)
	}
}

func TestGateway_SessionEventsSubscribeReplayOrdered(t *testing.T) {
	store := openStoreForGatewayTest(t)
	ctx := context.Background()
	sessionID := "106bc58c-8928-4aa5-beac-44f1ebc787f5"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	// Seed events via task lifecycle transitions.
	for i := 0; i < 3; i++ {
		taskID, err := store.CreateTask(ctx, sessionID, fmt.Sprintf(`{"content":"event-%d"}`, i))
		if err != nil {
			t.Fatalf("create task %d: %v", i, err)
		}
		task, err := store.ClaimNextPendingTask(ctx)
		if err != nil || task == nil {
			t.Fatalf("claim task %d: task=%#v err=%v", i, task, err)
		}
		if err := store.StartTaskRun(ctx, taskID, task.LeaseOwner, ""); err != nil {
			t.Fatalf("start run %d: %v", i, err)
		}
		if err := store.CompleteTask(ctx, taskID, `{"reply":"ok"}`); err != nil {
			t.Fatalf("complete task %d: %v", i, err)
		}
	}

	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	sendHello(t, conn)

	req := rpcReq{
		JSONRPC: "2.0",
		ID:      77,
		Method:  "session.events.subscribe",
		Params: map[string]any{
			"session_id":    sessionID,
			"from_event_id": 0,
		},
	}
	if err := wsjson.Write(ctx, conn, req); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}

	var (
		lastEventID float64
		sawResponse bool
		seenEvents  int
	)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var msg rpcResp
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			t.Fatalf("read subscribe stream: %v", err)
		}
		if msg.Method == "session.event" {
			seenEvents++
			var params map[string]any
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				t.Fatalf("unmarshal event params: %v", err)
			}
			evID, _ := params["event_id"].(float64)
			if evID <= lastEventID {
				t.Fatalf("expected monotonic event_id, got %.0f after %.0f", evID, lastEventID)
			}
			lastEventID = evID
			continue
		}
		if idNum, ok := msg.ID.(float64); ok && int(idNum) == 77 {
			if msg.Error != nil {
				t.Fatalf("subscribe response error: %+v", msg.Error)
			}
			sawResponse = true
			break
		}
	}
	if !sawResponse {
		t.Fatalf("expected subscribe response")
	}
	if seenEvents == 0 {
		t.Fatalf("expected replayed session events")
	}
}

func TestGateway_SessionEventsSubscribeReplayGap(t *testing.T) {
	store := openStoreForGatewayTest(t)
	ctx := context.Background()
	sessionID := "1bcecf55-cda6-49d3-b8a1-13725fb5f7d7"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	taskID, err := store.CreateTask(ctx, sessionID, `{"content":"gap"}`)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	task, err := store.ClaimNextPendingTask(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim task: task=%#v err=%v", task, err)
	}
	if err := store.StartTaskRun(ctx, taskID, task.LeaseOwner, ""); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := store.CompleteTask(ctx, taskID, `{"reply":"ok"}`); err != nil {
		t.Fatalf("complete: %v", err)
	}
	// Force replay gap by removing early events.
	if _, err := store.DB().ExecContext(ctx, `DELETE FROM task_events WHERE session_id = ? AND event_id <= 2;`, sessionID); err != nil {
		t.Fatalf("delete early events: %v", err)
	}

	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	sendHello(t, conn)

	req := rpcReq{
		JSONRPC: "2.0",
		ID:      88,
		Method:  "session.events.subscribe",
		Params: map[string]any{
			"session_id":    sessionID,
			"from_event_id": 1,
		},
	}
	if err := wsjson.Write(ctx, conn, req); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	var msg rpcResp
	if err := wsjson.Read(ctx, conn, &msg); err != nil {
		t.Fatalf("read subscribe: %v", err)
	}
	if msg.Error == nil {
		t.Fatalf("expected replay gap error")
	}
	if !strings.Contains(msg.Error.Message, "replay_gap") {
		t.Fatalf("expected replay_gap message, got %+v", msg.Error)
	}
}

func TestGateway_SessionEventsSubscribeBackpressureClose(t *testing.T) {
	store := openStoreForGatewayTest(t)
	ctx := context.Background()
	sessionID := "8f604e8d-44f4-49a3-93ce-b72f20bf17a9"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	// Create enough events to exceed replay cap.
	for i := 0; i < 20; i++ {
		taskID, err := store.CreateTask(ctx, sessionID, fmt.Sprintf(`{"content":"bp-%d"}`, i))
		if err != nil {
			t.Fatalf("create task %d: %v", i, err)
		}
		task, err := store.ClaimNextPendingTask(ctx)
		if err != nil || task == nil {
			t.Fatalf("claim task %d: task=%#v err=%v", i, task, err)
		}
		if err := store.StartTaskRun(ctx, taskID, task.LeaseOwner, ""); err != nil {
			t.Fatalf("start run %d: %v", i, err)
		}
		if err := store.CompleteTask(ctx, taskID, `{"reply":"ok"}`); err != nil {
			t.Fatalf("complete task %d: %v", i, err)
		}
	}

	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	sendHello(t, conn)

	req := rpcReq{
		JSONRPC: "2.0",
		ID:      99,
		Method:  "session.events.subscribe",
		Params: map[string]any{
			"session_id":    sessionID,
			"from_event_id": 0,
		},
	}
	if err := wsjson.Write(ctx, conn, req); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}

	var msg rpcResp
	if err := wsjson.Read(ctx, conn, &msg); err != nil {
		t.Fatalf("read backpressure notification: %v", err)
	}
	if msg.Method != "system.backpressure" {
		t.Fatalf("expected system.backpressure notification, got method=%q", msg.Method)
	}
	// Next read should fail after deterministic close.
	readCtx, readCancel := context.WithTimeout(context.Background(), time.Second)
	defer readCancel()
	var next rpcResp
	if err := wsjson.Read(readCtx, conn, &next); err == nil {
		t.Fatalf("expected connection close after backpressure")
	}
}

func TestGateway_HeadlessApprovalWorkflow(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	sendHello(t, conn)
	ctx := context.Background()

	req := rpcReq{
		JSONRPC: "2.0",
		ID:      501,
		Method:  "approval.request",
		Params: map[string]any{
			"action":  "legacy.dangerous",
			"details": "rm -rf ./workspace/tmp",
		},
	}
	if err := wsjson.Write(ctx, conn, req); err != nil {
		t.Fatalf("write approval.request: %v", err)
	}

	var (
		approvalID     string
		sawRequiredEvt bool
		sawResp        bool
	)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var msg rpcResp
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			t.Fatalf("read approval.request stream: %v", err)
		}
		if msg.Method == "approval.required" {
			sawRequiredEvt = true
			var params map[string]any
			if err := json.Unmarshal(msg.Params, &params); err == nil {
				if id, ok := params["approval_id"].(string); ok {
					approvalID = id
				}
			}
			continue
		}
		if idNum, ok := msg.ID.(float64); ok && int(idNum) == 501 {
			if msg.Error != nil {
				t.Fatalf("approval.request error: %+v", msg.Error)
			}
			var result map[string]any
			if err := json.Unmarshal(msg.Result, &result); err != nil {
				t.Fatalf("unmarshal approval.request result: %v", err)
			}
			if id, ok := result["approval_id"].(string); ok {
				approvalID = id
			}
			sawResp = true
			if sawRequiredEvt {
				break
			}
		}
	}
	if !sawResp || !sawRequiredEvt || approvalID == "" {
		t.Fatalf("expected approval.request response + approval.required event, got resp=%t evt=%t id=%q", sawResp, sawRequiredEvt, approvalID)
	}

	respReq := rpcReq{
		JSONRPC: "2.0",
		ID:      502,
		Method:  "approval.respond",
		Params: map[string]any{
			"approval_id": approvalID,
			"decision":    "approve",
		},
	}
	if err := wsjson.Write(ctx, conn, respReq); err != nil {
		t.Fatalf("write approval.respond: %v", err)
	}

	var sawUpdate bool
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var msg rpcResp
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			t.Fatalf("read approval.respond stream: %v", err)
		}
		if msg.Method == "approval.updated" {
			sawUpdate = true
			continue
		}
		if idNum, ok := msg.ID.(float64); ok && int(idNum) == 502 {
			if msg.Error != nil {
				t.Fatalf("approval.respond error: %+v", msg.Error)
			}
			break
		}
	}
	if !sawUpdate {
		t.Fatalf("expected approval.updated event")
	}

	listReq := rpcReq{
		JSONRPC: "2.0",
		ID:      503,
		Method:  "approval.list",
	}
	if err := wsjson.Write(ctx, conn, listReq); err != nil {
		t.Fatalf("write approval.list: %v", err)
	}
	var listResp rpcResp
	if err := wsjson.Read(ctx, conn, &listResp); err != nil {
		t.Fatalf("read approval.list: %v", err)
	}
	if listResp.Error != nil {
		t.Fatalf("approval.list error: %+v", listResp.Error)
	}
}

func TestGateway_PolicyDecisionAudited(t *testing.T) {
	auditHome := t.TempDir()
	if err := audit.Init(auditHome); err != nil {
		t.Fatalf("init audit: %v", err)
	}
	t.Cleanup(func() { _ = audit.Close() })

	store := openStoreForGatewayTest(t)
	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    policy.Default(), // deny acp.read capability
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	sendHello(t, conn)

	req := rpcReq{
		JSONRPC: "2.0",
		ID:      601,
		Method:  "system.status",
	}
	if err := wsjson.Write(context.Background(), conn, req); err != nil {
		t.Fatalf("write request: %v", err)
	}
	var resp rpcResp
	if err := wsjson.Read(context.Background(), conn, &resp); err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.Error == nil {
		t.Fatalf("expected policy denial")
	}

	auditPath := filepath.Join(auditHome, "logs", "audit.jsonl")
	raw, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"capability":"acp.read"`) {
		t.Fatalf("expected acp.read audit entry, got: %s", text)
	}
	if !strings.Contains(text, `"decision":"deny"`) {
		t.Fatalf("expected deny decision in audit log, got: %s", text)
	}
	if !strings.Contains(text, `"reason":"missing_capability"`) {
		t.Fatalf("expected reason in audit log, got: %s", text)
	}
}

func TestHealthzEndpointContract(t *testing.T) {
	// V-OBS-005: Health endpoint MUST report db_ok, policy_version, replay_backlog, skill_runtime.
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, nil, engine.Config{WorkerCount: 1, PollInterval: 100 * time.Millisecond, TaskTimeout: 1 * time.Minute})
	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode healthz body: %v", err)
	}

	requiredFields := []string{"healthy", "db_ok", "policy_version", "skill_runtime", "replay_backlog_events"}
	for _, field := range requiredFields {
		if _, ok := body[field]; !ok {
			t.Errorf("healthz missing required field %q, got: %v", field, body)
		}
	}
	if body["healthy"] != true {
		t.Errorf("expected healthy=true, got %v", body["healthy"])
	}
	if body["db_ok"] != true {
		t.Errorf("expected db_ok=true, got %v", body["db_ok"])
	}
}

func TestMetricsEndpointCoverage(t *testing.T) {
	// V-OBS-004: Metrics MUST expose queue depth, active lanes, lease expiries, retries, DLQ size, deny rate.
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, nil, engine.Config{WorkerCount: 1, PollInterval: 100 * time.Millisecond, TaskTimeout: 1 * time.Minute})
	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/metrics", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode metrics body: %v", err)
	}

	requiredFields := []string{
		"pending_tasks", "running_tasks", "active_lanes",
		"lease_expiries", "retries", "dlq_size", "policy_deny_rate",
	}
	for _, field := range requiredFields {
		if _, ok := body[field]; !ok {
			t.Errorf("metrics missing required field %q, got: %v", field, body)
		}
	}
}

func TestMetricsEndpoint_RejectsMissingAuth(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, nil, engine.Config{WorkerCount: 1, PollInterval: 100 * time.Millisecond, TaskTimeout: 1 * time.Minute})
	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestPrometheusMetricsEndpoint(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, nil, engine.Config{WorkerCount: 1, PollInterval: 100 * time.Millisecond, TaskTimeout: 1 * time.Minute})
	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/metrics/prometheus", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /metrics/prometheus: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") {
		t.Fatalf("expected text/plain content type, got %q", contentType)
	}

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := string(rawBody)

	requiredMetrics := []string{
		"goclaw_pending_tasks",
		"goclaw_running_tasks",
		"goclaw_active_lanes",
		"goclaw_lease_expiries",
		"goclaw_retries",
		"goclaw_dlq_size",
		"goclaw_policy_deny_total",
		"goclaw_alloc_bytes",
	}
	for _, metric := range requiredMetrics {
		if !strings.Contains(body, metric) {
			t.Errorf("prometheus output missing metric %q", metric)
		}
	}

	// Verify Prometheus format: HELP and TYPE lines present.
	if !strings.Contains(body, "# HELP goclaw_pending_tasks") {
		t.Error("missing HELP line for goclaw_pending_tasks")
	}
	if !strings.Contains(body, "# TYPE goclaw_pending_tasks gauge") {
		t.Error("missing TYPE line for goclaw_pending_tasks")
	}
}

func TestHealthzEndpoint_IncludesAgentCount(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, nil, engine.Config{WorkerCount: 1, PollInterval: 100 * time.Millisecond, TaskTimeout: 1 * time.Minute})
	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode healthz: %v", err)
	}

	agentCount, ok := body["agent_count"]
	if !ok {
		t.Fatalf("healthz missing agent_count field, got: %v", body)
	}
	// We registered one "default" test agent.
	if count, ok := agentCount.(float64); !ok || count < 1 {
		t.Fatalf("expected agent_count >= 1, got %v", agentCount)
	}
}

func TestMetricsEndpoint_IncludesAgentAndDelegationFields(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, nil, engine.Config{WorkerCount: 1, PollInterval: 100 * time.Millisecond, TaskTimeout: 1 * time.Minute})
	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/metrics", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode metrics: %v", err)
	}

	// Verify agent-related fields.
	for _, field := range []string{"agent_count", "agent_messages_total", "delegations_total", "agents"} {
		if _, ok := body[field]; !ok {
			t.Errorf("metrics missing %q field, got: %v", field, body)
		}
	}

	// agents should be an array.
	agents, ok := body["agents"].([]interface{})
	if !ok {
		t.Fatalf("agents is not an array: %T", body["agents"])
	}
	if len(agents) < 1 {
		t.Fatalf("expected at least 1 agent in metrics, got %d", len(agents))
	}

	// Each agent entry should have agent_id, active_tasks, worker_count.
	agentEntry, ok := agents[0].(map[string]interface{})
	if !ok {
		t.Fatalf("agent entry is not a map")
	}
	for _, key := range []string{"agent_id", "active_tasks", "worker_count"} {
		if _, ok := agentEntry[key]; !ok {
			t.Errorf("agent entry missing %q, got: %v", key, agentEntry)
		}
	}
}

func TestPrometheusMetrics_PerAgentLabelsAndDelegations(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, nil, engine.Config{WorkerCount: 1, PollInterval: 100 * time.Millisecond, TaskTimeout: 1 * time.Minute})
	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/metrics/prometheus", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /metrics/prometheus: %v", err)
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := string(rawBody)

	// Per-agent labels should be present.
	if !strings.Contains(body, "goclaw_agent_active_tasks{") {
		t.Error("prometheus output missing per-agent goclaw_agent_active_tasks metric with labels")
	}

	// Delegation counter should be present.
	if !strings.Contains(body, "goclaw_delegations_total") {
		t.Error("prometheus output missing goclaw_delegations_total metric")
	}

	// Agent count metric should still be present.
	if !strings.Contains(body, "goclaw_agent_count") {
		t.Error("prometheus output missing goclaw_agent_count")
	}
}

func TestAgentListViaRPC(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	sendHello(t, conn)
	ctx := context.Background()

	req := rpcReq{JSONRPC: "2.0", ID: 1, Method: "agent.list"}
	if err := wsjson.Write(ctx, conn, req); err != nil {
		t.Fatalf("write agent.list: %v", err)
	}
	var resp rpcResp
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatalf("read agent.list: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("agent.list error: %+v", resp.Error)
	}

	var result struct {
		Agents []struct {
			AgentID     string `json:"agent_id"`
			WorkerCount int    `json:"worker_count"`
			Status      string `json:"status"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal agent.list: %v", err)
	}
	if len(result.Agents) < 1 {
		t.Fatalf("expected at least 1 agent, got %d", len(result.Agents))
	}
	found := false
	for _, a := range result.Agents {
		if a.AgentID == "default" {
			found = true
			if a.Status != "active" {
				t.Errorf("expected default agent status=active, got %q", a.Status)
			}
		}
	}
	if !found {
		t.Fatal("default agent not found in agent.list result")
	}
}

func TestAgentStatusViaRPC(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	sendHello(t, conn)
	ctx := context.Background()

	req := rpcReq{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "agent.status",
		Params:  map[string]any{"agent_id": "default"},
	}
	if err := wsjson.Write(ctx, conn, req); err != nil {
		t.Fatalf("write agent.status: %v", err)
	}
	var resp rpcResp
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatalf("read agent.status: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("agent.status error: %+v", resp.Error)
	}

	var result struct {
		AgentID     string `json:"agent_id"`
		WorkerCount int    `json:"worker_count"`
		ActiveTasks int32  `json:"active_tasks"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal agent.status: %v", err)
	}
	if result.AgentID != "default" {
		t.Fatalf("expected agent_id=default, got %q", result.AgentID)
	}
	if result.WorkerCount < 1 {
		t.Errorf("expected worker_count >= 1, got %d", result.WorkerCount)
	}
}

// --- Sprint 0 Bug Fix Tests ---

// mockStreamBrain implements engine.Brain for streaming tests.
type mockStreamBrain struct {
	chunks []string
}

func (m *mockStreamBrain) Respond(ctx context.Context, sessionID, content string) (string, error) {
	return strings.Join(m.chunks, ""), nil
}

func (m *mockStreamBrain) Stream(ctx context.Context, sessionID, content string, onChunk func(content string) error) error {
	for _, chunk := range m.chunks {
		if err := onChunk(chunk); err != nil {
			return err
		}
	}
	return nil
}

// TestBug1_OpenAIStreamingDeliversChunksBeforeDone verifies that the SSE stream
// contains content chunks before the [DONE] sentinel (was broken when streamChatTask
// ran asynchronously via goroutine).
func TestBug1_OpenAIStreamingDeliversChunksBeforeDone(t *testing.T) {
	store := openStoreForGatewayTest(t)
	brain := &mockStreamBrain{chunks: []string{"Hello", " world", "!"}}
	eng := engine.New(store, engine.EchoProcessor{Brain: brain}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  5 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := `{"model":"goclaw-v1","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req, _ := http.NewRequest("POST", ts.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/chat/completions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")

	var dataChunks []string
	sawDone := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "data: [DONE]" {
			sawDone = true
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			if sawDone {
				t.Fatalf("received data chunk after [DONE]")
			}
			dataChunks = append(dataChunks, line)
		}
	}
	if !sawDone {
		t.Fatalf("never saw [DONE] in response")
	}
	if len(dataChunks) == 0 {
		t.Fatalf("no content chunks received before [DONE]")
	}
	if len(dataChunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(dataChunks))
	}
}

// TestBug2_LiveEventPushViaBus verifies that events generated after a
// session.events.subscribe are pushed live to the WS client via the bus,
// not just replayed from the DB.
func TestBug2_LiveEventPushViaBus(t *testing.T) {
	store := openStoreForGatewayTest(t)
	ctx := context.Background()
	sessionID := "d06bc58c-8928-4aa5-beac-44f1ebc787f5"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	b := bus.New()
	eng := engine.New(store, successfulGatewayProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	}, nil)
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
		Bus:       b,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	sendHello(t, conn)

	// Subscribe with from_event_id=0 (no replay expected since no events exist yet).
	subReq := rpcReq{
		JSONRPC: "2.0",
		ID:      99,
		Method:  "session.events.subscribe",
		Params: map[string]any{
			"session_id":    sessionID,
			"from_event_id": 0,
		},
	}
	if err := wsjson.Write(ctx, conn, subReq); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}

	// Read the subscribe response first.
	var subResp rpcResp
	if err := wsjson.Read(ctx, conn, &subResp); err != nil {
		t.Fatalf("read subscribe response: %v", err)
	}
	if subResp.Error != nil {
		t.Fatalf("subscribe error: %+v", subResp.Error)
	}

	// Now create a task (which generates task_events in the DB).
	taskID, err := store.CreateTask(ctx, sessionID, `{"content":"live-test"}`)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	task, err := store.ClaimNextPendingTask(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim task: %v", err)
	}
	if err := store.StartTaskRun(ctx, taskID, task.LeaseOwner, ""); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := store.CompleteTask(ctx, taskID, `{"reply":"ok"}`); err != nil {
		t.Fatalf("complete task: %v", err)
	}

	// Publish a bus event to trigger the live push.
	b.Publish("task.succeeded", map[string]string{
		"task_id":    taskID,
		"session_id": sessionID,
	})

	// Read live events — we should see at least one pushed event.
	var liveEvents int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		readCtx, readCancel := context.WithTimeout(ctx, 500*time.Millisecond)
		var msg rpcResp
		err := wsjson.Read(readCtx, conn, &msg)
		readCancel()
		if err != nil {
			break // timeout = no more events
		}
		if msg.Method == "session.event" {
			liveEvents++
		}
	}

	if liveEvents == 0 {
		t.Fatalf("expected live events pushed via bus, got none")
	}
}

// TestBug3_MultiTurnContextPreserved verifies that prior messages in the
// OpenAI request are seeded into the session history so the Brain can see
// the full conversation context.
func TestBug3_MultiTurnContextPreserved(t *testing.T) {
	store := openStoreForGatewayTest(t)
	// This brain records the content it receives so we can verify it was invoked.
	brain := &mockStreamBrain{chunks: []string{"I remember your name"}}
	eng := engine.New(store, engine.EchoProcessor{Brain: brain}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  5 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Send a multi-turn conversation with a deterministic user to get a stable session.
	body := `{
		"model": "goclaw-v1",
		"user": "multi-turn-test-user",
		"stream": true,
		"messages": [
			{"role": "system", "content": "You are a helpful assistant."},
			{"role": "user", "content": "My name is Alice."},
			{"role": "assistant", "content": "Hello Alice!"},
			{"role": "user", "content": "What is my name?"}
		]
	}`
	req, _ := http.NewRequest("POST", ts.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/chat/completions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(b))
	}

	// Verify prior messages were seeded into the session.
	// Compute the session ID the same way the handler does.
	sessionID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("goclaw:user:multi-turn-test-user:agent:default")).String()
	history, err := store.ListHistory(context.Background(), sessionID, "", 100)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}

	// We expect: system, user, assistant (seeded) + user (from StreamChatTask) = 4 minimum.
	if len(history) < 4 {
		t.Fatalf("expected at least 4 history entries (3 seeded + 1 from engine), got %d", len(history))
	}

	// Verify the seeded messages are present in order.
	roleContent := make(map[string]bool)
	for _, h := range history {
		roleContent[h.Role+":"+h.Content] = true
	}
	expected := []string{
		"system:You are a helpful assistant.",
		"user:My name is Alice.",
		"assistant:Hello Alice!",
	}
	for _, exp := range expected {
		if !roleContent[exp] {
			t.Errorf("expected seeded message %q in history, not found", exp)
		}
	}
}

// GC-SPEC-OBS-006: incident.export returns a bounded run bundle.
func TestGateway_IncidentExportReturnsBundleForTask(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, successfulGatewayProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	configFP := "test-cfg-fp-incident"
	srv := gateway.New(gateway.Config{
		Store:             store,
		Registry:          makeTestRegistry(store, eng),
		Policy:            gatewayTestPolicy,
		AuthToken:         gatewayTestAuthToken,
		ConfigFingerprint: configFP,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	sendHello(t, conn)
	ctx := context.Background()

	// Create a task via agent.chat and wait for it to succeed.
	sessionID := uuid.NewString()
	chatReq := rpcReq{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "agent.chat",
		Params: map[string]any{
			"session_id": sessionID,
			"content":    "incident-test",
		},
	}
	if err := wsjson.Write(ctx, conn, chatReq); err != nil {
		t.Fatalf("write chat: %v", err)
	}
	var chatResp rpcResp
	if err := wsjson.Read(ctx, conn, &chatResp); err != nil {
		t.Fatalf("read chat: %v", err)
	}
	if chatResp.Error != nil {
		t.Fatalf("agent.chat error: %+v", chatResp.Error)
	}
	var chatResult struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(chatResp.Result, &chatResult); err != nil {
		t.Fatalf("unmarshal chat result: %v", err)
	}
	waitForTaskDone(t, store, chatResult.TaskID)

	// Call incident.export for the completed task.
	exportReq := rpcReq{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "incident.export",
		Params:  map[string]any{"task_id": chatResult.TaskID},
	}
	if err := wsjson.Write(ctx, conn, exportReq); err != nil {
		t.Fatalf("write incident.export: %v", err)
	}
	var exportResp rpcResp
	if err := wsjson.Read(ctx, conn, &exportResp); err != nil {
		t.Fatalf("read incident.export: %v", err)
	}
	if exportResp.Error != nil {
		t.Fatalf("incident.export error: %+v", exportResp.Error)
	}

	var bundle struct {
		TaskID     string `json:"task_id"`
		ConfigHash string `json:"config_hash"`
		ExportedAt string `json:"exported_at"`
		Task       struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"task"`
		Events []struct {
			EventID   int64  `json:"event_id"`
			TaskID    string `json:"task_id"`
			EventType string `json:"event_type"`
		} `json:"events"`
	}
	if err := json.Unmarshal(exportResp.Result, &bundle); err != nil {
		t.Fatalf("unmarshal incident bundle: %v", err)
	}
	if bundle.TaskID != chatResult.TaskID {
		t.Fatalf("expected task_id=%s, got %s", chatResult.TaskID, bundle.TaskID)
	}
	if bundle.ConfigHash != configFP {
		t.Fatalf("expected config_hash=%s, got %s", configFP, bundle.ConfigHash)
	}
	if bundle.ExportedAt == "" {
		t.Fatal("expected exported_at to be set")
	}
	if bundle.Task.ID != chatResult.TaskID {
		t.Fatalf("expected task.id=%s, got %s", chatResult.TaskID, bundle.Task.ID)
	}
	if len(bundle.Events) == 0 {
		t.Fatal("expected at least one event in the incident bundle")
	}
}

// GC-SPEC-OBS-006: incident.export requires task_id parameter.
func TestGateway_IncidentExportRejectsMissingTaskID(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	sendHello(t, conn)
	ctx := context.Background()

	req := rpcReq{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "incident.export",
		Params:  map[string]any{},
	}
	if err := wsjson.Write(ctx, conn, req); err != nil {
		t.Fatalf("write: %v", err)
	}
	var resp rpcResp
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for missing task_id")
	}
	if resp.Error.Code != gateway.ErrCodeInvalid {
		t.Fatalf("expected invalid code %d, got %d", gateway.ErrCodeInvalid, resp.Error.Code)
	}
}

func TestPrometheusMetricsEndpoint_RejectsMissingAuth(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, nil, engine.Config{WorkerCount: 1, PollInterval: 100 * time.Millisecond, TaskTimeout: 1 * time.Minute})
	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics/prometheus")
	if err != nil {
		t.Fatalf("GET /metrics/prometheus: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// GC-SPEC-TUI-001: config.list ACP method.
func TestGateway_ConfigListReturnsAPIKeys(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	sendHello(t, conn)
	ctx := context.Background()

	req := rpcReq{JSONRPC: "2.0", ID: 1, Method: "config.list"}
	if err := wsjson.Write(ctx, conn, req); err != nil {
		t.Fatalf("write: %v", err)
	}
	var resp rpcResp
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("config.list error: %+v", resp.Error)
	}
	var result struct {
		APIKeys map[string]string `json:"api_keys"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.APIKeys == nil {
		t.Fatal("expected api_keys map in result")
	}
}

// GC-SPEC-TUI-001: policy.domain.add ACP method rejects missing domain.
func TestGateway_PolicyDomainAddRejectsMissingDomain(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	conn := connectWS(t, ts.URL, gatewayTestAuthToken)
	sendHello(t, conn)
	ctx := context.Background()

	req := rpcReq{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "policy.domain.add",
		Params:  map[string]any{},
	}
	if err := wsjson.Write(ctx, conn, req); err != nil {
		t.Fatalf("write: %v", err)
	}
	var resp rpcResp
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for missing domain")
	}
	if resp.Error.Code != gateway.ErrCodeInvalid {
		t.Fatalf("expected invalid code %d, got %d", gateway.ErrCodeInvalid, resp.Error.Code)
	}
}

// TestGateway_ExecutePlan verifies the POST /api/plans/{name}/execute REST endpoint.
// GC-SPEC-PDR-v4-Phase-4: Plan execution endpoint integration test.
func TestGateway_ExecutePlan(t *testing.T) {
	store := openStoreForGatewayTest(t)
	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	testPlan := &coordinator.Plan{
		Name: "test-plan",
		Steps: []coordinator.PlanStep{
			{ID: "step1", AgentID: "default", Prompt: "echo hello"},
		},
	}

	// Create executor with a mock router (no waiter = test mode).
	mockRouter := &testChatRouter{store: store}
	executor := coordinator.NewExecutor(mockRouter, nil, store)

	srv := gateway.New(gateway.Config{
		Store:     store,
		Registry:  makeTestRegistry(store, eng),
		Policy:    gatewayTestPolicy,
		AuthToken: gatewayTestAuthToken,
		PlansMap: map[string]*coordinator.Plan{
			"test-plan": testPlan,
		},
		Plans: map[string]gateway.PlanSummary{
			"test-plan": {Name: "test-plan", StepCount: 1, AgentIDs: []string{"default"}},
		},
		Executor: executor,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Test 1: Successful execution (POST /api/plans/test-plan/execute).
	t.Run("success", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/plans/test-plan/execute", strings.NewReader(`{"session_id":"`+uuid.NewString()+`"}`))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("execute request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusAccepted {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 202, got %d: %s", resp.StatusCode, string(body))
		}

		var result map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("decode response: %v", err)
		}

		if result["execution_id"] == nil || result["execution_id"].(string) == "" {
			t.Fatal("expected non-empty execution_id")
		}
		if result["plan_name"] != "test-plan" {
			t.Fatalf("plan_name: got %v, want test-plan", result["plan_name"])
		}
		if result["status"] != "running" {
			t.Fatalf("status: got %v, want running", result["status"])
		}
		if result["session_id"] == nil || result["session_id"].(string) == "" {
			t.Fatal("expected non-empty session_id")
		}
	})

	// Test 2: Non-existent plan returns 404.
	t.Run("not_found", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/plans/nonexistent/execute", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("execute request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", resp.StatusCode)
		}
	})

	// Test 3: Missing executor returns 503.
	t.Run("executor_unavailable", func(t *testing.T) {
		srvNoExec := gateway.New(gateway.Config{
			Store:     store,
			Registry:  makeTestRegistry(store, eng),
			Policy:    gatewayTestPolicy,
			AuthToken: gatewayTestAuthToken,
			PlansMap: map[string]*coordinator.Plan{
				"test-plan": testPlan,
			},
			Executor: nil, // No executor.
		})
		tsNoExec := httptest.NewServer(srvNoExec.Handler())
		defer tsNoExec.Close()

		req, err := http.NewRequest(http.MethodPost, tsNoExec.URL+"/api/plans/test-plan/execute", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("execute request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d", resp.StatusCode)
		}
	})

	// Test 4: Auto-generated session_id when not provided.
	t.Run("auto_session_id", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/plans/test-plan/execute", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("execute request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusAccepted {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 202, got %d: %s", resp.StatusCode, string(body))
		}

		var result map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("decode response: %v", err)
		}

		sessionID, ok := result["session_id"].(string)
		if !ok || sessionID == "" {
			t.Fatal("expected auto-generated session_id")
		}
		// Verify it's a valid UUID.
		if _, err := uuid.Parse(sessionID); err != nil {
			t.Fatalf("auto-generated session_id is not a valid UUID: %v", err)
		}
	})
}

// testChatRouter is a minimal ChatTaskRouter for gateway plan execution tests.
type testChatRouter struct {
	store *persistence.Store
}

func (r *testChatRouter) CreateChatTask(ctx context.Context, agentID, sessionID, content string) (string, error) {
	taskID, err := r.store.CreateTaskForAgent(ctx, agentID, sessionID, fmt.Sprintf(`{"content":%q}`, content))
	if err != nil {
		return "", err
	}
	return taskID, nil
}
