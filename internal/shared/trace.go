package shared

import (
	"context"

	"github.com/google/uuid"
)

type traceKey struct{}

// WithTraceID attaches a trace_id to the context.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceKey{}, traceID)
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
