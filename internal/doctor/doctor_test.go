package doctor

import (
	"context"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/config"
)

func TestCheckNetwork_DefaultProvider(t *testing.T) {
	cfg := &config.Config{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result := checkNetwork(ctx, cfg)
	// DNS lookup should succeed for google's generativelanguage endpoint.
	if result.Status != "PASS" {
		t.Logf("network check result: %+v", result)
		// Allow FAIL in CI/offline environments.
		if result.Status != "FAIL" {
			t.Fatalf("expected PASS or FAIL, got %s", result.Status)
		}
	}
	if result.Name != "Network" {
		t.Fatalf("expected name Network, got %s", result.Name)
	}
}

func TestCheckNetwork_NilConfig(t *testing.T) {
	result := checkNetwork(context.Background(), nil)
	if result.Status != "SKIP" {
		t.Fatalf("expected SKIP for nil config, got %s", result.Status)
	}
}

func TestCheckNetwork_AnthropicProvider(t *testing.T) {
	cfg := &config.Config{}
	cfg.LLM.Provider = "anthropic"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result := checkNetwork(ctx, cfg)
	if result.Name != "Network" {
		t.Fatalf("expected name Network, got %s", result.Name)
	}
	// Should try to resolve api.anthropic.com.
	if result.Status == "PASS" && result.Detail == "" {
		t.Fatal("expected detail to be set on PASS")
	}
}

func TestCheckNetwork_LegacyProvider(t *testing.T) {
	cfg := &config.Config{
		LLMProvider: "openai",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result := checkNetwork(ctx, cfg)
	if result.Name != "Network" {
		t.Fatalf("expected name Network, got %s", result.Name)
	}
	// Should try to resolve api.openai.com.
	if result.Status != "PASS" && result.Status != "FAIL" {
		t.Fatalf("expected PASS or FAIL, got %s", result.Status)
	}
}

func TestCheckNetwork_UnknownProvider(t *testing.T) {
	cfg := &config.Config{}
	cfg.LLM.Provider = "unknown_provider"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result := checkNetwork(ctx, cfg)
	// Should fall back to google endpoint.
	if result.Status != "PASS" && result.Status != "FAIL" {
		t.Fatalf("expected PASS or FAIL for unknown provider, got %s", result.Status)
	}
}

func TestCheckAPIKey_NilConfig(t *testing.T) {
	result := checkAPIKey(context.Background(), nil)
	if result.Status != "SKIP" {
		t.Fatalf("expected SKIP for nil config, got %s", result.Status)
	}
}

func TestCheckAPIKey_DefaultGoogle(t *testing.T) {
	cfg := &config.Config{}
	t.Setenv("GEMINI_API_KEY", "")

	result := checkAPIKey(context.Background(), cfg)
	if result.Status != "WARN" {
		t.Fatalf("expected WARN when GEMINI_API_KEY empty, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckAPIKey_GoogleSet(t *testing.T) {
	cfg := &config.Config{}
	t.Setenv("GEMINI_API_KEY", "test-key")

	result := checkAPIKey(context.Background(), cfg)
	if result.Status != "PASS" {
		t.Fatalf("expected PASS when GEMINI_API_KEY set, got %s: %s", result.Status, result.Message)
	}
}

func TestCheckAPIKey_OllamaNoKeyNeeded(t *testing.T) {
	cfg := &config.Config{}
	cfg.LLM.Provider = "ollama"

	result := checkAPIKey(context.Background(), cfg)
	if result.Status != "PASS" {
		t.Fatalf("expected PASS for ollama (no key needed), got %s: %s", result.Status, result.Message)
	}
}

func TestCheckNetwork_CanceledContext(t *testing.T) {
	cfg := &config.Config{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := checkNetwork(ctx, cfg)
	if result.Status != "FAIL" {
		t.Fatalf("expected FAIL for canceled context, got %s", result.Status)
	}
}
