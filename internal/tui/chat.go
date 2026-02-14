package tui

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"

	"github.com/basket/go-claw/internal/config"
	"github.com/basket/go-claw/internal/engine"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
	"github.com/basket/go-claw/internal/tools"
)

// AgentSwitcher allows the TUI to list and switch between agents.
// Implemented in main.go to avoid importing the agent package here.
type AgentSwitcher interface {
	SwitchAgent(agentID string) (brain engine.Brain, name, emoji string, err error)
	ListAgentIDs() []string
}

// ChatConfig holds the dependencies for the chat REPL.
type ChatConfig struct {
	Brain        engine.Brain
	Store        *persistence.Store
	Policy       *policy.LivePolicy
	ModelName    string
	HomeDir      string
	Cfg          *config.Config
	CancelFunc   context.CancelFunc
	Providers    []tools.SearchProvider
	AgentName    string
	AgentEmoji   string
	Switcher     AgentSwitcher // nil = single agent mode (backward compat)
	CurrentAgent string
}

// RunChat runs an interactive chat UI on stdin/stdout.
// It blocks until the user types /quit, presses ctrl+d, or quits via the UI.
func RunChat(ctx context.Context, cc ChatConfig) error {
	sessionID := uuid.New().String()
	if err := cc.Store.EnsureSession(ctx, sessionID); err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	model := cc.ModelName
	if model == "" {
		model = "Gemini 2.5 Flash"
	}

	// Build personalized header and prompt prefix.
	agentPrefix := "goclaw"
	if cc.AgentName != "" && cc.AgentEmoji != "" {
		agentPrefix = fmt.Sprintf("%s %s", cc.AgentEmoji, cc.AgentName)
	} else if cc.AgentName != "" {
		agentPrefix = cc.AgentName
	} else {
		agentPrefix = "GoClaw"
	}

	m := newChatModel(ctx, cc, sessionID, agentPrefix, model)
	return runChatTUI(ctx, m, cc.CancelFunc)
}

// handleCommand processes a slash command. Returns true if the chat should exit.
func handleCommand(line string, cc *ChatConfig, sessionID string, out io.Writer) bool {
	parts := strings.SplitN(line, " ", 2)
	cmd := strings.ToLower(parts[0])
	// Common typo/alias.
	if cmd == "/domain" {
		cmd = "/domains"
	}
	arg := ""
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}

	switch cmd {
	case "/quit", "/exit":
		return true

	case "/help":
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Commands:")
		fmt.Fprintln(out, "    /help                        Show this help message")
		fmt.Fprintln(out, "    /agent                       List agents (current marked with *)")
		fmt.Fprintln(out, "    /agent <id>                  Switch to a different agent")
		fmt.Fprintln(out, "    /skills                      List all skills with live status")
		fmt.Fprintln(out, "    /skills setup <name>         Auto-configure a skill")
		fmt.Fprintln(out, "    /allow <domain>              Allow a domain for web access (e.g. /allow reddit.com)")
		fmt.Fprintln(out, "    /domains                     List currently allowed domains")
		fmt.Fprintln(out, "    /config list                 Show configured API keys (values masked)")
		fmt.Fprintln(out, "    /config set <key> <value>    Set an API key (e.g. /config set brave_search BSA...)")
		fmt.Fprintln(out, "    /model                       Interactive provider/model selector")
		fmt.Fprintln(out, "    /model list                  List all providers and models")
		fmt.Fprintln(out, "    /model set <provider/model>  Set model (e.g. /model set gemini/gemini-2.5-pro)")
		fmt.Fprintln(out, "    /session                     Show current session ID")
		fmt.Fprintln(out, "    /quit                        Exit the chat")
		fmt.Fprintln(out)

	case "/allow":
		if arg == "" {
			fmt.Fprintln(out, "  Usage: /allow <domain>  (e.g. /allow reddit.com)")
			fmt.Fprintln(out)
			return false
		}
		if cc.Policy == nil {
			fmt.Fprintln(out, "  Policy not available.")
			fmt.Fprintln(out)
			return false
		}
		if err := cc.Policy.AllowDomain(arg); err != nil {
			fmt.Fprintf(out, "  Error: %v\n\n", err)
			return false
		}
		fmt.Fprintf(out, "  Allowed: %s (saved to policy.yaml)\n\n", strings.ToLower(arg))

	case "/domains":
		if cc.Policy == nil {
			fmt.Fprintln(out, "  Policy not available.")
			fmt.Fprintln(out)
			return false
		}
		snap := cc.Policy.Snapshot()
		if len(snap.AllowDomains) == 0 {
			fmt.Fprintln(out, "  No domains allowed.")
		} else {
			fmt.Fprintln(out)
			fmt.Fprintln(out, "  Allowed domains:")
			for _, d := range snap.AllowDomains {
				fmt.Fprintf(out, "    - %s\n", d)
			}
		}
		fmt.Fprintln(out)

	case "/session":
		fmt.Fprintf(out, "  Session: %s\n\n", sessionID)

	case "/agent":
		handleAgentCommand(arg, cc, out)

	case "/skills":
		handleSkillsCommand(arg, cc, out)

	case "/config":
		handleConfigCommand(arg, cc, out)

	case "/model":
		handleModelCommand(arg, cc, out)

	default:
		fmt.Fprintf(out, "  Unknown command: %s (type /help for available commands)\n\n", cmd)
	}

	return false
}

