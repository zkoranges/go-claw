package doctor

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/basket/go-claw/internal/config"
	"github.com/basket/go-claw/internal/persistence"
)

type CheckResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "PASS", "FAIL", "WARN", "SKIP"
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

type Diagnosis struct {
	Timestamp time.Time     `json:"timestamp"`
	System    SystemInfo    `json:"system"`
	Results   []CheckResult `json:"results"`
}

type SystemInfo struct {
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Go      string `json:"go_version"`
	Version string `json:"version"`
}

// Run executes all diagnostic checks.
func Run(ctx context.Context, cfg *config.Config, version string) Diagnosis {
	d := Diagnosis{
		Timestamp: time.Now().UTC(),
		System: SystemInfo{
			OS:      runtime.GOOS,
			Arch:    runtime.GOARCH,
			Go:      runtime.Version(),
			Version: version,
		},
	}

	checks := []func(context.Context, *config.Config) CheckResult{
		checkConfig,
		checkAPIKey,
		checkDatabase,
		checkPermissions,
		checkExternalTools,
		checkNetwork,
	}

	for _, check := range checks {
		d.Results = append(d.Results, check(ctx, cfg))
	}

	return d
}

func checkConfig(ctx context.Context, cfg *config.Config) CheckResult {
	if cfg == nil {
		return CheckResult{Name: "Config", Status: "FAIL", Message: "Configuration not loaded"}
	}
	if cfg.NeedsGenesis {
		return CheckResult{Name: "Config", Status: "WARN", Message: "Configuration missing (needs genesis)"}
	}
	return CheckResult{Name: "Config", Status: "PASS", Message: fmt.Sprintf("Loaded from %s", cfg.HomeDir)}
}

func checkAPIKey(_ context.Context, cfg *config.Config) CheckResult {
	if cfg == nil {
		return CheckResult{Name: "API Key", Status: "SKIP", Message: "Config missing"}
	}

	provider := "google"
	if cfg.LLM.Provider != "" {
		provider = strings.ToLower(cfg.LLM.Provider)
	} else if cfg.LLMProvider != "" {
		provider = strings.ToLower(cfg.LLMProvider)
	}

	envVars := map[string]string{
		"google":    "GEMINI_API_KEY",
		"openai":    "OPENAI_API_KEY",
		"anthropic": "ANTHROPIC_API_KEY",
	}

	envVar, ok := envVars[provider]
	if !ok {
		// Ollama, openai_compatible, etc. — no key required or provider-specific
		return CheckResult{Name: "API Key", Status: "PASS", Message: fmt.Sprintf("Provider %q uses api_key from config (no standard env var)", provider)}
	}

	if os.Getenv(envVar) != "" {
		return CheckResult{Name: "API Key", Status: "PASS", Message: fmt.Sprintf("%s is set", envVar)}
	}

	return CheckResult{
		Name:    "API Key",
		Status:  "WARN",
		Message: fmt.Sprintf("%s not set (required for %s provider)", envVar, provider),
		Detail:  fmt.Sprintf("Set %s or use /config in the TUI to configure", envVar),
	}
}

func checkDatabase(ctx context.Context, cfg *config.Config) CheckResult {
	if cfg == nil || cfg.NeedsGenesis {
		return CheckResult{Name: "Database", Status: "SKIP", Message: "Config missing"}
	}
	// Try to open DB
	// Reconstruct path logic from main.go
	// DefaultDBPath in persistence uses hardcoded ~/.goclaw if not provided.
	// main.go uses filepath.Join(cfg.HomeDir, "goclaw.db")
	realDBPath := fmt.Sprintf("%s/goclaw.db", cfg.HomeDir)

	store, err := persistence.Open(realDBPath, nil)
	if err != nil {
		return CheckResult{Name: "Database", Status: "FAIL", Message: fmt.Sprintf("Connection failed: %v", err)}
	}
	defer store.Close()

	// Check schema version (implicit in Open(), but let's check a query)
	if _, err := store.TotalEventCount(ctx); err != nil {
		return CheckResult{Name: "Database", Status: "FAIL", Message: fmt.Sprintf("Query failed: %v", err)}
	}

	return CheckResult{Name: "Database", Status: "PASS", Message: "Connection and schema valid"}
}

