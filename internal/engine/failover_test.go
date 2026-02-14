package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// mockBrain is a minimal Brain implementation for failover tests.
type mockBrain struct {
	name      string
	respondFn func(ctx context.Context, sessionID, content string) (string, error)
	streamFn  func(ctx context.Context, sessionID, content string, onChunk func(string) error) error
}

func (m *mockBrain) Respond(ctx context.Context, sessionID, content string) (string, error) {
	if m.respondFn != nil {
		return m.respondFn(ctx, sessionID, content)
	}
	return "", fmt.Errorf("not implemented")
}

func (m *mockBrain) Stream(ctx context.Context, sessionID, content string, onChunk func(string) error) error {
	if m.streamFn != nil {
		return m.streamFn(ctx, sessionID, content, onChunk)
	}
	return fmt.Errorf("not implemented")
}

func TestFailover_PrimarySucceeds(t *testing.T) {
	primaryCalled := false
	fallbackCalled := false

	primary := namedBrain{
		name: "primary",
		brain: &mockBrain{
			respondFn: func(ctx context.Context, sessionID, content string) (string, error) {
				primaryCalled = true
				return "primary response", nil
			},
		},
	}
	fallback := namedBrain{
		name: "fallback",
		brain: &mockBrain{
			respondFn: func(ctx context.Context, sessionID, content string) (string, error) {
				fallbackCalled = true
				return "fallback response", nil
			},
		},
	}

	fb := NewFailoverBrain(primary, []namedBrain{fallback}, 5, 5*time.Minute)
	resp, err := fb.Respond(context.Background(), "session-1", "hello")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if resp != "primary response" {
		t.Fatalf("expected primary response, got: %s", resp)
	}
	if !primaryCalled {
		t.Fatal("expected primary to be called")
	}
	if fallbackCalled {
		t.Fatal("expected fallback NOT to be called when primary succeeds")
	}
}

func TestFailover_FallbackOnFailure(t *testing.T) {
	primaryCalls := 0
	fallbackCalls := 0

	primary := namedBrain{
		name: "primary",
		brain: &mockBrain{
			respondFn: func(ctx context.Context, sessionID, content string) (string, error) {
				primaryCalls++
				return "", fmt.Errorf("500: internal server error")
			},
		},
	}
	fallback := namedBrain{
		name: "fallback",
		brain: &mockBrain{
			respondFn: func(ctx context.Context, sessionID, content string) (string, error) {
				fallbackCalls++
				return "fallback response", nil
			},
		},
	}

	fb := NewFailoverBrain(primary, []namedBrain{fallback}, 5, 5*time.Minute)
	resp, err := fb.Respond(context.Background(), "session-1", "hello")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if resp != "fallback response" {
		t.Fatalf("expected fallback response, got: %s", resp)
	}
	if primaryCalls != 1 {
		t.Fatalf("expected primary called once, got: %d", primaryCalls)
	}
	if fallbackCalls != 1 {
		t.Fatalf("expected fallback called once, got: %d", fallbackCalls)
	}
}

func TestFailover_BreakerTrips(t *testing.T) {
	primaryCalls := 0
	fallbackCalls := 0
	threshold := 3

	primary := namedBrain{
		name: "primary",
		brain: &mockBrain{
			respondFn: func(ctx context.Context, sessionID, content string) (string, error) {
				primaryCalls++
				return "", fmt.Errorf("rate limit exceeded")
			},
		},
	}
	fallback := namedBrain{
		name: "fallback",
		brain: &mockBrain{
			respondFn: func(ctx context.Context, sessionID, content string) (string, error) {
				fallbackCalls++
				return "fallback ok", nil
			},
		},
	}

	fb := NewFailoverBrain(primary, []namedBrain{fallback}, threshold, 5*time.Minute)

	// Trip the breaker by failing threshold times.
	for i := 0; i < threshold; i++ {
		_, _ = fb.Respond(context.Background(), "session-1", "hello")
	}

	if primaryCalls != threshold {
		t.Fatalf("expected primary called %d times, got: %d", threshold, primaryCalls)
	}

	// Now the breaker should be tripped; the next call should skip primary entirely.
	primaryCalls = 0
	fallbackCalls = 0

	resp, err := fb.Respond(context.Background(), "session-1", "hello")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if resp != "fallback ok" {
		t.Fatalf("expected fallback response, got: %s", resp)
	}
	if primaryCalls != 0 {
		t.Fatalf("expected primary NOT called after breaker tripped, got: %d calls", primaryCalls)
	}
	if fallbackCalls != 1 {
		t.Fatalf("expected fallback called once, got: %d", fallbackCalls)
	}
}

