package legacy

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/policy"
	"gopkg.in/yaml.v3"
)

type Skill struct {
	// Required (Agent Skills spec)
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	// Optional (Agent Skills spec)
	License       string         `yaml:"license,omitempty"`
	Compatibility string         `yaml:"compatibility,omitempty"`
	AllowedTools  string         `yaml:"allowed-tools,omitempty"`
	Metadata      map[string]any `yaml:"metadata,omitempty"`

	// GoClaw V1 compat (top-level shorthand)
	Bins   []string `yaml:"bins,omitempty"`
	Script string   `yaml:"script,omitempty"`

	// Parsed from markdown body (not from YAML)
	Instructions string `yaml:"-"`

	// Resolved at load time
	SourceDir string `yaml:"-"`
	Source    string `yaml:"-"` // "project", "user", "github", "builtin"
}

func ParseSkillMD(data []byte) (Skill, error) {
	yamlBytes, markdownBody, err := extractFrontmatter(data)
	if err != nil {
		return Skill{}, err
	}

	// Stage 1: canonical frontmatter.
	if len(yamlBytes) > 0 {
		var skill Skill
		if err := yaml.Unmarshal(yamlBytes, &skill); err != nil {
			return Skill{}, fmt.Errorf("parse frontmatter yaml: %w", err)
		}
		skill.Name = strings.TrimSpace(skill.Name)
		skill.Description = strings.TrimSpace(skill.Description)
		skill.Script = strings.TrimSpace(skill.Script)
		skill.Instructions = strings.TrimSpace(markdownBody)
		fillBinsFromMetadata(&skill)
		if skill.Name == "" {
			return Skill{}, fmt.Errorf("missing skill name")
		}
		return skill, nil
	}

	// Stage 2: V1 plain YAML (backward compat).
	var skill Skill
	if err := yaml.Unmarshal(data, &skill); err == nil && strings.TrimSpace(skill.Name) != "" {
		skill.Name = strings.TrimSpace(skill.Name)
		skill.Description = strings.TrimSpace(skill.Description)
		skill.Script = strings.TrimSpace(skill.Script)
		fillBinsFromMetadata(&skill)
		return skill, nil
	}

	// Stage 3: markdown fallback (regex + fenced code block).
	skill = Skill{}
	text := string(data)
	nameRe := regexp.MustCompile(`(?m)^name:\s*(.+)\s*$`)
	descRe := regexp.MustCompile(`(?m)^description:\s*(.+)\s*$`)
	if m := nameRe.FindStringSubmatch(text); len(m) == 2 {
		skill.Name = strings.TrimSpace(m[1])
	}
	if m := descRe.FindStringSubmatch(text); len(m) == 2 {
		skill.Description = strings.TrimSpace(m[1])
	}

	script, err := extractFencedScript(text)
	if err != nil {
		return Skill{}, err
	}
	skill.Script = script
	if skill.Name == "" {
		return Skill{}, fmt.Errorf("missing skill name")
	}
	return skill, nil
}

func extractFrontmatter(data []byte) (yamlBytes []byte, markdownBody string, err error) {
	// Detect a canonical YAML frontmatter block:
	// - first line is `---`
	// - second `---` line terminates the block
	//
	// Anything after the terminating delimiter is markdown body.
	s := string(data)
	if s == "" {
		return nil, "", nil
	}

	// Read first line (without newline).
	firstLineEnd := strings.IndexByte(s, '\n')
	firstLine := s
	restStart := len(s)
	if firstLineEnd >= 0 {
		firstLine = s[:firstLineEnd]
		restStart = firstLineEnd + 1
	}
	firstLine = strings.TrimSpace(strings.TrimSuffix(firstLine, "\r"))
	if firstLine != "---" {
		return nil, "", nil
	}

	i := restStart
	for {
		if i > len(s) {
			break
		}

		nextNL := strings.IndexByte(s[i:], '\n')
		line := ""
		next := len(s)
		if nextNL >= 0 {
			line = s[i : i+nextNL]
			next = i + nextNL + 1
		} else {
			line = s[i:]
			next = len(s)
		}
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if trimmed == "---" {
			return []byte(s[restStart:i]), s[next:], nil
		}

		if next == len(s) {
			break
		}
		i = next
	}

	// No terminating delimiter found. This is an explicit error — the author
	// started a frontmatter block but never closed it.
	return nil, "", fmt.Errorf("unclosed frontmatter: opening --- found but no closing ---")
}

