package otel

import "go.opentelemetry.io/otel/metric"

// Metrics holds all GoClaw metrics instruments.
type Metrics struct {
	RequestDuration  metric.Float64Histogram
	TaskDuration     metric.Float64Histogram
	LLMCallDuration  metric.Float64Histogram
	TokensUsed       metric.Int64Counter
	ToolCallDuration metric.Float64Histogram
	ToolCallErrors   metric.Int64Counter
	ActiveLoops      metric.Int64UpDownCounter
	LoopStepsTotal   metric.Int64Counter
	StreamTokens     metric.Int64Counter
	RateLimitRejects metric.Int64Counter
}

// NewMetrics creates all metric instruments from the given meter.
func NewMetrics(meter metric.Meter) (*Metrics, error) {
	m := &Metrics{}
	var err error

	m.RequestDuration, err = meter.Float64Histogram("goclaw.request.duration",
		metric.WithDescription("Gateway request duration in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	m.TaskDuration, err = meter.Float64Histogram("goclaw.task.duration",
		metric.WithDescription("Task processing duration in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	m.LLMCallDuration, err = meter.Float64Histogram("goclaw.llm.duration",
		metric.WithDescription("LLM API call duration in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	m.TokensUsed, err = meter.Int64Counter("goclaw.llm.tokens",
		metric.WithDescription("Total tokens consumed"),
	)
	if err != nil {
		return nil, err
	}

	m.ToolCallDuration, err = meter.Float64Histogram("goclaw.tool.duration",
		metric.WithDescription("Tool call duration in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	m.ToolCallErrors, err = meter.Int64Counter("goclaw.tool.errors",
		metric.WithDescription("Tool call error count"),
	)
	if err != nil {
		return nil, err
	}

	m.ActiveLoops, err = meter.Int64UpDownCounter("goclaw.loop.active",
		metric.WithDescription("Number of currently active agent loops"),
	)
	if err != nil {
		return nil, err
	}

	m.LoopStepsTotal, err = meter.Int64Counter("goclaw.loop.steps",
		metric.WithDescription("Total loop steps executed"),
	)
	if err != nil {
		return nil, err
	}

	m.StreamTokens, err = meter.Int64Counter("goclaw.stream.tokens",
		metric.WithDescription("Total streaming tokens delivered"),
	)
	if err != nil {
		return nil, err
	}

	m.RateLimitRejects, err = meter.Int64Counter("goclaw.ratelimit.rejects",
		metric.WithDescription("Requests rejected by rate limiter"),
	)
	if err != nil {
		return nil, err
	}

	return m, nil
}
