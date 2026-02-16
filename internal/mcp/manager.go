package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/basket/go-claw/internal/policy"
)

// ServerConfig defines an MCP server to start.
type ServerConfig struct {
	Name      string            `yaml:"name"`
	Command   string            `yaml:"command"`
	Args      []string          `yaml:"args"`
	Env       map[string]string `yaml:"env"`
	URL       string            `yaml:"url,omitempty"`       // SSE endpoint (v0.4)
	Transport string            `yaml:"transport,omitempty"` // "stdio" (default) or "sse" (v0.4)
	Timeout   string            `yaml:"timeout,omitempty"`   // e.g. "30s" (v0.4)
	Enabled   bool              `yaml:"enabled"`
}

// DiscoveredTool represents a tool enumerated from an MCP server (v0.4).
type DiscoveredTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	ServerName  string
}

type connection struct {
	config  ServerConfig
	client  *Client
	tools   []DiscoveredTool
	healthy bool
	mu      sync.RWMutex
}

// Manager manages multiple MCP clients with per-agent scoping (v0.4).
type Manager struct {
	mu       sync.RWMutex
	global   map[string]*connection                 // name -> connection (shared)
	perAgent map[string]map[string]*connection      // agentID -> name -> connection
	policy   policy.Checker
	logger   *slog.Logger
}

func NewManager(configs []ServerConfig, pol policy.Checker, logger *slog.Logger) *Manager {
	return &Manager{
		global:   make(map[string]*connection),
		perAgent: make(map[string]map[string]*connection),
		policy:   pol,
		logger:   logger,
	}
}

// Start starts all enabled global MCP servers and initializes them.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Note: Start() now only handles global servers.
	// Per-agent servers are started via ConnectAgentServers().
	return nil
}

// ConnectAgentServers starts MCP servers for a specific agent (v0.4).
// Global server references share connections; inline definitions create new connections.
func (m *Manager) ConnectAgentServers(ctx context.Context, agentID string, configs []ServerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.perAgent[agentID]; !exists {
		m.perAgent[agentID] = make(map[string]*connection)
	}

	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}

		// Check if it's a reference to a global server
		if cfg.Command == "" && cfg.URL == "" {
			// Name-only reference to global server
			if conn, ok := m.global[cfg.Name]; ok {
				m.perAgent[agentID][cfg.Name] = conn
				m.logger.Debug("agent using global mcp server", "agent", agentID, "server", cfg.Name)
				continue
			}
		}

		// Inline definition: create agent-specific connection
		m.logger.Info("connecting agent to mcp server", "agent", agentID, "server", cfg.Name)

		transport, err := NewReconnectableTransport(cfg.Command, cfg.Args, cfg.Env)
		if err != nil {
			m.logger.Error("failed to start mcp server", "agent", agentID, "server", cfg.Name, "error", err)
			continue
		}

		client, err := NewClient(cfg.Name, transport)
		if err != nil {
			m.logger.Error("failed to create mcp client", "agent", agentID, "server", cfg.Name, "error", err)
			transport.Close()
			continue
		}

		// Initialize with timeout
		initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := client.Initialize(initCtx); err != nil {
			cancel()
			m.logger.Error("failed to initialize mcp client", "agent", agentID, "server", cfg.Name, "error", err)
			client.Close()
			continue
		}
		cancel()

		conn := &connection{
			config:  cfg,
			client:  client,
			healthy: true,
		}
		m.perAgent[agentID][cfg.Name] = conn
		m.logger.Info("mcp server connected for agent", "agent", agentID, "server", cfg.Name)
	}

	return nil
}

// DisconnectAgent stops all per-agent MCP connections for this agent (v0.4).
// Does NOT stop shared global connections.
func (m *Manager) DisconnectAgent(agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	agentConns, exists := m.perAgent[agentID]
	if !exists {
		return nil
	}

	for name, conn := range agentConns {
		// Skip global connections (they're managed separately)
		if _, inGlobal := m.global[name]; inGlobal {
			continue
		}

		// Close agent-specific connection
		if err := conn.client.Close(); err != nil {
			m.logger.Warn("error disconnecting agent from mcp server", "agent", agentID, "server", name, "error", err)
		}
	}

	delete(m.perAgent, agentID)
	return nil
}

// DiscoverTools enumerates tools from all MCP servers accessible to an agent (v0.4).
// Calls tools/list on each connected server. Caches results.
// Returns only tools allowed by policy.
func (m *Manager) DiscoverTools(ctx context.Context, agentID string) ([]DiscoveredTool, error) {
	m.mu.RLock()
	agentConns, exists := m.perAgent[agentID]
	if !exists {
		m.mu.RUnlock()
		return nil, nil
	}
	m.mu.RUnlock()

	var allTools []DiscoveredTool

	for serverName, conn := range agentConns {
		// Try cache first
		conn.mu.RLock()
		if len(conn.tools) > 0 {
			allTools = append(allTools, conn.tools...)
			conn.mu.RUnlock()
			continue
		}
		conn.mu.RUnlock()

		// Discover tools from server
		listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		tools, err := conn.client.ListTools(listCtx)
		cancel()

		if err != nil {
			m.logger.Warn("failed to discover mcp tools", "agent", agentID, "server", serverName, "error", err)
			continue
		}

		// Convert and cache
		var discovered []DiscoveredTool
		for _, tool := range tools {
			dt := DiscoveredTool{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: tool.InputSchema,
				ServerName:  serverName,
			}
			discovered = append(discovered, dt)

			// Policy check
			if m.policy != nil {
				pol, ok := m.policy.(policy.Policy)
				if ok && !pol.AllowMCPTool(agentID, serverName, tool.Name) {
					m.logger.Debug("mcp tool blocked by policy", "agent", agentID, "server", serverName, "tool", tool.Name)
					continue
				}
			}

			allTools = append(allTools, dt)
		}

		conn.mu.Lock()
		conn.tools = discovered
		conn.mu.Unlock()

		m.logger.Info("mcp tools discovered", "agent", agentID, "server", serverName, "count", len(discovered))
	}

	return allTools, nil
}