func fillBinsFromMetadata(skill *Skill) {
	if skill == nil || len(skill.Bins) > 0 || len(skill.Metadata) == 0 {
		return
	}

	openclaw, ok := skill.Metadata["openclaw"].(map[string]any)
	if !ok {
		return
	}
	requires, ok := openclaw["requires"].(map[string]any)
	if !ok {
		return
	}
	raw, ok := requires["bins"]
	if !ok || raw == nil {
		return
	}

	var bins []string
	switch v := raw.(type) {
	case []string:
		bins = append(bins, v...)
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				bins = append(bins, trimmed)
			}
		}
	}
	if len(bins) > 0 {
		skill.Bins = bins
	}
}

func extractFencedScript(text string) (string, error) {
	re := regexp.MustCompile("(?s)```\\w*\\s*(.*?)```")
	m := re.FindStringSubmatch(text)
	if len(m) != 2 {
		return "", fmt.Errorf("missing script section")
	}
	return strings.TrimSpace(m[1]), nil
}

func CheckBins(bins []string) map[string]bool {
	out := make(map[string]bool, len(bins))
	for _, b := range bins {
		_, err := exec.LookPath(b)
		out[b] = err == nil
	}
	return out
}

type Runner struct {
	WorkspaceDir     string
	ConfirmDangerous func(command string) bool
	Policy           policy.Checker
}

func (r Runner) Run(ctx context.Context, skill Skill) (string, error) {
	if strings.TrimSpace(skill.Script) == "" {
		return "", fmt.Errorf("empty script")
	}
	if r.Policy == nil || !r.Policy.AllowCapability("legacy.run") {
		pv := ""
		if r.Policy != nil {
			pv = r.Policy.PolicyVersion()
		}
		audit.Record("deny", "legacy.run", "missing_capability", pv, skill.Name)
		return "", fmt.Errorf("policy denied capability %q", "legacy.run")
	}
	audit.Record("allow", "legacy.run", "capability_granted", r.Policy.PolicyVersion(), skill.Name)
	if r.WorkspaceDir == "" {
		r.WorkspaceDir = "./workspace"
	}
	if err := os.MkdirAll(r.WorkspaceDir, 0o755); err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}

	if isDangerous(skill.Script) {
		if !r.Policy.AllowCapability("legacy.dangerous") {
			audit.Record("deny", "legacy.dangerous", "missing_capability", r.Policy.PolicyVersion(), skill.Name)
			return "", fmt.Errorf("policy denied capability %q", "legacy.dangerous")
		}
		audit.Record("allow", "legacy.dangerous", "capability_granted", r.Policy.PolicyVersion(), skill.Name)
		confirm := r.ConfirmDangerous
		if confirm == nil || !confirm(skill.Script) {
			audit.Record("deny", "legacy.dangerous", "approval_denied", r.Policy.PolicyVersion(), skill.Name)
			return "", fmt.Errorf("dangerous command blocked by policy")
		}
		audit.Record("allow", "legacy.dangerous", "approval_granted", r.Policy.PolicyVersion(), skill.Name)
	}
	if err := enforceWriteRestriction(skill.Script); err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, "/bin/sh", "-lc", skill.Script)
	cmd.Dir = r.WorkspaceDir
	cmd.Env = buildMinimalEnv(r.WorkspaceDir, skill)

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("legacy skill run failed: %w", err)
	}
	return out.String(), nil
}

