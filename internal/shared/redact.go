package shared

import (
	"regexp"
	"strings"
)

const redactedPlaceholder = "[REDACTED]"

// secretPatterns matches common secret-bearing patterns in log/event/error strings.
var secretPatterns = []*regexp.Regexp{
	// API keys (generic: long hex/base64 strings preceded by key-like prefixes)
	regexp.MustCompile(`(?i)(api[_-]?key|apikey|secret[_-]?key|auth[_-]?token|bearer)\s*[:=]\s*"?([A-Za-z0-9_\-./+=]{16,})"?`),
	// Bearer tokens in Authorization headers
	regexp.MustCompile(`(?i)(Bearer\s+)([A-Za-z0-9_\-./+=]{16,})`),
	// Gemini/Google API keys (AIza pattern)
	regexp.MustCompile(`AIza[A-Za-z0-9_\-]{30,}`),
	// UUIDs that look like tokens (after auth-related prefixes)
	regexp.MustCompile(`(?i)(token|secret)\s*[:=]\s*"?([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})"?`),
}

// Redact replaces secret-bearing patterns in the input string with [REDACTED].
// This is used for GC-SPEC-SEC-005 compliance.
func Redact(input string) string {
	if input == "" {
		return input
	}
	result := input
	for _, pat := range secretPatterns {
		result = pat.ReplaceAllStringFunc(result, func(match string) string {
			// For patterns with a prefix group, keep the prefix and redact the value.
			submatch := pat.FindStringSubmatch(match)
			if len(submatch) >= 3 {
				return submatch[1] + redactedPlaceholder
			}
			return redactedPlaceholder
		})
	}
	return result
}

// RedactEnvValue checks if a key name looks secret and returns redacted value if so.
func RedactEnvValue(key, value string) string {
	keyLower := strings.ToLower(key)
	sensitiveKeys := []string{"api_key", "apikey", "secret", "token", "password", "credential"}
	for _, sensitive := range sensitiveKeys {
		if strings.Contains(keyLower, sensitive) {
			return redactedPlaceholder
		}
	}
	return value
}
