package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/policy"
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
)

const (
	maxReadBytes   = 100 * 1024 // 100KB
	maxListEntries = 200
)

// ReadFileInput is the input for the read_file tool.
type ReadFileInput struct {
	Path string `json:"path"`
}

// ReadFileOutput is the output for the read_file tool.
type ReadFileOutput struct {
	Content string `json:"content"`
	Size    int64  `json:"size"`
}

// WriteFileInput is the input for the write_file tool.
type WriteFileInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// WriteFileOutput is the output for the write_file tool.
type WriteFileOutput struct {
	Written bool   `json:"written"`
	Path    string `json:"path"`
	Size    int    `json:"size"`
}

// ListDirectoryInput is the input for the list_directory tool.
type ListDirectoryInput struct {
	Path string `json:"path"`
}

// DirEntry represents a single directory entry.
type DirEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

// ListDirectoryOutput is the output for the list_directory tool.
type ListDirectoryOutput struct {
	Entries []DirEntry `json:"entries"`
	Path    string     `json:"path"`
}

// EditFileInput is the input for the edit_file tool.
type EditFileInput struct {
	Path    string `json:"path"`
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

// EditFileOutput is the output for the edit_file tool.
type EditFileOutput struct {
	Edited bool   `json:"edited"`
	Path   string `json:"path"`
}

// isPathAllowed checks that the resolved path is safe (no traversal out of allowed dirs).
func isPathAllowed(rawPath string) (string, error) {
	if rawPath == "" {
		return "", fmt.Errorf("empty path")
	}
	resolved, err := filepath.Abs(rawPath)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	// Resolve symlinks to prevent symlink-based traversal.
	evaluated, err := filepath.EvalSymlinks(filepath.Dir(resolved))
	if err != nil {
		// Parent dir doesn't exist yet — that's OK for write_file.
		evaluated = filepath.Dir(resolved)
	}
	resolved = filepath.Join(evaluated, filepath.Base(resolved))
	return resolved, nil
}

func registerFileTools(g *genkit.Genkit, reg *Registry) []ai.ToolRef {
	readFile := genkit.DefineTool(g, "read_file",
		"Read the contents of a file at the given path. Returns the file content as text. Maximum 100KB.",
		func(ctx *ai.ToolContext, input ReadFileInput) (ReadFileOutput, error) {
			reg.publishToolCall(ctx, "read_file")
			if reg.Policy == nil || !reg.Policy.AllowCapability("tools.read_file") {
				pv := policyVersion(reg.Policy)
				audit.Record("deny", "tools.read_file", "missing_capability", pv, "read_file")
				return ReadFileOutput{}, fmt.Errorf("policy denied capability %q", "tools.read_file")
			}
			audit.Record("allow", "tools.read_file", "capability_granted", policyVersion(reg.Policy), input.Path)

			resolved, err := isPathAllowed(input.Path)
			if err != nil {
				return ReadFileOutput{}, err
			}
			if reg.Policy != nil && !reg.Policy.AllowPath(resolved) {
				audit.Record("deny", "tools.read_file", "path_denied", policyVersion(reg.Policy), resolved)
				return ReadFileOutput{}, fmt.Errorf("policy denied path %q", resolved)
			}

			info, err := os.Stat(resolved)
			if err != nil {
				return ReadFileOutput{}, fmt.Errorf("stat: %w", err)
			}
			if info.IsDir() {
				return ReadFileOutput{}, fmt.Errorf("path is a directory, use list_directory instead")
			}
			if info.Size() > maxReadBytes {
				return ReadFileOutput{}, fmt.Errorf("file too large: %d bytes (max %d)", info.Size(), maxReadBytes)
			}

			data, err := os.ReadFile(resolved)
			if err != nil {
				return ReadFileOutput{}, fmt.Errorf("read: %w", err)
			}

			return ReadFileOutput{
				Content: string(data),
				Size:    info.Size(),
			}, nil
		},
	)

	writeFile := genkit.DefineTool(g, "write_file",
		"Write content to a file at the given path. Creates parent directories if needed. Uses atomic write.",
		func(ctx *ai.ToolContext, input WriteFileInput) (WriteFileOutput, error) {
			reg.publishToolCall(ctx, "write_file")
			if reg.Policy == nil || !reg.Policy.AllowCapability("tools.write_file") {
				pv := policyVersion(reg.Policy)
				audit.Record("deny", "tools.write_file", "missing_capability", pv, "write_file")
				return WriteFileOutput{}, fmt.Errorf("policy denied capability %q", "tools.write_file")
			}
			audit.Record("allow", "tools.write_file", "capability_granted", policyVersion(reg.Policy), input.Path)

			resolved, err := isPathAllowed(input.Path)
			if err != nil {
				return WriteFileOutput{}, err
			}
			if reg.Policy != nil && !reg.Policy.AllowPath(resolved) {
				audit.Record("deny", "tools.write_file", "path_denied", policyVersion(reg.Policy), resolved)
				return WriteFileOutput{}, fmt.Errorf("policy denied path %q", resolved)
			}

			// Create parent directories.
			if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
				return WriteFileOutput{}, fmt.Errorf("mkdir: %w", err)
			}

			// Atomic write: write to temp file then rename.
			tmpFile := resolved + ".tmp"
			if err := os.WriteFile(tmpFile, []byte(input.Content), 0o644); err != nil {
				return WriteFileOutput{}, fmt.Errorf("write temp: %w", err)
			}
			if err := os.Rename(tmpFile, resolved); err != nil {
				_ = os.Remove(tmpFile)
				return WriteFileOutput{}, fmt.Errorf("rename: %w", err)
			}

			return WriteFileOutput{
				Written: true,
				Path:    resolved,
				Size:    len(input.Content),
			}, nil
		},
	)

	listDir := genkit.DefineTool(g, "list_directory",
		"List the contents of a directory. Returns file names, types, and sizes. Maximum 200 entries.",
		func(ctx *ai.ToolContext, input ListDirectoryInput) (ListDirectoryOutput, error) {
			reg.publishToolCall(ctx, "list_directory")
			if reg.Policy == nil || !reg.Policy.AllowCapability("tools.read_file") {
				pv := policyVersion(reg.Policy)
				audit.Record("deny", "tools.read_file", "missing_capability", pv, "list_directory")
				return ListDirectoryOutput{}, fmt.Errorf("policy denied capability %q", "tools.read_file")
			}

			resolved, err := isPathAllowed(input.Path)
			if err != nil {
				return ListDirectoryOutput{}, err
			}
			if reg.Policy != nil && !reg.Policy.AllowPath(resolved) {
				audit.Record("deny", "tools.read_file", "path_denied", policyVersion(reg.Policy), resolved)
				return ListDirectoryOutput{}, fmt.Errorf("policy denied path %q", resolved)
			}

			entries, err := os.ReadDir(resolved)
			if err != nil {
				return ListDirectoryOutput{}, fmt.Errorf("read dir: %w", err)
			}

			var result []DirEntry
			for i, entry := range entries {
				if i >= maxListEntries {
					break
				}
				info, err := entry.Info()
				var size int64
				if err == nil {
					size = info.Size()
				}
				result = append(result, DirEntry{
					Name:  entry.Name(),
					IsDir: entry.IsDir(),
					Size:  size,
				})
			}

			return ListDirectoryOutput{
				Entries: result,
				Path:    resolved,
			}, nil
		},
	)

	editFile := genkit.DefineTool(g, "edit_file",
		"Edit a file by replacing old_text with new_text. The old_text must appear exactly once in the file.",
		func(ctx *ai.ToolContext, input EditFileInput) (EditFileOutput, error) {
			reg.publishToolCall(ctx, "edit_file")
			if reg.Policy == nil || !reg.Policy.AllowCapability("tools.write_file") {
				pv := policyVersion(reg.Policy)
				audit.Record("deny", "tools.write_file", "missing_capability", pv, "edit_file")
				return EditFileOutput{}, fmt.Errorf("policy denied capability %q", "tools.write_file")
			}

			resolved, err := isPathAllowed(input.Path)
			if err != nil {
				return EditFileOutput{}, err
			}
			if reg.Policy != nil && !reg.Policy.AllowPath(resolved) {
				audit.Record("deny", "tools.write_file", "path_denied", policyVersion(reg.Policy), resolved)
				return EditFileOutput{}, fmt.Errorf("policy denied path %q", resolved)
			}

			data, err := os.ReadFile(resolved)
			if err != nil {
				return EditFileOutput{}, fmt.Errorf("read: %w", err)
			}

			content := string(data)
			count := strings.Count(content, input.OldText)
			if count == 0 {
				return EditFileOutput{}, fmt.Errorf("old_text not found in file")
			}
			if count > 1 {
				return EditFileOutput{}, fmt.Errorf("old_text appears %d times (must be unique)", count)
			}

			newContent := strings.Replace(content, input.OldText, input.NewText, 1)

			// Atomic write.
			tmpFile := resolved + ".tmp"
			if err := os.WriteFile(tmpFile, []byte(newContent), 0o644); err != nil {
				return EditFileOutput{}, fmt.Errorf("write temp: %w", err)
			}
			if err := os.Rename(tmpFile, resolved); err != nil {
				_ = os.Remove(tmpFile)
				return EditFileOutput{}, fmt.Errorf("rename: %w", err)
			}

			return EditFileOutput{Edited: true, Path: resolved}, nil
		},
	)

	return []ai.ToolRef{readFile, writeFile, listDir, editFile}
}

func policyVersion(pol policy.Checker) string {
	if pol != nil {
		return pol.PolicyVersion()
	}
	return ""
}
