package gateway

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"sync"

	"github.com/basket/go-claw/internal/config"
)

// authContextKey is the context key type for authenticated API key entries.
type authContextKey struct{}

// AuthMiddleware validates API keys from the Authorization header.
type AuthMiddleware struct {
	keys    map[string]*config.APIKeyEntry
	enabled bool
	mu      sync.RWMutex
}

// NewAuthMiddleware creates an auth middleware from config.
func NewAuthMiddleware(cfg config.AuthConfig) *AuthMiddleware {
	am := &AuthMiddleware{
		keys:    make(map[string]*config.APIKeyEntry),
		enabled: cfg.Enabled,
	}
	for i := range cfg.Keys {
		am.keys[cfg.Keys[i].Key] = &cfg.Keys[i]
	}
	return am
}

// Wrap wraps an http.Handler with API key authentication checking.
func (am *AuthMiddleware) Wrap(next http.Handler) http.Handler {
	if !am.enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for health check and metrics endpoints.
		if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" || r.URL.Path == "/metrics/prometheus" {
			next.ServeHTTP(w, r)
			return
		}

		key := ExtractAPIKey(r)
		if key == "" {
			http.Error(w, `{"error":"missing API key"}`, http.StatusUnauthorized)
			return
		}

		am.mu.RLock()
		entry, exists := am.lookupKey(key)
		am.mu.RUnlock()

		if !exists {
			http.Error(w, `{"error":"invalid API key"}`, http.StatusForbidden)
			return
		}

		// Inject key entry into context for downstream handlers.
		ctx := context.WithValue(r.Context(), authContextKey{}, entry)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ExtractAPIKey extracts an API key from request headers or query params.
// It checks, in order: Authorization: Bearer <key>, X-API-Key header, api_key query param.
func ExtractAPIKey(r *http.Request) string {
	// Check Authorization: Bearer <key>
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	// Check X-API-Key header
	if key := r.Header.Get("X-API-Key"); key != "" {
		return key
	}
	// Check query param (useful for SSE endpoints where headers are difficult).
	return r.URL.Query().Get("api_key")
}

// lookupKey uses constant-time comparison to prevent timing attacks.
func (am *AuthMiddleware) lookupKey(candidate string) (*config.APIKeyEntry, bool) {
	for k, entry := range am.keys {
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(k)) == 1 {
			return entry, true
		}
	}
	return nil, false
}

// KeyEntryFromContext retrieves the authenticated API key entry from context.
func KeyEntryFromContext(ctx context.Context) *config.APIKeyEntry {
	if entry, ok := ctx.Value(authContextKey{}).(*config.APIKeyEntry); ok {
		return entry
	}
	return nil
}
