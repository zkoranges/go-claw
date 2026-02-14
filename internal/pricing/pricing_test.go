package pricing

import "testing"

func TestEstimateCost_KnownModel(t *testing.T) {
	cost := EstimateCost("gpt-4o", 1000, 500)
	if cost < 0.007 || cost > 0.008 {
		t.Fatalf("expected ~0.0075, got %f", cost)
	}
}

func TestEstimateCost_UnknownModel(t *testing.T) {
	cost := EstimateCost("unknown-model-xyz", 1000, 500)
	if cost != 0.0 {
		t.Fatalf("expected 0.0 for unknown model, got %f", cost)
	}
}

func TestEstimateCost_GeminiModel(t *testing.T) {
	// Gemini 2.5 Flash: $0.075 per 1M prompt, $0.30 per 1M completion
	cost := EstimateCost("gemini-2.5-flash", 1000000, 1000000)
	expected := 0.075 + 0.30 // $0.375
	if cost != expected {
		t.Fatalf("expected %f, got %f", expected, cost)
	}
}
