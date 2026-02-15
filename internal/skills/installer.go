package skills

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/sandbox/legacy"
)

type Installer struct {
	installDir string // $GOCLAW_HOME/installed/
	store      *persistence.Store
	logger     *slog.Logger
	updateMu   sync.Map // Per-skill mutex for Update() serialization
}

func NewInstaller(homeDir string, store *persistence.Store, logger *slog.Logger) *Installer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Installer{
		installDir: filepath.Join(homeDir, "installed"),
		store:      store,
		logger:     logger,
	}
}

// ParseInstallName returns the install directory name components derived from a URL.
// It is primarily used by the CLI.
func ParseInstallName(githubURL string) (owner, repo string, err error) {
	owner, repo, _, _, err = parseGitHubishURL(githubURL)
	return owner, repo, err
}

func (i *Installer) Install(ctx context.Context, githubURL string, ref string) error {
	owner, repo, subdir, refFromURL, err := parseGitHubishURL(githubURL)
	if err != nil {
		return err
	}
	if strings.TrimSpace(ref) == "" {
		ref = refFromURL
	}

	name := owner + "-" + repo
	destDir := filepath.Join(i.installDir, name)
	if _, err := os.Stat(destDir); err == nil {
		return fmt.Errorf("skill already installed: %s", name)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat install dir: %w", err)
	}

	return i.installToDir(ctx, name, destDir, githubURL, ref, subdir, false)
}

func (i *Installer) Remove(ctx context.Context, name string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	safeName, destDir, err := i.resolveInstallDir(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(destDir); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("skill not installed: %s", safeName)
		}
		return fmt.Errorf("stat install dir: %w", err)
	}
	if err := os.RemoveAll(destDir); err != nil {
		return fmt.Errorf("remove install dir: %w", err)
	}
	if i.store != nil {
		if err := i.store.RemoveInstalledSkill(ctx, safeName); err != nil {
			i.log().Warn("failed to remove skill DB record", "name", safeName, "error", err)
		}
	}
	i.log().Info("skill removed", "name", safeName, "dir", destDir)
	return nil
}

func (i *Installer) Update(ctx context.Context, name string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	safeName, destDir, err := i.resolveInstallDir(name)
	if err != nil {
		return err
	}

	// Serialize concurrent updates to the same skill.
	mu, _ := i.updateMu.LoadOrStore(safeName, &sync.Mutex{})
	skillMu := mu.(*sync.Mutex)
	skillMu.Lock()
	defer skillMu.Unlock()

	if i.store == nil {
		return fmt.Errorf("missing store")
	}
	recs, err := i.store.ListInstalledSkills(ctx)
	if err != nil {
		return err
	}
	var rec *persistence.InstalledSkillRecord
	for idx := range recs {
		if recs[idx].SkillID == safeName {
			rec = &recs[idx]
			break
		}
	}
	if rec == nil {
		return fmt.Errorf("installed skill not found: %s", safeName)
	}

	_, _, subdir, refFromURL, err := parseGitHubishURL(rec.SourceURL)
	if err != nil {
		return err
	}
	ref := strings.TrimSpace(rec.Ref)
	if ref == "" {
		ref = refFromURL
	}

	return i.installToDir(ctx, safeName, destDir, rec.SourceURL, ref, subdir, true)
}

func (i *Installer) UpdateAll(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if i.store == nil {
		return fmt.Errorf("missing store")
	}
	recs, err := i.store.ListInstalledSkills(ctx)
	if err != nil {
		return err
	}
	for _, rec := range recs {
		if err := i.Update(ctx, rec.SkillID); err != nil {
			return err
		}
	}
	return nil
}

