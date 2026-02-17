package gateway

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/basket/go-claw/internal/config"
)

// NewCORSMiddleware creates a CORS middleware from config.
// When disabled, it returns a pass-through wrapper.
func NewCORSMiddleware(cfg config.CORSConfig) func(http.Handler) http.Handler {
	if !cfg.Enabled {
		return func(next http.Handler) http.Handler { return next }
	}

	origins := make(map[string]bool)
	allowAll := false
	for _, o := range cfg.AllowedOrigins {
		if o == "*" {
			allowAll = true
		}
		origins[o] = true
	}

	methods := cfg.AllowedMethods
	if len(methods) == 0 {
		methods = []string{"GET", "POST", "OPTIONS"}
	}
	headers := cfg.AllowedHeaders
	if len(headers) == 0 {
		headers = []string{"Content-Type", "Authorization", "X-API-Key"}
	}
	maxAge := cfg.MaxAge
	if maxAge == 0 {
		maxAge = 3600
	}

	methodStr := strings.Join(methods, ", ")
	headerStr := strings.Join(headers, ", ")
	maxAgeStr := fmt.Sprintf("%d", maxAge)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && (allowAll || origins[origin]) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", methodStr)
				w.Header().Set("Access-Control-Allow-Headers", headerStr)
				w.Header().Set("Access-Control-Max-Age", maxAgeStr)
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequestSizeLimitMiddleware limits request body size to prevent abuse.
func RequestSizeLimitMiddleware(maxBytes int64) func(http.Handler) http.Handler {
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024 // 10MB default
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}
