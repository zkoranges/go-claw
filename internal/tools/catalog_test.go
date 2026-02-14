package tools

import (
	"testing"

	"github.com/basket/go-claw/internal/policy"
)

func TestBuiltinCatalog(t *testing.T) {
	catalog := BuiltinCatalog()
	if len(catalog) < 1 {
		t.Fatalf("expected at least 1 builtin skill, got %d", len(catalog))
	}

	names := map[string]bool{}
	for _, s := range catalog {
		if s.Name == "" {
			t.Error("skill with empty name")
		}
		if s.Type != "builtin" {
			t.Errorf("skill %s: expected type builtin, got %s", s.Name, s.Type)
		}
		names[s.Name] = true
	}
	if !names["read_url"] {
		t.Error("missing expected skill read_url")
	}
}

func TestFullCatalog_IncludesAllProviders(t *testing.T) {
	providers := []SearchProvider{
		NewBraveProvider("key"),
		NewPerplexityProvider("key"),
		NewDDGProvider(),
	}

	catalog := FullCatalog(providers)

	names := map[string]bool{}
	for _, s := range catalog {
		names[s.Name] = true
	}

	for _, want := range []string{"brave_search", "perplexity_search", "duckduckgo", "read_url"} {
		if !names[want] {
			t.Errorf("missing expected skill %q in FullCatalog", want)
		}
	}

	// Each search provider should have tools.web_search capability.
	for _, s := range catalog {
		if s.Name == "read_url" {
			continue
		}
		hasWebSearch := false
		for _, cap := range s.Capabilities {
			if cap == "tools.web_search" {
				hasWebSearch = true
			}
		}
		if !hasWebSearch {
			t.Errorf("provider %s missing tools.web_search capability", s.Name)
		}
	}
}

func TestFullCatalog_ProviderDomains(t *testing.T) {
	providers := []SearchProvider{
		NewBraveProvider("key"),
		NewPerplexityProvider("key"),
		NewDDGProvider(),
	}

	catalog := FullCatalog(providers)

	domainMap := map[string][]string{}
	for _, s := range catalog {
		domainMap[s.Name] = s.Domains
	}

	tests := []struct {
		name   string
		domain string
	}{
		{"brave_search", "api.search.brave.com"},
		{"perplexity_search", "api.perplexity.ai"},
		{"duckduckgo", "html.duckduckgo.com"},
	}
	for _, tt := range tests {
		domains := domainMap[tt.name]
		found := false
		for _, d := range domains {
			if d == tt.domain {
				found = true
			}
		}
		if !found {
			t.Errorf("provider %s: expected domain %s, got %v", tt.name, tt.domain, domains)
		}
	}
}

func TestResolveStatus_AllConfigured(t *testing.T) {
	providers := []SearchProvider{
		NewBraveProvider("test-key"),
		NewDDGProvider(),
	}

	pol := policy.Policy{
		AllowCapabilities: []string{"tools.web_search", "tools.read_url"},
		AllowDomains:      []string{"api.search.brave.com", "html.duckduckgo.com"},
	}
	apiKeys := map[string]string{"brave_search": "test-key"}

	statuses := ResolveStatus(FullCatalog(providers), apiKeys, pol, nil)

	for _, ss := range statuses {
		if !ss.Enabled {
			t.Errorf("skill %s: expected enabled=true", ss.Info.Name)
		}
	}
}

func TestResolveStatus_MissingCapability(t *testing.T) {
	providers := []SearchProvider{NewBraveProvider("key"), NewDDGProvider()}
	pol := policy.Policy{
		AllowCapabilities: []string{}, // No capabilities.
		AllowDomains:      []string{"api.search.brave.com"},
	}
	apiKeys := map[string]string{"brave_search": "key"}

	statuses := ResolveStatus(FullCatalog(providers), apiKeys, pol, nil)

	for _, ss := range statuses {
		if ss.Enabled {
			t.Errorf("skill %s: expected enabled=false with no capabilities", ss.Info.Name)
		}
	}
}

func TestResolveStatus_NilPolicy(t *testing.T) {
	providers := []SearchProvider{NewDDGProvider()}
	apiKeys := map[string]string{}
	statuses := ResolveStatus(FullCatalog(providers), apiKeys, nil, nil)

	for _, ss := range statuses {
		if ss.Enabled {
			t.Errorf("skill %s: expected enabled=false with nil policy", ss.Info.Name)
		}
	}
}

func TestResolveStatus_PerplexityMissingKey(t *testing.T) {
	providers := []SearchProvider{NewPerplexityProvider("")}
	pol := policy.Policy{
		AllowCapabilities: []string{"tools.web_search"},
		AllowDomains:      []string{"api.perplexity.ai"},
	}

	statuses := ResolveStatus(FullCatalog(providers), map[string]string{}, pol, nil)

	var ps *SkillStatus
	for i := range statuses {
		if statuses[i].Info.Name == "perplexity_search" {
			ps = &statuses[i]
			break
		}
	}
	if ps == nil {
		t.Fatal("perplexity_search not found in statuses")
	}
	if ps.Configured {
		t.Error("expected configured=false when perplexity key is missing")
	}
}

func TestResolveWASMStatus(t *testing.T) {
	records := []SkillRecord{
		{SkillID: "random", Version: "1.0", ABIVersion: "1", State: "active", FaultCount: 0},
		{SkillID: "broken", Version: "0.2", ABIVersion: "1", State: "quarantined", FaultCount: 5},
	}

	statuses := ResolveWASMStatus(records)
	if len(statuses) != 2 {
		t.Fatalf("expected 2, got %d", len(statuses))
	}

	if statuses[0].Info.Name != "random" || statuses[0].State != "active" {
		t.Errorf("first skill: name=%q state=%q", statuses[0].Info.Name, statuses[0].State)
	}
	if statuses[1].Info.Name != "broken" || statuses[1].State != "quarantined" {
		t.Errorf("second skill: name=%q state=%q", statuses[1].Info.Name, statuses[1].State)
	}
	if statuses[0].Info.Type != "wasm" {
		t.Errorf("expected type=wasm, got %q", statuses[0].Info.Type)
	}
}