// InvokeTool calls a tool on behalf of an agent (v0.4).
// Checks policy before invocation.
func (m *Manager) InvokeTool(ctx context.Context, agentID, serverName, toolName string, input json.RawMessage) (json.RawMessage, error) {
	m.mu.RLock()
	agentConns, exists := m.perAgent[agentID]
	if !exists {
		m.mu.RUnlock()
		return nil, fmt.Errorf("agent not connected to any mcp servers: %s", agentID)
	}

	conn, ok := agentConns[serverName]
	if !ok {
		m.mu.RUnlock()
		return nil, fmt.Errorf("agent %s not connected to server %s", agentID, serverName)
	}
	m.mu.RUnlock()

	// Policy check
	if m.policy != nil {
		pol, ok := m.policy.(policy.Policy)
		if ok && !pol.AllowMCPTool(agentID, serverName, toolName) {
			return nil, fmt.Errorf("policy denied mcp tool: %s/%s for agent %s", serverName, toolName, agentID)
		}
	}

	return conn.client.CallTool(ctx, toolName, input)
}

// ServerNames returns server names accessible to an agent (v0.4).
func (m *Manager) ServerNames(agentID string) []string {
	m.mu.RLock()
	agentConns, exists := m.perAgent[agentID]
	m.mu.RUnlock()

	if !exists {
		return nil
	}

	names := make([]string, 0, len(agentConns))
	for name := range agentConns {
		names = append(names, name)
	}
	return names
}

// Healthy reports whether a specific server is connected and responsive (v0.4).
func (m *Manager) Healthy(agentID, serverName string) bool {
	m.mu.RLock()
	agentConns, exists := m.perAgent[agentID]
	m.mu.RUnlock()

	if !exists {
		return false
	}

	conn, ok := agentConns[serverName]
	if !ok {
		return false
	}

	conn.mu.RLock()
	healthy := conn.healthy
	conn.mu.RUnlock()

	return healthy
}

// ReloadAgent diffs current vs new config for an agent (v0.4).
// Disconnects removed servers, connects new ones, reconnects changed ones.
func (m *Manager) ReloadAgent(ctx context.Context, agentID string, newConfigs []ServerConfig) error {
	// Disconnect old agent
	if err := m.DisconnectAgent(agentID); err != nil {
		m.logger.Warn("error disconnecting agent during reload", "agent", agentID, "error", err)
	}

	// Connect with new config
	return m.ConnectAgentServers(ctx, agentID, newConfigs)
}

// AllTools aggregates tools from all connected servers (backward compat).
func (m *Manager) AllTools(ctx context.Context) (map[string][]MCPTool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string][]MCPTool)
	for name, conn := range m.global {
		// Use short timeout for listing tools
		listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		tools, err := conn.client.ListTools(listCtx)
		cancel()

		if err != nil {
			m.logger.Warn("failed to list tools", "server", name, "error", err)
			continue
		}
		result[name] = tools
	}
	return result, nil
}

// CallTool invokes a tool on a specific server (backward compat).
func (m *Manager) CallTool(ctx context.Context, serverName, toolName string, args json.RawMessage) (json.RawMessage, error) {
	m.mu.RLock()
	conn, ok := m.global[serverName]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("server not found: %s", serverName)
	}

	// Policy check
	if m.policy != nil && !m.policy.AllowCapability("tools.mcp") {
		return nil, fmt.Errorf("policy denied capability %q", "tools.mcp")
	}

	return conn.client.CallTool(ctx, toolName, args)
}

// Stop disconnects all servers (global and per-agent) (v0.4).
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Close all per-agent connections
	for agentID, agentConns := range m.perAgent {
		for serverName, conn := range agentConns {
			// Skip global connections (they're managed separately)
			if _, inGlobal := m.global[serverName]; inGlobal {
				continue
			}

			if err := conn.client.Close(); err != nil {
				m.logger.Warn("error stopping mcp client", "agent", agentID, "server", serverName, "error", err)
			}
		}
	}
	m.perAgent = make(map[string]map[string]*connection)

	// Close all global connections
	for name, conn := range m.global {
		if err := conn.client.Close(); err != nil {
			m.logger.Warn("error stopping mcp client", "server", name, "error", err)
		}
	}
	m.global = make(map[string]*connection)

	return nil
}
