package smoke

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func moduleRoot(t *testing.T) string {
	t.Helper()

	cmd := exec.Command("go", "env", "GOMOD")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		t.Fatalf("go env GOMOD returned %q; expected path to go.mod", gomod)
	}
	return filepath.Dir(gomod)
}

func TestSmoke_BuildsIronclawBinary(t *testing.T) {
	// Smoke test for the "single-binary" build property.
	// [SPEC: SPEC-GOAL-G2] [PDR: V-24]

	root := moduleRoot(t)
	outPath := filepath.Join(t.TempDir(), "goclaw")

	cmd := exec.Command("go", "build", "-o", outPath, "./cmd/goclaw")
	cmd.Dir = root

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build ./cmd/goclaw failed: %v\n%s", err, buf.String())
	}

	fi, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat built binary: %v", err)
	}
	if fi.Size() <= 0 {
		t.Fatalf("built binary has unexpected size %d", fi.Size())
	}
}
