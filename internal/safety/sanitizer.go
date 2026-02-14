package safety

import (
	"fmt"
	"regexp"
	"strings"
)

// Action indicates the recommended response to a detected threat.
type Action int

const (
	// ActionAllow means the input is safe.
	ActionAllow Action = iota
	// ActionWarn means a potential issue was detected but input may proceed.
	ActionWarn
	// ActionBlock means the input should be rejected.
	ActionBlock
)

// CheckResult is the outcome of a sanitizer check.
type CheckResult struct {
	Action  Action
	Reason  string
	Pattern string // which pattern matched (for logging)
}

// Sanitizer detects prompt injection attacks in user inputs.
type Sanitizer struct{}

// NewSanitizer creates a new Sanitizer instance.
func NewSanitizer() *Sanitizer {
	return &Sanitizer{}
}

type injectionPattern struct {
	re     *regexp.Regexp
	action Action
	reason string
}

var injectionPatterns = []injectionPattern{
	// Role manipulation attempts.
	{
		re:     regexp.MustCompile(`(?i)\b(ignore\s+(all\s+)?(previous|above|prior)\s+(instructions?|prompts?|rules?))\b`),
		action: ActionBlock,
		reason: "role manipulation: ignore previous instructions",
	},
	{
		re:     regexp.MustCompile(`(?i)\b(you\s+are\s+now\s+(a|an|the)\s+\w+)`),
		action: ActionBlock,
		reason: "role manipulation: identity override",
	},
	{
		re:     regexp.MustCompile(`(?i)\b(new\s+instructions?|override\s+(system\s+)?prompt|system\s+prompt\s+override)\b`),
		action: ActionBlock,
		reason: "role manipulation: system prompt override",
	},
	{
		re:     regexp.MustCompile(`(?i)\b(forget\s+(everything|all|your)\s+(you|instructions?)?)`),
		action: ActionBlock,
		reason: "role manipulation: memory wipe",
	},
	// Prompt leaking attempts.
	{
		re:     regexp.MustCompile(`(?i)\b(reveal|show|display|print|output|repeat)\s+(\w+\s+)?(your\s+)?(system\s+)?(prompt|instructions?|rules?|guidelines?)\b`),
		action: ActionBlock,
		reason: "prompt leaking: system prompt extraction",
	},
	{
		re:     regexp.MustCompile(`(?i)\b(what\s+(are|is)\s+your\s+(system\s+)?(prompt|instructions?|rules?))\b`),
		action: ActionBlock,
		reason: "prompt leaking: system prompt query",
	},
	// Injection markers (suspicious but not definitively malicious).
	{
		re:     regexp.MustCompile(`(?i)\[\s*SYSTEM\s*\]`),
		action: ActionWarn,
		reason: "injection marker: [SYSTEM] tag",
	},
	{
		re:     regexp.MustCompile(`(?i)<\s*\|?\s*(system|im_start|im_end)\s*\|?\s*>`),
		action: ActionWarn,
		reason: "injection marker: chat template tag",
	},
	// Base64 encoded variants of "ignore" patterns.
	{
		re:     regexp.MustCompile(`(?i)(aWdub3Jl|SWdub3Jl)`), // base64 of "ignore"/"Ignore"
		action: ActionWarn,
		reason: "potential encoded injection",
	},
}

// Check analyzes input text for prompt injection attempts.
func (s *Sanitizer) Check(input string) CheckResult {
	if strings.TrimSpace(input) == "" {
		return CheckResult{Action: ActionAllow}
	}

	for _, pat := range injectionPatterns {
		if pat.re.MatchString(input) {
			return CheckResult{
				Action:  pat.action,
				Reason:  pat.reason,
				Pattern: pat.re.String(),
			}
		}
	}

	return CheckResult{Action: ActionAllow}
}

// MustAllow returns an error if the check result is Block.
func (r CheckResult) MustAllow() error {
	if r.Action == ActionBlock {
		return fmt.Errorf("prompt injection detected: %s", r.Reason)
	}
	return nil
}
