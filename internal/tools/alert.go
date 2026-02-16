package tools

import (
	"fmt"

	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/policy"
	"github.com/basket/go-claw/internal/shared"
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
)

const (
	capSendAlert = "tools.send_alert"
)

// AlertInput is the input for the send_alert tool.
type AlertInput struct {
	// Severity is the alert severity level: "info", "warning", or "critical".
	Severity string `json:"severity"`
	// Title is a short descriptive title for the alert.
	Title string `json:"title"`
	// Body is the detailed message content.
	Body string `json:"body"`
}

// AlertOutput is the output for the send_alert tool.
type AlertOutput struct {
	// Delivered indicates if the alert was successfully published.
	Delivered bool `json:"delivered"`
	// MessageID is an optional identifier for the alert message.
	MessageID string `json:"message_id,omitempty"`
}

// sendAlert publishes an alert event to the event bus.
func sendAlert(ctx *ai.ToolContext, input AlertInput, b *bus.Bus, pol policy.Checker) (AlertOutput, error) {
	// Policy check.
	if pol == nil || !pol.AllowCapability(capSendAlert) {
		pv := ""
		if pol != nil {
			pv = pol.PolicyVersion()
		}
		audit.Record("deny", capSendAlert, "missing_capability", pv, "send_alert")
		return AlertOutput{}, fmt.Errorf("policy denied capability %q", capSendAlert)
	}

	pv := pol.PolicyVersion()
	audit.Record("allow", capSendAlert, "capability_granted", pv, "send_alert")

	// Validate severity.
	validSeverities := map[string]bool{
		"info":     true,
		"warning":  true,
		"critical": true,
	}

	if !validSeverities[input.Severity] {
		return AlertOutput{}, fmt.Errorf(`invalid severity "%s": must be one of "info", "warning", "critical"`, input.Severity)
	}

	// Get agent ID from context.
	agentID := shared.AgentID(ctx.Context)

	// Create and publish alert event.
	alert := bus.AgentAlert{
		Severity: input.Severity,
		Message:  input.Body,
	}

	// Only publish if bus is available.
	if b != nil {
		b.Publish(bus.TopicAgentAlert, alert)
	}

	return AlertOutput{
		Delivered: true,
		MessageID: agentID + "_alert",
	}, nil
}

// registerAlert registers the send_alert tool with Genkit.
func registerAlert(g *genkit.Genkit, reg *Registry) ai.ToolRef {
	return genkit.DefineTool(g, "send_alert",
		"Send an alert to operators with a specified severity level (info, warning, or critical). Severity must be one of the three supported levels.",
		func(ctx *ai.ToolContext, input AlertInput) (AlertOutput, error) {
			var b *bus.Bus
			if reg.Store != nil {
				b = reg.Store.Bus()
			}
			out, err := sendAlert(ctx, input, b, reg.Policy)
			if err != nil {
				return AlertOutput{}, err
			}
			return out, nil
		},
	)
}
