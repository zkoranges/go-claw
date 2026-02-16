package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseAgentMCPServers verifies that agents can specify per-agent MCP servers.
func TestParseAgentMCPServers(t *testing.T) {
	yaml := `
worker_count: 4
agents:
  - agent_id: researcher
    model: gemini-2.5-pro
    provider: google
    mcp_servers:
      - name: github
      - name: filesystem
  - agent_id: writer
    model: gemini-2.5-pro
    provider: google
    mcp_servers: []
mcp:
  servers:
    - name: github
      command: npx
      args: ["-y", "@modelcontextprotocol/server-github"]
      enabled: true
    - name: filesystem
      command: npx
      args: ["-y", "@modelcontextprotocol/server-filesystem"]
      enabled: true
`
	home := filepath.Join(t.TempDir(), "home")
	goclawHome := filepath.Join(home, ".goclaw")
	if err := os.MkdirAll(goclawHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goclawHome, "config.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOCLAW_HOME", goclawHome)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify agent configs were parsed
	if len(cfg.Agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(cfg.Agents))
	}

	// Researcher should have 2 MCP servers
	researcher := cfg.Agents[0]
	if researcher.AgentID != "researcher" {
		t.Errorf("expected agent_id=researcher, got %s", researcher.AgentID)
	}
	if len(researcher.MCPServers) != 2 {
		t.Errorf("researcher: expected 2 mcp_servers, got %d", len(researcher.MCPServers))
	}
	if researcher.MCPServers[0].Name != "github" {
		t.Errorf("researcher first server: expected name=github, got %s", researcher.MCPServers[0].Name)
	}

	// Writer should have 0 MCP servers (explicit empty list)
	writer := cfg.Agents[1]
	if writer.AgentID != "writer" {
		t.Errorf("expected agent_id=writer, got %s", writer.AgentID)
	}
	if len(writer.MCPServers) != 0 {
		t.Errorf("writer: expected 0 mcp_servers, got %d", len(writer.MCPServers))
	}
}

// TestInlineMCPServerDefinition verifies that agents can define inline MCP servers.
func TestInlineMCPServerDefinition(t *testing.T) {
	yaml := `
worker_count: 4
agents:
  - agent_id: devops
    model: gemini-2.5-pro
    provider: google
    mcp_servers:
      - name: postgres
        transport: sse
        url: "http://localhost:3001/sse"
        timeout: "30s"
`
	home := filepath.Join(t.TempDir(), "home")
	goclawHome := filepath.Join(home, ".goclaw")
	if err := os.MkdirAll(goclawHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goclawHome, "config.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOCLAW_HOME", goclawHome)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	agent := cfg.Agents[0]
	if len(agent.MCPServers) != 1 {
		t.Errorf("expected 1 mcp_server, got %d", len(agent.MCPServers))
	}

	server := agent.MCPServers[0]
	if server.Name != "postgres" {
		t.Errorf("expected name=postgres, got %s", server.Name)
	}
	if server.Transport != "sse" {
		t.Errorf("expected transport=sse, got %s", server.Transport)
	}
	if server.URL != "http://localhost:3001/sse" {
		t.Errorf("expected url=http://localhost:3001/sse, got %s", server.URL)
	}
	if server.Timeout != "30s" {
		t.Errorf("expected timeout=30s, got %s", server.Timeout)
	}
}

// TestMCPServerEnvMapSupport verifies that MCP servers support env as a map.
func TestMCPServerEnvMapSupport(t *testing.T) {
	yaml := `
worker_count: 4
mcp:
  servers:
    - name: github
      command: npx
      args: ["-y", "@modelcontextprotocol/server-github"]
      env:
        GITHUB_TOKEN: "my-token"
        GITHUB_OWNER: "myorg"
      enabled: true
`
	home := filepath.Join(t.TempDir(), "home")
	goclawHome := filepath.Join(home, ".goclaw")
	if err := os.MkdirAll(goclawHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goclawHome, "config.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOCLAW_HOME", goclawHome)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	server := cfg.MCP.Servers[0]
	if server.Env["GITHUB_TOKEN"] != "my-token" {
		t.Errorf("expected GITHUB_TOKEN=my-token, got %s", server.Env["GITHUB_TOKEN"])
	}
	if server.Env["GITHUB_OWNER"] != "myorg" {
		t.Errorf("expected GITHUB_OWNER=myorg, got %s", server.Env["GITHUB_OWNER"])
	}
}
