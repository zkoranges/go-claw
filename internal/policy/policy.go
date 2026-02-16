package policy

import (
	"fmt"
	"hash/fnv"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Checker is the interface used by consumers to check URL access.
type Checker interface {
	AllowHTTPURL(raw string) bool
	AllowCapability(capability string) bool
	AllowPath(path string) bool
	PolicyVersion() string
}

// MCPRule is a single MCP policy rule (v0.4).
type MCPRule struct {
	Agent  string   `yaml:"agent"`  // agent_id or "*"
	Server string   `yaml:"server"` // server name or "*"
	Tools  []string `yaml:"tools"`  // tool names or ["*"]
}

// MCPPolicyConfig holds MCP-specific policy rules (v0.4).
type MCPPolicyConfig struct {
	Default string    `yaml:"default"` // "deny" or "allow"
	Rules   []MCPRule `yaml:"rules"`
}

// Policy is the serializable policy data.
type Policy struct {
	AllowDomains      []string       `yaml:"allow_domains"`
	AllowPaths        []string       `yaml:"allow_paths"`
	AllowCapabilities []string       `yaml:"allow_capabilities"`
	AllowLoopback     bool           `yaml:"allow_loopback"`
	MCP               MCPPolicyConfig `yaml:"mcp,omitempty"` // v0.4
}

func Default() Policy {
	return Policy{
		AllowDomains:      nil,
		AllowPaths:        nil,
		AllowCapabilities: nil,
	}
}

var knownCapabilities = map[string]struct{}{
	"acp.read":               {},
	"acp.mutate":             {},
	"tools.web_search":       {},
	"tools.read_url":         {},
	"tools.read_file":        {},
	"tools.write_file":       {},
	"tools.exec":             {},
	"tools.spawn_task":       {},
	"tools.delegate_task":    {},
	"tools.send_message":     {},
	"tools.read_messages":    {},
	"tools.memory_read":      {},
	"tools.memory_write":     {},
	"tools.send_alert":       {},
	"wasm.http.get":          {},
	"wasm.kv.set":            {},
	"legacy.run":             {},
	"legacy.dangerous":       {},
	"skill.inject":           {},
	"tools.mcp":              {},
	"agent.create":           {},
	"agent.remove":           {},
	"tools.price_comparison": {},
}

func Load(path string) (Policy, error) {
	if path == "" {
		return Default(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return Policy{}, fmt.Errorf("read policy: %w", err)
	}
	if len(data) == 0 {
		return Default(), nil
	}
	var p Policy
	if err := yaml.Unmarshal(data, &p); err != nil {
		return Policy{}, fmt.Errorf("parse policy: %w", err)
	}
	if err := p.validate(); err != nil {
		return Policy{}, err
	}
	return p, nil
}

func (p Policy) AllowHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return false
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if isBlockedHost(host, p.AllowLoopback) {
		return false
	}
	for _, domain := range p.AllowDomains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" {
			continue
		}
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func isBlockedHost(host string, allowLoopback bool) bool {
	if host == "localhost" {
		return !allowLoopback
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false // Not an IP address (e.g. a hostname).
	}
	if allowLoopback && ip.IsLoopback() {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

func (p Policy) AllowCapability(capability string) bool {
	capability = strings.ToLower(strings.TrimSpace(capability))
	if capability == "" {
		return false
	}
	for _, allowed := range p.AllowCapabilities {
		if strings.ToLower(strings.TrimSpace(allowed)) == capability {
			return true
		}
	}
	return false
}

func (p Policy) PolicyVersion() string {
	return policyVersionFor(p)
}

// AllowPath checks whether a filesystem path is within an allowed prefix.
// An empty AllowPaths list permits all paths (backward compatibility).
func (p Policy) AllowPath(path string) bool {
	if len(p.AllowPaths) == 0 {
		return true
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		// For new files, try resolving the parent directory.
		resolved, err = filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			return false
		}
		resolved = filepath.Join(resolved, filepath.Base(path))
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return false
	}
	for _, allowed := range p.AllowPaths {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			continue
		}
		allowedAbs, err := filepath.Abs(allowed)
		if err != nil {
			continue
		}
		// Resolve symlinks on the allowed path as well (e.g. /var -> /private/var on macOS).
		if evalAllowed, evalErr := filepath.EvalSymlinks(allowedAbs); evalErr == nil {
			allowedAbs = evalAllowed
		}
		if resolved == allowedAbs || strings.HasPrefix(resolved, allowedAbs+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// AllowMCPTool checks whether agent may invoke tool on an MCP server.
// Specificity order (highest to lowest):
//   1. Exact agent + exact server + exact tool
//   2. Exact agent + exact server + wildcard tool
//   3. Exact agent + wildcard server + wildcard tool
//   4. Wildcard agent + exact server + exact tool
//   ... etc (most-specific rule wins)
// If no rule matches, falls back to default (default deny if unset).
func (p Policy) AllowMCPTool(agentID, serverName, toolName string) bool {
	// Default is deny unless explicitly set to "allow"
	defaultAllow := strings.ToLower(strings.TrimSpace(p.MCP.Default)) == "allow"

	agentID = strings.ToLower(strings.TrimSpace(agentID))
	serverName = strings.ToLower(strings.TrimSpace(serverName))
	toolName = strings.ToLower(strings.TrimSpace(toolName))

	// Find the most-specific matching rule
	var bestMatch *MCPRule
	var bestScore int

	for i := range p.MCP.Rules {
		rule := &p.MCP.Rules[i]

		ruleAgent := strings.ToLower(strings.TrimSpace(rule.Agent))
		ruleServer := strings.ToLower(strings.TrimSpace(rule.Server))

		// Calculate specificity score (higher = more specific)
		score := 0

		// Match agent
		if ruleAgent == agentID {
			score += 4 // Exact agent match
		} else if ruleAgent == "*" {
			score += 1 // Wildcard agent
		} else {
			continue // No match on agent dimension
		}

		// Match server
		if ruleServer == serverName {
			score += 2 // Exact server match
		} else if ruleServer == "*" {
			score += 0 // Wildcard server (adds no score)
		} else {
			continue // No match on server dimension
		}

		// Match tool
		toolMatches := false
		for _, t := range rule.Tools {
			t = strings.ToLower(strings.TrimSpace(t))
			if t == "*" || t == toolName {
				toolMatches = true
				break
			}
		}
		if !toolMatches {
			if len(rule.Tools) > 0 {
				// Explicit tool list that doesn't match
				continue
			}
			// Empty tool list means deny this combo
		}

		// This rule matches; is it more specific than what we've seen?
		if bestMatch == nil || score > bestScore {
			bestMatch = rule
			bestScore = score
		}
	}

	// Apply the best matching rule
	if bestMatch != nil {
		// Check if tool is in the allowed list
		for _, t := range bestMatch.Tools {
			t = strings.ToLower(strings.TrimSpace(t))
			if t == "*" || t == toolName {
				return true
			}
		}
		// Rule matched but tool not in list → deny
		return false
	}

	// No rule matched → apply default
	return defaultAllow
}

func (p Policy) validate() error {
	for _, capName := range p.AllowCapabilities {
		capability := strings.ToLower(strings.TrimSpace(capName))
		if capability == "" {
			continue
		}
		if _, ok := knownCapabilities[capability]; !ok {
			return fmt.Errorf("unknown capability %q", capName)
		}
	}
	return nil
}

// LivePolicy wraps a Policy with thread-safe mutation and persistence.
type LivePolicy struct {
	mu   sync.RWMutex
	data Policy
	path string // file path for persistence; empty = no persistence
}

// NewLivePolicy creates a LivePolicy from an initial Policy snapshot.
// If path is non-empty, mutations are persisted to that file.
func NewLivePolicy(initial Policy, path string) *LivePolicy {
	return &LivePolicy{data: initial, path: path}
}

// AllowHTTPURL is the thread-safe check used at runtime.
func (lp *LivePolicy) AllowHTTPURL(raw string) bool {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	return lp.data.AllowHTTPURL(raw)
}

// AllowCapability is the thread-safe capability check used at runtime.
func (lp *LivePolicy) AllowCapability(capability string) bool {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	return lp.data.AllowCapability(capability)
}

func (lp *LivePolicy) PolicyVersion() string {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	return policyVersionFor(lp.data)
}

// AllowPath is the thread-safe path check used at runtime.
func (lp *LivePolicy) AllowPath(path string) bool {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	return lp.data.AllowPath(path)
}

// containsNormalized checks if a slice already contains a value (case-insensitive, trimmed).
func containsNormalized(slice []string, val string) bool {
	for _, s := range slice {
		if strings.ToLower(strings.TrimSpace(s)) == val {
			return true
		}
	}
	return false
}

// AllowDomain adds a domain at runtime and persists the change.
func (lp *LivePolicy) AllowDomain(domain string) error {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return fmt.Errorf("empty domain")
	}

	lp.mu.Lock()
	defer lp.mu.Unlock()

	if containsNormalized(lp.data.AllowDomains, domain) {
		return nil
	}
	lp.data.AllowDomains = append(lp.data.AllowDomains, domain)
	return lp.persist()
}

// AddCapability grants a capability at runtime and persists the change.
func (lp *LivePolicy) AddCapability(cap string) error {
	cap = strings.ToLower(strings.TrimSpace(cap))
	if cap == "" {
		return fmt.Errorf("empty capability")
	}
	if _, ok := knownCapabilities[cap]; !ok {
		return fmt.Errorf("unknown capability %q", cap)
	}

	lp.mu.Lock()
	defer lp.mu.Unlock()

	if containsNormalized(lp.data.AllowCapabilities, cap) {
		return nil
	}
	lp.data.AllowCapabilities = append(lp.data.AllowCapabilities, cap)
	return lp.persist()
}

// Reload replaces the policy data from a fresh Policy snapshot.
func (lp *LivePolicy) Reload(p Policy) {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	lp.data = p
}

// Snapshot returns a copy of the current policy data.
func (lp *LivePolicy) Snapshot() Policy {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	cp := lp.data
	cp.AllowDomains = append([]string(nil), lp.data.AllowDomains...)
	cp.AllowPaths = append([]string(nil), lp.data.AllowPaths...)
	cp.AllowCapabilities = append([]string(nil), lp.data.AllowCapabilities...)
	cp.AllowLoopback = lp.data.AllowLoopback
	return cp
}

// ReloadFromFile updates the live policy only when the incoming file parses and validates.
// On error, the previous policy remains active.
func ReloadFromFile(lp *LivePolicy, path string) error {
	if lp == nil {
		return fmt.Errorf("nil live policy")
	}
	p, err := Load(path)
	if err != nil {
		return err
	}
	lp.Reload(p)
	return nil
}

func policyVersionFor(p Policy) string {
	h := fnv.New64a()
	for _, v := range p.AllowDomains {
		_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(v)) + "|"))
	}
	for _, v := range p.AllowPaths {
		_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(v)) + "|"))
	}
	for _, v := range p.AllowCapabilities {
		_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(v)) + "|"))
	}
	if p.AllowLoopback {
		_, _ = h.Write([]byte("allow_loopback=true|"))
	}
	return "policy-" + strconv.FormatUint(h.Sum64(), 16)
}

func (lp *LivePolicy) persist() error {
	if lp.path == "" {
		return nil
	}
	out, err := yaml.Marshal(&lp.data)
	if err != nil {
		return fmt.Errorf("marshal policy: %w", err)
	}
	return os.WriteFile(lp.path, out, 0o644)
}
