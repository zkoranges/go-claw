package coordinator

import (
	"fmt"
)

// Plan is a DAG of steps to be executed in dependency order.
// GC-SPEC-PDR-v4-Phase-3: Working DAG executor.
type Plan struct {
	Name  string
	Steps []PlanStep
}

// PlanStep is a single step in a DAG plan.
type PlanStep struct {
	ID         string
	AgentID    string
	Prompt     string
	DependsOn  []string // Step IDs that must complete before this step
	MaxRetries int      // Max retry count on failure (default: 2)
}

// StepResult is the outcome of a single step.
type StepResult struct {
	TaskID     string
	Status     string
	Output     string
	Error      string
	CostUSD    float64
	DurationMs int64
}

// ExecutionResult is the overall result of a plan execution.
type ExecutionResult struct {
	ExecutionID string
	StepResults map[string]StepResult
}

// TotalCost sums the cost of all steps.
func (r *ExecutionResult) TotalCost() float64 {
	var total float64
	for _, sr := range r.StepResults {
		total += sr.CostUSD
	}
	return total
}

// Validate checks that the plan is well-formed.
func (p *Plan) Validate() error {
	if len(p.Steps) == 0 {
		return fmt.Errorf("plan has no steps")
	}

	// Check unique IDs
	seen := make(map[string]bool)
	for _, s := range p.Steps {
		if s.ID == "" {
			return fmt.Errorf("step has empty ID")
		}
		if seen[s.ID] {
			return fmt.Errorf("duplicate step ID: %s", s.ID)
		}
		seen[s.ID] = true
	}

	// Check all dependencies reference existing steps
	for _, s := range p.Steps {
		for _, dep := range s.DependsOn {
			if !seen[dep] {
				return fmt.Errorf("step %s depends on nonexistent step %s", s.ID, dep)
			}
		}
	}

	// Check for cycles via topological sort (implemented in executor.go)
	_, err := topoSort(p.Steps)
	return err
}