func checkPermissions(ctx context.Context, cfg *config.Config) CheckResult {
	if cfg == nil {
		return CheckResult{Name: "Permissions", Status: "SKIP", Message: "Config missing"}
	}

	// Check HomeDir write
	testFile := fmt.Sprintf("%s/.write_test", cfg.HomeDir)
	if err := os.WriteFile(testFile, []byte("test"), 0o600); err != nil {
		return CheckResult{Name: "Permissions", Status: "FAIL", Message: fmt.Sprintf("Home dir unwritable: %v", err)}
	}
	os.Remove(testFile)

	return CheckResult{Name: "Permissions", Status: "PASS", Message: "Home directory writable"}
}

func checkExternalTools(ctx context.Context, cfg *config.Config) CheckResult {
	var details []string
	status := "PASS"

	// Check git
	if _, err := exec.LookPath("git"); err != nil {
		details = append(details, "git: missing (required for skill install)")
		status = "WARN"
	} else {
		details = append(details, "git: ok")
	}

	// Check docker if sandbox enabled
	if cfg != nil && cfg.Tools.Shell.Sandbox {
		if _, err := exec.LookPath("docker"); err != nil {
			details = append(details, "docker: missing (required for sandbox)")
			status = "FAIL"
		} else {
			// Try basic docker command
			cmd := exec.CommandContext(ctx, "docker", "info")
			if err := cmd.Run(); err != nil {
				details = append(details, fmt.Sprintf("docker: daemon unreachable (%v)", err))
				status = "FAIL"
			} else {
				details = append(details, "docker: ok")
			}
		}
	} else {
		details = append(details, "docker: skipped (sandbox disabled)")
	}

	return CheckResult{
		Name:    "External Tools",
		Status:  status,
		Message: fmt.Sprintf("Checked %d tools", len(details)),
		Detail:  fmt.Sprintf("%v", details), // Simplified formatting
	}
}

func checkNetwork(ctx context.Context, cfg *config.Config) CheckResult {
	if cfg == nil {
		return CheckResult{Name: "Network", Status: "SKIP", Message: "Config missing"}
	}

	// Determine LLM provider endpoint.
	provider := "google"
	if cfg.LLM.Provider != "" {
		provider = strings.ToLower(cfg.LLM.Provider)
	} else if cfg.LLMProvider != "" {
		provider = strings.ToLower(cfg.LLMProvider)
	}

	endpoints := map[string]string{
		"google":            "generativelanguage.googleapis.com",
		"anthropic":         "api.anthropic.com",
		"openai":            "api.openai.com",
		"openrouter":        "openrouter.ai",
		"openai_compatible": "api.openai.com",
	}

	host, ok := endpoints[provider]
	if !ok {
		host = "generativelanguage.googleapis.com"
	}

	// DNS lookup with timeout.
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	start := time.Now()
	addrs, err := net.DefaultResolver.LookupHost(lookupCtx, host)
	latency := time.Since(start)

	if err != nil {
		return CheckResult{
			Name:    "Network",
			Status:  "FAIL",
			Message: fmt.Sprintf("DNS lookup failed for %s: %v", host, err),
			Detail:  fmt.Sprintf("provider=%s, latency=%dms", provider, latency.Milliseconds()),
		}
	}

	return CheckResult{
		Name:    "Network",
		Status:  "PASS",
		Message: fmt.Sprintf("DNS resolved %s (%d addresses, %dms)", host, len(addrs), latency.Milliseconds()),
		Detail:  fmt.Sprintf("provider=%s, addresses=%v", provider, addrs),
	}
}
