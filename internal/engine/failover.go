package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// KVStore is the minimal interface needed for breaker state persistence.
type KVStore interface {
	KVSet(ctx context.Context, key, val string) error
	KVGet(ctx context.Context, key string) (string, error)
}

// namedBrain pairs a Brain implementation with a human-readable provider name
// for circuit breaker tracking and logging.
type namedBrain struct {
	name  string
	brain Brain
}

// CircuitBreaker tracks failure counts and trip state for a single provider.
type CircuitBreaker struct {
	failures    int
	lastFailure time.Time
	tripped     bool
}

// FailoverBrain wraps a primary Brain with ordered fallbacks and per-provider
// circuit breakers. It implements the Brain interface.
type FailoverBrain struct {
	primary   namedBrain
	fallbacks []namedBrain
	breakers  map[string]*CircuitBreaker

	mu             sync.Mutex
	threshold      int           // failures before tripping (default 5)
	cooldownPeriod time.Duration // time before resetting (default 5min)
	kvStore        KVStore
}

// NewFailoverBrain creates a FailoverBrain that tries the primary brain first,
// then each fallback in order. The circuit breaker trips after threshold
// consecutive failures and resets after cooldown elapses.
func NewFailoverBrain(primary namedBrain, fallbacks []namedBrain, threshold int, cooldown time.Duration) *FailoverBrain {
	if threshold <= 0 {
		threshold = 5
	}
	if cooldown <= 0 {
		cooldown = 5 * time.Minute
	}

	breakers := make(map[string]*CircuitBreaker)
	breakers[primary.name] = &CircuitBreaker{}
	for _, fb := range fallbacks {
		breakers[fb.name] = &CircuitBreaker{}
	}

	return &FailoverBrain{
		primary:        primary,
		fallbacks:      fallbacks,
		breakers:       breakers,
		threshold:      threshold,
		cooldownPeriod: cooldown,
	}
}

// Respond tries the primary brain first. If it fails or its circuit breaker
// is tripped, it iterates through fallbacks in order. Returns the first
// successful response or a combined error if all providers fail.
func (fb *FailoverBrain) Respond(ctx context.Context, sessionID, content string) (string, error) {
	candidates := append([]namedBrain{fb.primary}, fb.fallbacks...)
	var lastErr error

	for _, c := range candidates {
		if fb.isTripped(c.name) {
			slog.Info("failover: skipping tripped provider", "provider", c.name)
			continue
		}

		resp, err := c.brain.Respond(ctx, sessionID, content)
		if err == nil {
			fb.recordSuccess(c.name)
			return resp, nil
		}

		lastErr = err
		fb.recordFailure(c.name)
		ec := ClassifyError(err)
		slog.Warn("failover: provider failed",
			"provider", c.name,
			"error_class", string(ec),
			"error", err,
		)

		// Auth and billing errors are not transient; don't retry on other providers
		// for context overflow since the prompt is the same everywhere.
		if ec == ErrorClassContextOverflow {
			return "", fmt.Errorf("failover: context overflow from %s: %w", c.name, err)
		}
	}

	return "", fmt.Errorf("failover: all providers failed, last error: %w", lastErr)
}

// Stream tries the primary brain first for streaming. If it fails or its
// circuit breaker is tripped, it iterates through fallbacks in order.
func (fb *FailoverBrain) Stream(ctx context.Context, sessionID, content string, onChunk func(content string) error) error {
	candidates := append([]namedBrain{fb.primary}, fb.fallbacks...)
	var lastErr error

	for _, c := range candidates {
		if fb.isTripped(c.name) {
			slog.Info("failover: skipping tripped provider for stream", "provider", c.name)
			continue
		}

		err := c.brain.Stream(ctx, sessionID, content, onChunk)
		if err == nil {
			fb.recordSuccess(c.name)
			return nil
		}

		lastErr = err
		fb.recordFailure(c.name)
		ec := ClassifyError(err)
		slog.Warn("failover: stream provider failed",
			"provider", c.name,
			"error_class", string(ec),
			"error", err,
		)

		if ec == ErrorClassContextOverflow {
			return fmt.Errorf("failover: context overflow from %s: %w", c.name, err)
		}
	}

	return fmt.Errorf("failover: all providers failed for stream, last error: %w", lastErr)
}

// isTripped returns true if the named provider's circuit breaker is tripped
// and the cooldown period has not yet elapsed.
func (fb *FailoverBrain) isTripped(name string) bool {
	fb.mu.Lock()
	defer fb.mu.Unlock()

	cb, ok := fb.breakers[name]
	if !ok {
		return false
	}
	if !cb.tripped {
		return false
	}
	// Check if cooldown has elapsed — if so, reset the breaker.
	if time.Since(cb.lastFailure) >= fb.cooldownPeriod {
		cb.tripped = false
		cb.failures = 0
		slog.Info("failover: circuit breaker reset after cooldown", "provider", name)
		return false
	}
	return true
}

// SetKVStore enables persistent circuit breaker state.
func (fb *FailoverBrain) SetKVStore(store KVStore) {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	fb.kvStore = store
}

// recordFailure increments the failure count and trips the breaker if threshold is reached.
func (fb *FailoverBrain) recordFailure(name string) {
	fb.mu.Lock()
	defer fb.mu.Unlock()

	cb, ok := fb.breakers[name]
	if !ok {
		cb = &CircuitBreaker{}
		fb.breakers[name] = cb
	}
	cb.failures++
	cb.lastFailure = time.Now()
	if cb.failures >= fb.threshold {
		cb.tripped = true
		slog.Warn("failover: circuit breaker tripped", "provider", name, "failures", cb.failures)
	}
	if fb.kvStore != nil {
		fb.persistBreakerState(name, cb)
	}
}

// recordSuccess resets the failure count for the named provider.
func (fb *FailoverBrain) recordSuccess(name string) {
	fb.mu.Lock()
	defer fb.mu.Unlock()

	cb, ok := fb.breakers[name]
	if !ok {
		return
	}
	cb.failures = 0
	cb.tripped = false
	if fb.kvStore != nil {
		fb.persistBreakerState(name, cb)
	}
}

// persistBreakerState saves a single breaker's state to KV store.
// Must be called with fb.mu held.
func (fb *FailoverBrain) persistBreakerState(name string, cb *CircuitBreaker) {
	if fb.kvStore == nil {
		return
	}
	state := struct {
		Failures    int       `json:"failures"`
		LastFailure time.Time `json:"last_failure"`
		Tripped     bool      `json:"tripped"`
	}{
		Failures:    cb.failures,
		LastFailure: cb.lastFailure,
		Tripped:     cb.tripped,
	}
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	_ = fb.kvStore.KVSet(context.Background(), "cb:"+name, string(data))
}

// LoadBreakerState restores circuit breaker state from KV store.
func (fb *FailoverBrain) LoadBreakerState(ctx context.Context) {
	if fb.kvStore == nil {
		return
	}
	fb.mu.Lock()
	defer fb.mu.Unlock()
	for name, cb := range fb.breakers {
		val, err := fb.kvStore.KVGet(ctx, "cb:"+name)
		if err != nil || val == "" {
			continue
		}
		var state struct {
			Failures    int       `json:"failures"`
			LastFailure time.Time `json:"last_failure"`
			Tripped     bool      `json:"tripped"`
		}
		if err := json.Unmarshal([]byte(val), &state); err != nil {
			continue
		}
		cb.failures = state.Failures
		cb.lastFailure = state.LastFailure
		cb.tripped = state.Tripped
	}
}
