package tui

import "strings"

// humanError extracts the innermost error message from a Go error chain.
// "engine: brain: provider: connection refused" → "Connection refused"
func humanError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if idx := strings.LastIndex(msg, ": "); idx != -1 && idx+2 < len(msg) {
		inner := msg[idx+2:]
		if len(inner) > 0 {
			inner = strings.ToUpper(inner[:1]) + inner[1:]
		}
		return inner
	}
	return msg
}