// handleSkillsCommand processes /skills and /skills setup <name>.
func handleSkillsCommand(arg string, cc *ChatConfig, out io.Writer) {
	catalog := tools.FullCatalog(cc.Providers)
	apiKeys := make(map[string]string)
	if cc.Cfg != nil {
		for k, v := range cc.Cfg.APIKeys {
			apiKeys[k] = v
		}
	}

	statuses := tools.ResolveStatus(catalog, apiKeys, cc.Policy, nil)

	// Fetch WASM skills from store.
	var wasmStatuses []tools.SkillStatus
	if cc.Store != nil {
		records, err := cc.Store.ListSkills(context.Background())
		if err == nil && len(records) > 0 {
			toolRecords := make([]tools.SkillRecord, len(records))
			for i, r := range records {
				toolRecords[i] = tools.SkillRecord{
					SkillID:    r.SkillID,
					Version:    r.Version,
					ABIVersion: r.ABIVersion,
					State:      r.State,
					FaultCount: r.FaultCount,
				}
			}
			wasmStatuses = tools.ResolveWASMStatus(toolRecords)
		}
	}

	parts := strings.SplitN(arg, " ", 2)
	sub := ""
	if len(parts) > 0 {
		sub = strings.ToLower(parts[0])
	}

	switch sub {
	case "setup":
		name := ""
		if len(parts) > 1 {
			name = strings.TrimSpace(parts[1])
		}
		if name == "" {
			fmt.Fprintln(out, "  Usage: /skills setup <name>")
			fmt.Fprintln(out)
			return
		}
		handleSkillSetup(name, catalog, apiKeys, cc, out)

	default:
		// List all skills, grouped by type.
		fmt.Fprintln(out)
		allOK := true

		// Search Providers section.
		var searchStatuses, otherStatuses []tools.SkillStatus
		for _, ss := range statuses {
			isSearch := false
			for _, cap := range ss.Info.Capabilities {
				if cap == "tools.web_search" {
					isSearch = true
					break
				}
			}
			if isSearch {
				searchStatuses = append(searchStatuses, ss)
			} else {
				otherStatuses = append(otherStatuses, ss)
			}
		}

		if len(searchStatuses) > 0 {
			fmt.Fprintln(out, "  Search Providers:")
			for _, ss := range searchStatuses {
				printSkillStatus(ss, out)
				if len(ss.Missing) > 0 {
					allOK = false
				}
			}
		}

		if len(otherStatuses) > 0 {
			fmt.Fprintln(out, "  Other Tools:")
			for _, ss := range otherStatuses {
				printSkillStatus(ss, out)
				if len(ss.Missing) > 0 {
					allOK = false
				}
			}
		}

		if len(wasmStatuses) > 0 {
			fmt.Fprintln(out, "  WASM Skills:")
			for _, ws := range wasmStatuses {
				state := ws.State
				if state == "" {
					state = "active"
				}
				fmt.Fprintf(out, "    %-14s %s\n", ws.Info.Name, state)
				if state == "quarantined" {
					allOK = false
				}
			}
			fmt.Fprintln(out)
		}

		if allOK {
			fmt.Fprintln(out, "  All skills operational.")
		} else {
			fmt.Fprintln(out, "  Some skills need setup. Use /skills setup <name> to configure.")
		}
		fmt.Fprintln(out)
	}
}

