package shared

import (
	"testing"
)

func TestRedact_BearerToken(t *testing.T) {
	input := "Bearer abc123def456ghi789jkl0"
	result := Redact(input)
	if result == input {
		t.Fatalf("expected redaction, got %q", result)
	}
	if result != "Bearer [REDACTED]" {
		t.Fatalf("expected 'Bearer [REDACTED]', got %q", result)
	}
}

func TestRedact_APIKey(t *testing.T) {
	input := `api_key=abcdef1234567890abcdef`
	result := Redact(input)
	if result == input {
		t.Fatalf("expected redaction, got %q", result)
	}
}

func TestRedact_GeminiKey(t *testing.T) {
	input := "key is AIzaSyA1234567890abcdefghijklmnopqrstuvwx"
	result := Redact(input)
	if result == input {
		t.Fatalf("expected redaction, got %q", result)
	}
}

func TestRedact_NoSecret(t *testing.T) {
	input := "this is a normal log message"
	result := Redact(input)
	if result != input {
		t.Fatalf("expected no redaction, got %q", result)
	}
}

func TestRedact_Empty(t *testing.T) {
	result := Redact("")
	if result != "" {
		t.Fatalf("expected empty, got %q", result)
	}
}

func TestRedactEnvValue_Sensitive(t *testing.T) {
	cases := []struct {
		key, value string
		expect     string
	}{
		{"GEMINI_API_KEY", "some-secret", "[REDACTED]"},
		{"auth_token", "abc123", "[REDACTED]"},
		{"password", "s3cret", "[REDACTED]"},
		{"BIND_ADDR", "127.0.0.1:8080", "127.0.0.1:8080"},
		{"LOG_LEVEL", "info", "info"},
	}
	for _, tc := range cases {
		got := RedactEnvValue(tc.key, tc.value)
		if got != tc.expect {
			t.Errorf("RedactEnvValue(%q, %q) = %q, want %q", tc.key, tc.value, got, tc.expect)
		}
	}
}
