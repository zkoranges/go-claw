package telemetry

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/basket/go-claw/internal/shared"
)

func NewLogger(homeDir, level string, quiet bool) (*slog.Logger, io.Closer, error) {
	logDir := filepath.Join(homeDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, nil, err
	}

	logFilePath := filepath.Join(logDir, "system.jsonl")
	file, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, err
	}

	lvl := parseLevel(level)
	var w io.Writer
	if quiet {
		w = file
	} else {
		w = io.MultiWriter(os.Stdout, file)
	}
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: lvl,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Key = "timestamp"
			}
			if shouldRedactKey(a.Key) {
				return slog.String(a.Key, "[REDACTED]")
			}
			if a.Value.Kind() == slog.KindString {
				if redacted, ok := redactStringValue(a.Value.String()); ok {
					return slog.String(a.Key, redacted)
				}
			}
			return a
		},
	})
	logger := slog.New(handler).With("component", "runtime", "trace_id", "-")
	return logger, file, nil
}

func shouldRedactKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	if lower == "" {
		return false
	}
	sensitiveTokens := []string{"token", "secret", "password", "authorization", "api_key", "apikey", "bearer"}
	for _, token := range sensitiveTokens {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func redactStringValue(v string) (string, bool) {
	lower := strings.ToLower(v)
	// Full redaction for strings containing bearer tokens or auth headers.
	if strings.Contains(lower, "bearer ") {
		return "[REDACTED]", true
	}
	if strings.Contains(lower, "api_key") || strings.Contains(lower, "authorization:") {
		return "[REDACTED]", true
	}
	// Apply shared pattern-based redaction for other secrets (GC-SPEC-SEC-005).
	redacted := shared.Redact(v)
	if redacted != v {
		return redacted, true
	}
	return v, false
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
