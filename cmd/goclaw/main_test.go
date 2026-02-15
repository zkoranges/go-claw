package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basket/go-claw/internal/config"
	"github.com/basket/go-claw/internal/coordinator"
	"github.com/basket/go-claw/internal/gateway"
)

func TestParseDaemonSubcommandArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    daemonSubcommandMode
		wantErr bool
	}{
		{name: "no args means run", args: nil, want: daemonSubcommandRun},
		{name: "double dash help", args: []string{"--help"}, want: daemonSubcommandHelp},
		{name: "single dash help", args: []string{"-h"}, want: daemonSubcommandHelp},
		{name: "help token", args: []string{"help"}, want: daemonSubcommandHelp},
		{name: "unexpected arg", args: []string{"extra"}, want: daemonSubcommandRun, wantErr: true},
		{name: "too many args", args: []string{"--help", "extra"}, want: daemonSubcommandRun, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDaemonSubcommandArgs(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("mode mismatch: got %v want %v", got, tt.want)
			}
		})
	}
}

func TestPrintDaemonSubcommandUsage(t *testing.T) {
	var buf bytes.Buffer
	printDaemonSubcommandUsage(&buf)
	out := buf.String()

	if !strings.Contains(out, "usage: goclaw daemon [--help]") {
		t.Fatalf("usage output missing daemon subcommand usage: %q", out)
	}
	if !strings.Contains(out, "goclaw -daemon") {
		t.Fatalf("usage output missing flag usage: %q", out)
	}
}

// TestMain_PlanLoading verifies plan loading from config.yaml during daemon startup.
// GC-SPEC-PDR-v4-Phase-4: Plan system integration test.
func TestMain_PlanLoading(t *testing.T) {
	// Create temporary GOCLAW_HOME with config.yaml containing a test plan.
	tmpDir := t.TempDir()
	configYAML := `
worker_count: 4
plans:
  - name: test-plan
    steps:
      - id: step1
        agent_id: default
        prompt: "echo hello"
  - name: multi-step
    steps:
      - id: research
        agent_id: default
        prompt: "research topic"
      - id: write
        agent_id: default
        prompt: "write about {research.output}"
        depends_on:
          - research
`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.yaml"), []byte(configYAML), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}

	// Set GOCLAW_HOME to temp dir and restore after test.
	prev := os.Getenv("GOCLAW_HOME")
	os.Setenv("GOCLAW_HOME", tmpDir)
	t.Cleanup(func() {
		if prev == "" {
			os.Unsetenv("GOCLAW_HOME")
		} else {
			os.Setenv("GOCLAW_HOME", prev)
		}
	})

	// Load config (mirrors main.go startup path).
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	if len(cfg.Plans) != 2 {
		t.Fatalf("expected 2 plans in config, got %d", len(cfg.Plans))
	}

	// Load plans using the same path as main.go.
	// "default" is the known agent for these plans.
	knownAgents := []string{"default"}
	plans, err := coordinator.LoadPlansFromConfig(cfg.Plans, knownAgents)
	if err != nil {
		t.Fatalf("LoadPlansFromConfig: %v", err)
	}

	// Build summaries the same way main.go does.
	planSummaries := make(map[string]gateway.PlanSummary)
	plansMap := make(map[string]*coordinator.Plan)
	for name, p := range plans {
		planCopy := p
		plansMap[name] = &planCopy

		agents := make(map[string]bool)
		for _, s := range p.Steps {
			agents[s.AgentID] = true
		}
		agentList := make([]string, 0, len(agents))
		for a := range agents {
			agentList = append(agentList, a)
		}
		planSummaries[name] = gateway.PlanSummary{
			Name:      name,
			StepCount: len(p.Steps),
			AgentIDs:  agentList,
		}
	}

	// Verify test-plan exists with correct structure.
	if _, ok := planSummaries["test-plan"]; !ok {
		t.Fatal("planSummaries missing test-plan")
	}
	if planSummaries["test-plan"].StepCount != 1 {
		t.Fatalf("test-plan step count: got %d, want 1", planSummaries["test-plan"].StepCount)
	}

	if p, ok := plansMap["test-plan"]; !ok {
		t.Fatal("plansMap missing test-plan")
	} else {
		if len(p.Steps) != 1 {
			t.Fatalf("test-plan steps: got %d, want 1", len(p.Steps))
		}
		if p.Steps[0].Prompt != "echo hello" {
			t.Fatalf("test-plan step prompt: got %q, want %q", p.Steps[0].Prompt, "echo hello")
		}
	}

	// Verify multi-step plan.
	if _, ok := planSummaries["multi-step"]; !ok {
		t.Fatal("planSummaries missing multi-step")
	}
	if planSummaries["multi-step"].StepCount != 2 {
		t.Fatalf("multi-step step count: got %d, want 2", planSummaries["multi-step"].StepCount)
	}

	if p, ok := plansMap["multi-step"]; !ok {
		t.Fatal("plansMap missing multi-step")
	} else {
		if len(p.Steps) != 2 {
			t.Fatalf("multi-step steps: got %d, want 2", len(p.Steps))
		}
		// Verify dependency is preserved.
		var writeStep *coordinator.PlanStep
		for i := range p.Steps {
			if p.Steps[i].ID == "write" {
				writeStep = &p.Steps[i]
				break
			}
		}
		if writeStep == nil {
			t.Fatal("multi-step missing write step")
		}
		if len(writeStep.DependsOn) != 1 || writeStep.DependsOn[0] != "research" {
			t.Fatalf("write step depends_on: got %v, want [research]", writeStep.DependsOn)
		}
	}
}
