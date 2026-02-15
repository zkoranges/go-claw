package gateway_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOpenAI_ToolsRejection(t *testing.T) {
	ts, _ := apiTestServer(t)

	body := `{
		"model": "goclaw-v1",
		"messages": [{"role": "user", "content": "hello"}],
		"tools": [{"type": "function", "function": {"name": "get_weather", "parameters": {}}}]
	}`

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestOpenAI_ToolsRejection_ErrorFormat(t *testing.T) {
	ts, _ := apiTestServer(t)

	body := `{
		"model": "goclaw-v1",
		"messages": [{"role": "user", "content": "hello"}],
		"tools": [{"type": "function", "function": {"name": "get_weather"}}]
	}`

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var errResp struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rawBody, &errResp); err != nil {
		t.Fatalf("decode error response: %v\nbody: %s", err, string(rawBody))
	}

	if errResp.Error.Type != "invalid_request_error" {
		t.Errorf("expected error type 'invalid_request_error', got %q", errResp.Error.Type)
	}
	if errResp.Error.Code != "invalid_request_error" {
		t.Errorf("expected error code 'invalid_request_error', got %q", errResp.Error.Code)
	}
	if !strings.Contains(errResp.Error.Message, "Tools are not supported") {
		t.Errorf("expected error message to mention tools not supported, got %q", errResp.Error.Message)
	}
}

func TestOpenAI_ToolsAbsent_Success(t *testing.T) {
	ts, _ := apiTestServer(t)

	// Request without tools field — should pass validation and reach the handler.
	// We expect it to proceed past the tools check (may fail later due to no Brain, but NOT 400).
	body := `{
		"model": "goclaw-v1",
		"messages": [{"role": "user", "content": "hello"}]
	}`

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	// Should NOT be 400 — the tools validation should not trigger.
	if resp.StatusCode == http.StatusBadRequest {
		rawBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("got unexpected 400 for request without tools: %s", string(rawBody))
	}
}
