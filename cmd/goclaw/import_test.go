package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDotEnvFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	kv, err := parseDotEnvFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(kv) != 0 {
		t.Errorf("expected 0 keys from empty file, got %d", len(kv))
	}
}

func TestParseDotEnvFile_MissingFile(t *testing.T) {
	kv, err := parseDotEnvFile("/nonexistent/.env")
	if err != nil {
		t.Fatal("expected no error for missing file")
	}
	if len(kv) != 0 {
		t.Errorf("expected 0 keys from missing file, got %d", len(kv))
	}
}

func TestParseDotEnvFile_ValidEntries(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	content := `
# Comment line
GEMINI_API_KEY=AIza123abc
GEMINI_MODEL=gemini-2.5-pro

BRAVE_API_KEY=BSA456def
EMPTY_KEY=
`
	if err := os.WriteFile(envFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	kv, err := parseDotEnvFile(envFile)
	if err != nil {
		t.Fatal(err)
	}

	if kv["GEMINI_API_KEY"] != "AIza123abc" {
		t.Errorf("GEMINI_API_KEY = %q, want AIza123abc", kv["GEMINI_API_KEY"])
	}
	if kv["GEMINI_MODEL"] != "gemini-2.5-pro" {
		t.Errorf("GEMINI_MODEL = %q, want gemini-2.5-pro", kv["GEMINI_MODEL"])
	}
	if kv["BRAVE_API_KEY"] != "BSA456def" {
		t.Errorf("BRAVE_API_KEY = %q, want BSA456def", kv["BRAVE_API_KEY"])
	}
}

func TestParseDotEnvFile_SkipsComments(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	content := `# This is a comment
# Another comment
KEY=VALUE`
	if err := os.WriteFile(envFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	kv, err := parseDotEnvFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(kv) != 1 {
		t.Errorf("expected 1 key, got %d", len(kv))
	}
	if kv["KEY"] != "VALUE" {
		t.Errorf("KEY = %q, want VALUE", kv["KEY"])
	}
}

func TestParseDotEnvFile_SkipsMalformed(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	content := `VALID=yes
no_equals_sign
=empty_key
 `
	if err := os.WriteFile(envFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	kv, err := parseDotEnvFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(kv) != 1 {
		t.Errorf("expected 1 valid key, got %d: %v", len(kv), kv)
	}
	if kv["VALID"] != "yes" {
		t.Errorf("VALID = %q, want yes", kv["VALID"])
	}
}

func TestParseDotEnvFile_TrimsWhitespace(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	content := `  KEY_WITH_SPACES  =  value with spaces  `
	if err := os.WriteFile(envFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	kv, err := parseDotEnvFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if kv["KEY_WITH_SPACES"] != "value with spaces" {
		t.Errorf("KEY_WITH_SPACES = %q, want 'value with spaces'", kv["KEY_WITH_SPACES"])
	}
}

func TestRunImportCommand_ExtraArgs(t *testing.T) {
	// Running import with extra positional args should return exit code 2.
	code := runImportCommand(nil, []string{"extra"})
	if code != 2 {
		t.Errorf("expected exit code 2, got %d", code)
	}
}

func TestRunImportCommand_EmptyEnvFile(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	// Set GOCLAW_HOME to a temp dir
	homeDir := filepath.Join(dir, "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOCLAW_HOME", homeDir)

	code := runImportCommand(nil, []string{"--path", envFile})
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
}

func TestRunImportCommand_ImportsKeys(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	content := strings.Join([]string{
		"GEMINI_API_KEY=test-key-123",
		"BRAVE_API_KEY=brave-test-456",
	}, "\n")
	if err := os.WriteFile(envFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	homeDir := filepath.Join(dir, "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOCLAW_HOME", homeDir)

	code := runImportCommand(nil, []string{"--path", envFile})
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}

	// Verify config.yaml was written
	cfgPath := filepath.Join(homeDir, "config.yaml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal("config.yaml not created")
	}
	if !strings.Contains(string(data), "test-key-123") {
		t.Error("config.yaml missing imported gemini key")
	}
}
