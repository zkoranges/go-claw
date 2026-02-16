package policy

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAllowMCPTool_ExactMatch verifies exact agent+server+tool matching.
func TestAllowMCPTool_ExactMatch(t *testing.T) {
	yaml := `
mcp:
  default: deny
  rules:
    - agent: coder
      server: github
      tools: ["create_issue", "list_repos"]
    - agent: devops
      server: postgres
      tools: ["*"]
`
	policy, err := loadPolicyFromYAML(t, yaml)
	if err != nil {
		t.Fatalf("loadPolicyFromYAML failed: %v", err)
	}

	// Exact match: allowed
	if !policy.AllowMCPTool("coder", "github", "create_issue") {
		t.Error("expected coder/github/create_issue to be allowed")
	}

	// Exact match: allowed
	if !policy.AllowMCPTool("devops", "postgres", "query") {
		t.Error("expected devops/postgres/query (wildcard tools) to be allowed")
	}

	// No rule: denied
	if policy.AllowMCPTool("coder", "filesystem", "read_file") {
		t.Error("expected coder/filesystem/read_file to be denied (no rule)")
	}

	// Tool not in list: denied
	if policy.AllowMCPTool("coder", "github", "delete_repo") {
		t.Error("expected coder/github/delete_repo to be denied (not in tools list)")
	}
}

// TestAllowMCPTool_WildcardServer verifies wildcard server matching.
func TestAllowMCPTool_WildcardServer(t *testing.T) {
	yaml := `
mcp:
  default: deny
  rules:
    - agent: admin
      server: "*"
      tools: ["*"]
`
	policy, err := loadPolicyFromYAML(t, yaml)
	if err != nil {
		t.Fatalf("loadPolicyFromYAML failed: %v", err)
	}

	// Wildcard server allows any server
	if !policy.AllowMCPTool("admin", "github", "anything") {
		t.Error("expected admin/* to be allowed")
	}
	if !policy.AllowMCPTool("admin", "postgres", "query") {
		t.Error("expected admin/* to be allowed")
	}

	// Other agents denied
	if policy.AllowMCPTool("user", "github", "anything") {
		t.Error("expected user/github/* to be denied (no rule)")
	}
}

// TestAllowMCPTool_DefaultDeny verifies default-deny behavior.
func TestAllowMCPTool_DefaultDeny(t *testing.T) {
	yaml := `
mcp:
  default: deny
  rules: []
`
	policy, err := loadPolicyFromYAML(t, yaml)
	if err != nil {
		t.Fatalf("loadPolicyFromYAML failed: %v", err)
	}

	// Everything denied when no rules match
	if policy.AllowMCPTool("coder", "github", "anything") {
		t.Error("expected denial with empty rules and default deny")
	}
}

// TestAllowMCPTool_DefaultAllow verifies default-allow behavior.
func TestAllowMCPTool_DefaultAllow(t *testing.T) {
	yaml := `
mcp:
  default: allow
  rules:
    - agent: untrusted
      server: "*"
      tools: [] # explicit deny for this agent
`
	policy, err := loadPolicyFromYAML(t, yaml)
	if err != nil {
		t.Fatalf("loadPolicyFromYAML failed: %v", err)
	}

	// Default allow: allowed
	if !policy.AllowMCPTool("trusted", "github", "anything") {
		t.Error("expected default allow to permit trusted/github/anything")
	}

	// Explicit rule denies (empty tools list)
	if policy.AllowMCPTool("untrusted", "github", "anything") {
		t.Error("expected untrusted/* to be denied (explicit empty tools)")
	}
}

// TestAllowMCPTool_Specificity verifies most-specific rule wins.
func TestAllowMCPTool_Specificity(t *testing.T) {
	yaml := `
mcp:
  default: deny
  rules:
    - agent: "*"
      server: "*"
      tools: ["dangerous"]
    - agent: coder
      server: github
      tools: ["*"]
`
	policy, err := loadPolicyFromYAML(t, yaml)
	if err != nil {
		t.Fatalf("loadPolicyFromYAML failed: %v", err)
	}

	// Most-specific rule (coder+github) wins over generic rule
	if !policy.AllowMCPTool("coder", "github", "create_issue") {
		t.Error("expected coder/github/* to be allowed (more specific rule wins)")
	}

	// Generic rule allows dangerous from any server
	if !policy.AllowMCPTool("user", "filesystem", "dangerous") {
		t.Error("expected user/filesystem/dangerous to be allowed (generic rule)")
	}

	// Generic rule doesn't allow other tools
	if policy.AllowMCPTool("user", "filesystem", "safe") {
		t.Error("expected user/filesystem/safe to be denied (not in generic tools list)")
	}
}

// loadPolicyFromYAML is a test helper that creates a Policy from YAML.
func loadPolicyFromYAML(t *testing.T, yaml string) (Policy, error) {
	tmpDir := t.TempDir()
	policyFile := filepath.Join(tmpDir, "policy.yaml")
	if err := os.WriteFile(policyFile, []byte(yaml), 0644); err != nil {
		t.Fatalf("write policy file: %v", err)
	}
	return Load(policyFile)
}
