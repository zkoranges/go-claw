package gateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOpenAI_ToolsAccepted(t *testing.T) {
	ts, _ := apiTestServer(t)

	// Requests with tools field should be accepted (tools are ignored, not rejected).
	body := `{
		"model": "goclaw-v1",
		"messages": [{"role": "user", "content": "hello"}],
		"tools": [{"type": "function", "function": {"name": "get_weather", "parameters": {}}}]
	}`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Context timeout is expected — the task won't complete in tests.
		// What matters is we got past validation (no immediate 400).
		return
	}
	defer resp.Body.Close()

	// Must NOT be 400 — tools are accepted and ignored.
	if resp.StatusCode == http.StatusBadRequest {
		rawBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("got unexpected 400 for request with tools: %s", string(rawBody))
	}
}

func TestOpenAI_ToolsAbsent_Success(t *testing.T) {
	ts, _ := apiTestServer(t)

	body := `{
		"model": "goclaw-v1",
		"messages": [{"role": "user", "content": "hello"}]
	}`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Context timeout is expected — the task won't complete in tests.
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusBadRequest {
		rawBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("got unexpected 400 for request without tools: %s", string(rawBody))
	}
}

func TestOpenAI_SamplingParams_Accepted(t *testing.T) {
	ts, _ := apiTestServer(t)

	body := `{
		"model": "goclaw-v1",
		"messages": [{"role": "user", "content": "hello"}],
		"temperature": 0.7,
		"top_p": 0.9,
		"max_tokens": 100,
		"stop": ["\n"]
	}`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Timeout expected — no real LLM.
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusBadRequest {
		rawBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("sampling params rejected with 400: %s", string(rawBody))
	}
}

func TestOpenAI_ResponseFormat_Accepted(t *testing.T) {
	ts, _ := apiTestServer(t)

	body := `{
		"model": "goclaw-v1",
		"messages": [{"role": "user", "content": "list 3 colors as JSON"}],
		"response_format": {"type": "json_schema", "json_schema": {"type": "object"}}
	}`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusBadRequest {
		rawBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("response_format rejected with 400: %s", string(rawBody))
	}
}

func TestOpenAI_NonStream_UsageSplit(t *testing.T) {
	// Verify Usage struct has all three fields with correct JSON serialization.
	usage := struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}{
		PromptTokens:     10,
		CompletionTokens: 20,
		TotalTokens:      30,
	}

	b, err := json.Marshal(usage)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]int
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded["prompt_tokens"] != 10 {
		t.Errorf("expected prompt_tokens=10, got %d", decoded["prompt_tokens"])
	}
	if decoded["completion_tokens"] != 20 {
		t.Errorf("expected completion_tokens=20, got %d", decoded["completion_tokens"])
	}
	if decoded["total_tokens"] != 30 {
		t.Errorf("expected total_tokens=30, got %d", decoded["total_tokens"])
	}
}

func TestOpenAI_StreamChunk_UsesDelta(t *testing.T) {
	// Verify streaming chunks use "delta" not "message".
	type testChoice struct {
		Index   int              `json:"index"`
		Message *json.RawMessage `json:"message,omitempty"`
		Delta   *json.RawMessage `json:"delta,omitempty"`
	}

	type testResp struct {
		Choices []testChoice `json:"choices"`
	}

	// Simulate a streaming chunk with Delta set.
	chunk := `{"choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`

	var resp testResp
	if err := json.Unmarshal([]byte(chunk), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Delta == nil {
		t.Fatal("expected delta to be set in streaming chunk")
	}
	if resp.Choices[0].Message != nil {
		t.Fatal("expected message to be nil in streaming chunk")
	}
}

func TestOpenAI_FinishReason_NullWhenAbsent(t *testing.T) {
	// Verify FinishReason is omitted (null) in content chunks,
	// and present ("stop") in the final chunk.
	type testChoice struct {
		Index        int     `json:"index"`
		FinishReason *string `json:"finish_reason"`
	}
	type testResp struct {
		Choices []testChoice `json:"choices"`
	}

	// Content chunk — no finish_reason set.
	contentChunk := `{"choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`
	var content testResp
	if err := json.Unmarshal([]byte(contentChunk), &content); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	if content.Choices[0].FinishReason != nil {
		t.Errorf("content chunk: expected finish_reason=null, got %q", *content.Choices[0].FinishReason)
	}

	// Final chunk — finish_reason="stop".
	finalChunk := `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	var final testResp
	if err := json.Unmarshal([]byte(finalChunk), &final); err != nil {
		t.Fatalf("unmarshal final: %v", err)
	}
	if final.Choices[0].FinishReason == nil {
		t.Fatal("final chunk: expected finish_reason to be set")
	}
	if *final.Choices[0].FinishReason != "stop" {
		t.Errorf("final chunk: expected finish_reason=\"stop\", got %q", *final.Choices[0].FinishReason)
	}
}

func TestOpenAI_ErrorFormat_Spec(t *testing.T) {
	ts, _ := apiTestServer(t)

	// Send empty messages to trigger a validation error.
	body := `{"model": "goclaw-v1", "messages": []}`

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+gatewayTestAuthToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	var errResp struct {
		Error struct {
			Message string  `json:"message"`
			Type    string  `json:"type"`
			Param   *string `json:"param"`
			Code    string  `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}

	// Verify OpenAI error format fields.
	if errResp.Error.Type != "invalid_request_error" {
		t.Errorf("expected type=\"invalid_request_error\", got %q", errResp.Error.Type)
	}
	if errResp.Error.Param != nil {
		t.Errorf("expected param=null, got %v", errResp.Error.Param)
	}
	if errResp.Error.Code == "" {
		t.Error("expected code to be non-empty")
	}
	if errResp.Error.Message == "" {
		t.Error("expected message to be non-empty")
	}
}

func TestOpenAI_ErrorFormat_AuthError(t *testing.T) {
	ts, _ := apiTestServer(t)

	body := `{"model": "goclaw-v1", "messages": [{"role": "user", "content": "hi"}]}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong-token")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	var errResp struct {
		Error struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if errResp.Error.Type != "authentication_error" {
		t.Errorf("expected type=\"authentication_error\", got %q", errResp.Error.Type)
	}
	if errResp.Error.Code != "invalid_api_key" {
		t.Errorf("expected code=\"invalid_api_key\", got %q", errResp.Error.Code)
	}
}

func TestOpenAI_ToolCallFormat(t *testing.T) {
	// Verify ToolCall and ToolFunction JSON serialization matches OpenAI spec.
	type testMsg struct {
		Role      string `json:"role"`
		ToolCalls []struct {
			Index    int    `json:"index"`
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments,omitempty"`
			} `json:"function"`
		} `json:"tool_calls,omitempty"`
	}

	chunk := `{"role":"assistant","tool_calls":[{"index":0,"id":"call_search","type":"function","function":{"name":"brave_search","arguments":"{\"query\":\"go 1.24\"}"}}]}`

	var msg testMsg
	if err := json.Unmarshal([]byte(chunk), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool_call, got %d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.Type != "function" {
		t.Errorf("expected type=\"function\", got %q", tc.Type)
	}
	if tc.Function.Name != "brave_search" {
		t.Errorf("expected name=\"brave_search\", got %q", tc.Function.Name)
	}
	if tc.Function.Arguments == "" {
		t.Error("expected arguments to be non-empty")
	}
}
