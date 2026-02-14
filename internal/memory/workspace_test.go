package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspace_ReadWrite(t *testing.T) {
	ws, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}

	content := "hello, workspace!\nline two\n"
	if err := ws.Write("notes/test.txt", content); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := ws.Read("notes/test.txt")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != content {
		t.Errorf("Read mismatch:\n  got:  %q\n  want: %q", got, content)
	}

	// Overwrite and re-read to verify atomic replacement.
	content2 := "replaced content"
	if err := ws.Write("notes/test.txt", content2); err != nil {
		t.Fatalf("Write overwrite: %v", err)
	}
	got2, err := ws.Read("notes/test.txt")
	if err != nil {
		t.Fatalf("Read after overwrite: %v", err)
	}
	if got2 != content2 {
		t.Errorf("Read after overwrite mismatch:\n  got:  %q\n  want: %q", got2, content2)
	}
}

func TestWorkspace_Append(t *testing.T) {
	ws, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}

	// Write initial content.
	if err := ws.Write("log.txt", "first\n"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Append more.
	if err := ws.Append("log.txt", "second\n"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := ws.Append("log.txt", "third\n"); err != nil {
		t.Fatalf("Append 2: %v", err)
	}

	got, err := ws.Read("log.txt")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := "first\nsecond\nthird\n"
	if got != want {
		t.Errorf("Append content mismatch:\n  got:  %q\n  want: %q", got, want)
	}

	// Append to a new file (creates it).
	if err := ws.Append("new.txt", "created via append"); err != nil {
		t.Fatalf("Append new file: %v", err)
	}
	got2, err := ws.Read("new.txt")
	if err != nil {
		t.Fatalf("Read new file: %v", err)
	}
	if got2 != "created via append" {
		t.Errorf("Append new file content: got %q", got2)
	}
}

func TestWorkspace_List(t *testing.T) {
	root := t.TempDir()
	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}

	// Create several files and a subdirectory.
	files := []string{"alpha.txt", "beta.md", "gamma.json"}
	for _, f := range files {
		if err := ws.Write(f, "content of "+f); err != nil {
			t.Fatalf("Write %s: %v", f, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "subdir"), 0o755); err != nil {
		t.Fatalf("MkdirAll subdir: %v", err)
	}

	entries, err := ws.List(".")
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	// Build a map for easier lookup.
	found := make(map[string]FileInfo, len(entries))
	for _, e := range entries {
		found[e.Name] = e
	}

	for _, f := range files {
		fi, ok := found[f]
		if !ok {
			t.Errorf("expected file %q in listing, not found", f)
			continue
		}
		if fi.IsDir {
			t.Errorf("file %q should not be a directory", f)
		}
		if fi.Size == 0 {
			t.Errorf("file %q should have non-zero size", f)
		}
	}

	sd, ok := found["subdir"]
	if !ok {
		t.Fatal("expected subdir in listing, not found")
	}
	if !sd.IsDir {
		t.Error("subdir should be a directory")
	}
}

func TestWorkspace_Search(t *testing.T) {
	ws, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}

	// Write files with known content.
	if err := ws.Write("a.txt", "The quick brown fox jumps over the lazy dog\nAnother line\n"); err != nil {
		t.Fatalf("Write a.txt: %v", err)
	}
	if err := ws.Write("b.txt", "Nothing here\nThe QUICK fox again\n"); err != nil {
		t.Fatalf("Write b.txt: %v", err)
	}
	if err := ws.Write("sub/c.txt", "Totally unrelated content\n"); err != nil {
		t.Fatalf("Write sub/c.txt: %v", err)
	}

	hits, err := ws.Search("quick")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// Should match line 1 of a.txt and line 2 of b.txt (case-insensitive).
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d: %+v", len(hits), hits)
	}

	// Verify the hits point to the right files.
	hitPaths := make(map[string]bool)
	for _, h := range hits {
		hitPaths[h.Path] = true
		if h.Line < 1 {
			t.Errorf("hit line should be >= 1, got %d", h.Line)
		}
		if !strings.Contains(strings.ToLower(h.Content), "quick") {
			t.Errorf("hit content should contain 'quick': %q", h.Content)
		}
	}
	if !hitPaths["a.txt"] {
		t.Error("expected hit in a.txt")
	}
	if !hitPaths["b.txt"] {
		t.Error("expected hit in b.txt")
	}

	// Search for something present only in the subdirectory.
	hits2, err := ws.Search("unrelated")
	if err != nil {
		t.Fatalf("Search unrelated: %v", err)
	}
	if len(hits2) != 1 {
		t.Fatalf("expected 1 hit for 'unrelated', got %d: %+v", len(hits2), hits2)
	}
	if hits2[0].Path != filepath.Join("sub", "c.txt") {
		t.Errorf("expected hit path sub/c.txt, got %q", hits2[0].Path)
	}
}

func TestWorkspace_PathTraversalBlocked(t *testing.T) {
	ws, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}

	// Attempts to escape the workspace root should be rejected.
	traversalPaths := []string{
		"../etc/passwd",
		"../../etc/shadow",
		"foo/../../..",
		"/etc/passwd",
	}

	for _, p := range traversalPaths {
		_, err := ws.Read(p)
		if err == nil {
			t.Errorf("Read(%q) should have failed with traversal error", p)
		} else if !strings.Contains(err.Error(), "traversal") && !strings.Contains(err.Error(), "stat") {
			// Allow stat errors for absolute paths that resolve within root
			// but don't exist; the key is that traversal errors are caught.
			t.Logf("Read(%q) error (ok): %v", p, err)
		}

		err = ws.Write(p, "evil")
		if err == nil {
			t.Errorf("Write(%q) should have failed with traversal error", p)
		}
	}

	// Verify that a valid relative path inside the workspace still works.
	if err := ws.Write("safe/file.txt", "ok"); err != nil {
		t.Fatalf("Write safe path failed: %v", err)
	}
	got, err := ws.Read("safe/file.txt")
	if err != nil {
		t.Fatalf("Read safe path failed: %v", err)
	}
	if got != "ok" {
		t.Errorf("expected 'ok', got %q", got)
	}
}

func TestWorkspace_SymlinkTraversalBlocked(t *testing.T) {
	root := t.TempDir()
	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}

	// Create a directory outside the workspace.
	outsideDir := t.TempDir()
	secretFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("top secret"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	// Create a symlink inside the workspace pointing outside.
	symlinkPath := filepath.Join(root, "escape")
	if err := os.Symlink(outsideDir, symlinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	// Reading via the symlink should be blocked because EvalSymlinks
	// resolves it to a path outside rootDir.
	_, readErr := ws.Read("escape/secret.txt")
	if readErr == nil {
		t.Fatal("expected symlink traversal to be blocked for Read")
	}
	if !strings.Contains(readErr.Error(), "traversal") {
		t.Fatalf("expected traversal error, got: %v", readErr)
	}

	// Writing via the symlink should also be blocked.
	writeErr := ws.Write("escape/new.txt", "evil")
	if writeErr == nil {
		t.Fatal("expected symlink traversal to be blocked for Write")
	}
	if !strings.Contains(writeErr.Error(), "traversal") {
		t.Fatalf("expected traversal error, got: %v", writeErr)
	}
}