func TestFailover_BreakerResets(t *testing.T) {
	primaryCalls := 0
	threshold := 2
	cooldown := 50 * time.Millisecond // Short cooldown for test.

	primary := namedBrain{
		name: "primary",
		brain: &mockBrain{
			respondFn: func(ctx context.Context, sessionID, content string) (string, error) {
				primaryCalls++
				// Fail only the first threshold calls; succeed afterwards.
				if primaryCalls <= threshold {
					return "", fmt.Errorf("timeout: request timed out")
				}
				return "primary recovered", nil
			},
		},
	}
	fallback := namedBrain{
		name: "fallback",
		brain: &mockBrain{
			respondFn: func(ctx context.Context, sessionID, content string) (string, error) {
				return "fallback ok", nil
			},
		},
	}

	fb := NewFailoverBrain(primary, []namedBrain{fallback}, threshold, cooldown)

	// Trip the breaker.
	for i := 0; i < threshold; i++ {
		_, _ = fb.Respond(context.Background(), "session-1", "hello")
	}

	// Verify breaker is tripped (primary should be skipped).
	resp, err := fb.Respond(context.Background(), "session-1", "hello")
	if err != nil {
		t.Fatalf("expected fallback to succeed, got: %v", err)
	}
	if resp != "fallback ok" {
		t.Fatalf("expected fallback response while tripped, got: %s", resp)
	}

	// Wait for cooldown to elapse.
	time.Sleep(cooldown + 10*time.Millisecond)

	// After cooldown, primary should be tried again and succeed.
	resp, err = fb.Respond(context.Background(), "session-1", "hello")
	if err != nil {
		t.Fatalf("expected primary to recover, got: %v", err)
	}
	if resp != "primary recovered" {
		t.Fatalf("expected primary recovered response, got: %s", resp)
	}
}

func TestFailover_AllFail(t *testing.T) {
	primary := namedBrain{
		name: "primary",
		brain: &mockBrain{
			respondFn: func(ctx context.Context, sessionID, content string) (string, error) {
				return "", fmt.Errorf("primary: 500 internal error")
			},
		},
	}
	fallback1 := namedBrain{
		name: "fallback1",
		brain: &mockBrain{
			respondFn: func(ctx context.Context, sessionID, content string) (string, error) {
				return "", fmt.Errorf("fallback1: 503 service unavailable")
			},
		},
	}
	fallback2 := namedBrain{
		name: "fallback2",
		brain: &mockBrain{
			respondFn: func(ctx context.Context, sessionID, content string) (string, error) {
				return "", fmt.Errorf("fallback2: connection refused")
			},
		},
	}

	fb := NewFailoverBrain(primary, []namedBrain{fallback1, fallback2}, 5, 5*time.Minute)
	_, err := fb.Respond(context.Background(), "session-1", "hello")
	if err == nil {
		t.Fatal("expected an error when all providers fail")
	}
	if !strings.Contains(err.Error(), "all providers failed") {
		t.Fatalf("expected 'all providers failed' in error, got: %v", err)
	}
	// The last error should be wrapped.
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("expected last error (fallback2) to be wrapped, got: %v", err)
	}

	// Verify Stream also fails correctly.
	streamErr := fb.Stream(context.Background(), "session-1", "hello", func(s string) error { return nil })
	if streamErr == nil {
		t.Fatal("expected stream error when all providers fail")
	}
	if !strings.Contains(streamErr.Error(), "all providers failed") {
		t.Fatalf("expected 'all providers failed' in stream error, got: %v", streamErr)
	}
}

