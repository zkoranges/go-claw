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
	"github.com/google/uuid"
)

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcReq struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

func main() {
	url := flag.String("url", "ws://127.0.0.1:18789/ws", "websocket endpoint")
	token := flag.String("token", "", "bearer token")
	timeout := flag.Duration("timeout", 15*time.Second, "overall timeout")
	sessionID := flag.String("session-id", uuid.NewString(), "session uuid for chat request")
	flag.Parse()

	if strings.TrimSpace(*token) == "" {
		fmt.Fprintln(os.Stderr, "token is required")
		os.Exit(2)
	}
	if _, err := uuid.Parse(strings.TrimSpace(*sessionID)); err != nil {
		fmt.Fprintf(os.Stderr, "invalid session-id: %v\n", err)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, *url, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + strings.TrimSpace(*token)},
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial failed: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close(websocket.StatusNormalClosure, "runtime smoke done")

	if err := writeReq(ctx, conn, rpcReq{
		JSONRPC: "2.0",
		ID:      1001,
		Method:  "system.hello",
		Params:  map[string]interface{}{"version": "1.0"},
	}); err != nil {
		fatal("write system.hello", err)
	}
	helloResp, err := readResponseByID(ctx, conn, 1001)
	if err != nil {
		fatal("read system.hello response", err)
	}
	if helloResp.Error != nil {
		fatalf("system.hello error: %+v", helloResp.Error)
	}
	fmt.Println("CHECK hello ok")

	if err := writeReq(ctx, conn, rpcReq{
		JSONRPC: "2.0",
		ID:      1002,
		Method:  "system.status",
		Params:  map[string]interface{}{},
	}); err != nil {
		fatal("write system.status", err)
	}
	statusResp, err := readResponseByID(ctx, conn, 1002)
	if err != nil {
		fatal("read system.status response", err)
	}
	if statusResp.Error != nil {
		fatalf("system.status error: %+v", statusResp.Error)
	}
	fmt.Println("CHECK status ok")

	if err := writeReq(ctx, conn, rpcReq{
		JSONRPC: "2.0",
		ID:      1003,
		Method:  "agent.chat",
		Params: map[string]interface{}{
			"session_id": *sessionID,
			"content":    "runtime smoke test",
		},
	}); err != nil {
		fatal("write agent.chat", err)
	}
	chatResp, err := readResponseByID(ctx, conn, 1003)
	if err != nil {
		fatal("read agent.chat response", err)
	}
	if chatResp.Error != nil {
		fatalf("agent.chat error: %+v", chatResp.Error)
	}
	taskID, err := extractField(chatResp.Result, "task_id")
	if err != nil || strings.TrimSpace(taskID) == "" {
		fatalf("agent.chat missing task_id: %v", err)
	}
	fmt.Printf("CHECK chat enqueued task_id=%s\n", taskID)

	if err := writeReq(ctx, conn, rpcReq{
		JSONRPC: "2.0",
		ID:      1004,
		Method:  "approval.request",
		Params: map[string]interface{}{
			"action":  "runtime-smoke-approval",
			"details": "verify approval broker lifecycle",
		},
	}); err != nil {
		fatal("write approval.request", err)
	}

	approvalID, err := waitForApprovalRequestLifecycle(ctx, conn, 1004)
	if err != nil {
		fatal("approval.request lifecycle", err)
	}
	fmt.Printf("CHECK approval required approval_id=%s\n", approvalID)

	if err := writeReq(ctx, conn, rpcReq{
		JSONRPC: "2.0",
		ID:      1005,
		Method:  "approval.respond",
		Params: map[string]interface{}{
			"approval_id": approvalID,
			"decision":    "deny",
		},
	}); err != nil {
		fatal("write approval.respond", err)
	}
	if err := waitForApprovalUpdated(ctx, conn, 1005, approvalID, "DENIED"); err != nil {
		fatal("approval.respond lifecycle", err)
	}
	fmt.Printf("CHECK approval updated approval_id=%s status=DENIED\n", approvalID)

	fmt.Println("VERDICT PASS")
}

func writeReq(ctx context.Context, conn *websocket.Conn, req rpcReq) error {
	return wsjson.Write(ctx, conn, req)
}

func readResponseByID(ctx context.Context, conn *websocket.Conn, wantID int) (rpcFrame, error) {
	for {
		frame, err := readFrame(ctx, conn)
		if err != nil {
			return rpcFrame{}, err
		}
		if id, ok := frameID(frame.ID); ok && id == wantID {
			return frame, nil
		}
	}
}

func waitForApprovalRequestLifecycle(ctx context.Context, conn *websocket.Conn, responseID int) (string, error) {
	var approvalID string
	sawResponse := false
	sawRequired := false
	for !sawResponse || !sawRequired {
		frame, err := readFrame(ctx, conn)
		if err != nil {
			return "", err
		}
		if id, ok := frameID(frame.ID); ok && id == responseID {
			if frame.Error != nil {
				return "", fmt.Errorf("approval.request error: %d %s", frame.Error.Code, frame.Error.Message)
			}
			reqID, err := extractField(frame.Result, "approval_id")
			if err != nil {
				return "", fmt.Errorf("approval.request missing approval_id in result: %w", err)
			}
			if approvalID == "" {
				approvalID = reqID
			}
			sawResponse = true
			continue
		}
		if frame.Method == "approval.required" {
			reqID, err := extractField(frame.Params, "approval_id")
			if err != nil {
				return "", fmt.Errorf("approval.required missing approval_id: %w", err)
			}
			if approvalID == "" {
				approvalID = reqID
			}
			sawRequired = true
		}
	}
	if strings.TrimSpace(approvalID) == "" {
		return "", fmt.Errorf("approval id unresolved")
	}
	return approvalID, nil
}

func waitForApprovalUpdated(ctx context.Context, conn *websocket.Conn, responseID int, approvalID string, wantStatus string) error {
	sawResponse := false
	sawUpdated := false
	for !sawResponse || !sawUpdated {
		frame, err := readFrame(ctx, conn)
		if err != nil {
			return err
		}
		if id, ok := frameID(frame.ID); ok && id == responseID {
			if frame.Error != nil {
				return fmt.Errorf("approval.respond error: %d %s", frame.Error.Code, frame.Error.Message)
			}
			sawResponse = true
			continue
		}
		if frame.Method == "approval.updated" {
			id, err := extractField(frame.Params, "approval_id")
			if err != nil {
				return fmt.Errorf("approval.updated missing approval_id: %w", err)
			}
			if id != approvalID {
				continue
			}
			status, err := extractField(frame.Params, "status")
			if err != nil {
				return fmt.Errorf("approval.updated missing status: %w", err)
			}
			if status != wantStatus {
				return fmt.Errorf("approval.updated unexpected status: got %q want %q", status, wantStatus)
			}
			sawUpdated = true
		}
	}
	return nil
}

func readFrame(ctx context.Context, conn *websocket.Conn) (rpcFrame, error) {
	var frame rpcFrame
	if err := wsjson.Read(ctx, conn, &frame); err != nil {
		return rpcFrame{}, err
	}
	return frame, nil
}

func frameID(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var id any
	if err := json.Unmarshal(raw, &id); err != nil {
		return 0, false
	}
	switch v := id.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	default:
		return 0, false
	}
}

func extractField(raw json.RawMessage, field string) (string, error) {
	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", err
	}
	val, ok := payload[field]
	if !ok {
		return "", fmt.Errorf("missing field %q", field)
	}
	asString, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("field %q is not string", field)
	}
	return asString, nil
}

func fatal(msg string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", msg, err)
	os.Exit(1)
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
