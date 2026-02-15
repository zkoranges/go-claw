package coordinator

import (
	"testing"

	"github.com/basket/go-claw/internal/config"
)

// GC-SPEC-PDR-v4-Phase-4: Test loading valid plans from config.
func TestLoadPlansFromConfig_Valid(t *testing.T) {
	configs := []config.PlanConfig{
		{
			Name: "test-pipeline",
			Steps: []config.PlanStepConfig{
				{ID: "research", AgentID: "researcher", Prompt: "do research"},
				{ID: "write", AgentID: "writer", Prompt: "{research.output}", DependsOn: []string{"research"}},
			},
		},
	}
	plans, err := LoadPlansFromConfig(configs, []string{"researcher", "writer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}
	if _, ok := plans["test-pipeline"]; !ok {
		t.Fatal("plan test-pipeline not found")
	}
}

// GC-SPEC-PDR-v4-Phase-4: Test loading with unknown agent.
func TestLoadPlansFromConfig_UnknownAgent(t *testing.T) {
	configs := []config.PlanConfig{
		{
			Name: "bad-plan",
			Steps: []config.PlanStepConfig{
				{ID: "step1", AgentID: "nonexistent", Prompt: "hello"},
			},
		},
	}
	_, err := LoadPlansFromConfig(configs, []string{"researcher"})
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

// GC-SPEC-PDR-v4-Phase-4: Test loading with duplicate plan names.
func TestLoadPlansFromConfig_DuplicateName(t *testing.T) {
	configs := []config.PlanConfig{
		{Name: "pipeline", Steps: []config.PlanStepConfig{{ID: "a", AgentID: "x", Prompt: "1"}}},
		{Name: "pipeline", Steps: []config.PlanStepConfig{{ID: "b", AgentID: "y", Prompt: "2"}}},
	}
	_, err := LoadPlansFromConfig(configs, []string{"x", "y"})
	if err == nil {
		t.Fatal("expected error for duplicate plan name")
	}
}

// GC-SPEC-PDR-v4-Phase-4: Test loading with invalid plan (cycle).
func TestLoadPlansFromConfig_Cycle(t *testing.T) {
	configs := []config.PlanConfig{
		{
			Name: "bad-plan",
			Steps: []config.PlanStepConfig{
				{ID: "a", AgentID: "x", Prompt: "1", DependsOn: []string{"b"}},
				{ID: "b", AgentID: "y", Prompt: "2", DependsOn: []string{"a"}},
			},
		},
	}
	_, err := LoadPlansFromConfig(configs, []string{"x", "y"})
	if err == nil {
		t.Fatal("expected error for cycle")
	}
}
