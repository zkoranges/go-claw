package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/basket/go-claw/internal/config"
)

// TokenBucket implements a simple token bucket rate limiter.
type TokenBucket struct {
	tokens     float64
	maxTokens  float64
	refillRate float64 // tokens per second
	lastRefill time.Time
	lastAccess time.Time // tracks last request for eviction
	mu         sync.Mutex
}

// NewTokenBucket creates a token bucket with the given rate and burst capacity.
func NewTokenBucket(requestsPerMinute, burstSize int) *TokenBucket {
	rate := float64(requestsPerMinute) / 60.0
	now := time.Now()
	return &TokenBucket{
		tokens:     float64(burstSize),
		maxTokens:  float64(burstSize),
		refillRate: rate,
		lastRefill: now,
		lastAccess: now,
	}
}

// Allow checks if a request is allowed and consumes a token if so.
func (tb *TokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.tokens += elapsed * tb.refillRate
	if tb.tokens > tb.maxTokens {
		tb.tokens = tb.maxTokens
	}
	tb.lastRefill = now
	tb.lastAccess = now

	if tb.tokens >= 1.0 {
		tb.tokens -= 1.0
		return true
	}
	return false
}

// LastAccess returns the time of the last Allow() call.
func (tb *TokenBucket) LastAccess() time.Time {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return tb.lastAccess
}

// RateLimitMiddleware enforces per-key rate limits using token buckets.
type RateLimitMiddleware struct {
	buckets map[string]*TokenBucket
	config  config.RateLimitConfig
	mu      sync.RWMutex
}

// NewRateLimitMiddleware creates a rate limit middleware from config.
func NewRateLimitMiddleware(cfg config.RateLimitConfig) *RateLimitMiddleware {
	rpm := cfg.RequestsPerMinute
	if rpm == 0 {
		rpm = 60
	}
	burst := cfg.BurstSize
	if burst == 0 {
		burst = 10
	}
	return &RateLimitMiddleware{
		buckets: make(map[string]*TokenBucket),
		config: config.RateLimitConfig{
			Enabled:           cfg.Enabled,
			RequestsPerMinute: rpm,
			BurstSize:         burst,
		},
	}
}

// StartEviction launches a background goroutine that periodically removes
// stale token buckets (no requests in the last maxAge). This prevents
// unbounded memory growth from unique API keys or IP addresses.
func (rl *RateLimitMiddleware) StartEviction(ctx context.Context, interval, maxAge time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rl.EvictStale(maxAge)
			}
		}
	}()
}

// EvictStale removes buckets that haven't been accessed within maxAge.
func (rl *RateLimitMiddleware) EvictStale(maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	evicted := 0
	for key, bucket := range rl.buckets {
		if bucket.LastAccess().Before(cutoff) {
			delete(rl.buckets, key)
			evicted++
		}
	}
	if evicted > 0 {
		slog.Debug("rate limiter eviction", "evicted", evicted, "remaining", len(rl.buckets))
	}
}

// BucketCount returns the current number of tracked buckets (for testing/metrics).
func (rl *RateLimitMiddleware) BucketCount() int {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return len(rl.buckets)
}

// Wrap wraps an http.Handler with rate limiting.
func (rl *RateLimitMiddleware) Wrap(next http.Handler) http.Handler {
	if !rl.config.Enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip for health/metrics endpoints.
		if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" || r.URL.Path == "/metrics/prometheus" {
			next.ServeHTTP(w, r)
			return
		}

		key := ExtractAPIKey(r)
		if key == "" {
			key = r.RemoteAddr // fallback to IP-based bucketing
		}

		bucket := rl.getBucket(key)
		if !bucket.Allow() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// getBucket returns the token bucket for the given key, creating one if needed.
func (rl *RateLimitMiddleware) getBucket(key string) *TokenBucket {
	rl.mu.RLock()
	bucket, exists := rl.buckets[key]
	rl.mu.RUnlock()
	if exists {
		return bucket
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()
	// Double-check after acquiring write lock.
	if bucket, exists = rl.buckets[key]; exists {
		return bucket
	}

	bucket = NewTokenBucket(rl.config.RequestsPerMinute, rl.config.BurstSize)
	rl.buckets[key] = bucket
	return bucket
}
