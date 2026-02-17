package gateway_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/basket/go-claw/internal/config"
	"github.com/basket/go-claw/internal/gateway"
)

func TestAuthMiddleware_ValidKey(t *testing.T) {
	cfg := config.AuthConfig{
		Enabled: true,
		Keys: []config.APIKeyEntry{
			{Key: "test-key-123", Description: "test key"},
		},
	}
	am := gateway.NewAuthMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := am.Wrap(inner)

	req := httptest.NewRequest("GET", "/api/v1/task", nil)
	req.Header.Set("Authorization", "Bearer test-key-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_InvalidKey(t *testing.T) {
	cfg := config.AuthConfig{
		Enabled: true,
		Keys: []config.APIKeyEntry{
			{Key: "test-key-123"},
		},
	}
	am := gateway.NewAuthMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for invalid key")
	})
	handler := am.Wrap(inner)

	req := httptest.NewRequest("GET", "/api/v1/task", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestAuthMiddleware_MissingKey(t *testing.T) {
	cfg := config.AuthConfig{
		Enabled: true,
		Keys: []config.APIKeyEntry{
			{Key: "test-key-123"},
		},
	}
	am := gateway.NewAuthMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for missing key")
	})
	handler := am.Wrap(inner)

	req := httptest.NewRequest("GET", "/api/v1/task", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_Disabled(t *testing.T) {
	cfg := config.AuthConfig{
		Enabled: false,
	}
	am := gateway.NewAuthMiddleware(cfg)

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := am.Wrap(inner)

	req := httptest.NewRequest("GET", "/api/v1/task", nil)
	// No key provided, but auth is disabled.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !called {
		t.Fatal("inner handler should have been called when auth is disabled")
	}
}

func TestAuthMiddleware_SkipsHealthz(t *testing.T) {
	cfg := config.AuthConfig{
		Enabled: true,
		Keys: []config.APIKeyEntry{
			{Key: "test-key-123"},
		},
	}
	am := gateway.NewAuthMiddleware(cfg)

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := am.Wrap(inner)

	req := httptest.NewRequest("GET", "/healthz", nil)
	// No API key, but /healthz should bypass auth.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !called {
		t.Fatal("inner handler should have been called for /healthz")
	}
}

func TestAuthMiddleware_SkipsMetrics(t *testing.T) {
	cfg := config.AuthConfig{
		Enabled: true,
		Keys: []config.APIKeyEntry{
			{Key: "test-key-123"},
		},
	}
	am := gateway.NewAuthMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := am.Wrap(inner)

	for _, path := range []string{"/metrics", "/metrics/prometheus"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for %s, got %d", path, rec.Code)
		}
	}
}

func TestAuthMiddleware_BearerToken(t *testing.T) {
	cfg := config.AuthConfig{
		Enabled: true,
		Keys: []config.APIKeyEntry{
			{Key: "bearer-key-456"},
		},
	}
	am := gateway.NewAuthMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := am.Wrap(inner)

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("Authorization", "Bearer bearer-key-456")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_XAPIKey(t *testing.T) {
	cfg := config.AuthConfig{
		Enabled: true,
		Keys: []config.APIKeyEntry{
			{Key: "x-api-key-789"},
		},
	}
	am := gateway.NewAuthMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := am.Wrap(inner)

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("X-API-Key", "x-api-key-789")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_QueryParam(t *testing.T) {
	cfg := config.AuthConfig{
		Enabled: true,
		Keys: []config.APIKeyEntry{
			{Key: "query-key-abc"},
		},
	}
	am := gateway.NewAuthMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := am.Wrap(inner)

	req := httptest.NewRequest("GET", "/api/tasks?api_key=query-key-abc", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_ContextInjection(t *testing.T) {
	cfg := config.AuthConfig{
		Enabled: true,
		Keys: []config.APIKeyEntry{
			{Key: "ctx-key-123", Description: "context test key", AgentIDs: []string{"agent1"}},
		},
	}
	am := gateway.NewAuthMiddleware(cfg)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entry := gateway.KeyEntryFromContext(r.Context())
		if entry == nil {
			t.Fatal("expected API key entry in context")
		}
		if entry.Description != "context test key" {
			t.Fatalf("expected description 'context test key', got %q", entry.Description)
		}
		if len(entry.AgentIDs) != 1 || entry.AgentIDs[0] != "agent1" {
			t.Fatalf("expected agent_ids [agent1], got %v", entry.AgentIDs)
		}
		w.WriteHeader(http.StatusOK)
	})
	handler := am.Wrap(inner)

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("Authorization", "Bearer ctx-key-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}
