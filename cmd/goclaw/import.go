package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/basket/go-claw/internal/config"
	"gopkg.in/yaml.v3"
)

func runImportCommand(ctx context.Context, args []string) int {
	_ = ctx

	fs := flag.NewFlagSet("goclaw import", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	envPath := fs.String("path", ".env", "path to legacy .env file")
	force := fs.Bool("force", false, "overwrite existing config.yaml values")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(fs.Args()) != 0 {
		fmt.Fprintln(os.Stderr, "usage: goclaw import [--path .env] [--force]")
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config load: %v\n", err)
		return 1
	}

	kv, err := parseDotEnvFile(*envPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read env: %v\n", err)
		return 1
	}
	if len(kv) == 0 {
		fmt.Fprintln(os.Stdout, "no keys imported (empty env file)")
		return 0
	}

	cfgPath := config.ConfigPath(cfg.HomeDir)
	raw := make(map[string]any)
	if b, err := os.ReadFile(cfgPath); err == nil && len(b) > 0 {
		if err := yaml.Unmarshal(b, &raw); err != nil {
			fmt.Fprintf(os.Stderr, "parse config.yaml: %v\n", err)
			return 1
		}
	}

	setIfEmpty := func(key string, val any) (changed bool) {
		existing, ok := raw[key]
		if ok && !*force {
			if s, ok := existing.(string); ok && strings.TrimSpace(s) != "" {
				return false
			}
		}
		raw[key] = val
		return true
	}

	changedAny := false
	var imported []string
	var skipped []string

	if v := strings.TrimSpace(kv["GEMINI_API_KEY"]); v != "" {
		if setIfEmpty("gemini_api_key", v) {
			imported = append(imported, "GEMINI_API_KEY")
			changedAny = true
		} else {
			skipped = append(skipped, "GEMINI_API_KEY")
		}
	}
	if v := strings.TrimSpace(kv["GEMINI_MODEL"]); v != "" {
		if setIfEmpty("gemini_model", v) {
			imported = append(imported, "GEMINI_MODEL")
			changedAny = true
		} else {
			skipped = append(skipped, "GEMINI_MODEL")
		}
	}

	apiKeys, _ := raw["api_keys"].(map[string]any)
	if apiKeys == nil {
		apiKeys = make(map[string]any)
	}
	setAPIKey := func(configKey, envKey string) {
		v := strings.TrimSpace(kv[envKey])
		if v == "" {
			return
		}
		existing, ok := apiKeys[configKey]
		if ok && !*force {
			if s, ok := existing.(string); ok && strings.TrimSpace(s) != "" {
				skipped = append(skipped, envKey)
				return
			}
		}
		apiKeys[configKey] = v
		imported = append(imported, envKey)
		changedAny = true
	}
	setAPIKey("brave_search", "BRAVE_API_KEY")
	setAPIKey("perplexity_search", "PERPLEXITY_API_KEY")
	setAPIKey("openrouter", "OPENROUTER_API_KEY")
	if len(apiKeys) > 0 {
		raw["api_keys"] = apiKeys
	}

	if !changedAny {
		fmt.Fprintln(os.Stdout, "no keys imported (already set)")
		return 0
	}

	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir config dir: %v\n", err)
		return 1
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal config.yaml: %v\n", err)
		return 1
	}
	if err := os.WriteFile(cfgPath, out, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write config.yaml: %v\n", err)
		return 1
	}

	if len(imported) > 0 {
		fmt.Fprintf(os.Stdout, "imported: %s\n", strings.Join(imported, ", "))
	}
	if len(skipped) > 0 {
		fmt.Fprintf(os.Stdout, "skipped: %s\n", strings.Join(skipped, ", "))
	}
	return 0
}

func parseDotEnvFile(path string) (map[string]string, error) {
	out := make(map[string]string)
	b, err := os.ReadFile(path)
	if err != nil {
		// Missing .env is not fatal for import.
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	lines := strings.Split(string(b), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		if k == "" {
			continue
		}
		out[k] = v
	}
	return out, nil
}
