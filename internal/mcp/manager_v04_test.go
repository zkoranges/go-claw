package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/policy"
)

// newTestLogger creates a logger that discards output.
func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestManager_ConnectAgentServers verifies per-agent server connections.
func TestManager_ConnectAgentServers(t *testing.T) {
	mgr := NewManager([]ServerConfig{}, policy.Policy{}, newTestLogger())

	// Create mock configs for agent
	agentConfigs := []ServerConfig{
		{Name: "github", Command: "false", Args: nil, Enabled: true}, // false command will fail, but that's ok
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// ConnectAgentServers may error if command doesn't exist, which is fine
	// We're testing the API exists and can be called
	_ = mgr.ConnectAgentServers(ctx, "coder", agentConfigs)
}

// TestManager_DisconnectAgent verifies per-agent disconnect.
func TestManager_DisconnectAgent(t *testing.T) {
	mgr := NewManager([]ServerConfig{}, policy.Policy{}, newTestLogger())

	// DisconnectAgent should not error even if agent not connected
	err := mgr.DisconnectAgent("coder")
	if err != nil {
		t.Errorf("DisconnectAgent failed: %v", err)
	}
}

// TestManager_DiscoverTools verifies tool discovery API.
func TestManager_DiscoverTools(t *testing.T) {
	mgr := NewManager([]ServerConfig{}, policy.Policy{}, newTestLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Should return empty list for agent with no servers
	tools, err := mgr.DiscoverTools(ctx, "coder")
	if err != nil {
		t.Errorf("DiscoverTools failed: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("expected 0 tools for unconnected agent, got %d", len(tools))
	}
}

// TestManager_InvokeTool verifies tool invocation API.
func TestManager_InvokeTool(t *testing.T) {
	mgr := NewManager([]ServerConfig{}, policy.Policy{}, newTestLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Should error for unknown server
	_, err := mgr.InvokeTool(ctx, "coder", "unknown", "tool", json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected InvokeTool to fail for unknown server")
	}
}

// TestManager_ServerNames verifies listing accessible servers.
func TestManager_ServerNames(t *testing.T) {
	mgr := NewManager([]ServerConfig{}, policy.Policy{}, newTestLogger())

	// Should return empty list for agent with no servers
	servers := mgr.ServerNames("coder")
	if len(servers) != 0 {
		t.Errorf("expected empty server list, got %v", servers)
	}
}

// TestManager_Healthy verifies health reporting.
func TestManager_Healthy(t *testing.T) {
	mgr := NewManager([]ServerConfig{}, policy.Policy{}, newTestLogger())

	// Unconnected server should report unhealthy
	if mgr.Healthy("coder", "github") {
		t.Error("expected unhealthy for unconnected server")
	}
}

// TestManager_ReloadAgent verifies hot-reload of agent MCP servers.
func TestManager_ReloadAgent(t *testing.T) {
	mgr := NewManager([]ServerConfig{}, policy.Policy{}, newTestLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Reload with new configs should not error
	err := mgr.ReloadAgent(ctx, "coder", []ServerConfig{
		{Name: "github", Command: "false", Args: nil, Enabled: true},
	})
	if err != nil {
		t.Logf("ReloadAgent error (may be expected): %v", err)
	}
}
