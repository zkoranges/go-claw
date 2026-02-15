package config

import (
	"os"
	"testing"
)

func TestStarterAgents_Count(t *testing.T) {
	agents := StarterAgents()
	if len(agents) != 3 {
		t.Fatalf("expected 3 starter agents, got %d", len(agents))
	}
}

func TestStarterAgents_ExpectedIDs(t *testing.T) {
	agents := StarterAgents()
	expected := map[string]bool{"coder": true, "researcher": true, "writer": true}
	for _, a := range agents {
		if !expected[a.AgentID] {
			t.Errorf("unexpected agent ID: %q", a.AgentID)
		}
		delete(expected, a.AgentID)
	}
	for missing := range expected {
		t.Errorf("missing expected agent: %q", missing)
	}
}

func TestStarterAgents_FieldsNonEmpty(t *testing.T) {
	for _, a := range StarterAgents() {
		if a.AgentID == "" {
			t.Error("agent has empty AgentID")
		}
		if a.DisplayName == "" {
			t.Errorf("agent %s: empty DisplayName", a.AgentID)
		}
		if a.Soul == "" {
			t.Errorf("agent %s: empty Soul", a.AgentID)
		}
		if len(a.Capabilities) == 0 {
			t.Errorf("agent %s: no capabilities", a.AgentID)
		}
	}
}

func TestStarterAgents_UniqueIDs(t *testing.T) {
	seen := make(map[string]bool)
	for _, a := range StarterAgents() {
		if seen[a.AgentID] {
			t.Errorf("duplicate agent ID: %q", a.AgentID)
		}
		seen[a.AgentID] = true
	}
}

func TestLoad_PopulatesStarterAgentsWhenEmpty(t *testing.T) {
	// Setup isolated home directory
	tmpDir := t.TempDir()
	oldGoclawHome := os.Getenv("GOCLAW_HOME")
	defer os.Setenv("GOCLAW_HOME", oldGoclawHome)
	os.Setenv("GOCLAW_HOME", tmpDir)

	// Load config (should populate with starters since config.yaml doesn't exist)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Verify starter agents were populated
	if len(cfg.Agents) != 3 {
		t.Fatalf("expected 3 starter agents, got %d", len(cfg.Agents))
	}

	// Check IDs
	agentIDs := map[string]bool{}
	for _, a := range cfg.Agents {
		agentIDs[a.AgentID] = true
	}

	expected := []string{"coder", "researcher", "writer"}
	for _, id := range expected {
		if !agentIDs[id] {
			t.Errorf("missing starter agent: %s", id)
		}
	}
}
