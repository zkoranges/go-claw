package legacy_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basket/go-claw/internal/policy"
	"github.com/basket/go-claw/internal/sandbox/legacy"
)

func TestParseSkillSubsetAndBinChecks(t *testing.T) {
	// [SPEC: SPEC-COMPAT-SKILLMD-1, SPEC-COMPAT-SKILLMD-2] [PDR: V-11]
	skillDoc := `name: test-skill
description: A minimal test skill
bins: ["sh","definitely-not-real-bin"]

script: |
  echo "hello from skill"
`
	skill, err := legacy.ParseSkillMD([]byte(skillDoc))
	if err != nil {
		t.Fatalf("parse skill: %v", err)
	}
	if skill.Name != "test-skill" {
		t.Fatalf("unexpected name: %s", skill.Name)
	}
	if len(skill.Bins) != 2 {
		t.Fatalf("unexpected bins: %#v", skill.Bins)
	}
	found := legacy.CheckBins(skill.Bins)
	if !found["sh"] {
		t.Fatalf("expected sh to be found in PATH")
	}
	if found["definitely-not-real-bin"] {
		t.Fatalf("expected fake bin to be missing")
	}
}

func TestRunner_DangerousCommandRequiresConfirmation(t *testing.T) {
	// [SPEC: SPEC-SEC-POLICY-1] [PDR: V-18]
	r := legacy.Runner{
		WorkspaceDir: t.TempDir(),
		Policy: policy.Policy{
			AllowCapabilities: []string{"legacy.run", "legacy.dangerous"},
		},
		ConfirmDangerous: func(command string) bool {
			return false
		},
	}
	_, err := r.Run(context.Background(), legacy.Skill{
		Name:   "danger",
		Script: "rm -rf /tmp/nope",
	})
	if err == nil {
		t.Fatalf("expected dangerous command to be blocked")
	}
}

