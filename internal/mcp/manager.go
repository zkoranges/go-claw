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
	Name    string            `yaml:"name"`
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env"`
	Enabled bool              `yaml:"enabled"`
}

type serverHealth struct {
	healthy   bool
	lastCheck time.Time
}

// Manager manages multiple MCP clients.
type Manager struct {
	configs []ServerConfig
	clients map[string]*Client
	policy  policy.Checker
	logger  *slog.Logger
	mu      sync.RWMutex
	health  map[string]*serverHealth
}

func NewManager(configs []ServerConfig, pol policy.Checker, logger *slog.Logger) *Manager {
	return &Manager{
		configs: configs,
		clients: make(map[string]*Client),
		policy:  pol,
		logger:  logger,
		health:  make(map[string]*serverHealth),
	}
}

// Start starts all enabled MCP servers and initializes them.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, cfg := range m.configs {
		if !cfg.Enabled {
			continue
		}

		m.logger.Info("starting mcp server", "name", cfg.Name, "command", cfg.Command)

		transport, err := NewReconnectableTransport(cfg.Command, cfg.Args, cfg.Env)
		if err != nil {
			m.logger.Error("failed to start mcp server", "name", cfg.Name, "error", err)
			continue
		}

		client, err := NewClient(cfg.Name, transport)
		if err != nil {
			m.logger.Error("failed to create mcp client", "name", cfg.Name, "error", err)
			transport.Close()
			continue
		}

		// Initialize with timeout
		initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := client.Initialize(initCtx); err != nil {
			cancel()
			m.logger.Error("failed to initialize mcp client", "name", cfg.Name, "error", err)
			client.Close()
			continue
		}
		cancel()

		m.clients[cfg.Name] = client
		m.health[cfg.Name] = &serverHealth{healthy: true, lastCheck: time.Now()}
		m.logger.Info("mcp server initialized", "name", cfg.Name)
	}

	return nil
}

// AllTools aggregates tools from all connected servers.
// Returns a map of serverName -> []MCPTool.
func (m *Manager) AllTools(ctx context.Context) (map[string][]MCPTool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string][]MCPTool)
	for name, client := range m.clients {
		// Use short timeout for listing tools
		listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		tools, err := client.ListTools(listCtx)
		cancel()

		if err != nil {
			m.logger.Warn("failed to list tools", "server", name, "error", err)
			continue
		}
		result[name] = tools
	}
	return result, nil
}

// CallTool invokes a tool on a specific server.
func (m *Manager) CallTool(ctx context.Context, serverName, toolName string, args json.RawMessage) (json.RawMessage, error) {
	m.mu.RLock()
	client, ok := m.clients[serverName]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("server not found: %s", serverName)
	}

	// Policy check
	if m.policy != nil && !m.policy.AllowCapability("tools.mcp") {
		return nil, fmt.Errorf("policy denied capability %q", "tools.mcp")
	}

	return client.CallTool(ctx, toolName, args)
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, client := range m.clients {
		if err := client.Close(); err != nil {
			m.logger.Warn("error stopping mcp client", "server", name, "error", err)
		}
	}
	m.clients = make(map[string]*Client)
	return nil
}
