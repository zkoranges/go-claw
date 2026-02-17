package shared

import (
	"context"

	"github.com/google/uuid"
)

type traceKey struct{}
type agentIDKey struct{}
type taskIDKey struct{}
type sessionIDKey struct{}
type runIDKey struct{}
type delegationHopKey struct{}
type messageDepthKey struct{}

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

// WithTaskID attaches a task_id to the context.
func WithTaskID(ctx context.Context, taskID string) context.Context {
	return context.WithValue(ctx, taskIDKey{}, taskID)
}

// TaskID extracts task_id from context. Returns "" if absent.
func TaskID(ctx context.Context) string {
	if v, ok := ctx.Value(taskIDKey{}).(string); ok {
		return v
	}
	return ""
}

// WithSessionID attaches a session_id to the context.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDKey{}, sessionID)
}

// SessionID extracts session_id from context. Returns "" if absent.
func SessionID(ctx context.Context) string {
	if v, ok := ctx.Value(sessionIDKey{}).(string); ok {
		return v
	}
	return ""
}

// WithRunID attaches a run_id to the context.
func WithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runIDKey{}, runID)
}

// RunID extracts run_id from context. Returns "" if absent.
func RunID(ctx context.Context) string {
	if v, ok := ctx.Value(runIDKey{}).(string); ok {
		return v
	}
	return ""
}

// NewRunID generates a new run_id.
func NewRunID() string {
	return uuid.NewString()
}

// WithDelegationHop attaches hop count to context.
func WithDelegationHop(ctx context.Context, hop int) context.Context {
	return context.WithValue(ctx, delegationHopKey{}, hop)
}

// DelegationHop extracts hop count (0 if absent).
func DelegationHop(ctx context.Context) int {
	if v, ok := ctx.Value(delegationHopKey{}).(int); ok {
		return v
	}
	return 0
}

// WithMessageDepth attaches inter-agent message depth to context.
func WithMessageDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, messageDepthKey{}, depth)
}

// MessageDepth extracts inter-agent message depth (0 if absent).
func MessageDepth(ctx context.Context) int {
	if v, ok := ctx.Value(messageDepthKey{}).(int); ok {
		return v
	}
	return 0
}

const DefaultAgentID = "default"