func TestRunner_ExecutesSafeScriptInsideWorkspace(t *testing.T) {
	// [SPEC: SPEC-COMPAT-SKILLMD-1] [PDR: V-11]
	workspace := t.TempDir()
	r := legacy.Runner{
		WorkspaceDir: workspace,
		Policy: policy.Policy{
			AllowCapabilities: []string{"legacy.run"},
		},
		ConfirmDangerous: func(command string) bool {
			return true
		},
	}
	_, err := r.Run(context.Background(), legacy.Skill{
		Name:   "safe",
		Script: "echo hello > output.txt",
	})
	if err != nil {
		t.Fatalf("run safe script: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(workspace, "output.txt"))
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if string(data) == "" {
		t.Fatalf("expected output file to contain data")
	}
}

func TestRunner_WriteOutsideWorkspaceDenied_PathTraversal(t *testing.T) {
	workspace := t.TempDir()
	parent := filepath.Dir(workspace)
	outside := filepath.Join(parent, "outside.txt")
	_ = os.Remove(outside)

	r := legacy.Runner{
		WorkspaceDir: workspace,
		Policy: policy.Policy{
			AllowCapabilities: []string{"legacy.run"},
		},
		ConfirmDangerous: func(command string) bool { return true },
	}
	_, err := r.Run(context.Background(), legacy.Skill{
		Name:   "escape",
		Script: "echo nope > ../outside.txt",
	})
	if err == nil {
		t.Fatalf("expected path traversal write to be blocked")
	}
	if _, statErr := os.Stat(outside); statErr == nil {
		t.Fatalf("expected outside file not to be created")
	}
}

func TestRunner_WriteOutsideWorkspaceDenied_AbsolutePath(t *testing.T) {
	r := legacy.Runner{
		WorkspaceDir: t.TempDir(),
		Policy: policy.Policy{
			AllowCapabilities: []string{"legacy.run"},
		},
		ConfirmDangerous: func(command string) bool { return true },
	}
	_, err := r.Run(context.Background(), legacy.Skill{
		Name:   "abs",
		Script: "echo nope > /tmp/outside.txt",
	})
	if err == nil {
		t.Fatalf("expected absolute path write to be blocked")
	}
}

func TestRunner_DefaultDenyWhenCapabilityMissing(t *testing.T) {
	r := legacy.Runner{
		WorkspaceDir: t.TempDir(),
		Policy:       policy.Default(),
	}
	_, err := r.Run(context.Background(), legacy.Skill{
		Name:   "safe",
		Script: "echo hello",
	})
	if err == nil {
		t.Fatalf("expected policy denial when legacy.run capability missing")
	}
}

func TestParseFrontmatter_Canonical(t *testing.T) {
	skillDoc := `---
name: fm-skill
description: canonical frontmatter
---

## Instructions
Do the thing.
`
	skill, err := legacy.ParseSkillMD([]byte(skillDoc))
	if err != nil {
		t.Fatalf("parse skill: %v", err)
	}
	if skill.Name != "fm-skill" {
		t.Fatalf("unexpected name: %q", skill.Name)
	}
	if skill.Description != "canonical frontmatter" {
		t.Fatalf("unexpected description: %q", skill.Description)
	}
	if skill.Instructions != "## Instructions\nDo the thing." {
		t.Fatalf("unexpected instructions: %q", skill.Instructions)
	}
}

func TestParseFrontmatter_WithMetadata(t *testing.T) {
	skillDoc := `---
name: meta-skill
description: metadata example
bins: ["sh"]
metadata:
  openclaw:
    requires:
      bins: ["python3"]
      env: ["META_TEST_ENV"]
---

Instructions.
`
	skill, err := legacy.ParseSkillMD([]byte(skillDoc))
	if err != nil {
		t.Fatalf("parse skill: %v", err)
	}
	if skill.Name != "meta-skill" {
		t.Fatalf("unexpected name: %q", skill.Name)
	}
	if len(skill.Bins) != 1 || skill.Bins[0] != "sh" {
		t.Fatalf("expected top-level bins to win; got: %#v", skill.Bins)
	}
	if skill.Metadata == nil {
		t.Fatalf("expected metadata to be parsed")
	}
	openclaw, ok := skill.Metadata["openclaw"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata.openclaw to be a map; got: %#v", skill.Metadata["openclaw"])
	}
	requires, ok := openclaw["requires"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata.openclaw.requires to be a map; got: %#v", openclaw["requires"])
	}
	env, ok := requires["env"].([]any)
	if !ok || len(env) != 1 || env[0] != "META_TEST_ENV" {
		t.Fatalf("unexpected metadata.openclaw.requires.env: %#v", requires["env"])
	}
}

func TestParseFrontmatter_NoBody(t *testing.T) {
	skillDoc := `---
name: fm-only
description: no body
---
`
	skill, err := legacy.ParseSkillMD([]byte(skillDoc))
	if err != nil {
		t.Fatalf("parse skill: %v", err)
	}
	if skill.Instructions != "" {
		t.Fatalf("expected empty instructions; got: %q", skill.Instructions)
	}
}

func TestParseFrontmatter_NoFrontmatter(t *testing.T) {
	skillDoc := `name: v1-skill
description: v1 plain yaml
bins: ["sh"]

script: |
  echo "hello"
`
	skill, err := legacy.ParseSkillMD([]byte(skillDoc))
	if err != nil {
		t.Fatalf("parse skill: %v", err)
	}
	if skill.Name != "v1-skill" {
		t.Fatalf("unexpected name: %q", skill.Name)
	}
	if skill.Script == "" {
		t.Fatalf("expected script to be parsed for v1 yaml")
	}
}

func TestParseFrontmatter_MarkdownFallback(t *testing.T) {
	skillDoc := "# Skill\n" +
		"name: fallback-skill\n" +
		"description: markdown fallback\n" +
		"\n" +
		"```sh\n" +
		"echo hello\n" +
		"```\n"
	skill, err := legacy.ParseSkillMD([]byte(skillDoc))
	if err != nil {
		t.Fatalf("parse skill: %v", err)
	}
	if skill.Name != "fallback-skill" {
		t.Fatalf("unexpected name: %q", skill.Name)
	}
	if skill.Description != "markdown fallback" {
		t.Fatalf("unexpected description: %q", skill.Description)
	}
	if skill.Script != "echo hello" {
		t.Fatalf("unexpected script: %q", skill.Script)
	}
}

func TestParseFrontmatter_UnclosedDelimiter(t *testing.T) {
	// AUD-001: opening --- with no closing --- must return an error.
	skillDoc := `---
name: broken
description: unclosed frontmatter
`
	_, err := legacy.ParseSkillMD([]byte(skillDoc))
	if err == nil {
		t.Fatalf("expected error for unclosed frontmatter")
	}
	if got := err.Error(); got != "unclosed frontmatter: opening --- found but no closing ---" {
		t.Fatalf("unexpected error message: %q", got)
	}
}

func TestParseFrontmatter_MarkdownFallback_PythonFence(t *testing.T) {
	// AUD-002: fenced code blocks with non-bash language hints should be matched.
	skillDoc := "# Skill\n" +
		"name: python-skill\n" +
		"description: python fallback\n" +
		"\n" +
		"```python\n" +
		"print('hello')\n" +
		"```\n"
	skill, err := legacy.ParseSkillMD([]byte(skillDoc))
	if err != nil {
		t.Fatalf("parse skill: %v", err)
	}
	if skill.Name != "python-skill" {
		t.Fatalf("unexpected name: %q", skill.Name)
	}
	if skill.Script != "print('hello')" {
		t.Fatalf("unexpected script: %q", skill.Script)
	}
}

func TestParseFrontmatter_BinsFromMetadata(t *testing.T) {
	skillDoc := `---
name: bins-from-meta
description: uses metadata bins
metadata:
  openclaw:
    requires:
      bins: ["sh","python3"]
---

Instructions.
`
	skill, err := legacy.ParseSkillMD([]byte(skillDoc))
	if err != nil {
		t.Fatalf("parse skill: %v", err)
	}
	if len(skill.Bins) != 2 || skill.Bins[0] != "sh" || skill.Bins[1] != "python3" {
		t.Fatalf("expected bins copied from metadata; got: %#v", skill.Bins)
	}
}

// ---------------------------------------------------------------------------
// AUD-016: enforceWriteRestriction additional patterns
// ---------------------------------------------------------------------------

func TestEnforceWriteRestriction_WriteCommands(t *testing.T) {
	// Each subtest verifies that a file-writing command targeting an absolute
	// path is caught by enforceWriteRestriction (via Runner.Run).
	r := legacy.Runner{
		WorkspaceDir:     t.TempDir(),
		Policy:           policy.Policy{AllowCapabilities: []string{"legacy.run", "legacy.dangerous"}},
		ConfirmDangerous: func(string) bool { return true },
	}
	cases := []struct {
		name   string
		script string
	}{
		{"cp_abs", "cp file.txt /tmp/stolen.txt"},
		{"mv_abs", "mv file.txt /tmp/stolen.txt"},
		{"install_abs", "install -m 755 file /usr/local/bin/x"},
		{"curl_o_abs", "curl -o /tmp/out http://example.com"},
		{"wget_O_abs", "wget -O /tmp/out http://example.com"},
		{"dd_of_abs", "dd if=file of=/dev/sda"},
		{"curl_o_home", "curl -o ~/out http://example.com"},
		{"wget_O_home", "wget -O ~/out http://example.com"},
		{"dd_of_home", "dd if=file of=~/out"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Run(context.Background(), legacy.Skill{
				Name:   tc.name,
				Script: tc.script,
			})
			if err == nil {
				t.Fatalf("expected write command %q to be blocked", tc.script)
			}
		})
	}
}

func TestEnforceWriteRestriction_CommandSubstitution(t *testing.T) {
	r := legacy.Runner{
		WorkspaceDir:     t.TempDir(),
		Policy:           policy.Policy{AllowCapabilities: []string{"legacy.run"}},
		ConfirmDangerous: func(string) bool { return true },
	}
	cases := []struct {
		name   string
		script string
	}{
		{"backtick", "echo `whoami`"},
		{"dollar_paren", "echo $(whoami)"},
		{"eval", "eval echo hello"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Run(context.Background(), legacy.Skill{
				Name:   tc.name,
				Script: tc.script,
			})
			if err == nil {
				t.Fatalf("expected command substitution %q to be blocked", tc.script)
			}
		})
	}
}

