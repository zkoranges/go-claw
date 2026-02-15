package skills

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basket/go-claw/internal/persistence"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("readdir %s: %v", src, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	for _, ent := range entries {
		srcPath := filepath.Join(src, ent.Name())
		dstPath := filepath.Join(dst, ent.Name())
		if ent.IsDir() {
			copyDir(t, srcPath, dstPath)
			continue
		}
		b, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatalf("read %s: %v", srcPath, err)
		}
		if err := os.WriteFile(dstPath, b, 0o644); err != nil {
			t.Fatalf("write %s: %v", dstPath, err)
		}
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}

func initRepoFromTestdata(t *testing.T, testdataDir string) string {
	t.Helper()
	repoDir := t.TempDir()
	copyDir(t, testdataDir, repoDir)
	git(t, repoDir, "init", "-b", "main")
	git(t, repoDir, "config", "user.email", "test@example.com")
	git(t, repoDir, "config", "user.name", "Test")
	git(t, repoDir, "add", ".")
	git(t, repoDir, "commit", "-m", "initial")
	return repoDir
}

func openStore(t *testing.T, dir string) *persistence.Store {
	t.Helper()
	dbPath := filepath.Join(dir, "goclaw.db")
	store, err := persistence.Open(dbPath, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newTestInstaller(t *testing.T, homeDir string, store *persistence.Store) *Installer {
	t.Helper()
	return &Installer{
		installDir: filepath.Join(homeDir, "installed"),
		store:      store,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestInstall_ValidRepo(t *testing.T) {
	requireGit(t)
	ctx := context.Background()

	home := t.TempDir()
	store := openStore(t, home)
	inst := newTestInstaller(t, home, store)

	repo := initRepoFromTestdata(t, filepath.Join("testdata", "valid_repo"))
	if err := inst.Install(ctx, repo, ""); err != nil {
		t.Fatalf("Install: %v", err)
	}

	ownerRepo := "local-" + filepath.Base(repo)
	got, err := store.ListInstalledSkills(ctx)
	if err != nil {
		t.Fatalf("ListInstalledSkills: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 installed skill, got %d (%#v)", len(got), got)
	}
	if got[0].SkillID != ownerRepo {
		t.Fatalf("expected skill_id %q, got %q", ownerRepo, got[0].SkillID)
	}
	if got[0].Source != "local" {
		t.Fatalf("expected source local, got %q", got[0].Source)
	}
	if got[0].SourceURL != repo {
		t.Fatalf("expected source_url %q, got %q", repo, got[0].SourceURL)
	}

	if _, err := os.Stat(filepath.Join(home, "installed", ownerRepo, "SKILL.md")); err != nil {
		t.Fatalf("expected installed SKILL.md to exist: %v", err)
	}
}

func TestInstall_InvalidRepo_NoSkillMD(t *testing.T) {
	requireGit(t)
	ctx := context.Background()

	home := t.TempDir()
	store := openStore(t, home)
	inst := newTestInstaller(t, home, store)

	repo := initRepoFromTestdata(t, filepath.Join("testdata", "invalid_repo_no_skill_md"))
	if err := inst.Install(ctx, repo, ""); err == nil {
		t.Fatalf("expected install to fail without SKILL.md")
	}
}

func TestInstall_DuplicateName(t *testing.T) {
	requireGit(t)
	ctx := context.Background()

	home := t.TempDir()
	store := openStore(t, home)
	inst := newTestInstaller(t, home, store)

	repo := initRepoFromTestdata(t, filepath.Join("testdata", "valid_repo"))
	if err := inst.Install(ctx, repo, ""); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := inst.Install(ctx, repo, ""); err == nil {
		t.Fatalf("expected duplicate install to fail")
	}
}

func TestRemove_ExistingSkill(t *testing.T) {
	requireGit(t)
	ctx := context.Background()

	home := t.TempDir()
	store := openStore(t, home)
	inst := newTestInstaller(t, home, store)

	repo := initRepoFromTestdata(t, filepath.Join("testdata", "valid_repo"))
	if err := inst.Install(ctx, repo, ""); err != nil {
		t.Fatalf("Install: %v", err)
	}

	name := "local-" + filepath.Base(repo)
	if err := inst.Remove(ctx, name); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "installed", name)); err == nil {
		t.Fatalf("expected installed dir to be removed")
	}
	got, err := store.ListInstalledSkills(ctx)
	if err != nil {
		t.Fatalf("ListInstalledSkills: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 installed skills, got %d (%#v)", len(got), got)
	}
}

func TestRemove_NonExistent(t *testing.T) {
	ctx := context.Background()

	home := t.TempDir()
	store := openStore(t, home)
	inst := newTestInstaller(t, home, store)

	if err := inst.Remove(ctx, "does-not-exist"); err == nil {
		t.Fatalf("expected remove to fail for non-existent skill")
	}
}

func TestRemove_RejectsPathTraversalName(t *testing.T) {
	ctx := context.Background()

	home := t.TempDir()
	store := openStore(t, home)
	inst := newTestInstaller(t, home, store)

	victimDir := filepath.Join(home, "victim")
	if err := os.MkdirAll(victimDir, 0o755); err != nil {
		t.Fatalf("mkdir victim: %v", err)
	}
	if err := os.WriteFile(filepath.Join(victimDir, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatalf("write victim file: %v", err)
	}

	err := inst.Remove(ctx, "../victim")
	if err == nil || !strings.Contains(err.Error(), "invalid skill name") {
		t.Fatalf("expected invalid skill name error, got: %v", err)
	}
	if _, err := os.Stat(victimDir); err != nil {
		t.Fatalf("victim dir should remain intact: %v", err)
	}
}

func TestUpdate_PullsLatest(t *testing.T) {
	requireGit(t)
	ctx := context.Background()

	home := t.TempDir()
	store := openStore(t, home)
	inst := newTestInstaller(t, home, store)

	repo := initRepoFromTestdata(t, filepath.Join("testdata", "valid_repo"))
	if err := inst.Install(ctx, repo, ""); err != nil {
		t.Fatalf("Install: %v", err)
	}
	name := "local-" + filepath.Base(repo)

	// Update upstream and commit.
	skillPath := filepath.Join(repo, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(`---
name: valid-skill
description: Updated description
---

Updated body.
`), 0o644); err != nil {
		t.Fatalf("write upstream SKILL.md: %v", err)
	}
	git(t, repo, "add", "SKILL.md")
	git(t, repo, "commit", "-m", "update")

	if err := inst.Update(ctx, name); err != nil {
		t.Fatalf("Update: %v", err)
	}
	installedSkillMD := filepath.Join(home, "installed", name, "SKILL.md")
	b, err := os.ReadFile(installedSkillMD)
	if err != nil {
		t.Fatalf("read installed SKILL.md: %v", err)
	}
	if !strings.Contains(string(b), "Updated description") {
		t.Fatalf("expected updated SKILL.md to be installed; got:\n%s", string(b))
	}
}

// AUD-007: Verify that update failure preserves the original skill (no data loss).
func TestUpdate_CloneFailure_PreservesOriginal(t *testing.T) {
	requireGit(t)
	ctx := context.Background()

	home := t.TempDir()
	store := openStore(t, home)
	inst := newTestInstaller(t, home, store)

	repo := initRepoFromTestdata(t, filepath.Join("testdata", "valid_repo"))
	if err := inst.Install(ctx, repo, ""); err != nil {
		t.Fatalf("Install: %v", err)
	}
	name := "local-" + filepath.Base(repo)
	installedSkillMD := filepath.Join(home, "installed", name, "SKILL.md")

	// Read the original content.
	original, err := os.ReadFile(installedSkillMD)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}

	// Remove the upstream repo so the clone will fail during update.
	if err := os.RemoveAll(repo); err != nil {
		t.Fatalf("remove upstream: %v", err)
	}

	// Update should fail since the source repo no longer exists.
	if err := inst.Update(ctx, name); err == nil {
		t.Fatalf("expected update to fail when source is missing")
	}

	// Verify the original skill is still intact.
	after, err := os.ReadFile(installedSkillMD)
	if err != nil {
		t.Fatalf("installed SKILL.md should still exist after failed update: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("installed SKILL.md content changed after failed update")
	}
}

// AUD-027: Verify .git files (not just directories) are excluded during copy.
func TestCopyTreeExcludingGit_SkipsGitFile(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	// Create a regular file, a .git directory, and a .git file in a subdirectory.
	if err := os.WriteFile(filepath.Join(src, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatalf("write keep.txt: %v", err)
	}
	// .git directory at root (standard clone).
	if err := os.MkdirAll(filepath.Join(src, ".git", "objects"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, ".git", "HEAD"), []byte("ref: refs/heads/main"), 0o644); err != nil {
		t.Fatalf("write .git/HEAD: %v", err)
	}
	// Nested subdir with .git as a plain file (submodule/worktree).
	nested := filepath.Join(src, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, ".git"), []byte("gitdir: ../../.git/modules/nested"), 0o644); err != nil {
		t.Fatalf("write nested/.git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "code.go"), []byte("package nested"), 0o644); err != nil {
		t.Fatalf("write nested/code.go: %v", err)
	}

	if err := copyTreeExcludingGit(src, dst); err != nil {
		t.Fatalf("copyTreeExcludingGit: %v", err)
	}

	// keep.txt should be copied.
	if _, err := os.Stat(filepath.Join(dst, "keep.txt")); err != nil {
		t.Fatalf("expected keep.txt: %v", err)
	}
	// .git directory should NOT be copied.
	if _, err := os.Stat(filepath.Join(dst, ".git")); err == nil {
		t.Fatalf("expected .git dir to be excluded")
	}
	// nested/.git file should NOT be copied.
	if _, err := os.Stat(filepath.Join(dst, "nested", ".git")); err == nil {
		t.Fatalf("expected nested/.git file to be excluded")
	}
	// nested/code.go should be copied.
	if _, err := os.Stat(filepath.Join(dst, "nested", "code.go")); err != nil {
		t.Fatalf("expected nested/code.go: %v", err)
	}
}

func TestHasCanonicalFrontmatter(t *testing.T) {
	// AUD-004: TrimSpace should handle leading/trailing whitespace consistently.
	tests := []struct {
		name string
		data string
		want bool
	}{
		{"canonical", "---\nname: x\n---\n", true},
		{"canonical_crlf", "---\r\nname: x\r\n---\r\n", true},
		{"leading_whitespace", "  \n\t\n---\nname: x\n---\n", true},
		{"trailing_whitespace_only", "   \t  ", false},
		{"no_frontmatter", "name: x\n", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasCanonicalFrontmatter([]byte(tt.data))
			if got != tt.want {
				t.Fatalf("hasCanonicalFrontmatter(%q) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}

func TestUpdate_RejectsPathTraversalName(t *testing.T) {
	ctx := context.Background()

	home := t.TempDir()
	store := openStore(t, home)
	inst := newTestInstaller(t, home, store)

	victimDir := filepath.Join(home, "victim")
	if err := os.MkdirAll(victimDir, 0o755); err != nil {
		t.Fatalf("mkdir victim: %v", err)
	}

	err := inst.Update(ctx, "../victim")
	if err == nil || !strings.Contains(err.Error(), "invalid skill name") {
		t.Fatalf("expected invalid skill name error, got: %v", err)
	}
	if _, err := os.Stat(victimDir); err != nil {
		t.Fatalf("victim dir should remain intact: %v", err)
	}
}
