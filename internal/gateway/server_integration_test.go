package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/agent"
	"github.com/basket/go-claw/internal/engine"
	"github.com/basket/go-claw/internal/gateway"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestGateway_RealTCPServerRespondsToStatusAndChat(t *testing.T) {
	// [SPEC: SPEC-ACP-WS-1, SPEC-ACP-JSONRPC-1] [PDR: V-11]
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	eng := engine.New(store, engine.EchoProcessor{}, engine.Config{
		WorkerCount:  1,
		PollInterval: 5 * time.Millisecond,
		TaskTimeout:  2 * time.Second,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(runCtx)

	reg := agent.NewRegistry(store, nil, nil, nil, nil)
	reg.RegisterTestAgent("default", eng)

	srv := gateway.New(gateway.Config{
		Store:    store,
		Registry: reg,
		Policy: policy.Policy{
			AllowCapabilities: []string{"acp.read", "acp.mutate"},
		},
		AuthToken: gatewayTestAuthToken,
	})
	httpSrv := &http.Server{Handler: srv.Handler()}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		_ = httpSrv.Serve(ln)
	}()
	defer func() {
		_ = httpSrv.Shutdown(context.Background())
	}()

	addr := ln.Addr().String()
	ctx, cancelDial := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelDial()
	conn, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://%s/ws", addr), &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + gatewayTestAuthToken},
		},
	})
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	if err := wsjson.Write(context.Background(), conn, map[string]any{
		"jsonrpc": "2.0",
		"id":      0,
		"method":  "system.hello",
		"params":  map[string]any{"version": "1.0"},
	}); err != nil {
		t.Fatalf("write system.hello: %v", err)
	}
	var helloResp map[string]any
	if err := wsjson.Read(context.Background(), conn, &helloResp); err != nil {
		t.Fatalf("read system.hello: %v", err)
	}
	if helloResp["error"] != nil {
		t.Fatalf("unexpected system.hello error: %#v", helloResp["error"])
	}

	if err := wsjson.Write(context.Background(), conn, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "system.status",
	}); err != nil {
		t.Fatalf("write system.status: %v", err)
	}
	var statusResp map[string]any
	if err := wsjson.Read(context.Background(), conn, &statusResp); err != nil {
		t.Fatalf("read system.status: %v", err)
	}
	if statusResp["error"] != nil {
		t.Fatalf("unexpected system.status error: %#v", statusResp["error"])
	}

	if err := wsjson.Write(context.Background(), conn, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "agent.chat",
		"params": map[string]any{
			"session_id": "3a488f03-3376-4355-8c6e-8e4c215ae9d5",
			"content":    "integration ping",
		},
	}); err != nil {
		t.Fatalf("write agent.chat: %v", err)
	}
	var chatResp struct {
		Result json.RawMessage `json:"result"`
		Error  interface{}     `json:"error"`
	}
	if err := wsjson.Read(context.Background(), conn, &chatResp); err != nil {
		t.Fatalf("read agent.chat: %v", err)
	}
	if chatResp.Error != nil {
		t.Fatalf("unexpected agent.chat error: %#v", chatResp.Error)
	}
	var result struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(chatResp.Result, &result); err != nil {
		t.Fatalf("decode chat result: %v", err)
	}
	if result.TaskID == "" {
		t.Fatalf("expected task_id")
	}
}
