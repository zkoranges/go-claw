package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/basket/go-claw/internal/policy"
)

func main() {
	p, err := policy.Load(filepath.Join("/tmp", "goclaw-missing-policy.yaml"))
	if err != nil {
		fmt.Printf("load_error=%v\n", err)
		os.Exit(1)
	}

	ok := true
	assertFalse := func(name string, got bool) {
		fmt.Printf("%s=%v\n", name, got)
		if got {
			ok = false
		}
	}
	assertTrue := func(name string, got bool) {
		fmt.Printf("%s=%v\n", name, got)
		if !got {
			ok = false
		}
	}

	assertFalse("default_allow_example", p.AllowHTTPURL("https://example.com"))
	assertFalse("default_allow_html_duckduckgo", p.AllowHTTPURL("https://html.duckduckgo.com/html/?q=test"))
	assertFalse("default_allow_cap_acp_read", p.AllowCapability("acp.read"))
	assertFalse("default_allow_cap_legacy_run", p.AllowCapability("legacy.run"))

	dir, err := os.MkdirTemp("", "goclaw-policy-verify-*")
	if err != nil {
		fmt.Printf("mktemp_error=%v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	policyPath := filepath.Join(dir, "policy.yaml")
	valid := "allow_domains:\n  - api.weather.com\nallow_capabilities:\n  - acp.read\n"
	if err := os.WriteFile(policyPath, []byte(valid), 0o644); err != nil {
		fmt.Printf("write_valid_error=%v\n", err)
		os.Exit(1)
	}
	initial, err := policy.Load(policyPath)
	if err != nil {
		fmt.Printf("load_valid_error=%v\n", err)
		os.Exit(1)
	}
	live := policy.NewLivePolicy(initial, policyPath)

	invalid := "allow_capabilities:\n  - acp.read\n  - acp.unknown\n"
	if err := os.WriteFile(policyPath, []byte(invalid), 0o644); err != nil {
		fmt.Printf("write_invalid_error=%v\n", err)
		os.Exit(1)
	}
	reloadErr := policy.ReloadFromFile(live, policyPath)
	fmt.Printf("reload_error_present=%v\n", reloadErr != nil)
	if reloadErr == nil {
		ok = false
	}

	assertTrue("retain_previous_domain", live.AllowHTTPURL("https://api.weather.com/v3/wx/conditions/current"))
	assertTrue("retain_previous_cap", live.AllowCapability("acp.read"))
	assertFalse("deny_unknown_cap", live.AllowCapability("acp.unknown"))

	if !ok {
		fmt.Println("VERDICT FAIL")
		os.Exit(1)
	}
	fmt.Println("VERDICT PASS")
}