func printSkillStatus(ss tools.SkillStatus, out io.Writer) {
	fmt.Fprintf(out, "    %-20s %s\n", ss.Info.Name, ss.Info.Description)

	for _, ak := range ss.Info.APIKeys {
		status := "[configured]"
		if isMissing(ss.Missing, ak.ConfigKey) {
			status = "[not set]"
		}
		label := fmt.Sprintf("%s %s", ak.ConfigKey, status)
		if ak.Optional {
			label += " (optional)"
		}
		fmt.Fprintf(out, "                        API Key:  %s\n", label)
	}

	for _, cap := range ss.Info.Capabilities {
		status := "[granted]"
		if isMissing(ss.Missing, cap) {
			status = "[denied]"
		}
		fmt.Fprintf(out, "                        Caps:     %s %s\n", cap, status)
	}

	for _, domain := range ss.Info.Domains {
		status := "[allowed]"
		if isMissing(ss.Missing, domain) {
			status = "[not allowed]"
		}
		fmt.Fprintf(out, "                        Domains:  %s %s\n", domain, status)
	}

	if ss.Info.SetupHint != "" && len(ss.Missing) > 0 {
		fmt.Fprintf(out, "                        Tip:      %s\n", ss.Info.SetupHint)
	}
	fmt.Fprintln(out)
}

// isMissing reports whether needle appears in any of the Missing entries.
func isMissing(missing []string, needle string) bool {
	for _, m := range missing {
		if strings.Contains(m, needle) {
			return true
		}
	}
	return false
}

func handleSkillSetup(name string, catalog []tools.SkillInfo, apiKeys map[string]string, cc *ChatConfig, out io.Writer) {
	var info *tools.SkillInfo
	for i := range catalog {
		if strings.EqualFold(catalog[i].Name, name) {
			info = &catalog[i]
			break
		}
	}
	if info == nil {
		fmt.Fprintf(out, "  Unknown skill: %s\n\n", name)
		return
	}

	if cc.Policy == nil {
		fmt.Fprintln(out, "  Policy not available.")
		fmt.Fprintln(out)
		return
	}

	configured := 0

	// Add required capabilities.
	for _, cap := range info.Capabilities {
		if cc.Policy.AllowCapability(cap) {
			fmt.Fprintf(out, "  Capability %s already granted.\n", cap)
		} else {
			if err := cc.Policy.AddCapability(cap); err != nil {
				fmt.Fprintf(out, "  Error adding capability %s: %v\n", cap, err)
			} else {
				fmt.Fprintf(out, "  Added capability: %s\n", cap)
				configured++
			}
		}
	}

	// Add required domains.
	for _, domain := range info.Domains {
		testURL := "https://" + domain + "/"
		if cc.Policy.AllowHTTPURL(testURL) {
			fmt.Fprintf(out, "  Domain %s already allowed.\n", domain)
		} else {
			if err := cc.Policy.AllowDomain(domain); err != nil {
				fmt.Fprintf(out, "  Error allowing domain %s: %v\n", domain, err)
			} else {
				fmt.Fprintf(out, "  Allowed domain: %s\n", domain)
				configured++
			}
		}
	}

	// Print API key instructions if needed.
	for _, ak := range info.APIKeys {
		if apiKeys[ak.ConfigKey] != "" {
			fmt.Fprintf(out, "  API key %s already set.\n", ak.ConfigKey)
			continue
		}
		optLabel := ""
		if ak.Optional {
			optLabel = " (optional)"
		}
		fmt.Fprintf(out, "\n  %s%s:\n", ak.Description, optLabel)
		if ak.SignupURL != "" {
			fmt.Fprintf(out, "    1. Sign up at: %s\n", ak.SignupURL)
		}
		fmt.Fprintf(out, "    2. Run: /config set %s <your-key>\n", ak.ConfigKey)
		if ak.EnvVar != "" {
			fmt.Fprintf(out, "    Or set env var: %s\n", ak.EnvVar)
		}
	}

	if configured > 0 {
		fmt.Fprintf(out, "\n  Setup complete — %d setting(s) applied to policy.yaml.\n", configured)
	} else {
		fmt.Fprintln(out, "\n  Nothing to configure — skill is already set up.")
	}
	fmt.Fprintln(out)
}

