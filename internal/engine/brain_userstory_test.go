package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/basket/go-claw/internal/engine"
	"github.com/basket/go-claw/internal/policy"
)

func TestUserStory_US2_PriceComparisonIsRegisteredAsTool(t *testing.T) {
	// [SPEC: SPEC-GOAL-G3] Price comparison is now a registered Genkit tool
	// (price_comparison) rather than a brain fast-path. This test verifies
	// the tool is registered and discoverable by the brain.
	store := openStoreForEngineTest(t)
	pol := policy.Policy{
		AllowDomains:      []string{"example.com"},
		AllowCapabilities: []string{"tools.web_search", "tools.read_url", "tools.price_comparison"},
		AllowLoopback:     true,
	}
	brain := engine.NewGenkitBrain(context.Background(), store, engine.BrainConfig{
		Policy: pol,
		Soul:   "You are a technical assistant.",
	})

	// Verify the brain has the price_comparison tool registered.
	reg := brain.Registry()
	found := false
	for _, tool := range reg.Tools {
		if strings.Contains(tool.Name(), "price_comparison") {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, len(reg.Tools))
		for i, tool := range reg.Tools {
			names[i] = tool.Name()
		}
		t.Fatalf("expected price_comparison tool to be registered, got tools: %v", names)
	}
}

func TestUserStory_US3_RandomSkillCanAnswerImmediately(t *testing.T) {
	// [SPEC: SPEC-GOAL-G4] [PDR: V-26]
	store := openStoreForEngineTest(t)
	brain := engine.NewGenkitBrain(context.Background(), store, engine.BrainConfig{
		Policy: policy.Default(),
		Soul:   "You are a friendly assistant.",
	})
	brain.RegisterSkill("random")

	reply, err := brain.Respond(context.Background(), "1bcde8fa-bf5f-4c07-8624-5b8440ad5b9f", "Generate a random number")
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if !strings.Contains(strings.ToLower(reply), "random number") {
		t.Fatalf("expected random skill response, got %q", reply)
	}
}
