// Package memory provides a sandboxed file-based workspace for persistent
// agent memory. All paths are confined to a root directory via traversal
// protection.
package memory

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	maxReadBytes   = 1 * 1024 * 1024 // 1 MB
	maxListEntries = 500
	maxSearchDepth = 3
	maxSearchHits  = 100
)

// FileInfo describes a single directory entry.
type FileInfo struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

// SearchHit describes a single search match.
type SearchHit struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

// Workspace is a sandboxed file-based workspace rooted at rootDir.
type Workspace struct {
	rootDir string
}

// NewWorkspace creates a Workspace rooted at rootDir. The directory is created
// if it does not already exist.
func NewWorkspace(rootDir string) (*Workspace, error) {
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("memory: resolve root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("memory: create root dir: %w", err)
	}
	// Resolve symlinks in root to prevent bypass.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("memory: eval symlinks on root: %w", err)
	}
	return &Workspace{rootDir: resolved}, nil
}

// resolve validates that path stays within the workspace root. It returns the
// absolute path or an error if traversal is detected.
func (w *Workspace) resolve(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("memory: empty path")
	}

	// Clean and join with root.
	cleaned := filepath.Clean(path)
	var full string
	if filepath.IsAbs(cleaned) {
		full = cleaned
	} else {
		full = filepath.Join(w.rootDir, cleaned)
	}

	abs, err := filepath.Abs(full)
	if err != nil {
		return "", fmt.Errorf("memory: resolve path: %w", err)
	}

	// Resolve symlinks to prevent traversal via symlink.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// For non-existent paths (new files/dirs), walk up to find the
		// deepest existing ancestor and resolve symlinks from there.
		resolved, err = evalSymlinksPartial(abs)
		if err != nil {
			return "", fmt.Errorf("memory: resolve symlinks: %w", err)
		}
	}

	// The resolved path must be within rootDir (or equal to it).
	if resolved != w.rootDir && !strings.HasPrefix(resolved, w.rootDir+string(filepath.Separator)) {
		return "", fmt.Errorf("memory: path traversal blocked: %s", path)
	}

	return resolved, nil
}

// evalSymlinksPartial walks up from path until it finds an existing ancestor,
// resolves symlinks on that ancestor, then re-appends the remaining segments.
func evalSymlinksPartial(abs string) (string, error) {
	// Collect path segments that don't exist yet.
	current := abs
	var trailing []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			// Found an existing ancestor; rebuild the full path.
			for i := len(trailing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, trailing[i])
			}
			return resolved, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			// Reached filesystem root without finding an existing path.
			return "", fmt.Errorf("no existing ancestor for %s", abs)
		}
		trailing = append(trailing, filepath.Base(current))
		current = parent
	}
}

// Read reads the contents of a file. Maximum size is 1 MB.
func (w *Workspace) Read(path string) (string, error) {
	resolved, err := w.resolve(path)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("memory: stat: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("memory: path is a directory")
	}
	if info.Size() > maxReadBytes {
		return "", fmt.Errorf("memory: file too large: %d bytes (max %d)", info.Size(), maxReadBytes)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("memory: read: %w", err)
	}
	return string(data), nil
}

// Write writes content to a file atomically (temp file + rename). Parent
// directories are created as needed.
func (w *Workspace) Write(path, content string) error {
	resolved, err := w.resolve(path)
	if err != nil {
		return err
	}

	dir := filepath.Dir(resolved)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("memory: mkdir: %w", err)
	}

	// Atomic write: temp file in the same directory, then rename.
	tmp, err := os.CreateTemp(dir, ".mem-*.tmp")
	if err != nil {
		return fmt.Errorf("memory: create temp: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("memory: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("memory: close temp: %w", err)
	}
	if err := os.Rename(tmpName, resolved); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("memory: rename: %w", err)
	}
	return nil
}

// Append appends content to a file, creating it if it does not exist.
func (w *Workspace) Append(path, content string) error {
	resolved, err := w.resolve(path)
	if err != nil {
		return err
	}

	dir := filepath.Dir(resolved)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("memory: mkdir: %w", err)
	}

	f, err := os.OpenFile(resolved, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("memory: open append: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("memory: append: %w", err)
	}
	return nil
}

// List returns directory entries (max 500).
func (w *Workspace) List(dir string) ([]FileInfo, error) {
	resolved, err := w.resolve(dir)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(resolved)
	if err != nil {
		return nil, fmt.Errorf("memory: read dir: %w", err)
	}

	var result []FileInfo
	for i, entry := range entries {
		if i >= maxListEntries {
			break
		}
		info, err := entry.Info()
		var size int64
		if err == nil {
			size = info.Size()
		}
		result = append(result, FileInfo{
			Name:  entry.Name(),
			IsDir: entry.IsDir(),
			Size:  size,
		})
	}
	return result, nil
}

// Search performs a case-insensitive substring search across all text files in
// the workspace. It walks up to maxSearchDepth levels deep, skips binary files,
// and returns at most maxSearchHits results.
func (w *Workspace) Search(query string) ([]SearchHit, error) {
	if query == "" {
		return nil, fmt.Errorf("memory: empty search query")
	}

	lowerQuery := strings.ToLower(query)
	var hits []SearchHit

	err := filepath.WalkDir(w.rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}
		if len(hits) >= maxSearchHits {
			return fs.SkipAll
		}

		// Compute depth relative to root.
		rel, relErr := filepath.Rel(w.rootDir, path)
		if relErr != nil {
			return nil
		}
		depth := 0
		if rel != "." {
			depth = strings.Count(rel, string(filepath.Separator)) + 1
		}
		if d.IsDir() {
			if depth > maxSearchDepth {
				return fs.SkipDir
			}
			return nil
		}

		// Skip files deeper than maxSearchDepth.
		if depth > maxSearchDepth {
			return nil
		}

		// Skip large files.
		info, infoErr := d.Info()
		if infoErr != nil || info.Size() > maxReadBytes {
			return nil
		}

		// Open and scan line by line.
		f, fErr := os.Open(path)
		if fErr != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			// Skip binary-looking lines.
			if !utf8.ValidString(line) {
				return nil // skip this file entirely
			}
			if strings.Contains(strings.ToLower(line), lowerQuery) {
				hits = append(hits, SearchHit{
					Path:    rel,
					Line:    lineNum,
					Content: truncate(line, 200),
				})
				if len(hits) >= maxSearchHits {
					return fs.SkipAll
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("memory: search walk: %w", err)
	}
	return hits, nil
}

// Delete removes a single file. Directories cannot be deleted for safety.
func (w *Workspace) Delete(path string) error {
	resolved, err := w.resolve(path)
	if err != nil {
		return err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("memory: stat: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("memory: cannot delete directory")
	}
	if err := os.Remove(resolved); err != nil {
		return fmt.Errorf("memory: remove: %w", err)
	}
	return nil
}

// truncate shortens s to at most maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
