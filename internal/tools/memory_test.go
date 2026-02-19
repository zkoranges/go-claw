package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceRoot_DefaultPath(t *testing.T) {
	// Clear GOCLAW_HOME to test default path.
	t.Setenv("GOCLAW_HOME", "")
	t.Setenv("HOME", "/tmp/test-home")

	root := workspaceRoot()
	expected := filepath.Join("/tmp/test-home", ".goclaw", "workspace")
	if root != expected {
		t.Errorf("workspaceRoot() = %q, want %q", root, expected)
	}
}

func TestWorkspaceRoot_CustomHome(t *testing.T) {
	t.Setenv("GOCLAW_HOME", "/custom/goclaw")

	root := workspaceRoot()
	expected := filepath.Join("/custom/goclaw", "workspace")
	if root != expected {
		t.Errorf("workspaceRoot() = %q, want %q", root, expected)
	}
}

func TestMemoryReadInput_Struct(t *testing.T) {
	input := MemoryReadInput{Path: "notes/todo.md"}
	if input.Path != "notes/todo.md" {
		t.Error("MemoryReadInput path mismatch")
	}
}

func TestMemoryWriteInput_Struct(t *testing.T) {
	input := MemoryWriteInput{Path: "data.txt", Content: "hello", Append: true}
	if input.Path != "data.txt" {
		t.Error("MemoryWriteInput path mismatch")
	}
	if input.Content != "hello" {
		t.Error("MemoryWriteInput content mismatch")
	}
	if !input.Append {
		t.Error("MemoryWriteInput append should be true")
	}
}

func TestMemorySearchInput_Struct(t *testing.T) {
	input := MemorySearchInput{Query: "search term"}
	if input.Query != "search term" {
		t.Error("MemorySearchInput query mismatch")
	}
}

func TestMemoryReadOutput_Struct(t *testing.T) {
	output := MemoryReadOutput{Content: "file contents"}
	if output.Content != "file contents" {
		t.Error("MemoryReadOutput content mismatch")
	}
}

func TestMemoryWriteOutput_Struct(t *testing.T) {
	output := MemoryWriteOutput{Written: true, Path: "out.txt"}
	if !output.Written {
		t.Error("MemoryWriteOutput.Written should be true")
	}
	if output.Path != "out.txt" {
		t.Error("MemoryWriteOutput path mismatch")
	}
}

func TestWorkspaceRoot_EnvExpansion(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOCLAW_HOME", dir)

	root := workspaceRoot()
	if !filepath.IsAbs(root) {
		t.Errorf("workspace root should be absolute, got: %q", root)
	}
	if filepath.Base(root) != "workspace" {
		t.Errorf("workspace root should end in 'workspace', got: %q", root)
	}
}

func TestWorkspaceRoot_DirectoryCreatable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOCLAW_HOME", dir)

	root := workspaceRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Errorf("failed to create workspace dir: %v", err)
	}

	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Error("workspace root should be a directory")
	}
}
