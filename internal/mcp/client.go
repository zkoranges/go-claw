package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
)

// Client implements a basic MCP client.
type Client struct {
	name      string
	transport Transport
	nextID    int64

	pendingMu sync.Mutex
	pending   map[int64]chan jsonRPCResponse
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      int64           `json:"id"`
}

type jsonRPCNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
	ID      int64           `json:"id"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

func NewClient(name string, transport Transport) (*Client, error) {
	c := &Client{
		name:      name,
		transport: transport,
		pending:   make(map[int64]chan jsonRPCResponse),
	}
	// Start listener
	go c.listen()
	return c, nil
}

func (c *Client) listen() {
	for {
		msg, err := c.transport.Receive(context.Background())
		if err != nil {
			// Transport closed or error
			return
		}

		var resp jsonRPCResponse
		if err := json.Unmarshal(msg, &resp); err != nil {
			// Maybe it's a notification or invalid JSON, ignore for now
			continue
		}

		if resp.ID != 0 {
			c.pendingMu.Lock()
			ch, ok := c.pending[resp.ID]
			if ok {
				delete(c.pending, resp.ID)
				ch <- resp
			}
			c.pendingMu.Unlock()
		}
	}
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)

	var paramsJSON json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		paramsJSON = b
	}

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsJSON,
		ID:      id,
	}

	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ch := make(chan jsonRPCResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	if err := c.transport.Send(ctx, b); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

// Initialize performs the MCP handshake.
func (c *Client) Initialize(ctx context.Context) error {
	// Send initialize request
	// Spec: method "initialize"
	// Params: { "protocolVersion": "2024-11-05", "capabilities": {}, "clientInfo": {...} }

	params := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"roots": map[string]any{
				"listChanged": true,
			},
		},
		"clientInfo": map[string]string{
			"name":    "goclaw",
			"version": "0.1.0",
		},
	}

	_, err := c.call(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("initialize failed: %w", err)
	}

	// Server returns its capabilities and version. We interpret it as success.
	// We must send "notifications/initialized" afterwards.

	notif := jsonRPCNotification{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	b, _ := json.Marshal(notif)
	if err := c.transport.Send(ctx, b); err != nil {
		return fmt.Errorf("send initialized notification: %w", err)
	}

	return nil
}

// ListTools calls tools/list.
func (c *Client) ListTools(ctx context.Context) ([]MCPTool, error) {
	res, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("tools/list failed: %w", err)
	}

	var result struct {
		Tools []MCPTool `json:"tools"`
	}
	if err := json.Unmarshal(res, &result); err != nil {
		return nil, fmt.Errorf("unmarshal tools: %w", err)
	}
	return result.Tools, nil
}

// CallTool calls tools/call.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	res, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return nil, fmt.Errorf("tools/call failed: %w", err)
	}

	// Spec: returns { "content": [ { "type": "text", "text": "..." } ], "isError": false }
	// We want to return the raw result structure or just the content?
	// The Genkit bridge will likely want to format it.
	// Let's return the raw result JSON (the value of 'result' in JSON-RPC response).
	return res, nil
}

func (c *Client) Close() error {
	return c.transport.Close()
}
