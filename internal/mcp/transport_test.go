package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStdioTransport_NewWithInvalidCommand(t *testing.T) {
	_, err := NewStdioTransport("nonexistent-command-xyz", nil, nil)
	if err == nil {
		t.Fatal("expected error for nonexistent command")
	}
	if !strings.Contains(err.Error(), "nonexistent-command-xyz") {
		t.Errorf("error should mention command name, got: %v", err)
	}
}

func TestStdioTransport_NewWithCat(t *testing.T) {
	transport, err := NewStdioTransport("cat", nil, nil)
	if err != nil {
		t.Fatalf("failed to start cat: %v", err)
	}
	// Just verify it starts - don't call Receive to avoid goroutine leak on Close.
	if !transport.running {
		t.Error("expected transport to be running")
	}
	transport.Close()
}

func TestStdioTransport_SendReceive(t *testing.T) {
	// cat echoes stdin to stdout, so Send then Receive should round-trip.
	transport, err := NewStdioTransport("cat", nil, nil)
	if err != nil {
		t.Fatalf("failed to start cat: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	msg := json.RawMessage(`{"jsonrpc":"2.0","method":"test"}`)
	if err := transport.Send(ctx, msg); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	received, err := transport.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(received, &parsed); err != nil {
		t.Fatalf("received message is not valid JSON: %v (raw: %q)", err, string(received))
	}
	if parsed["method"] != "test" {
		t.Errorf("method = %v, want 'test'", parsed["method"])
	}

	// Close after Receive completes (goroutine already returned via channel).
	transport.Close()
}

func TestStdioTransport_SendAfterClose(t *testing.T) {
	transport, err := NewStdioTransport("cat", nil, nil)
	if err != nil {
		t.Fatalf("failed to start cat: %v", err)
	}

	transport.Close()

	ctx := context.Background()
	err = transport.Send(ctx, json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error sending after close")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("expected 'closed' error, got: %v", err)
	}
}

func TestStdioTransport_DoubleClose(t *testing.T) {
	transport, err := NewStdioTransport("cat", nil, nil)
	if err != nil {
		t.Fatalf("failed to start cat: %v", err)
	}

	if err := transport.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	if err := transport.Close(); err != nil {
		t.Errorf("second Close should not error, got: %v", err)
	}
}

func TestReconnectableTransport_NewWithInvalidCommand(t *testing.T) {
	_, err := NewReconnectableTransport("nonexistent-cmd-xyz", nil, nil)
	if err == nil {
		t.Fatal("expected error for nonexistent command")
	}
}

func TestReconnectableTransport_Close(t *testing.T) {
	rt, err := NewReconnectableTransport("cat", nil, nil)
	if err != nil {
		t.Fatalf("failed to create reconnectable transport: %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestReconnectableTransport_SendReceive(t *testing.T) {
	rt, err := NewReconnectableTransport("cat", nil, nil)
	if err != nil {
		t.Fatalf("failed to create reconnectable transport: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	msg := json.RawMessage(`{"id":1}`)
	if err := rt.Send(ctx, msg); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	received, err := rt.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(received, &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}

	rt.Close()
}

func TestReconnectableTransport_MaxRetry(t *testing.T) {
	rt, err := NewReconnectableTransport("cat", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rt.maxRetry != 3 {
		t.Errorf("maxRetry = %d, want 3", rt.maxRetry)
	}
	rt.Close()
}
