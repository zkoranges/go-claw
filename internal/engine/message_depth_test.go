package engine

import (
	"encoding/json"
	"testing"
)

func TestChatTaskPayload_MessageDepthOmitEmpty(t *testing.T) {
	// Zero depth should omit message_depth from JSON.
	p := chatTaskPayload{Content: "hello"}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	raw := string(data)
	if raw != `{"content":"hello"}` {
		t.Fatalf("expected no message_depth, got %s", raw)
	}
}

func TestChatTaskPayload_MessageDepthIncluded(t *testing.T) {
	// Non-zero depth should be included.
	p := chatTaskPayload{Content: "hi", MessageDepth: 3}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded chatTaskPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.MessageDepth != 3 {
		t.Fatalf("expected depth 3, got %d", decoded.MessageDepth)
	}
	if decoded.Content != "hi" {
		t.Fatalf("expected content 'hi', got %q", decoded.Content)
	}
}

func TestChatTaskPayload_BackwardCompatible(t *testing.T) {
	// Old payloads without message_depth should decode to depth=0.
	data := []byte(`{"content":"old message"}`)
	var p chatTaskPayload
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.MessageDepth != 0 {
		t.Fatalf("expected depth 0 for legacy payload, got %d", p.MessageDepth)
	}
	if p.Content != "old message" {
		t.Fatalf("expected content 'old message', got %q", p.Content)
	}
}
