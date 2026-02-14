package skills

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeSkillMD(t *testing.T, dir string, contents string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(contents), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

func TestLoadAll_Precedence(t *testing.T) {
	ctx := context.Background()
	projectDir := t.TempDir()
	userDir := t.TempDir()
	installedDir := t.TempDir()

	writeSkillMD(t, filepath.Join(projectDir, "dupe"), `---
name: dupe
description: from project
---

Project instructions.
`)
	writeSkillMD(t, filepath.Join(userDir, "dupe"), `---
name: dupe
description: from user
---

User instructions.
`)
	writeSkillMD(t, filepath.Join(installedDir, "dupe"), `---
name: dupe
description: from installed
---

Installed instructions.
`)

	l := &Loader{
		projectDir:   projectDir,
		userDir:      userDir,
		installedDir: installedDir,
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	loaded, err := l.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d (%#v)", len(loaded), loaded)
	}
	if loaded[0].Source != "project" {
		t.Fatalf("expected source project, got %q", loaded[0].Source)
	}
	if loaded[0].Skill.Description != "from project" {
		t.Fatalf("expected project description, got %q", loaded[0].Skill.Description)
	}
}

func TestLoadAll_Eligibility_MissingBin(t *testing.T) {
	ctx := context.Background()
	projectDir := t.TempDir()

	writeSkillMD(t, filepath.Join(projectDir, "missingbin"), `---
name: missingbin
description: requires missing bin
bins: ["definitely-not-real-bin-xyz-123"]
---
Instructions.
`)

	l := &Loader{
		projectDir: projectDir,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	loaded, err := l.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if loaded[0].Eligible {
		t.Fatalf("expected skill to be ineligible")
	}
	want := "missing bin: definitely-not-real-bin-xyz-123"
	found := false
	for _, m := range loaded[0].Missing {
		if m == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected missing to include %q; got %#v", want, loaded[0].Missing)
	}
}

func TestLoadAll_Eligibility_MissingEnv(t *testing.T) {
	ctx := context.Background()
	projectDir := t.TempDir()

	t.Setenv("GOCLAW_TEST_MISSING_ENV_123", "")

	writeSkillMD(t, filepath.Join(projectDir, "missingenv"), `---
name: missingenv
description: requires missing env
metadata:
  openclaw:
    requires:
      env: ["GOCLAW_TEST_MISSING_ENV_123"]
---
Instructions.
`)

	l := &Loader{
		projectDir: projectDir,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	loaded, err := l.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if loaded[0].Eligible {
		t.Fatalf("expected skill to be ineligible")
	}
	want := "missing env: GOCLAW_TEST_MISSING_ENV_123"
	found := false
	for _, m := range loaded[0].Missing {
		if m == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected missing to include %q; got %#v", want, loaded[0].Missing)
	}
}

func TestLoadAll_Eligibility_WrongOS(t *testing.T) {
	ctx := context.Background()
	projectDir := t.TempDir()

	allowed := "linux"
	if runtime.GOOS == "linux" {
		allowed = "darwin"
	}

	writeSkillMD(t, filepath.Join(projectDir, "wrongos"), `---
name: wrongos
description: wrong os
metadata:
  openclaw:
    os: ["`+allowed+`"]
---
Instructions.
`)

	l := &Loader{
		projectDir: projectDir,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	loaded, err := l.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if loaded[0].Eligible {
		t.Fatalf("expected skill to be ineligible")
	}
	want := "unsupported os: " + runtime.GOOS
	found := false
	for _, m := range loaded[0].Missing {
		if m == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected missing to include %q; got %#v", want, loaded[0].Missing)
	}
}

func TestLoadAll_EmptyDirs(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	l := &Loader{
		projectDir:   filepath.Join(tmp, "nope_project"),
		userDir:      filepath.Join(tmp, "nope_user"),
		installedDir: filepath.Join(tmp, "nope_installed"),
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	loaded, err := l.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected empty slice, got %d", len(loaded))
	}
}

func TestLoadOne_RejectsOversizedSkillMD(t *testing.T) {
	// AUD-003: SKILL.md files larger than 1 MiB must be rejected.
	ctx := context.Background()
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "big")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write a file slightly over the 1 MiB limit.
	oversize := make([]byte, (1<<20)+1)
	for i := range oversize {
		oversize[i] = 'x'
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), oversize, 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	l := &Loader{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	_, err := l.LoadOne(ctx, skillDir, "project")
	if err == nil {
		t.Fatalf("expected error for oversized SKILL.md")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected 'too large' in error; got: %v", err)
	}
}

func TestLoadOne_InvalidSkillMD(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "bad")

	writeSkillMD(t, skillDir, `---
name: [unclosed
description: bad yaml
---

Body.
`)

	l := &Loader{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	_, err := l.LoadOne(ctx, skillDir, "project")
	if err == nil {
		t.Fatalf("expected error for invalid SKILL.md")
	}
}

// AUD-005: CanonicalSkillKey returns consistent normalized keys.
func TestCanonicalSkillKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"MySkill", "myskill"},
		{"  MySkill  ", "myskill"},
		{"ALLCAPS", "allcaps"},
		{"", ""},
		{"  ", ""},
		{"already-lower", "already-lower"},
	}
	for _, tt := range tests {
		got := CanonicalSkillKey(tt.input)
		if got != tt.want {
			t.Errorf("CanonicalSkillKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// AUD-028: Symlinked skill directories produce a warning and are skipped.
func TestLoadAll_SymlinkWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test not supported on windows")
	}

	ctx := context.Background()
	projectDir := t.TempDir()

	// Create a real skill directory with a SKILL.md.
	realDir := filepath.Join(t.TempDir(), "real-skill")
	writeSkillMD(t, realDir, `---
name: real-skill
description: real skill
---
Instructions.
`)

	// Create a symlink to it inside the project dir.
	linkPath := filepath.Join(projectDir, "linked-skill")
	if err := os.Symlink(realDir, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	l := &Loader{
		projectDir: projectDir,
		logger:     logger,
	}
	loaded, err := l.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	// The symlink should NOT be loaded as a skill.
	if len(loaded) != 0 {
		t.Fatalf("expected 0 skills (symlink should be skipped), got %d", len(loaded))
	}
	// The warning should have been emitted.
	if !strings.Contains(buf.String(), "symlinks are not followed") {
		t.Fatalf("expected symlink warning in logs; got: %s", buf.String())
	}
}
