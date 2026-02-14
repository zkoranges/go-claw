package safety

import (
	"regexp"
)

// LeakWarning describes a detected secret leak in tool output.
type LeakWarning struct {
	Pattern string
	Sample  string // first few chars of the match for logging (redacted)
}

// LeakDetector scans strings for leaked secrets.
type LeakDetector struct{}

// NewLeakDetector creates a new LeakDetector.
func NewLeakDetector() *LeakDetector {
	return &LeakDetector{}
}

var leakPatterns = []struct {
	re   *regexp.Regexp
	desc string
}{
	{
		re:   regexp.MustCompile(`(?i)(api[_-]?key|apikey)\s*[:=]\s*"?([A-Za-z0-9_\-./+=]{16,})"?`),
		desc: "API key",
	},
	{
		re:   regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9_\-./+=]{16,}`),
		desc: "Bearer token",
	},
	{
		re:   regexp.MustCompile(`AIza[A-Za-z0-9_\-]{30,}`),
		desc: "Google API key",
	},
	{
		re:   regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`),
		desc: "OpenAI API key",
	},
	{
		re:   regexp.MustCompile(`-----BEGIN\s+(RSA\s+)?PRIVATE\s+KEY-----`),
		desc: "private key",
	},
	{
		re:   regexp.MustCompile(`(?i)(password|passwd|pwd)\s*[:=]\s*"?[^\s"]{8,}"?`),
		desc: "password",
	},
}

// Scan checks output text for leaked secrets.
// Returns a list of warnings without modifying the input.
func (d *LeakDetector) Scan(output string) []LeakWarning {
	if output == "" {
		return nil
	}

	var warnings []LeakWarning
	for _, pat := range leakPatterns {
		matches := pat.re.FindAllString(output, 3) // limit to 3 matches per pattern
		for _, match := range matches {
			sample := match
			if len(sample) > 20 {
				sample = sample[:17] + "..."
			}
			warnings = append(warnings, LeakWarning{
				Pattern: pat.desc,
				Sample:  sample,
			})
		}
	}
	return warnings
}