func TestErrorClassification(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected ErrorClass
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: ErrorClassUnknown,
		},
		{
			name:     "401 unauthorized",
			err:      errors.New("HTTP 401: Unauthorized"),
			expected: ErrorClassAuth,
		},
		{
			name:     "invalid api key",
			err:      errors.New("invalid api key provided"),
			expected: ErrorClassAuth,
		},
		{
			name:     "403 forbidden",
			err:      errors.New("403 Forbidden: access denied"),
			expected: ErrorClassAuth,
		},
		{
			name:     "429 rate limit",
			err:      errors.New("HTTP 429: rate limit exceeded"),
			expected: ErrorClassRateLimit,
		},
		{
			name:     "quota exceeded",
			err:      errors.New("quota exceeded for project"),
			expected: ErrorClassRateLimit,
		},
		{
			name:     "too many requests",
			err:      errors.New("too many requests, please slow down"),
			expected: ErrorClassRateLimit,
		},
		{
			name:     "deadline exceeded",
			err:      errors.New("context deadline exceeded"),
			expected: ErrorClassTimeout,
		},
		{
			name:     "timeout",
			err:      errors.New("request timeout after 30s"),
			expected: ErrorClassTimeout,
		},
		{
			name:     "timed out",
			err:      errors.New("connection timed out"),
			expected: ErrorClassTimeout,
		},
		{
			name:     "billing issue",
			err:      errors.New("billing account not active"),
			expected: ErrorClassBilling,
		},
		{
			name:     "payment required",
			err:      errors.New("payment required for this model"),
			expected: ErrorClassBilling,
		},
		{
			name:     "insufficient funds",
			err:      errors.New("insufficient funds in account"),
			expected: ErrorClassBilling,
		},
		{
			name:     "context_length exceeded",
			err:      errors.New("context_length_exceeded: max 128000 tokens"),
			expected: ErrorClassContextOverflow,
		},
		{
			name:     "token limit",
			err:      errors.New("token limit exceeded for this request"),
			expected: ErrorClassContextOverflow,
		},
		{
			name:     "max tokens",
			err:      errors.New("max tokens exceeded"),
			expected: ErrorClassContextOverflow,
		},
		{
			name:     "context window",
			err:      errors.New("input exceeds context window"),
			expected: ErrorClassContextOverflow,
		},
		{
			name:     "unknown error",
			err:      errors.New("something went wrong"),
			expected: ErrorClassUnknown,
		},
		{
			name:     "generic server error",
			err:      errors.New("500 internal server error"),
			expected: ErrorClassUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyError(tt.err)
			if got != tt.expected {
				t.Errorf("ClassifyError(%v) = %s, want %s", tt.err, got, tt.expected)
			}
		})
	}
}

// mockKVStore implements KVStore for testing circuit breaker persistence.
type mockKVStore struct {
	data map[string]string
}

func newMockKVStore() *mockKVStore {
	return &mockKVStore{data: make(map[string]string)}
}

func (m *mockKVStore) KVSet(_ context.Context, key, val string) error {
	m.data[key] = val
	return nil
}

func (m *mockKVStore) KVGet(_ context.Context, key string) (string, error) {
	return m.data[key], nil
}

func TestFailover_BreakerPersistence(t *testing.T) {
	kv := newMockKVStore()
	threshold := 3

	primary := namedBrain{
		name: "primary",
		brain: &mockBrain{
			respondFn: func(ctx context.Context, sessionID, content string) (string, error) {
				return "", fmt.Errorf("always fails")
			},
		},
	}
	fallback := namedBrain{
		name: "fallback",
		brain: &mockBrain{
			respondFn: func(ctx context.Context, sessionID, content string) (string, error) {
				return "fallback ok", nil
			},
		},
	}

	fb := NewFailoverBrain(primary, []namedBrain{fallback}, threshold, 5*time.Minute)
	fb.SetKVStore(kv)

	// Trip the breaker.
	for i := 0; i < threshold; i++ {
		_, _ = fb.Respond(context.Background(), "s1", "hello")
	}

	// Verify KV store has breaker state.
	val, err := kv.KVGet(context.Background(), "cb:primary")
	if err != nil {
		t.Fatalf("kvget: %v", err)
	}
	if val == "" {
		t.Fatal("expected persisted breaker state for primary")
	}
	if !strings.Contains(val, `"tripped":true`) {
		t.Fatalf("expected tripped=true in persisted state, got: %s", val)
	}

	// Create a new FailoverBrain and restore from KV.
	fb2 := NewFailoverBrain(primary, []namedBrain{fallback}, threshold, 5*time.Minute)
	fb2.SetKVStore(kv)
	fb2.LoadBreakerState(context.Background())

	// The primary breaker should be tripped immediately (no calls needed to trip it).
	resp, err := fb2.Respond(context.Background(), "s1", "hello")
	if err != nil {
		t.Fatalf("expected fallback to succeed: %v", err)
	}
	if resp != "fallback ok" {
		t.Fatalf("expected fallback response after restore, got: %s", resp)
	}
}
