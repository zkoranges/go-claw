package skills

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/basket/go-claw/internal/sandbox/legacy"
)

// maxSkillMDSize is the maximum allowed size for a SKILL.md file (1 MiB).
const maxSkillMDSize = 1 << 20

// CanonicalSkillKey returns a normalized key used for collision detection.
// Both the Loader and the daemon's loadAllSkillMD must use this function
// to derive collision keys consistently.
func CanonicalSkillKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

type LoadedSkill struct {
	Skill     legacy.Skill
	Source    string   // "project", "user", "installed", "builtin"
	SourceDir string   // absolute path to skill directory
	Eligible  bool     // passed eligibility checks
	Missing   []string // human-readable missing requirements
}

type Loader struct {
	projectDir   string // <workspace>/skills/
	userDir      string // $GOCLAW_HOME/skills/
	installedDir string // $GOCLAW_HOME/installed/
	logger       *slog.Logger
}

func NewLoader(projectDir, userDir, installedDir string, logger *slog.Logger) *Loader {
	if logger == nil {
		logger = slog.Default()
	}
	return &Loader{
		projectDir:   projectDir,
		userDir:      userDir,
		installedDir: installedDir,
		logger:       logger,
	}
}

func (l *Loader) LoadAll(ctx context.Context) ([]LoadedSkill, error) {
	type scanSpec struct {
		dir    string
		source string
	}
	specs := []scanSpec{
		{dir: l.projectDir, source: "project"},
		{dir: l.userDir, source: "user"},
		{dir: l.installedDir, source: "installed"},
	}

	seen := make(map[string]string) // canonical name -> winning source
	var out []LoadedSkill
	var errs []error

	for _, spec := range specs {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if strings.TrimSpace(spec.dir) == "" {
			continue
		}
		base, err := filepath.Abs(spec.dir)
		if err != nil {
			errs = append(errs, fmt.Errorf("abs skills dir (%s): %w", spec.dir, err))
			continue
		}

		entries, err := os.ReadDir(base)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			errs = append(errs, fmt.Errorf("read skills dir (%s): %w", base, err))
			continue
		}

		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, ent := range entries {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			if !ent.IsDir() {
				// AUD-028: Detect symlinked skill directories and warn.
				if ent.Type()&os.ModeSymlink != 0 {
					l.log().Warn("skill directory is a symlink; symlinks are not followed",
						"name", ent.Name(),
						"dir", base,
					)
				}
				continue
			}
			canonicalName := ent.Name()
			key := CanonicalSkillKey(canonicalName)
			if winner, ok := seen[key]; ok {
				l.log().Info("skill collision: skipping lower-priority duplicate",
					"skill", canonicalName,
					"winner_source", winner,
					"skipped_source", spec.source,
				)
				continue
			}

			skillDir := filepath.Join(base, canonicalName)
			skillMD := filepath.Join(skillDir, "SKILL.md")
			if _, err := os.Stat(skillMD); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				errs = append(errs, fmt.Errorf("stat SKILL.md (%s): %w", skillMD, err))
				continue
			}

			ls, err := l.LoadOne(ctx, skillDir, spec.source)
			if err != nil {
				errs = append(errs, fmt.Errorf("load skill (%s): %w", canonicalName, err))
				continue
			}
			out = append(out, ls)
			seen[key] = spec.source
		}
	}

	return out, errors.Join(errs...)
}

func (l *Loader) LoadOne(ctx context.Context, dir string, source string) (LoadedSkill, error) {
	if ctx.Err() != nil {
		return LoadedSkill{}, ctx.Err()
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return LoadedSkill{}, fmt.Errorf("abs dir: %w", err)
	}
	skillMD := filepath.Join(absDir, "SKILL.md")
	fi, err := os.Stat(skillMD)
	if err != nil {
		return LoadedSkill{}, fmt.Errorf("stat SKILL.md: %w", err)
	}
	if fi.Size() > maxSkillMDSize {
		return LoadedSkill{}, fmt.Errorf("SKILL.md too large: %d bytes (max %d)", fi.Size(), maxSkillMDSize)
	}
	data, err := os.ReadFile(skillMD)
	if err != nil {
		return LoadedSkill{}, fmt.Errorf("read SKILL.md: %w", err)
	}
	s, err := legacy.ParseSkillMD(data)
	if err != nil {
		return LoadedSkill{}, err
	}

	s.SourceDir = absDir
	s.Source = source

	eligible, missing := checkEligibility(s)

	// Compatibility is advisory and not enforced in v0.1.
	if strings.TrimSpace(s.Compatibility) != "" {
		l.log().Warn("skill compatibility is advisory (not enforced)", "skill", s.Name, "compatibility", s.Compatibility)
	}

	return LoadedSkill{
		Skill:     s,
		Source:    source,
		SourceDir: absDir,
		Eligible:  eligible,
		Missing:   missing,
	}, nil
}

func (l *Loader) log() *slog.Logger {
	if l != nil && l.logger != nil {
		return l.logger
	}
	return slog.Default()
}

func checkEligibility(skill legacy.Skill) (eligible bool, missing []string) {
	eligible = true

	// 1) Bins: all required bins must exist.
	for _, b := range skill.Bins {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		if _, err := exec.LookPath(b); err != nil {
			eligible = false
			missing = append(missing, fmt.Sprintf("missing bin: %s", b))
		}
	}

	// 2) anyBins: at least one must exist.
	anyBins := metaStringList(skill.Metadata, "openclaw", "requires", "anyBins")
	if len(anyBins) > 0 {
		foundAny := false
		for _, b := range anyBins {
			b = strings.TrimSpace(b)
			if b == "" {
				continue
			}
			if _, err := exec.LookPath(b); err == nil {
				foundAny = true
				break
			}
		}
		if !foundAny {
			eligible = false
			missing = append(missing, fmt.Sprintf("missing anyBins: %s", strings.Join(anyBins, ",")))
		}
	}

	// 3) env: all must be non-empty.
	envs := metaStringList(skill.Metadata, "openclaw", "requires", "env")
	for _, k := range envs {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if os.Getenv(k) == "" {
			eligible = false
			missing = append(missing, fmt.Sprintf("missing env: %s", k))
		}
	}

	// 4) os restriction.
	allowedOS := metaStringList(skill.Metadata, "openclaw", "os")
	if len(allowedOS) > 0 {
		ok := false
		for _, v := range allowedOS {
			if strings.TrimSpace(v) == runtime.GOOS {
				ok = true
				break
			}
		}
		if !ok {
			eligible = false
			missing = append(missing, fmt.Sprintf("unsupported os: %s", runtime.GOOS))
		}
	}

	return eligible, missing
}

func metaStringList(meta map[string]any, path ...string) []string {
	v, ok := metaGet(meta, path...)
	if !ok || v == nil {
		return nil
	}
	return anyToStringSlice(v)
}

func metaGet(meta map[string]any, path ...string) (any, bool) {
	if len(path) == 0 {
		return meta, true
	}
	var cur any = meta
	for _, key := range path {
		m, ok := asStringMap(cur)
		if !ok {
			return nil, false
		}
		next, ok := m[key]
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func asStringMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, v := range m {
			ks, ok := k.(string)
			if !ok {
				continue
			}
			out[ks] = v
		}
		return out, true
	default:
		return nil, false
	}
}

func anyToStringSlice(v any) []string {
	switch vv := v.(type) {
	case []string:
		return append([]string(nil), vv...)
	case []any:
		var out []string
		for _, item := range vv {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
