package coordinator

import (
	"fmt"

	"github.com/basket/go-claw/internal/config"
)

// LoadPlansFromConfig converts config plan definitions into validated Plan objects.
// GC-SPEC-PDR-v4-Phase-4: Load plans from configuration.
func LoadPlansFromConfig(configs []config.PlanConfig, knownAgents []string) (map[string]Plan, error) {
	plans := make(map[string]Plan)
	agentSet := make(map[string]bool)
	for _, a := range knownAgents {
		agentSet[a] = true
	}

	for _, pc := range configs {
		if pc.Name == "" {
			return nil, fmt.Errorf("plan has empty name")
		}
		if _, exists := plans[pc.Name]; exists {
			return nil, fmt.Errorf("duplicate plan name: %s", pc.Name)
		}

		plan := Plan{
			Name:  pc.Name,
			Steps: make([]PlanStep, len(pc.Steps)),
		}

		for i, sc := range pc.Steps {
			// Validate agent exists
			if !agentSet[sc.AgentID] {
				return nil, fmt.Errorf("plan %s step %s: unknown agent %s", pc.Name, sc.ID, sc.AgentID)
			}

			plan.Steps[i] = PlanStep{
				ID:        sc.ID,
				AgentID:   sc.AgentID,
				Prompt:    sc.Prompt,
				DependsOn: sc.DependsOn,
			}
		}

		if err := plan.Validate(); err != nil {
			return nil, fmt.Errorf("plan %s: %w", pc.Name, err)
		}

		plans[pc.Name] = plan
	}

	return plans, nil
}
