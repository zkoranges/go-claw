package memory

import (
	"strings"
	"testing"
)

func TestContextBudget_Format(t *testing.T) {
	t.Run("format_includes_agent_and_model", func(t *testing.T) {
		b := &ContextBudget{
			ModelLimit:   128000,
			OutputBuffer: 4096,
			Available:    123904,
			SoulTokens:   850,
			MemoryTokens: 120,
			TotalUsed:    970,
			Remaining:    122934,
		}

		formatted := b.Format("coder", "gemini-2.5-pro")
		if !strings.Contains(formatted, "@coder") {
			t.Errorf("formatted should include agent name")
		}
		if !strings.Contains(formatted, "gemini-2.5-pro") {
			t.Errorf("formatted should include model name")
		}
		if !strings.Contains(formatted, "123904") {
			t.Errorf("formatted should include available tokens")
		}
	})

	t.Run("format_shows_all_components", func(t *testing.T) {
		b := &ContextBudget{
			ModelLimit:     128000,
			OutputBuffer:   4096,
			Available:      123904,
			SoulTokens:     850,
			MemoryTokens:   120,
			MemoryCount:    3,
			PinTokens:      2400,
			PinCount:       2,
			SharedTokens:   480,
			SharedMemCount: 1,
			SharedPinCount: 1,
			SummaryTokens:  380,
			TruncatedCount: 45,
			MessageTokens:  3200,
			MessageCount:   12,
			TotalUsed:      7430,
			Remaining:      116474,
		}

		formatted := b.Format("coder", "gemini-2.5-pro")
		if !strings.Contains(formatted, "Soul") {
			t.Errorf("should show Soul tokens")
		}
		if !strings.Contains(formatted, "Memory") {
			t.Errorf("should show Memory tokens")
		}
		if !strings.Contains(formatted, "Pinned") {
			t.Errorf("should show Pinned tokens")
		}
		if !strings.Contains(formatted, "Shared") {
			t.Errorf("should show Shared tokens")
		}
		if !strings.Contains(formatted, "Summary") {
			t.Errorf("should show Summary tokens")
		}
		if !strings.Contains(formatted, "Messages") {
			t.Errorf("should show Messages tokens")
		}
	})

	t.Run("format_shows_totals", func(t *testing.T) {
		b := &ContextBudget{
			Available: 123904,
			TotalUsed: 7430,
			Remaining: 116474,
		}

		formatted := b.Format("coder", "gemini-2.5-pro")
		if !strings.Contains(formatted, "7430") {
			t.Errorf("should show total used")
		}
		if !strings.Contains(formatted, "116474") {
			t.Errorf("should show remaining")
		}
	})
}

func TestContextBudget_Percentage(t *testing.T) {
	t.Run("percentage_zero_when_empty", func(t *testing.T) {
		b := &ContextBudget{
			Available: 100,
			TotalUsed: 0,
		}

		pct := b.Percentage()
		if pct != 0.0 {
			t.Errorf("expected 0%%, got %f%%", pct)
		}
	})

	t.Run("percentage_50_when_half_full", func(t *testing.T) {
		b := &ContextBudget{
			Available: 100,
			TotalUsed: 50,
		}

		pct := b.Percentage()
		if pct != 50.0 {
			t.Errorf("expected 50%%, got %f%%", pct)
		}
	})

	t.Run("percentage_100_when_full", func(t *testing.T) {
		b := &ContextBudget{
			Available: 100,
			TotalUsed: 100,
		}

		pct := b.Percentage()
		if pct != 100.0 {
			t.Errorf("expected 100%%, got %f%%", pct)
		}
	})
}

func TestContextBudget_IsLow(t *testing.T) {
	t.Run("is_low_when_remaining_less_than_10_percent", func(t *testing.T) {
		b := &ContextBudget{
			Available: 100,
			Remaining: 9, // less than 10%
		}

		if !b.IsLow() {
			t.Errorf("should report low when remaining is less than 10%%")
		}
	})

	t.Run("not_low_when_remaining_more_than_10_percent", func(t *testing.T) {
		b := &ContextBudget{
			Available: 100,
			Remaining: 20, // 20% > 10%
		}

		if b.IsLow() {
			t.Errorf("should not report low when remaining is more than 10%%")
		}
	})

	t.Run("not_low_at_exactly_10_percent", func(t *testing.T) {
		b := &ContextBudget{
			Available: 100,
			Remaining: 10, // exactly 10%
		}

		if b.IsLow() {
			t.Errorf("should not report low at exactly 10%%")
		}
	})
}
