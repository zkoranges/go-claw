package shared

import (
	"context"

	"github.com/google/uuid"
)

type traceKey struct{}
type agentIDKey struct{}

// WithTraceID attaches a trace_id to the context.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceKey{}, traceID)
}

// WithAgentID attaches an agent_id to the context.
func WithAgentID(ctx context.Context, agentID string) context.Context {
	return context.WithValue(ctx, agentIDKey{}, agentID)
}

// AgentID extracts agent_id from context. Returns "" if absent.
func AgentID(ctx context.Context) string {
	if v, ok := ctx.Value(agentIDKey{}).(string); ok {
		return v
	}
	return ""
}

// TraceID extracts trace_id from context. Returns "-" if absent.
func TraceID(ctx context.Context) string {
	if v, ok := ctx.Value(traceKey{}).(string); ok && v != "" {
		return v
	}
	return "-"
}

// NewTraceID generates a new trace_id.
func NewTraceID() string {
	return uuid.NewString()
}