// handleAgentCommand processes /agent sub-commands.
func handleAgentCommand(arg string, cc *ChatConfig, out io.Writer) {
	if cc.Switcher == nil {
		fmt.Fprintln(out, "  Multi-agent not available.")
		fmt.Fprintln(out)
		return
	}

	if arg == "" {
		// List all agents, mark current with *.
		ids := cc.Switcher.ListAgentIDs()
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Agents:")
		for _, id := range ids {
			marker := "  "
			if id == cc.CurrentAgent {
				marker = "* "
			}
			fmt.Fprintf(out, "    %s%s\n", marker, id)
		}
		fmt.Fprintln(out)
		return
	}

	// Switch to specified agent.
	brain, name, emoji, err := cc.Switcher.SwitchAgent(arg)
	if err != nil {
		fmt.Fprintf(out, "  Error: %v\n\n", err)
		return
	}
	cc.Brain = brain
	cc.AgentName = name
	cc.AgentEmoji = emoji
	cc.CurrentAgent = arg
	fmt.Fprintf(out, "  Switched to agent: %s\n\n", arg)
}

// handleModelCommand processes /model sub-commands.
func handleModelCommand(arg string, cc *ChatConfig, out io.Writer) {
	current := "gemini-2.5-flash"
	if cc.Cfg != nil && cc.Cfg.GeminiModel != "" {
		current = cc.Cfg.GeminiModel
	}
	currentProvider := "google"
	if cc.Cfg != nil && cc.Cfg.LLMProvider != "" {
		currentProvider = cc.Cfg.LLMProvider
	}

	parts := strings.SplitN(arg, " ", 2)
	sub := ""
	if len(parts) > 0 {
		sub = strings.TrimSpace(strings.ToLower(parts[0]))
	}

	switch sub {
	case "":
		// Interactive selector.
		if cc.Cfg == nil {
			fmt.Fprintf(out, "  Current model: %s\n", current)
			fmt.Fprintln(out, "  Config not available for interactive selector.")
			fmt.Fprintln(out)
			return
		}

		provider, model, err := RunModelSelector(cc.Cfg)
		if err != nil {
			fmt.Fprintf(out, "  Current model: %s (provider: %s)\n", current, currentProvider)
			fmt.Fprintln(out)
			return
		}

		if model == current && provider == currentProvider {
			fmt.Fprintf(out, "  Already using %s.\n\n", model)
			return
		}

		if err := config.SetModel(cc.HomeDir, provider, model); err != nil {
			fmt.Fprintf(out, "  Error saving config: %v\n\n", err)
			return
		}

		cc.Cfg.LLMProvider = provider
		cc.Cfg.GeminiModel = model

		fmt.Fprintf(out, "  Model set to: %s (provider: %s)\n", model, provider)
		fmt.Fprintln(out, "  Restart GoClaw for the change to take effect.")
		fmt.Fprintln(out)

	case "list":
		fmt.Fprintln(out)
		// Show all providers and their models.
		for _, bp := range builtinProviders {
			models := BuiltinModels[bp.ID]
			if len(models) == 0 {
				continue
			}
			fmt.Fprintf(out, "  %s:\n", bp.Label)
			for _, m := range models {
				marker := "  "
				if m.ID == current && bp.ID == currentProvider {
					marker = "* "
				}
				fmt.Fprintf(out, "    %s%-34s %s\n", marker, m.ID, m.Desc)
			}
			fmt.Fprintln(out)
		}
		// Show custom providers from config.
		if cc.Cfg != nil && cc.Cfg.Providers != nil {
			for id, pc := range cc.Cfg.Providers {
				if _, isBuiltin := BuiltinModels[id]; isBuiltin {
					// Show user-added models for built-in providers.
					existing := make(map[string]bool)
					for _, m := range BuiltinModels[id] {
						existing[m.ID] = true
					}
					var extra []string
					for _, um := range pc.Models {
						if !existing[um] {
							extra = append(extra, um)
						}
					}
					if len(extra) > 0 {
						fmt.Fprintf(out, "  %s (custom):\n", id)
						for _, um := range extra {
							fmt.Fprintf(out, "      %-34s\n", um)
						}
						fmt.Fprintln(out)
					}
					continue
				}
				if len(pc.Models) > 0 {
					fmt.Fprintf(out, "  %s:\n", id)
					for _, um := range pc.Models {
						marker := "  "
						if um == current && id == currentProvider {
							marker = "* "
						}
						fmt.Fprintf(out, "    %s%-34s\n", marker, um)
					}
					fmt.Fprintln(out)
				}
			}
		}
		fmt.Fprintln(out, "  * = current. Use /model set <provider/model> to change.")
		fmt.Fprintln(out, "  Or use /model for interactive selection.")
		fmt.Fprintln(out)

	case "set":
		modelArg := ""
		if len(parts) > 1 {
			modelArg = strings.TrimSpace(parts[1])
		}
		if modelArg == "" {
			fmt.Fprintln(out, "  Usage: /model set <provider/model>")
			fmt.Fprintln(out, "  Example: /model set gemini/gemini-2.5-pro")
			fmt.Fprintln(out, "  Example: /model set anthropic/claude-sonnet-4-5-20250929")
			fmt.Fprintln(out)
			return
		}

		provider := currentProvider
		model := modelArg

		// Support provider/model format.
		if idx := strings.Index(modelArg, "/"); idx > 0 {
			provider = strings.ToLower(modelArg[:idx])
			model = modelArg[idx+1:]
		}

		// Normalize provider aliases.
		switch provider {
		case "gemini", "googleai":
			provider = "google"
		}

		if model == current && provider == currentProvider {
			fmt.Fprintf(out, "  Already using %s.\n\n", model)
			return
		}

		if err := config.SetModel(cc.HomeDir, provider, model); err != nil {
			fmt.Fprintf(out, "  Error saving config: %v\n\n", err)
			return
		}

		if cc.Cfg != nil {
			cc.Cfg.LLMProvider = provider
			cc.Cfg.GeminiModel = model
		}

		fmt.Fprintf(out, "  Model set to: %s (provider: %s)\n", model, provider)
		fmt.Fprintln(out, "  Restart GoClaw for the change to take effect.")
		fmt.Fprintln(out)

	default:
		fmt.Fprintln(out, "  Usage: /model | /model list | /model set <provider/model>")
		fmt.Fprintln(out)
	}
}

