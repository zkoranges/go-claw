package policy_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/basket/go-claw/internal/policy"
)

func TestLoad_DefaultDenyWhenMissing(t *testing.T) {
	// [SPEC: SPEC-SEC-POLICY-1] [PDR: V-18]
	p, err := policy.Load(filepath.Join(t.TempDir(), "missing-policy.yaml"))
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	if p.AllowHTTPURL("https://example.com") {
		t.Fatalf("default policy must deny all")
	}
	if p.AllowHTTPURL("https://html.duckduckgo.com/html/?q=test") {
		t.Fatalf("default policy must deny duckduckgo as well")
	}
	if p.AllowCapability("acp.mutate") {
		t.Fatalf("default policy must deny capabilities")
	}
}

func TestLoad_AllowlistedDomain(t *testing.T) {
	// [SPEC: SPEC-SEC-POLICY-1] [PDR: V-18]
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	if err := os.WriteFile(path, []byte("allow_domains:\n  - api.weather.com\n"), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	p, err := policy.Load(path)
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	if !p.AllowHTTPURL("https://api.weather.com/v3/wx/conditions/current") {
		t.Fatalf("expected allowlisted domain to be allowed")
	}
	if p.AllowHTTPURL("https://evil.example.com") {
		t.Fatalf("expected non-allowlisted domain to be denied")
	}
}

func TestLoad_UnknownCapabilityRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	if err := os.WriteFile(path, []byte("allow_capabilities:\n  - acp.mutate\n  - acp.unknown\n"), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	if _, err := policy.Load(path); err == nil {
		t.Fatalf("expected unknown capability to be rejected")
	}
}

func TestReloadFromFile_InvalidRetainsPrevious(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")

	if err := os.WriteFile(path, []byte("allow_domains:\n  - api.weather.com\nallow_capabilities:\n  - acp.read\n"), 0o644); err != nil {
		t.Fatalf("write initial policy: %v", err)
	}
	initial, err := policy.Load(path)
	if err != nil {
		t.Fatalf("load initial policy: %v", err)
	}
	live := policy.NewLivePolicy(initial, path)

	if !live.AllowHTTPURL("https://api.weather.com/v3/wx/conditions/current") {
		t.Fatalf("expected initial allowlisted domain")
	}
	if !live.AllowCapability("acp.read") {
		t.Fatalf("expected initial capability")
	}

	if err := os.WriteFile(path, []byte("allow_capabilities:\n  - acp.read\n  - acp.unknown\n"), 0o644); err != nil {
		t.Fatalf("write invalid policy: %v", err)
	}
	if err := policy.ReloadFromFile(live, path); err == nil {
		t.Fatalf("expected reload error for invalid capability")
	}

	// Previous valid snapshot must remain active (fail-closed on invalid reload).
	if !live.AllowHTTPURL("https://api.weather.com/v3/wx/conditions/current") {
		t.Fatalf("expected prior policy to remain active after invalid reload")
	}
	if !live.AllowCapability("acp.read") {
		t.Fatalf("expected prior capabilities to remain active after invalid reload")
	}
	if live.AllowCapability("acp.unknown") {
		t.Fatalf("unknown capability must remain denied")
	}
}

func TestAllowHTTPURL_SSRFAndSchemeBlocks(t *testing.T) {
	p := policy.Policy{
		AllowDomains: []string{"example.com", "127.0.0.1", "localhost"},
	}
	blocked := []string{
		"http://127.0.0.1:8080/",
		"http://localhost:8080/",
		"http://10.0.0.5/data",
		"http://169.254.1.2/meta",
		"ftp://example.com/file",
		"file:///etc/passwd",
	}
	for _, u := range blocked {
		if p.AllowHTTPURL(u) {
			t.Fatalf("expected blocked URL %q", u)
		}
	}
	if !p.AllowHTTPURL("https://example.com/api") {
		t.Fatalf("expected allowlisted public host to pass")
	}

	p.AllowLoopback = true
	if !p.AllowHTTPURL("http://127.0.0.1:8080/ok") {
		t.Fatalf("expected loopback allow when allow_loopback=true")
	}
}

// US-028: SSRF bypass corpus — encoded URLs, alternate representations, scheme tricks.
func TestAddCapability(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	lp := policy.NewLivePolicy(policy.Default(), path)

	// Initially no capabilities.
	if lp.AllowCapability("tools.web_search") {
		t.Fatal("expected default deny")
	}

	// Add a capability.
	if err := lp.AddCapability("tools.web_search"); err != nil {
		t.Fatalf("add capability: %v", err)
	}
	if !lp.AllowCapability("tools.web_search") {
		t.Fatal("expected capability to be granted after AddCapability")
	}

	// Dedup: adding again should not error.
	if err := lp.AddCapability("tools.web_search"); err != nil {
		t.Fatalf("dedup add: %v", err)
	}

	// Persisted: reload from file.
	p2, err := policy.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !p2.AllowCapability("tools.web_search") {
		t.Fatal("expected persisted capability after reload")
	}
}

func TestAddCapability_UnknownRejected(t *testing.T) {
	lp := policy.NewLivePolicy(policy.Default(), "")
	if err := lp.AddCapability("tools.does_not_exist"); err == nil {
		t.Fatal("expected error for unknown capability")
	}
}

func TestAddCapability_EmptyRejected(t *testing.T) {
	lp := policy.NewLivePolicy(policy.Default(), "")
	if err := lp.AddCapability(""); err == nil {
		t.Fatal("expected error for empty capability")
	}
}

func TestAllowHTTPURL_SSRFBypassCorpus(t *testing.T) {
	p := policy.Policy{
		AllowDomains: []string{"api.safe.com"},
	}
	// Every entry in this corpus MUST be denied.
	corpus := []struct {
		name string
		url  string
	}{
		// Standard private/loopback
		{"loopback_127", "http://127.0.0.1/admin"},
		{"loopback_localhost", "http://localhost/admin"},
		{"private_10", "http://10.0.0.1/metadata"},
		{"private_172", "http://172.16.0.1/internal"},
		{"private_192", "http://192.168.1.1/config"},
		{"link_local", "http://169.254.169.254/latest/meta-data/"},

		// IPv6 variants
		{"ipv6_loopback", "http://[::1]/admin"},
		{"ipv6_link_local", "http://[fe80::1]/data"},

		// Scheme bypass attempts
		{"ftp_scheme", "ftp://api.safe.com/file"},
		{"file_scheme", "file:///etc/passwd"},
		{"gopher_scheme", "gopher://api.safe.com:70/"},
		{"data_scheme", "data:text/html,<script>alert(1)</script>"},
		{"javascript_scheme", "javascript:alert(1)"},

		// URL-encoded loopback attempts
		{"encoded_127_dots", "http://127%2e0%2e0%2e1/admin"},
		{"encoded_localhost", "http://%6c%6f%63%61%6c%68%6f%73%74/admin"},

		// Missing or empty host
		{"empty_host", "http:///path"},
		{"no_host", "http://"},

		// Unspecified address
		{"unspecified_v4", "http://0.0.0.0/admin"},
		{"unspecified_v6", "http://[::]/admin"},

		// Not in allowlist
		{"evil_domain", "https://evil.example.com/steal"},
		{"subdomain_trick", "https://api.safe.com.evil.com/steal"},
	}

	for _, tc := range corpus {
		t.Run(tc.name, func(t *testing.T) {
			if p.AllowHTTPURL(tc.url) {
				t.Fatalf("SSRF bypass: %q was NOT denied", tc.url)
			}
		})
	}

	// Positive control: allowlisted domain should still work.
	if !p.AllowHTTPURL("https://api.safe.com/v1/data") {
		t.Fatal("allowlisted domain should pass")
	}
	// Subdomain of allowlisted domain should also work.
	if !p.AllowHTTPURL("https://sub.api.safe.com/v1/data") {
		t.Fatal("subdomain of allowlisted domain should pass")
	}
}

func TestAllowPath_EmptyAllowsAll(t *testing.T) {
	p := policy.Policy{AllowPaths: nil}
	if !p.AllowPath("/any/path/at/all") {
		t.Fatal("empty AllowPaths should allow all paths")
	}
}

func TestAllowPath_SpecificPaths(t *testing.T) {
	dir := t.TempDir()
	p := policy.Policy{AllowPaths: []string{dir}}

	allowed := filepath.Join(dir, "sub", "file.txt")
	// Create the parent so EvalSymlinks resolves it.
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	if !p.AllowPath(allowed) {
		t.Fatalf("path inside AllowPaths should be allowed: %s", allowed)
	}
	if !p.AllowPath(dir) {
		t.Fatal("exact AllowPaths entry should be allowed")
	}

	// A path outside AllowPaths must be denied.
	outside := filepath.Join(os.TempDir(), "not-allowed", "file.txt")
	if p.AllowPath(outside) {
		t.Fatalf("path outside AllowPaths should be denied: %s", outside)
	}
}

func TestAllowPath_TraversalDenied(t *testing.T) {
	dir := t.TempDir()
	p := policy.Policy{AllowPaths: []string{dir}}

	// Attempting to traverse out of allowed dir should be denied.
	traversal := filepath.Join(dir, "..", "escape")
	if p.AllowPath(traversal) {
		t.Fatalf("traversal path should be denied: %s", traversal)
	}
}

func TestLivePolicy_AllowPath(t *testing.T) {
	dir := t.TempDir()
	p := policy.Policy{AllowPaths: []string{dir}}
	lp := policy.NewLivePolicy(p, "")

	allowed := filepath.Join(dir, "file.txt")
	if !lp.AllowPath(allowed) {
		t.Fatal("LivePolicy.AllowPath should delegate to Policy.AllowPath")
	}

	outside := filepath.Join(os.TempDir(), "other")
	if lp.AllowPath(outside) {
		t.Fatal("LivePolicy.AllowPath should deny paths outside AllowPaths")
	}
}
