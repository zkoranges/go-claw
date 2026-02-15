package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/basket/go-claw/internal/config"
	"gopkg.in/yaml.v3"
)

type PullableAgent struct {
	ID           string   `yaml:"id"`
	DisplayName  string   `yaml:"display_name"`
	Soul         string   `yaml:"soul"`
	Model        string   `yaml:"model"`
	Provider     string   `yaml:"provider"`
	Capabilities []string `yaml:"capabilities"`
}

func runPullCommand(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, `Usage: goclaw pull <url>

Fetches an agent configuration from a URL and adds it to config.yaml.

Examples:
  goclaw pull https://gist.githubusercontent.com/user/abc/raw/agent.yaml
  goclaw pull https://raw.githubusercontent.com/user/repo/main/agents/sre.yaml

Agent YAML format (minimum required: id and soul):
  id: my-agent
  soul: |
    You are a helpful agent.
  display_name: My Agent     # optional
  model: gemini-2.5-pro      # optional
  capabilities: [coding]     # optional`)
		return 1
	}

	url := args[0]
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		fmt.Fprintln(os.Stderr, "Error: URL must start with http:// or https://")
		return 1
	}

	fmt.Printf("Fetching %s...\n", url)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to fetch: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: server returned %d %s\n", resp.StatusCode, http.StatusText(resp.StatusCode))
		return 1
	}

	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/html") {
		fmt.Fprintln(os.Stderr, "Error: URL returned HTML, not YAML. If using GitHub, use the 'Raw' URL.")
		fmt.Fprintln(os.Stderr, "  Tip: https://raw.githubusercontent.com/user/repo/main/file.yaml")
		return 1
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to read response: %v\n", err)
		return 1
	}

	var agent PullableAgent
	if err := yaml.Unmarshal(body, &agent); err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid YAML: %v\n", err)
		return 1
	}

	if agent.ID == "" {
		fmt.Fprintln(os.Stderr, "Error: agent config missing required 'id' field")
		return 1
	}
	if agent.Soul == "" {
		fmt.Fprintln(os.Stderr, "Error: agent config missing required 'soul' field")
		return 1
	}

	// Map PullableAgent to AgentConfigEntry
	entry := config.AgentConfigEntry{
		AgentID:      agent.ID,
		DisplayName:  agent.DisplayName,
		Soul:         agent.Soul,
		Capabilities: agent.Capabilities,
	}
	if entry.DisplayName == "" {
		entry.DisplayName = agent.ID
	}

	homeDir := os.Getenv("GOCLAW_HOME")
	if homeDir == "" {
		home, _ := os.UserHomeDir()
		homeDir = filepath.Join(home, ".goclaw")
	}
	configPath := filepath.Join(homeDir, "config.yaml")

	if err := config.AppendAgent(configPath, entry); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	fmt.Printf("✓ Installed agent @%s\n", agent.ID)
	fmt.Printf("  Source: %s\n", url)
	preview := agent.Soul
	if len(preview) > 80 {
		preview = preview[:80] + "..."
	}
	fmt.Printf("  Soul: %s\n", preview)
	fmt.Println()
	fmt.Printf("Restart GoClaw or use @%s in the TUI to start chatting.\n", agent.ID)
	return 0
}
