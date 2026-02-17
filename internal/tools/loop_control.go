package tools

import (
	"github.com/basket/go-claw/internal/bus"
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
)

// CheckpointInput is the input for the checkpoint_now tool.
type CheckpointInput struct {
	// Reason is an optional reason for triggering a checkpoint.
	Reason string `json:"reason,omitempty"`
}

// CheckpointOutput is the output for the checkpoint_now tool.
type CheckpointOutput struct {
	// Saved indicates the checkpoint request was acknowledged.
	Saved bool `json:"saved"`
}

// LoopStatusInput is the input for the set_loop_status tool.
type LoopStatusInput struct {
	// Status is the status message to report to users.
	Status string `json:"status"`
}

// LoopStatusOutput is the output for the set_loop_status tool.
type LoopStatusOutput struct {
	// Updated indicates the status was published.
	Updated bool `json:"updated"`
}

// RegisterLoopControlTools registers loop management tools with the Genkit instance.
// These are registered for agents with loop.enabled=true.
func RegisterLoopControlTools(g *genkit.Genkit, reg *Registry, eventBus *bus.Bus) []ai.ToolRef {
	checkpointTool := genkit.DefineTool(g, "checkpoint_now",
		"Force an immediate checkpoint of the current loop state. Use before risky operations.",
		func(ctx *ai.ToolContext, input CheckpointInput) (CheckpointOutput, error) {
			reg.publishToolCall(ctx, "checkpoint_now")
			// The engine intercepts this tool call to trigger a checkpoint save.
			// The tool itself simply acknowledges the request.
			return CheckpointOutput{Saved: true}, nil
		},
	)

	statusTool := genkit.DefineTool(g, "set_loop_status",
		"Update the loop's status message visible to users. Use to report progress.",
		func(ctx *ai.ToolContext, input LoopStatusInput) (LoopStatusOutput, error) {
			reg.publishToolCall(ctx, "set_loop_status")
			if eventBus != nil {
				eventBus.Publish("loop.status_update", map[string]string{
					"status": input.Status,
				})
			}
			return LoopStatusOutput{Updated: true}, nil
		},
	)

	return []ai.ToolRef{checkpointTool, statusTool}
}