func (i *Installer) installToDir(ctx context.Context, name, destDir, srcURL, ref, subdir string, overwrite bool) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if i.installDir == "" {
		return fmt.Errorf("missing installDir")
	}
	if err := os.MkdirAll(i.installDir, 0o755); err != nil {
		return fmt.Errorf("create install dir: %w", err)
	}

	if !overwrite {
		if _, err := os.Stat(destDir); err == nil {
			return fmt.Errorf("install dir exists: %s", destDir)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat install dir: %w", err)
		}
	}

	tmp, err := os.MkdirTemp(i.installDir, "clone-")
	if err != nil {
		return fmt.Errorf("mkdirtemp: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if err := gitClone(ctx, tmp, srcURL, ref); err != nil {
		return err
	}

	srcRoot := tmp
	if strings.TrimSpace(subdir) != "" {
		subdir = filepath.Clean(subdir)
		// Ensure subdir stays within repo.
		if strings.HasPrefix(subdir, ".."+string(filepath.Separator)) || subdir == ".." {
			return fmt.Errorf("invalid subdir: %q", subdir)
		}
		srcRoot = filepath.Join(tmp, subdir)
	}

	skillMDPath := filepath.Join(srcRoot, "SKILL.md")
	data, err := os.ReadFile(skillMDPath)
	if err != nil {
		return fmt.Errorf("read SKILL.md: %w", err)
	}
	// Installed skills must have canonical frontmatter.
	if ok := hasCanonicalFrontmatter(data); !ok {
		return fmt.Errorf("invalid SKILL.md: missing canonical YAML frontmatter")
	}
	parsed, err := legacy.ParseSkillMD(data)
	if err != nil {
		return fmt.Errorf("parse SKILL.md: %w", err)
	}
	if strings.TrimSpace(parsed.Name) == "" {
		return fmt.Errorf("invalid SKILL.md: missing name")
	}

	eligible, missing := checkEligibility(parsed)
	if !eligible && len(missing) > 0 {
		i.log().Warn("installed skill is not eligible on this host", "name", name, "missing", missing)
	}

	// Stage the new skill into a temporary directory within installDir.
	staged, err := os.MkdirTemp(i.installDir, "staged-")
	if err != nil {
		return fmt.Errorf("mkdirtemp staged: %w", err)
	}
	defer func() { _ = os.RemoveAll(staged) }()

	stagedDest := filepath.Join(staged, "skill")
	if err := copyTreeExcludingGit(srcRoot, stagedDest); err != nil {
		return err
	}

	// Determine source type from URL (AUD-012: distinguish local vs github).
	source := sourceTypeFromURL(srcURL)

	// AUD-007: Atomic swap — rename old to .bak, move new into place, remove .bak.
	// On failure, restore the backup.
	backupDir := destDir + ".bak"
	_ = os.RemoveAll(backupDir) // clean any stale backup

	if overwrite {
		if _, err := os.Stat(destDir); err == nil {
			if err := os.Rename(destDir, backupDir); err != nil {
				return fmt.Errorf("backup existing install: %w", err)
			}
		}
	}

	if err := os.Rename(stagedDest, destDir); err != nil {
		// Restore backup on failure.
		if overwrite {
			if _, statErr := os.Stat(backupDir); statErr == nil {
				_ = os.Rename(backupDir, destDir)
			}
		}
		return fmt.Errorf("move staged install: %w", err)
	}

	// Success — remove backup.
	_ = os.RemoveAll(backupDir)

	// Record provenance in DB AFTER successful swap to ensure disk state
	// always matches or leads DB state (no window where DB says "installed"
	// but disk has old version).
	if i.store != nil {
		if err := i.store.RegisterInstalledSkill(ctx, name, source, srcURL, ref); err != nil {
			i.log().Warn("skill installed but DB update failed", "name", name, "error", err)
			// Don't fail — skill is on disk, DB is just stale.
		}
	}

	// Record content hash for debugging; not yet surfaced.
	sum := sha256.Sum256(data)
	i.log().Info("skill installed", "name", name, "dir", destDir, "ref", ref, "skill_md_sha256", fmt.Sprintf("%x", sum[:]))
	return nil
}

// sourceTypeFromURL returns "local" for local paths and "github" for remote URLs.
func sourceTypeFromURL(srcURL string) string {
	trimmed := strings.TrimSpace(srcURL)
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, ".") || strings.HasPrefix(trimmed, "file://") {
		return "local"
	}
	return "github"
}

func (i *Installer) log() *slog.Logger {
	if i != nil && i.logger != nil {
		return i.logger
	}
	return slog.Default()
}

