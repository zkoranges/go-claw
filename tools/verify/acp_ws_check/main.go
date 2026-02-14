package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<marshal-error:%v>", err)
	}
	return string(b)
}

func main() {
	url := flag.String("url", "ws://127.0.0.1:18789/ws", "websocket endpoint")
	timeout := flag.Duration("timeout", 8*time.Second, "overall timeout")
	token := flag.String("token", "", "bearer token expected by ACP gateway")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if strings.TrimSpace(*token) == "" {
		fmt.Fprintln(os.Stderr, "token is required")
		os.Exit(2)
	}

	_, unauthResp, unauthErr := websocket.Dial(ctx, *url, nil)
	if unauthErr == nil {
		fmt.Fprintln(os.Stderr, "expected missing-auth dial to fail but it succeeded")
		os.Exit(1)
	}
	if unauthResp == nil || unauthResp.StatusCode != http.StatusUnauthorized {
		fmt.Fprintf(os.Stderr, "expected 401 for missing auth, got response=%v err=%v\n", unauthResp, unauthErr)
		os.Exit(1)
	}
	fmt.Printf("AUTH_CHECK missing token rejected status=%d\n", unauthResp.StatusCode)

	conn, _, err := websocket.Dial(ctx, *url, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + strings.TrimSpace(*token)},
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "authorized dial failed: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	requests := []rpcRequest{
		{
			JSONRPC: "2.0",
			ID:      0,
			Method:  "agent.chat",
			Params: map[string]interface{}{
				"session_id": "7ced61c5-923f-41c2-ac40-d2137193a676",
				"content":    "ping",
			},
		},
		{JSONRPC: "2.0", ID: 1, Method: "system.hello", Params: map[string]interface{}{"version": "1.0"}},
		{JSONRPC: "2.0", ID: 2, Method: "system.status", Params: map[string]interface{}{}},
	}

	for _, req := range requests {
		fmt.Printf(">> %s\n", mustJSON(req))
		if err := wsjson.Write(ctx, conn, req); err != nil {
			fmt.Fprintf(os.Stderr, "write failed: %v\n", err)
			os.Exit(1)
		}
		var resp map[string]interface{}
		if err := wsjson.Read(ctx, conn, &resp); err != nil {
			fmt.Fprintf(os.Stderr, "read failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("<< %s\n", mustJSON(resp))
		switch req.Method {
		case "agent.chat":
			if !hasErrorCode(resp, -32600) {
				fmt.Fprintln(os.Stderr, "expected handshake-required error (-32600) for pre-hello mutate")
				os.Exit(1)
			}
		case "system.hello":
			if hasAnyError(resp) {
				fmt.Fprintln(os.Stderr, "expected successful system.hello")
				os.Exit(1)
			}
		case "system.status":
			if hasAnyError(resp) {
				fmt.Fprintln(os.Stderr, "expected successful system.status")
				os.Exit(1)
			}
		}
	}

	fmt.Println("VERDICT PASS")
}

func hasAnyError(resp map[string]interface{}) bool {
	_, ok := resp["error"]
	return ok && resp["error"] != nil
}

func hasErrorCode(resp map[string]interface{}, want int) bool {
	errVal, ok := resp["error"]
	if !ok || errVal == nil {
		return false
	}
	errMap, ok := errVal.(map[string]interface{})
	if !ok {
		return false
	}
	code, ok := errMap["code"].(float64)
	if !ok {
		return false
	}
	return int(code) == want
}
