package tools

import (
	"encoding/json"
	"testing"
)

func TestUseSkillInput_JSONRoundTrip(t *testing.T) {
	input := UseSkillInput{
		SkillName: "weather",
		Input:     "What's the weather in Tokyo?",
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}

	var decoded UseSkillInput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}

	if decoded.SkillName != input.SkillName {
		t.Fatalf("SkillName = %q, want %q", decoded.SkillName, input.SkillName)
	}
	if decoded.Input != input.Input {
		t.Fatalf("Input = %q, want %q", decoded.Input, input.Input)
	}
}

func TestUseSkillOutput_JSONRoundTrip(t *testing.T) {
	output := UseSkillOutput{
		SkillName:    "weather",
		Output:       "Sunny, 25°C",
		Instructions: "Check weather API for current conditions.",
	}

	data, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}

	var decoded UseSkillOutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}

	if decoded.SkillName != output.SkillName {
		t.Fatalf("SkillName = %q, want %q", decoded.SkillName, output.SkillName)
	}
	if decoded.Output != output.Output {
		t.Fatalf("Output = %q, want %q", decoded.Output, output.Output)
	}
	if decoded.Instructions != output.Instructions {
		t.Fatalf("Instructions = %q, want %q", decoded.Instructions, output.Instructions)
	}
}

func TestUseSkillOutput_OmitsEmptyInstructions(t *testing.T) {
	output := UseSkillOutput{
		SkillName: "test",
		Output:    "result",
	}

	data, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}

	// With omitempty, the instructions field should not appear.
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	if _, ok := raw["instructions"]; ok {
		t.Fatal("expected instructions to be omitted when empty")
	}
}