// handleConfigCommand processes /config sub-commands.
func handleConfigCommand(arg string, cc *ChatConfig, out io.Writer) {
	parts := strings.SplitN(arg, " ", 3)
	sub := ""
	if len(parts) > 0 {
		sub = strings.ToLower(parts[0])
	}

	switch sub {
	case "list":
		if cc.Cfg == nil || len(cc.Cfg.APIKeys) == 0 {
			fmt.Fprintln(out, "  No API keys configured.")
			fmt.Fprintln(out, "  Use: /config set <key> <value>")
			fmt.Fprintln(out)
			return
		}
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  API Keys:")
		for k, v := range cc.Cfg.APIKeys {
			fmt.Fprintf(out, "    %s = %s\n", k, maskValue(v))
		}
		fmt.Fprintln(out)

	case "set":
		if len(parts) < 3 {
			fmt.Fprintln(out, "  Usage: /config set <key> <value>")
			fmt.Fprintln(out, "  Example: /config set brave_search BSAOk4...")
			fmt.Fprintln(out)
			return
		}
		key := parts[1]
		value := parts[2]
		if err := config.SetAPIKey(cc.HomeDir, key, value); err != nil {
			fmt.Fprintf(out, "  Error saving config: %v\n\n", err)
			return
		}
		// Update in-memory config so subsequent searches use the new key.
		if cc.Cfg != nil {
			if cc.Cfg.APIKeys == nil {
				cc.Cfg.APIKeys = make(map[string]string)
			}
			cc.Cfg.APIKeys[key] = value
		}
		fmt.Fprintf(out, "  Saved: api_keys.%s = %s (config.yaml updated)\n\n", key, maskValue(value))

	default:
		fmt.Fprintln(out, "  Usage: /config list | /config set <key> <value>")
		fmt.Fprintln(out)
	}
}

// maskValue shows the first 4 chars and masks the rest.
func maskValue(v string) string {
	if len(v) <= 4 {
		return "****"
	}
	return v[:4] + strings.Repeat("*", len(v)-4)
}
