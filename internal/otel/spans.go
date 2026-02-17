package otel

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Standard attribute keys for GoClaw spans.
var (
	AttrAgentID      = attribute.Key("goclaw.agent.id")
	AttrTaskID       = attribute.Key("goclaw.task.id")
	AttrToolName     = attribute.Key("goclaw.tool.name")
	AttrModel        = attribute.Key("goclaw.llm.model")
	AttrTokensInput  = attribute.Key("goclaw.llm.tokens.input")
	AttrTokensOutput = attribute.Key("goclaw.llm.tokens.output")
	AttrLoopID       = attribute.Key("goclaw.loop.id")
	AttrLoopStep     = attribute.Key("goclaw.loop.step")
	AttrMCPServer    = attribute.Key("goclaw.mcp.server")
	AttrSessionID    = attribute.Key("goclaw.session.id")
)

// StartSpan is a convenience wrapper that starts an internal span with common attributes.
func StartSpan(ctx context.Context, tracer trace.Tracer, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return tracer.Start(ctx, name,
		trace.WithAttributes(attrs...),
		trace.WithSpanKind(trace.SpanKindInternal),
	)
}

// StartServerSpan starts a span for an inbound request (Gateway).
func StartServerSpan(ctx context.Context, tracer trace.Tracer, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return tracer.Start(ctx, name,
		trace.WithAttributes(attrs...),
		trace.WithSpanKind(trace.SpanKindServer),
	)
}

// StartClientSpan starts a span for an outbound call (LLM API, MCP).
func StartClientSpan(ctx context.Context, tracer trace.Tracer, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return tracer.Start(ctx, name,
		trace.WithAttributes(attrs...),
		trace.WithSpanKind(trace.SpanKindClient),
	)
}
