package smoke

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSmoke_NoBrowserAutomationImports(t *testing.T) {
	// This corresponds to TODO.md Testing Gap T-12 and SPEC non-goal GC-SPEC-NG-001.
	// Keep this intentionally conservative: if these appear in deps or go.mod/sum,
	// we treat it as a release-blocking violation.
	root := moduleRoot(t)

	// A small denylist of common browser automation libraries.
	//
	// Note: build these strings from fragments so tools/verify/non_goals_audit
	// doesn't flag this test itself as a violation (it scans source text).
	banned := []string{
		strings.Join([]string{"github.com/", "chrome", "dp", "/"}, ""),
		strings.Join([]string{"github.com/", "go", "-", "rod", "/"}, ""),
		strings.Join([]string{"github.com/", "play", "wright", "-community/"}, ""),
		strings.Join([]string{"github.com/", "tebeka/", "sele", "nium"}, ""),
	}

	for _, p := range []string{"go.mod", "go.sum"} {
		b, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		lower := strings.ToLower(string(b))
		for _, s := range banned {
			if strings.Contains(lower, strings.ToLower(s)) {
				t.Fatalf("found banned browser automation dependency %q in %s", s, p)
			}
		}
	}

	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", "./...")
	cmd.Dir = root
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list -deps failed: %v\n%s", err, buf.String())
	}
	outLower := strings.ToLower(buf.String())
	for _, s := range banned {
		if strings.Contains(outLower, strings.ToLower(s)) {
			t.Fatalf("found banned browser automation import path %q in dependency graph", s)
		}
	}
}
