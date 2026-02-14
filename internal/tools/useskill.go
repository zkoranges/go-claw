package tools

// UseSkillInput is the input schema for the use_skill tool.
// The tool lets the LLM invoke a registered skill by name.
type UseSkillInput struct {
	// SkillName is the canonical name of the skill to invoke.
	SkillName string `json:"skill_name"`
	// Input is the free-form input to pass to the skill.
	Input string `json:"input,omitempty"`
}

// UseSkillOutput is the output schema for the use_skill tool.
type UseSkillOutput struct {
	// SkillName echoes which skill was invoked.
	SkillName string `json:"skill_name"`
	// Output is the skill's response.
	Output string `json:"output"`
	// Instructions contains the loaded SKILL.md instructions if available.
	Instructions string `json:"instructions,omitempty"`
}
