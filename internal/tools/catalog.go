package tools

import (
	"context"
	"strings"

	"github.com/basket/go-claw/internal/policy"
)

// SkillInfo describes a known skill and its requirements.
type SkillInfo struct {
	Name         string
	Description  string
	Type         string // "builtin", "wasm", "legacy", "instruction"
	Source       string // "project", "user", "installed", "builtin"
	SourceURL    string // GitHub URL for installed skills
	APIKeys      []APIKeyReq
	Capabilities []string
	Domains      []string
	SetupHint    string
}

// APIKeyReq describes a required or optional API key for a skill.
type APIKeyReq struct {
	ConfigKey   string // Key in config.yaml api_keys section, e.g. "brave_search"
	EnvVar      string // Env var override, e.g. "BRAVE_API_KEY"
	Description string
	SignupURL   string
	Optional    bool // true = enhances but not required
}

// SkillStatus is the resolved runtime status of a skill.
type SkillStatus struct {
	Info       SkillInfo
	Configured bool     // All required (non-optional) API keys present
	Enabled    bool     // All required capabilities granted
	Eligible   bool     // Passed skill eligibility checks (bins/env/os)
	Missing    []string // Human-readable list of what's missing
	State      string   // For WASM: "active", "quarantined", "not_loaded"
}

// BuiltinCatalog returns the static catalog of non-provider built-in skills.
func BuiltinCatalog() []SkillInfo {
	return []SkillInfo{
		{
			Name:         "read_url",
			Description:  "Fetch and read web pages",
			Type:         "builtin",
			Source:       "builtin",
			Capabilities: []string{"tools.read_url"},
			SetupHint:    "Use /allow <domain> to allow specific sites.",
		},
	}
}

// FullCatalog returns one SkillInfo per search provider plus the static
// built-in skills (read_url). Each provider is its own independent entry.
func FullCatalog(providers []SearchProvider) []SkillInfo {
	var catalog []SkillInfo
	for _, p := range providers {
		catalog = append(catalog, SkillInfo{
			Name:         p.Name(),
			Description:  p.Description(),
			Type:         "builtin",
			Source:       "builtin",
			APIKeys:      p.APIKeyReqs(),
			Capabilities: []string{"tools.web_search"},
			Domains:      p.Domains(),
		})
	}
	catalog = append(catalog, BuiltinCatalog()...)
	return catalog
}

// SkillRecord holds fields returned from the skill_registry table.
type SkillRecord struct {
	SkillID    string
	Version    string
	ABIVersion string
	State      string
	FaultCount int
}

// SkillLister is the interface for listing WASM skills from the store.
type SkillLister interface {
	ListSkills(ctx context.Context) ([]SkillRecord, error)
}

type SkillEligibility struct {
	Eligible bool
	Missing  []string
}

// ResolveStatus computes the live status of each skill in the catalog.
// If eligibility is provided, it is merged into the returned status rows.
func ResolveStatus(catalog []SkillInfo, apiKeys map[string]string, pol policy.Checker, eligibility map[string]SkillEligibility) []SkillStatus {
	out := make([]SkillStatus, 0, len(catalog))
	for _, info := range catalog {
		ss := SkillStatus{Info: info, Configured: true, Enabled: true, Eligible: true}

		// Merge eligibility info (skills loader requirements).
		if eligibility != nil {
			if el, ok := eligibility[strings.ToLower(strings.TrimSpace(info.Name))]; ok {
				ss.Eligible = el.Eligible
				if !el.Eligible && len(el.Missing) > 0 {
					ss.Missing = append(ss.Missing, el.Missing...)
				}
			}
		}

		// Check API keys.
		for _, ak := range info.APIKeys {
			if apiKeys[ak.ConfigKey] != "" || ak.Optional {
				continue
			}
			ss.Configured = false
			ss.Missing = append(ss.Missing, "API key: "+ak.ConfigKey+" ("+ak.Description+")")
		}

		// Check capabilities.
		for _, cap := range info.Capabilities {
			if pol == nil || !pol.AllowCapability(cap) {
				ss.Enabled = false
				ss.Missing = append(ss.Missing, "Capability: "+cap)
			}
		}

		// Check domains.
		for _, domain := range info.Domains {
			testURL := "https://" + domain + "/"
			if pol == nil || !pol.AllowHTTPURL(testURL) {
				ss.Missing = append(ss.Missing, "Domain: "+domain)
			}
		}

		out = append(out, ss)
	}
	return out
}

// ResolveWASMStatus builds SkillStatus entries from WASM skill records.
func ResolveWASMStatus(records []SkillRecord) []SkillStatus {
	out := make([]SkillStatus, 0, len(records))
	for _, r := range records {
		out = append(out, SkillStatus{
			Info: SkillInfo{
				Name:        r.SkillID,
				Description: "WASM skill (v" + r.Version + ")",
				Type:        "wasm",
				Source:      "user",
			},
			Configured: true,
			Enabled:    true,
			Eligible:   true,
			State:      strings.ToLower(r.State),
		})
	}
	return out
}
