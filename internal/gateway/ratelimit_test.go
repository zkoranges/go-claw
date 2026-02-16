package gateway_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/config"
	"github.com/basket/go-claw/internal/gateway"
)

func TestRateLimit_UnderLimit(t *testing.T) {
	cfg := config.RateLimitConfig{
		Enabled:           true,
		RequestsPerMinute: 60,
		BurstSize:         10,
	}
	rl := gateway.NewRateLimitMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Wrap(inner)

	// Send a few requests under the burst limit.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/api/tasks", nil)
		req.Header.Set("X-API-Key", "test-key")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, rec.Code)
		}
	}
}

func TestRateLimit_OverLimit(t *testing.T) {
	cfg := config.RateLimitConfig{
		Enabled:           true,
		RequestsPerMinute: 60,
		BurstSize:         3,
	}
	rl := gateway.NewRateLimitMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Wrap(inner)

	// Exhaust the burst.
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/api/tasks", nil)
		req.Header.Set("X-API-Key", "test-key")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("burst request %d: expected 200, got %d", i, rec.Code)
		}
	}

	// Next request should be rate limited.
	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("X-API-Key", "test-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
}

func TestRateLimit_RetryAfterHeader(t *testing.T) {
	cfg := config.RateLimitConfig{
		Enabled:           true,
		RequestsPerMinute: 60,
		BurstSize:         1,
	}
	rl := gateway.NewRateLimitMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Wrap(inner)

	// Exhaust burst.
	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("X-API-Key", "test-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Over limit.
	req = httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("X-API-Key", "test-key")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if retryAfter := rec.Header().Get("Retry-After"); retryAfter != "1" {
		t.Fatalf("expected Retry-After: 1, got %q", retryAfter)
	}
}

func TestRateLimit_BurstAllowed(t *testing.T) {
	cfg := config.RateLimitConfig{
		Enabled:           true,
		RequestsPerMinute: 60,
		BurstSize:         5,
	}
	rl := gateway.NewRateLimitMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Wrap(inner)

	// All 5 burst requests should succeed immediately.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/api/tasks", nil)
		req.Header.Set("X-API-Key", "burst-key")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("burst request %d: expected 200, got %d", i, rec.Code)
		}
	}

	// 6th request should be limited.
	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("X-API-Key", "burst-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after burst exhausted, got %d", rec.Code)
	}
}

func TestRateLimit_RefillOverTime(t *testing.T) {
	// 60 requests per minute = 1 per second.
	cfg := config.RateLimitConfig{
		Enabled:           true,
		RequestsPerMinute: 60,
		BurstSize:         1,
	}
	rl := gateway.NewRateLimitMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Wrap(inner)

	// Use up the initial token.
	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("X-API-Key", "refill-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", rec.Code)
	}

	// Should be limited now.
	req = httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("X-API-Key", "refill-key")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 immediately after, got %d", rec.Code)
	}

	// Wait for refill (>1 second for 1 req/sec rate).
	time.Sleep(1100 * time.Millisecond)

	// Should be allowed again.
	req = httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("X-API-Key", "refill-key")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after refill, got %d", rec.Code)
	}
}

func TestRateLimit_PerKeyIsolation(t *testing.T) {
	cfg := config.RateLimitConfig{
		Enabled:           true,
		RequestsPerMinute: 60,
		BurstSize:         2,
	}
	rl := gateway.NewRateLimitMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Wrap(inner)

	// Exhaust key-a's bucket.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/api/tasks", nil)
		req.Header.Set("X-API-Key", "key-a")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("key-a request %d: expected 200, got %d", i, rec.Code)
		}
	}

	// key-a should be limited.
	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("X-API-Key", "key-a")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("key-a: expected 429, got %d", rec.Code)
	}

	// key-b should still be allowed (separate bucket).
	req = httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("X-API-Key", "key-b")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("key-b: expected 200, got %d", rec.Code)
	}
}

func TestRateLimit_SkipsHealthz(t *testing.T) {
	cfg := config.RateLimitConfig{
		Enabled:           true,
		RequestsPerMinute: 60,
		BurstSize:         1,
	}
	rl := gateway.NewRateLimitMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Wrap(inner)

	// Exhaust the bucket for the remote addr.
	req := httptest.NewRequest("GET", "/api/tasks", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Bucket should be exhausted for non-healthz.
	req = httptest.NewRequest("GET", "/api/tasks", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for /api/tasks, got %d", rec.Code)
	}

	// /healthz should still work (bypasses rate limit).
	req = httptest.NewRequest("GET", "/healthz", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /healthz, got %d", rec.Code)
	}
}

func TestRateLimit_EvictStale(t *testing.T) {
	cfg := config.RateLimitConfig{
		Enabled:           true,
		RequestsPerMinute: 60,
		BurstSize:         10,
	}
	rl := gateway.NewRateLimitMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Wrap(inner)

	// Create buckets for 3 different keys.
	for _, key := range []string{"key-1", "key-2", "key-3"} {
		req := httptest.NewRequest("GET", "/api/tasks", nil)
		req.Header.Set("X-API-Key", key)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	if rl.BucketCount() != 3 {
		t.Fatalf("expected 3 buckets, got %d", rl.BucketCount())
	}

	// Evict with maxAge=0 removes everything (all buckets are "stale").
	rl.EvictStale(0)
	if rl.BucketCount() != 0 {
		t.Fatalf("expected 0 buckets after full eviction, got %d", rl.BucketCount())
	}

	// Re-create buckets then evict with a large maxAge (nothing should be removed).
	for _, key := range []string{"key-a", "key-b"} {
		req := httptest.NewRequest("GET", "/api/tasks", nil)
		req.Header.Set("X-API-Key", key)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
	rl.EvictStale(time.Hour)
	if rl.BucketCount() != 2 {
		t.Fatalf("expected 2 buckets after no-op eviction, got %d", rl.BucketCount())
	}
}

func TestRateLimit_Disabled(t *testing.T) {
	cfg := config.RateLimitConfig{
		Enabled: false,
	}
	rl := gateway.NewRateLimitMiddleware(cfg)

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Wrap(inner)

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !called {
		t.Fatal("inner handler should have been called when rate limit is disabled")
	}
}
