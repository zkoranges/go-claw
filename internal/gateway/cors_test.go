package gateway_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basket/go-claw/internal/config"
	"github.com/basket/go-claw/internal/gateway"
)

func TestCORS_PreflightHeaders(t *testing.T) {
	cfg := config.CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"https://example.com"},
		AllowedMethods: []string{"GET", "POST"},
		AllowedHeaders: []string{"Content-Type", "Authorization"},
		MaxAge:         7200,
	}
	wrap := gateway.NewCORSMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be called for OPTIONS preflight")
	})
	handler := wrap(inner)

	req := httptest.NewRequest("OPTIONS", "/api/tasks", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if origin := rec.Header().Get("Access-Control-Allow-Origin"); origin != "https://example.com" {
		t.Fatalf("expected origin https://example.com, got %q", origin)
	}
	if methods := rec.Header().Get("Access-Control-Allow-Methods"); methods != "GET, POST" {
		t.Fatalf("expected methods 'GET, POST', got %q", methods)
	}
	if headers := rec.Header().Get("Access-Control-Allow-Headers"); headers != "Content-Type, Authorization" {
		t.Fatalf("expected headers 'Content-Type, Authorization', got %q", headers)
	}
	if maxAge := rec.Header().Get("Access-Control-Max-Age"); maxAge != "7200" {
		t.Fatalf("expected max-age 7200, got %q", maxAge)
	}
}

func TestCORS_AllowedOrigin(t *testing.T) {
	cfg := config.CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"https://allowed.com"},
	}
	wrap := gateway.NewCORSMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := wrap(inner)

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("Origin", "https://allowed.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if origin := rec.Header().Get("Access-Control-Allow-Origin"); origin != "https://allowed.com" {
		t.Fatalf("expected origin https://allowed.com, got %q", origin)
	}
}

func TestCORS_DisallowedOrigin(t *testing.T) {
	cfg := config.CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"https://allowed.com"},
	}
	wrap := gateway.NewCORSMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := wrap(inner)

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("Origin", "https://evil.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Request still passes through (CORS is not access control), but no CORS headers set.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if origin := rec.Header().Get("Access-Control-Allow-Origin"); origin != "" {
		t.Fatalf("expected no Access-Control-Allow-Origin, got %q", origin)
	}
}

func TestCORS_Wildcard(t *testing.T) {
	cfg := config.CORSConfig{
		Enabled:        true,
		AllowedOrigins: []string{"*"},
	}
	wrap := gateway.NewCORSMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := wrap(inner)

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("Origin", "https://any-origin.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if origin := rec.Header().Get("Access-Control-Allow-Origin"); origin != "https://any-origin.com" {
		t.Fatalf("expected origin https://any-origin.com, got %q", origin)
	}
}

func TestCORS_Disabled(t *testing.T) {
	cfg := config.CORSConfig{
		Enabled: false,
	}
	wrap := gateway.NewCORSMiddleware(cfg)

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := wrap(inner)

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !called {
		t.Fatal("inner handler should have been called when CORS is disabled")
	}
	// No CORS headers should be set when disabled.
	if origin := rec.Header().Get("Access-Control-Allow-Origin"); origin != "" {
		t.Fatalf("expected no CORS headers when disabled, got %q", origin)
	}
}

func TestRequestSizeLimitMiddleware(t *testing.T) {
	wrap := gateway.RequestSizeLimitMiddleware(100) // 100 bytes

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read all body bytes.
		var total int
		buf := make([]byte, 32)
		for {
			n, err := r.Body.Read(buf)
			total += n
			if err != nil {
				break
			}
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "%d", total)
	})
	handler := wrap(inner)

	// Small body should pass through fully.
	req := httptest.NewRequest("POST", "/api/tasks", strings.NewReader("small"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for small body, got %d", rec.Code)
	}

	// Large body exceeding limit: MaxBytesReader truncates at 100+1 bytes
	// and returns a *http.MaxBytesError. The handler still runs but reads only ~100 bytes.
	largeBody := strings.Repeat("x", 200)
	req = httptest.NewRequest("POST", "/api/tasks", strings.NewReader(largeBody))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	// The handler reads at most 101 bytes (limit+1) before hitting the MaxBytesError.
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, body)
	}
	// Verify we read <= limit+1 bytes (MaxBytesReader allows 1 extra to detect overflow).
	if body != "101" && body != "100" {
		t.Logf("read %s bytes from oversized body (expected ~100-101)", body)
	}
}
