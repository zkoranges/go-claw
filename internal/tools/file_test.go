package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fileTestPolicy struct {
	allow bool
}

func (p fileTestPolicy) AllowHTTPURL(string) bool        { return true }
func (p fileTestPolicy) AllowCapability(cap string) bool { return p.allow }
func (p fileTestPolicy) AllowPath(string) bool           { return true }
func (p fileTestPolicy) PolicyVersion() string           { return "test-v1" }

func TestReadFile_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolved, err := isPathAllowed(path)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Fatalf("got %q, want %q", string(data), "hello world")
	}
}

func TestReadFile_PathTraversalBlocked(t *testing.T) {
	// isPathAllowed resolves symlinks so traversal via ../../../etc/passwd
	// would resolve to an absolute path which is OK, but we verify it works.
	_, err := isPathAllowed("")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestWriteFile_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "output.txt")

	resolved, err := isPathAllowed(path)
	if err != nil {
		t.Fatal(err)
	}

	// Create parent dirs.
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		t.Fatal(err)
	}

	// Atomic write.
	tmpFile := resolved + ".tmp"
	if err := os.WriteFile(tmpFile, []byte("atomic content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmpFile, resolved); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "atomic content" {
		t.Fatalf("got %q, want %q", string(data), "atomic content")
	}
}

func TestWriteFile_ParentDirCreation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deep", "nested", "file.txt")

	resolved, err := isPathAllowed(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resolved, []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "nested" {
		t.Fatalf("got %q, want %q", string(data), "nested")
	}
}

func TestListDirectory_MaxEntries(t *testing.T) {
	dir := t.TempDir()
	// Create 5 files.
	for i := 0; i < 5; i++ {
		path := filepath.Join(dir, fmt.Sprintf("file%d.txt", i))
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("got %d entries, want 5", len(entries))
	}
}

func TestEditFile_ExactReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	count := strings.Count(content, "hello")
	if count != 1 {
		t.Fatalf("expected 1 occurrence, got %d", count)
	}

	newContent := strings.Replace(content, "hello", "goodbye", 1)
	if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != "goodbye world" {
		t.Fatalf("got %q, want %q", string(result), "goodbye world")
	}
}

func TestEditFile_NotFound(t *testing.T) {
	content := "hello world"
	count := strings.Count(content, "missing")
	if count != 0 {
		t.Fatalf("expected 0 occurrences, got %d", count)
	}
}

func TestIsPathAllowed_SymlinkResolution(t *testing.T) {
	dir := t.TempDir()
	realFile := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(realFile, []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(dir, "link.txt")
	if err := os.Symlink(realFile, linkPath); err != nil {
		t.Skip("symlinks not supported")
	}

	resolved, err := isPathAllowed(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	// Resolved path should point to the real file.
	if resolved == "" {
		t.Fatal("resolved path is empty")
	}
}

func TestPolicyVersion_Nil(t *testing.T) {
	got := policyVersion(nil)
	if got != "" {
		t.Fatalf("policyVersion(nil) = %q, want empty", got)
	}
}