func TestEnforceWriteRestriction_InterpreterOneLiners(t *testing.T) {
	r := legacy.Runner{
		WorkspaceDir:     t.TempDir(),
		Policy:           policy.Policy{AllowCapabilities: []string{"legacy.run"}},
		ConfirmDangerous: func(string) bool { return true },
	}
	cases := []struct {
		name   string
		script string
	}{
		{"python_c", `python -c "import os; os.system('id')"`},
		{"python3_c", `python3 -c "print('hi')"`},
		{"ruby_e", `ruby -e "puts 'hi'"`},
		{"perl_e", `perl -e "print 'hi'"`},
		{"node_e", `node -e "console.log('hi')"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Run(context.Background(), legacy.Skill{
				Name:   tc.name,
				Script: tc.script,
			})
			if err == nil {
				t.Fatalf("expected interpreter one-liner %q to be blocked", tc.script)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// AUD-017: minimal safe environment — host env vars not inherited
// ---------------------------------------------------------------------------

func TestRunner_MinimalEnv_HostVarsNotInherited(t *testing.T) {
	// Set a unique env var on the host and verify it is NOT visible to the skill.
	const sentinel = "GOCLAW_TEST_SENTINEL_AUD017"
	t.Setenv(sentinel, "leaked")

	workspace := t.TempDir()
	r := legacy.Runner{
		WorkspaceDir:     workspace,
		Policy:           policy.Policy{AllowCapabilities: []string{"legacy.run"}},
		ConfirmDangerous: func(string) bool { return true },
	}

	out, err := r.Run(context.Background(), legacy.Skill{
		Name:   "env-check",
		Script: "printenv " + sentinel + " || true",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "leaked") {
		t.Fatalf("host env var %s leaked into legacy skill environment", sentinel)
	}
}

func TestRunner_MinimalEnv_SafeVarsPresent(t *testing.T) {
	// Verify that PATH is forwarded (required for the shell to work).
	workspace := t.TempDir()
	r := legacy.Runner{
		WorkspaceDir:     workspace,
		Policy:           policy.Policy{AllowCapabilities: []string{"legacy.run"}},
		ConfirmDangerous: func(string) bool { return true },
	}
	out, err := r.Run(context.Background(), legacy.Skill{
		Name:   "path-check",
		Script: "printenv PATH",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("expected PATH to be set in skill environment")
	}
}

func TestRunner_MinimalEnv_RequiredEnvForwarded(t *testing.T) {
	// A skill that declares a required env var in metadata should receive it.
	const envKey = "GOCLAW_TEST_REQUIRED_VAR"
	t.Setenv(envKey, "present")

	workspace := t.TempDir()
	r := legacy.Runner{
		WorkspaceDir:     workspace,
		Policy:           policy.Policy{AllowCapabilities: []string{"legacy.run"}},
		ConfirmDangerous: func(string) bool { return true },
	}

	out, err := r.Run(context.Background(), legacy.Skill{
		Name:   "meta-env",
		Script: "printenv " + envKey,
		Metadata: map[string]any{
			"openclaw": map[string]any{
				"requires": map[string]any{
					"env": []any{envKey},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "present" {
		t.Fatalf("expected required env var to be forwarded; got: %q", out)
	}
}