func (i *Installer) resolveInstallDir(name string) (string, string, error) {
	safeName := strings.TrimSpace(name)
	if safeName == "" {
		return "", "", fmt.Errorf("empty name")
	}
	// Names are logical identifiers, not filesystem paths.
	if safeName == "." || safeName == ".." || strings.Contains(safeName, "/") || strings.Contains(safeName, "\\") {
		return "", "", fmt.Errorf("invalid skill name: %q", name)
	}
	destDir := filepath.Join(i.installDir, safeName)
	rel, err := filepath.Rel(i.installDir, destDir)
	if err != nil {
		return "", "", fmt.Errorf("resolve install dir: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("invalid skill name: %q", name)
	}
	return safeName, destDir, nil
}

func hasCanonicalFrontmatter(data []byte) bool {
	trim := strings.TrimSpace(string(data))
	return strings.HasPrefix(trim, "---\n") || strings.HasPrefix(trim, "---\r\n")
}

func parseGitHubishURL(raw string) (owner, repo, subdir, ref string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", "", "", fmt.Errorf("empty url")
	}

	// Local filesystem path (used for hermetic tests and local installs).
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, ".") {
		abs, err := filepath.Abs(raw)
		if err != nil {
			return "", "", "", "", fmt.Errorf("abs url path: %w", err)
		}
		return "local", filepath.Base(abs), "", "", nil
	}

	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		// scp-style: git@github.com:owner/repo(.git)
		if strings.Contains(raw, "github.com:") {
			after := raw[strings.Index(raw, "github.com:")+len("github.com:"):]
			after = strings.TrimSuffix(after, ".git")
			parts := strings.Split(after, "/")
			if len(parts) >= 2 {
				return parts[0], parts[1], "", "", nil
			}
		}
		return "", "", "", "", fmt.Errorf("invalid url: %q", raw)
	}

	// file:// URL
	if u.Scheme == "file" {
		p := u.Path
		if p == "" {
			return "", "", "", "", fmt.Errorf("invalid file url: %q", raw)
		}
		return "local", filepath.Base(p), "", "", nil
	}

	host := strings.ToLower(u.Host)
	if host != "github.com" && host != "www.github.com" {
		return "", "", "", "", fmt.Errorf("unsupported host: %s", host)
	}

	path := strings.Trim(u.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return "", "", "", "", fmt.Errorf("invalid github url path: %q", u.Path)
	}
	owner = parts[0]
	repo = strings.TrimSuffix(parts[1], ".git")
	if owner == "" || repo == "" {
		return "", "", "", "", fmt.Errorf("invalid github url: %q", raw)
	}

	// Optional subdir form: /owner/repo/tree/<ref>/<subdir...>
	if len(parts) >= 4 && parts[2] == "tree" {
		ref = parts[3]
		if len(parts) > 4 {
			subdir = filepath.Join(parts[4:]...)
		}
	}
	return owner, repo, subdir, ref, nil
}

func gitClone(ctx context.Context, dstDir, srcURL, ref string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git not found in PATH")
	}

	args := []string{"clone"}
	// Use a shallow clone only for remote URLs; local paths don't always support --depth reliably.
	if looksRemote(srcURL) {
		args = append(args, "--depth", "1")
	}
	if strings.TrimSpace(ref) != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, srcURL, dstDir)

	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func looksRemote(srcURL string) bool {
	srcURL = strings.TrimSpace(strings.ToLower(srcURL))
	return strings.HasPrefix(srcURL, "https://") || strings.HasPrefix(srcURL, "http://") || strings.HasPrefix(srcURL, "ssh://") || strings.HasPrefix(srcURL, "git@")
}

func copyTreeExcludingGit(srcRoot, dstRoot string) error {
	if err := os.MkdirAll(dstRoot, 0o755); err != nil {
		return fmt.Errorf("mkdir dst: %w", err)
	}
	return filepath.WalkDir(srcRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		// Drop VCS metadata from installs (handles .git as both dir and file).
		base := filepath.Base(rel)
		if base == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			// .git can be a file in sparse checkouts, worktrees, or submodules.
			return nil
		}

		dst := filepath.Join(dstRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			// Fail closed: do not copy symlinks from external sources.
			return fmt.Errorf("symlink not allowed in install: %s", rel)
		}
		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		// Preserve executable bit if present.
		mode := info.Mode() & 0o777
		if mode == 0 {
			mode = 0o644
		}
		dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		defer dstFile.Close()
		if _, err := io.Copy(dstFile, srcFile); err != nil {
			return err
		}
		return nil
	})
}