func isDangerous(script string) bool {
	lower := strings.ToLower(script)
	dangerous := []string{
		"rm ",
		"rm\t",
		"dd ",
		"mkfs",
	}
	for _, pattern := range dangerous {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

var (
	reRedirectOutside = regexp.MustCompile(`(?m)(>>|>)\s*(/|~/?|\.\.)`)
	reTeeOutside      = regexp.MustCompile(`(?m)\btee\b[^\n]*\s+(/|~/?|\.\.)`)

	// Additional file-writing commands targeting absolute or home paths.
	reWriteCommands = []*regexp.Regexp{
		regexp.MustCompile(`(?m)\bcp\s+[^\n]*(/)`),
		regexp.MustCompile(`(?m)\bmv\s+[^\n]*(/)`),
		regexp.MustCompile(`(?m)\binstall\s+[^\n]*(/)`),
		regexp.MustCompile(`(?m)\bcurl\b[^\n]*\s+-o\s*(/|~/?)`),
		regexp.MustCompile(`(?m)\bwget\b[^\n]*\s+-o\s*(/|~/?)`),
		regexp.MustCompile(`(?m)\bdd\b[^\n]*\bof=(/|~/?)`),
	}

	// Command substitution and eval patterns.
	reSubstitutionPatterns = []*regexp.Regexp{
		regexp.MustCompile("`"),             // backtick substitution
		regexp.MustCompile(`\$\(`),          // $( substitution
		regexp.MustCompile(`(?m)\beval\s+`), // eval command
	}

	// Interpreter one-liner patterns (can perform arbitrary I/O).
	reInterpreterOneLiners = []*regexp.Regexp{
		regexp.MustCompile(`(?m)\bpython[23]?\s+-c\s`),
		regexp.MustCompile(`(?m)\bruby\s+-e\s`),
		regexp.MustCompile(`(?m)\bperl\s+-e\s`),
		regexp.MustCompile(`(?m)\bnode\s+-e\s`),
		regexp.MustCompile(`(?m)\bgo\s+run\s`),
		regexp.MustCompile(`(?m)\bphp\s+-r\s`),
		regexp.MustCompile(`(?m)\blua\s+-e\s`),
	}
)

// enforceWriteRestriction is a best-effort heuristic guard against scripts that
// attempt to write outside the workspace directory. It is NOT a security boundary
// — without an OS-level sandbox (namespaces, seccomp, etc.) a determined user can
// bypass these checks. The function exists as defense-in-depth to catch accidental
// or unsophisticated escape attempts. Gate: requires the "legacy.run" capability
// to reach this point; see Runner.Run.
func enforceWriteRestriction(script string) error {
	lower := strings.ToLower(script)

	// Redirects: > /foo, >> /foo, > ~/foo, > ../foo
	if reRedirectOutside.MatchString(lower) {
		return fmt.Errorf("write outside workspace denied")
	}

	// tee writes: tee /foo, tee ../foo, tee ~/foo
	if reTeeOutside.MatchString(lower) {
		return fmt.Errorf("write outside workspace denied")
	}

	// Path traversal (can target outside workspace): ../ or ..\ (Windows style).
	if strings.Contains(lower, "../") || strings.Contains(lower, `..\\`) {
		return fmt.Errorf("path traversal outside workspace denied")
	}

	// File-writing commands targeting absolute/home paths.
	for _, pat := range reWriteCommands {
		if pat.MatchString(lower) {
			return fmt.Errorf("write outside workspace denied")
		}
	}

	// Command substitution / eval — can hide arbitrary writes.
	for _, pat := range reSubstitutionPatterns {
		if pat.MatchString(lower) {
			return fmt.Errorf("command substitution or eval denied in legacy skill")
		}
	}

	// Interpreter one-liners that can perform arbitrary I/O.
	for _, pat := range reInterpreterOneLiners {
		if pat.MatchString(lower) {
			return fmt.Errorf("inline interpreter execution denied in legacy skill")
		}
	}

	return nil
}

// buildMinimalEnv constructs a minimal environment for legacy skill execution.
// Instead of inheriting the full host environment (which may contain secrets,
// tokens, or other sensitive values), only a safe allowlist of variables is
// forwarded. Skills that need additional environment variables must declare them
// in metadata.openclaw.requires.env.
func buildMinimalEnv(workspaceDir string, skill Skill) []string {
	ws := filepath.Clean(workspaceDir)
	env := []string{
		"HOME=" + ws,
		"WORKSPACE=" + ws,
	}

	// Safe allowlist: forward from host if set.
	safeVars := []string{"PATH", "TERM", "LANG", "LC_ALL", "GOCLAW_HOME", "USER"}
	for _, key := range safeVars {
		if val, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+val)
		}
	}

	// Forward any env vars explicitly declared in skill metadata.
	for _, key := range requiredEnvFromMetadata(skill) {
		if val, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+val)
		}
	}

	return env
}

// requiredEnvFromMetadata extracts the list of environment variable names from
// metadata.openclaw.requires.env ([]string).
func requiredEnvFromMetadata(skill Skill) []string {
	if len(skill.Metadata) == 0 {
		return nil
	}
	openclaw, ok := skill.Metadata["openclaw"].(map[string]any)
	if !ok {
		return nil
	}
	requires, ok := openclaw["requires"].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := requires["env"]
	if !ok || raw == nil {
		return nil
	}

	var envVars []string
	switch v := raw.(type) {
	case []string:
		envVars = append(envVars, v...)
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				envVars = append(envVars, trimmed)
			}
		}
	}
	return envVars
}
