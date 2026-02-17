package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/config"
	"github.com/basket/go-claw/internal/engine"
	"github.com/basket/go-claw/internal/memory"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
	"github.com/basket/go-claw/internal/tools"
)

// AgentInfo holds display information about an agent.
type AgentInfo struct {
	ID          string
	DisplayName string
	Emoji       string
	Model       string
}

// AgentSwitcher allows the TUI to list and switch between agents.
// Implemented in main.go to avoid importing the agent package here.
type AgentSwitcher interface {
	SwitchAgent(agentID string) (brain engine.Brain, name, emoji string, err error)
	ListAgentIDs() []string
	ListAgentInfo() []AgentInfo
	CreateAgent(ctx context.Context, id, name, provider, model, soul string) error
	RemoveAgent(ctx context.Context, id string) error
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
	EventBus     *bus.Bus // nil = no plan event tracking
	BindAddr     string   // gateway address for /plan execution
	AuthToken    string   // auth token for gateway API calls
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
		fmt.Fprintln(out, "    /agents                      Interactive agent selector")
		fmt.Fprintln(out, "    /agents list                 List agents (current marked with *)")
		fmt.Fprintln(out, "    /agents new <id> [soul]      Create agent with personality")
		fmt.Fprintln(out, "    /agents remove <id>          Remove an agent")
		fmt.Fprintln(out, "    /agents team <role> [roles..] Create a team (e.g. /agents team coder reviewer tester)")
		fmt.Fprintln(out, "    /skills                      List all skills with live status")
		fmt.Fprintln(out, "    /skills setup <name>         Auto-configure a skill")
		fmt.Fprintln(out, "    /allow <domain>              Allow a domain for web access (e.g. /allow reddit.com)")
		fmt.Fprintln(out, "    /domains                     List currently allowed domains")
		fmt.Fprintln(out, "    /config list                 Show configured API keys (values masked)")
		fmt.Fprintln(out, "    /config set <key> <value>    Set an API key (e.g. /config set brave_search BSA...)")
		fmt.Fprintln(out, "    /model                       Interactive provider/model selector")
		fmt.Fprintln(out, "    /model list                  List all providers and models")
		fmt.Fprintln(out, "    /model set <provider/model>  Set model (e.g. /model set gemini/gemini-2.5-pro)")
		fmt.Fprintln(out, "    /plan [<name>]               Run a configured plan (GC-SPEC-PDR-v4-Phase-4)")
		fmt.Fprintln(out, "    /plans                       Show active plan executions (any key to exit)")
		fmt.Fprintln(out, "    /session                     Show current session ID")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Memory & Context:")
		fmt.Fprintln(out, "    /memory list                 List stored facts for current agent")
		fmt.Fprintln(out, "    /memory search <query>       Search agent memory")
		fmt.Fprintln(out, "    /memory delete <key>         Remove a stored fact")
		fmt.Fprintln(out, "    /remember <key> <value>      Store a fact")
		fmt.Fprintln(out, "    /forget <key>                Remove a fact")
		fmt.Fprintln(out, "    /pin <filepath>              Pin file to agent's context")
		fmt.Fprintln(out, "    /pin text <label> <text>     Pin arbitrary text/notes")
		fmt.Fprintln(out, "    /unpin <source>              Remove a pinned item")
		fmt.Fprintln(out, "    /pinned                      List all pinned files for agent")
		fmt.Fprintln(out, "    /context                     Show token budget and context allocation")
		fmt.Fprintln(out, "    /share <key> with <agent>    Share a memory with another agent")
		fmt.Fprintln(out, "    /unshare <key> from <agent>  Revoke memory sharing")
		fmt.Fprintln(out, "    /shared                      List shared knowledge available to agent")
		fmt.Fprintln(out, "    /clear                       Clear conversation history")
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

	case "/agent", "/agents":
		handleAgentCommand(arg, cc, out)

	case "/skills":
		handleSkillsCommand(arg, cc, out)

	case "/config":
		handleConfigCommand(arg, cc, out)

	case "/model":
		handleModelCommand(arg, cc, out)

	case "/plan":
		handlePlanCommand(arg, cc, out)

	case "/memory":
		handleMemoryCommand(arg, cc, out)

	case "/remember":
		handleRememberCommand(arg, cc, out)

	case "/forget":
		handleForgetCommand(arg, cc, out)

	case "/clear":
		handleClearCommand(cc, sessionID, out)

	case "/pin":
		handlePinCommand(arg, cc, out)

	case "/unpin":
		handleUnpinCommand(arg, cc, out)

	case "/pinned":
		handlePinnedCommand(cc, out)

	case "/share":
		handleShareCommand(arg, cc, out)

	case "/unshare":
		handleUnshareCommand(arg, cc, out)

	case "/shared":
		handleSharedCommand(cc, out)

	case "/context":
		handleContextCommand(cc, out)

	default:
		fmt.Fprintf(out, "  Unknown command: %s (type /help for available commands)\n\n", cmd)
	}

	return false
}

// handlePlanCommand executes a plan via the gateway REST API.
func handlePlanCommand(arg string, cc *ChatConfig, out io.Writer) {
	name := strings.TrimSpace(arg)
	if name == "" {
		fmt.Fprintln(out, "  Usage: /plan <name>")
		fmt.Fprintln(out)
		return
	}
	if cc.BindAddr == "" || cc.AuthToken == "" {
		fmt.Fprintln(out, "  Plan execution not available (gateway not configured).")
		fmt.Fprintln(out)
		return
	}

	url := fmt.Sprintf("http://%s/api/plans/%s/execute", cc.BindAddr, name)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		fmt.Fprintf(out, "  Error: %s\n\n", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+cc.AuthToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(out, "  Error: %s\n\n", err)
		return
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Fprintf(out, "  Error decoding response: %s\n\n", err)
		return
	}

	if resp.StatusCode != http.StatusAccepted {
		msg := "unknown error"
		if e, ok := result["error"].(string); ok {
			msg = e
		}
		fmt.Fprintf(out, "  Error: %s\n\n", msg)
		return
	}

	execID, _ := result["execution_id"].(string)
	fmt.Fprintf(out, "  Plan '%s' started (execution_id: %s)\n\n", name, execID)
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

// handleAgentCommand processes /agents sub-commands.
func handleAgentCommand(arg string, cc *ChatConfig, out io.Writer) {
	if cc.Switcher == nil {
		fmt.Fprintln(out, "  Multi-agent not available.")
		fmt.Fprintln(out)
		return
	}

	parts := strings.SplitN(arg, " ", 2)
	sub := ""
	if len(parts) > 0 {
		sub = strings.TrimSpace(strings.ToLower(parts[0]))
	}
	subArg := ""
	if len(parts) > 1 {
		subArg = strings.TrimSpace(parts[1])
	}

	switch sub {
	case "", "list":
		// List all agents, mark current with *.
		infos := cc.Switcher.ListAgentInfo()
		fmt.Fprintln(out)
		fmt.Fprintf(out, "  %-2s %-16s %-20s %s\n", "", "ID", "Name", "Model")
		fmt.Fprintf(out, "  %-2s %-16s %-20s %s\n", "", strings.Repeat("-", 16), strings.Repeat("-", 20), strings.Repeat("-", 20))
		for _, info := range infos {
			marker := " "
			if info.ID == cc.CurrentAgent {
				marker = "*"
			}
			name := info.DisplayName
			if name == "" || name == info.ID {
				name = "-"
			}
			if info.Emoji != "" {
				name = fmt.Sprintf("%s %s", info.Emoji, name)
			}
			model := info.Model
			if model == "" {
				model = "default"
			}
			fmt.Fprintf(out, "  %-2s %-16s %-20s %s\n", marker, info.ID, name, model)
		}
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  * = current. Use /agents <id> to switch.")
		fmt.Fprintln(out)

	case "new":
		if subArg == "" {
			fmt.Fprintln(out, "  Usage: /agents new <id> [soul text]")
			fmt.Fprintln(out)
			return
		}
		newParts := strings.SplitN(subArg, " ", 2)
		newID := strings.TrimSpace(newParts[0])
		soul := ""
		if len(newParts) > 1 {
			soul = strings.TrimSpace(newParts[1])
		}
		if soul == "" {
			soul = fmt.Sprintf("You are a helpful AI assistant named %s.", newID)
		}

		// Inherit provider/model from current config.
		provider := ""
		model := ""
		if cc.Cfg != nil {
			provider = cc.Cfg.LLMProvider
			model = cc.Cfg.GeminiModel
		}

		if err := cc.Switcher.CreateAgent(context.Background(), newID, newID, provider, model, soul); err != nil {
			fmt.Fprintf(out, "  Error: %v\n\n", err)
			return
		}
		fmt.Fprintf(out, "  Created agent: %s\n", newID)

		// Auto-switch to the new agent.
		brain, name, emoji, err := cc.Switcher.SwitchAgent(newID)
		if err != nil {
			fmt.Fprintf(out, "  Warning: created but failed to switch: %v\n\n", err)
			return
		}
		cc.Brain = brain
		cc.AgentName = name
		cc.AgentEmoji = emoji
		cc.CurrentAgent = newID
		fmt.Fprintf(out, "  Switched to agent: %s\n\n", newID)

	case "remove":
		if subArg == "" {
			fmt.Fprintln(out, "  Usage: /agents remove <id>")
			fmt.Fprintln(out)
			return
		}
		removeID := strings.TrimSpace(subArg)
		if removeID == "default" {
			fmt.Fprintln(out, "  Cannot remove the default agent.")
			fmt.Fprintln(out)
			return
		}

		if err := cc.Switcher.RemoveAgent(context.Background(), removeID); err != nil {
			fmt.Fprintf(out, "  Error: %v\n\n", err)
			return
		}
		fmt.Fprintf(out, "  Removed agent: %s\n", removeID)

		// If we removed the current agent, switch to default.
		if cc.CurrentAgent == removeID {
			brain, name, emoji, err := cc.Switcher.SwitchAgent("default")
			if err == nil {
				cc.Brain = brain
				cc.AgentName = name
				cc.AgentEmoji = emoji
				cc.CurrentAgent = "default"
				fmt.Fprintf(out, "  Switched to agent: default\n")
			}
		}
		fmt.Fprintln(out)

	case "team":
		handleTeamCommand(subArg, cc, out)

	default:
		// Treat as agent ID to switch to (backward compat with /agent <id>).
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
}

// knownRoleSouls maps well-known role names to specialized soul descriptions.
var knownRoleSouls = map[string]string{
	"researcher": "You are a research specialist. Find information, analyze data, and provide thorough research summaries. Be methodical and cite your sources.",
	"writer":     "You are a writing specialist. Draft, edit, and polish text. Excel at clear communication, storytelling, and adapting tone for different audiences.",
	"critic":     "You are a critical analyst. Review ideas and work products, identify weaknesses, suggest improvements, and play devil's advocate constructively.",
	"coder":      "You are a coding specialist. Write clean, efficient code. Debug issues, suggest optimizations, and follow best practices for the relevant language.",
	"reviewer":   "You are a code reviewer. Examine code for bugs, security issues, performance problems, and style. Provide specific, actionable feedback.",
	"tester":     "You are a testing specialist. Design test cases, identify edge cases, write test plans, and verify that implementations meet requirements.",
	"planner":    "You are a project planner. Break down tasks, estimate effort, identify dependencies, and create actionable plans with clear milestones.",
	"editor":     "You are an editor. Improve clarity, grammar, structure, and flow. Ensure writing is concise, accurate, and appropriate for its audience.",
	"analyst":    "You are a data analyst. Examine data, identify patterns, draw conclusions, and present findings clearly with supporting evidence.",
	"designer":   "You are a design specialist. Focus on user experience, interface design, information architecture, and visual clarity.",
}

// soulForRole returns a soul description for a given role name.
func soulForRole(role string) string {
	if soul, ok := knownRoleSouls[strings.ToLower(role)]; ok {
		return soul
	}
	return fmt.Sprintf("You are a %s specialist. Focus on %s-related tasks and provide expert guidance in your area.", role, role)
}

// handleTeamCommand processes /agents team sub-commands.
func handleTeamCommand(arg string, cc *ChatConfig, out io.Writer) {
	if strings.TrimSpace(arg) == "" {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Usage: /agents team <role1> [role2] [role3...]")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Examples:")
		fmt.Fprintln(out, "    /agents team coder reviewer tester")
		fmt.Fprintln(out, "    /agents team researcher writer editor")
		fmt.Fprintln(out, "    /agents team analyst planner")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Known roles (auto-generate specialized personality):")
		sorted := []string{"researcher", "writer", "critic", "coder", "reviewer", "tester", "planner", "editor", "analyst", "designer"}
		for _, r := range sorted {
			fmt.Fprintf(out, "    %-12s %s\n", r, knownRoleSouls[r][:60]+"...")
		}
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Custom roles also work — a default personality is generated.")
		fmt.Fprintln(out)
		return
	}

	roles := strings.Fields(arg)
	if len(roles) == 0 {
		fmt.Fprintln(out, "  No roles specified. Usage: /agents team <role1> [role2...]")
		fmt.Fprintln(out)
		return
	}

	provider := ""
	model := ""
	if cc.Cfg != nil {
		provider = cc.Cfg.LLMProvider
		model = cc.Cfg.GeminiModel
	}

	created := 0
	for _, role := range roles {
		id := strings.ToLower(role)
		soul := soulForRole(id)
		if err := cc.Switcher.CreateAgent(context.Background(), id, id, provider, model, soul); err != nil {
			fmt.Fprintf(out, "  Skipped %s: %v\n", id, err)
			continue
		}
		created++
		fmt.Fprintf(out, "  Created: %s\n", id)
	}
	if created > 0 {
		fmt.Fprintf(out, "\n  Team created with %d agent(s). Use /agents to switch between them.\n", created)
		fmt.Fprintln(out, "  Agents can collaborate using delegate_task and send_message tools.")
	} else {
		fmt.Fprintln(out, "  No new agents created (team may already exist).")
	}
	fmt.Fprintln(out)
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
			models := config.BuiltinModels[bp.ID]
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
				if _, isBuiltin := config.BuiltinModels[id]; isBuiltin {
					// Show user-added models for built-in providers.
					existing := make(map[string]bool)
					for _, m := range config.BuiltinModels[id] {
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

// handleMemoryCommand processes /memory subcommands (list, search, delete, clear).
func handleMemoryCommand(arg string, cc *ChatConfig, out io.Writer) {
	if cc.Store == nil {
		fmt.Fprintln(out, "  Store not available.")
		fmt.Fprintln(out)
		return
	}

	parts := strings.SplitN(arg, " ", 2)
	subcmd := strings.ToLower(parts[0])
	subarg := ""
	if len(parts) > 1 {
		subarg = strings.TrimSpace(parts[1])
	}

	ctx := context.Background()
	agentID := cc.CurrentAgent
	if agentID == "" {
		agentID = "default"
	}

	switch subcmd {
	case "list":
		memories, err := cc.Store.ListMemories(ctx, agentID)
		if err != nil {
			fmt.Fprintf(out, "  Error loading memories: %v\n\n", err)
			return
		}
		if len(memories) == 0 {
			fmt.Fprintln(out, "  No stored facts.")
		} else {
			fmt.Fprintln(out)
			fmt.Fprintln(out, "  Stored Facts (by relevance):")
			for _, m := range memories {
				fmt.Fprintf(out, "    • %s: %s [relevance: %.2f, access: %d]\n",
					m.Key, m.Value, m.RelevanceScore, m.AccessCount)
			}
		}
		fmt.Fprintln(out)

	case "search":
		if subarg == "" {
			fmt.Fprintln(out, "  Usage: /memory search <query>")
			fmt.Fprintln(out)
			return
		}
		results, err := cc.Store.SearchMemories(ctx, agentID, subarg)
		if err != nil {
			fmt.Fprintf(out, "  Error searching: %v\n\n", err)
			return
		}
		if len(results) == 0 {
			fmt.Fprintf(out, "  No results for '%s'.\n\n", subarg)
		} else {
			fmt.Fprintln(out)
			fmt.Fprintf(out, "  Results for '%s':\n", subarg)
			for _, m := range results {
				fmt.Fprintf(out, "    • %s: %s\n", m.Key, m.Value)
			}
			fmt.Fprintln(out)
		}

	case "delete":
		if subarg == "" {
			fmt.Fprintln(out, "  Usage: /memory delete <key>")
			fmt.Fprintln(out)
			return
		}
		if err := cc.Store.DeleteMemory(ctx, agentID, subarg); err != nil {
			fmt.Fprintf(out, "  Error deleting: %v\n\n", err)
			return
		}
		fmt.Fprintf(out, "  Deleted fact: %s\n\n", subarg)

	case "clear":
		if err := cc.Store.DeleteAgentMemories(ctx, agentID); err != nil {
			fmt.Fprintf(out, "  Error clearing: %v\n\n", err)
			return
		}
		fmt.Fprintln(out, "  All memories cleared.")
		fmt.Fprintln(out)

	default:
		fmt.Fprintln(out, "  Usage: /memory list | search <query> | delete <key> | clear")
		fmt.Fprintln(out)
	}
}

// handleRememberCommand processes /remember <key> <value>.
func handleRememberCommand(arg string, cc *ChatConfig, out io.Writer) {
	if cc.Store == nil {
		fmt.Fprintln(out, "  Store not available.")
		fmt.Fprintln(out)
		return
	}

	parts := strings.SplitN(arg, " ", 2)
	if len(parts) < 2 {
		fmt.Fprintln(out, "  Usage: /remember <key> <value>")
		fmt.Fprintln(out)
		return
	}

	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])

	ctx := context.Background()
	agentID := cc.CurrentAgent
	if agentID == "" {
		agentID = "default"
	}

	if err := cc.Store.SetMemory(ctx, agentID, key, value, "user"); err != nil {
		fmt.Fprintf(out, "  Error saving: %v\n\n", err)
		return
	}
	fmt.Fprintf(out, "  📝 Remembered: %s = %s\n\n", key, value)
}

// handleForgetCommand processes /forget <key>.
func handleForgetCommand(arg string, cc *ChatConfig, out io.Writer) {
	if cc.Store == nil {
		fmt.Fprintln(out, "  Store not available.")
		fmt.Fprintln(out)
		return
	}

	arg = strings.TrimSpace(arg)
	if arg == "" {
		fmt.Fprintln(out, "  Usage: /forget <key>")
		fmt.Fprintln(out)
		return
	}

	ctx := context.Background()
	agentID := cc.CurrentAgent
	if agentID == "" {
		agentID = "default"
	}

	if err := cc.Store.DeleteMemory(ctx, agentID, arg); err != nil {
		fmt.Fprintf(out, "  Error deleting: %v\n\n", err)
		return
	}
	fmt.Fprintf(out, "  Forgot: %s\n\n", arg)
}

// handleClearCommand processes /clear.
func handleClearCommand(cc *ChatConfig, sessionID string, out io.Writer) {
	if cc.Store == nil {
		fmt.Fprintln(out, "  Store not available.")
		fmt.Fprintln(out)
		return
	}

	ctx := context.Background()
	agentID := cc.CurrentAgent
	if agentID == "" {
		agentID = "default"
	}

	if err := cc.Store.DeleteAgentMessages(ctx, agentID, sessionID); err != nil {
		fmt.Fprintf(out, "  Error clearing: %v\n\n", err)
		return
	}
	fmt.Fprintln(out, "  Conversation history cleared.")
	fmt.Fprintln(out)
}

// handlePinCommand processes /pin <filepath> or /pin text <label> <content>.
func handlePinCommand(arg string, cc *ChatConfig, out io.Writer) {
	if cc.Store == nil {
		fmt.Fprintln(out, "  Store not available.")
		fmt.Fprintln(out)
		return
	}

	arg = strings.TrimSpace(arg)
	if arg == "" {
		fmt.Fprintln(out, "  Usage: /pin <filepath> or /pin text <label> <content>")
		fmt.Fprintln(out)
		return
	}

	ctx := context.Background()
	agentID := cc.CurrentAgent
	if agentID == "" {
		agentID = "default"
	}

	// Check if it's a text pin
	if strings.HasPrefix(arg, "text ") {
		parts := strings.SplitN(strings.TrimPrefix(arg, "text "), " ", 2)
		if len(parts) < 2 {
			fmt.Fprintln(out, "  Usage: /pin text <label> <content>")
			fmt.Fprintln(out)
			return
		}
		label, content := parts[0], parts[1]
		if err := cc.Store.AddPin(ctx, agentID, "text", label, content, false); err != nil {
			fmt.Fprintf(out, "  Error pinning text: %v\n\n", err)
			return
		}
		fmt.Fprintf(out, "  Pinned text: %s\n\n", label)
		return
	}

	// File pin
	filepath := arg
	pinMgr := memory.NewPinManager(cc.Store)
	if err := pinMgr.AddFilePin(ctx, agentID, filepath, false); err != nil {
		fmt.Fprintf(out, "  Error pinning file: %v\n\n", err)
		return
	}
	fmt.Fprintf(out, "  Pinned file: %s\n\n", filepath)
}

// handleUnpinCommand processes /unpin <source>.
func handleUnpinCommand(arg string, cc *ChatConfig, out io.Writer) {
	if cc.Store == nil {
		fmt.Fprintln(out, "  Store not available.")
		fmt.Fprintln(out)
		return
	}

	arg = strings.TrimSpace(arg)
	if arg == "" {
		fmt.Fprintln(out, "  Usage: /unpin <source>")
		fmt.Fprintln(out)
		return
	}

	ctx := context.Background()
	agentID := cc.CurrentAgent
	if agentID == "" {
		agentID = "default"
	}

	if err := cc.Store.RemovePin(ctx, agentID, arg); err != nil {
		fmt.Fprintf(out, "  Error unpinning: %v\n\n", err)
		return
	}
	fmt.Fprintf(out, "  Unpinned: %s\n\n", arg)
}

// handlePinnedCommand processes /pinned.
func handlePinnedCommand(cc *ChatConfig, out io.Writer) {
	if cc.Store == nil {
		fmt.Fprintln(out, "  Store not available.")
		fmt.Fprintln(out)
		return
	}

	ctx := context.Background()
	agentID := cc.CurrentAgent
	if agentID == "" {
		agentID = "default"
	}

	pins, err := cc.Store.ListPins(ctx, agentID)
	if err != nil {
		fmt.Fprintf(out, "  Error listing pins: %v\n\n", err)
		return
	}

	if len(pins) == 0 {
		fmt.Fprintln(out, "  No pinned files or text.")
		fmt.Fprintln(out)
		return
	}

	fmt.Fprintf(out, "  Pinned context for @%s (%d items):\n", agentID, len(pins))
	for _, pin := range pins {
		fmt.Fprintf(out, "    • %s (%s) - %d tokens\n", pin.Source, pin.PinType, pin.TokenCount)
	}
	fmt.Fprintln(out)
}

// handleShareCommand processes /share commands.
// Usage: /share <key> with <agent> — Share a specific memory
//        /share all with <agent> — Share all memories
//        /share pin <source> with <agent> — Share a specific pin
func handleShareCommand(arg string, cc *ChatConfig, out io.Writer) {
	if cc.Store == nil {
		fmt.Fprintln(out, "  Store not available.")
		fmt.Fprintln(out)
		return
	}

	ctx := context.Background()
	sourceAgent := cc.CurrentAgent
	if sourceAgent == "" {
		sourceAgent = "default"
	}

	// Parse: <key|pin <source>|all> with <target>
	parts := strings.Fields(arg)
	if len(parts) < 3 {
		fmt.Fprintf(out, "  Usage: /share <key|all|pin <source>> with <agent>\n")
		fmt.Fprintf(out, "  Example: /share project with coder\n")
		fmt.Fprintf(out, "           /share pin notes.md with writer\n\n")
		return
	}

	var shareType, itemKey, targetAgent string

	if parts[0] == "pin" {
		// /share pin <source> with <target>
		if len(parts) < 5 {
			fmt.Fprintf(out, "  Usage: /share pin <source> with <agent>\n\n")
			return
		}
		shareType = "pin"
		itemKey = parts[1]
		targetAgent = parts[3]
	} else if parts[0] == "all" {
		// /share all with <target>
		if len(parts) < 3 {
			fmt.Fprintf(out, "  Usage: /share all with <agent>\n\n")
			return
		}
		shareType = "memory"
		itemKey = ""
		targetAgent = parts[2]
	} else {
		// /share <key> with <target>
		shareType = "memory"
		itemKey = parts[0]
		targetAgent = parts[2]
	}

	err := cc.Store.AddShare(ctx, sourceAgent, targetAgent, shareType, itemKey)
	if err != nil {
		fmt.Fprintf(out, "  Error sharing: %v\n\n", err)
		return
	}

	if itemKey != "" {
		fmt.Fprintf(out, "  Shared %s '%s' with @%s\n\n", shareType, itemKey, targetAgent)
	} else {
		fmt.Fprintf(out, "  Shared all %ss with @%s\n\n", shareType, targetAgent)
	}
}

// handleUnshareCommand processes /unshare commands.
// Usage: /unshare <key> from <agent> — Revoke a specific memory share
//        /unshare pin <source> from <agent> — Revoke a specific pin share
func handleUnshareCommand(arg string, cc *ChatConfig, out io.Writer) {
	if cc.Store == nil {
		fmt.Fprintln(out, "  Store not available.")
		fmt.Fprintln(out)
		return
	}

	ctx := context.Background()
	sourceAgent := cc.CurrentAgent
	if sourceAgent == "" {
		sourceAgent = "default"
	}

	// Parse: <key|pin <source>> from <target>
	parts := strings.Fields(arg)
	if len(parts) < 3 {
		fmt.Fprintf(out, "  Usage: /unshare <key|pin <source>> from <agent>\n\n")
		return
	}

	var shareType, itemKey, targetAgent string

	if parts[0] == "pin" {
		if len(parts) < 4 {
			fmt.Fprintf(out, "  Usage: /unshare pin <source> from <agent>\n\n")
			return
		}
		shareType = "pin"
		itemKey = parts[1]
		targetAgent = parts[3]
	} else {
		shareType = "memory"
		itemKey = parts[0]
		targetAgent = parts[2]
	}

	err := cc.Store.RemoveShare(ctx, sourceAgent, targetAgent, shareType, itemKey)
	if err != nil {
		fmt.Fprintf(out, "  Error removing share: %v\n\n", err)
		return
	}

	fmt.Fprintf(out, "  Revoked share of '%s' from @%s\n\n", itemKey, targetAgent)
}

// handleSharedCommand lists what's shared with the current agent.
func handleSharedCommand(cc *ChatConfig, out io.Writer) {
	if cc.Store == nil {
		fmt.Fprintln(out, "  Store not available.")
		fmt.Fprintln(out)
		return
	}

	ctx := context.Background()
	targetAgent := cc.CurrentAgent
	if targetAgent == "" {
		targetAgent = "default"
	}

	shares, err := cc.Store.ListSharesFor(ctx, targetAgent)
	if err != nil {
		fmt.Fprintf(out, "  Error listing shares: %v\n\n", err)
		return
	}

	if len(shares) == 0 {
		fmt.Fprintf(out, "  No shared knowledge available for @%s\n\n", targetAgent)
		return
	}

	fmt.Fprintf(out, "  Shared knowledge for @%s (%d items):\n", targetAgent, len(shares))
	for _, share := range shares {
		itemDesc := fmt.Sprintf("all %ss", share.ShareType)
		if share.ItemKey != "" {
			itemDesc = fmt.Sprintf("%s '%s'", share.ShareType, share.ItemKey)
		}
		fmt.Fprintf(out, "    • From @%s: %s\n", share.SourceAgentID, itemDesc)
	}
	fmt.Fprintln(out)
}

// handleContextCommand displays the token budget for the current agent.
func handleContextCommand(cc *ChatConfig, out io.Writer) {
	if cc.Store == nil {
		fmt.Fprintln(out, "  Store not available.")
		fmt.Fprintln(out)
		return
	}

	ctx := context.Background()
	agentID := cc.CurrentAgent
	if agentID == "" {
		agentID = "default"
	}

	// Model limits (default to Gemini 2.5 Flash)
	modelLimits := map[string]int{
		"gemini-2.5-flash":  128000,
		"gemini-2.5-pro":    128000,
		"gemini-1.5-flash":  128000,
		"gemini-1.5-pro":    128000,
		"gpt-4-turbo":       128000,
		"gpt-4":             8192,
		"claude-opus":       200000,
		"claude-sonnet":     200000,
		"claude-haiku":      200000,
	}
	modelName := cc.ModelName
	if modelName == "" {
		modelName = "gemini-2.5-flash"
	}

	modelLimit := modelLimits[modelName]
	if modelLimit == 0 {
		modelLimit = 128000 // safe default
	}
	outputBuffer := 4096 // reserved for response

	// Get actual data from store
	memories, _ := cc.Store.ListMemories(ctx, agentID)
	pins, _ := cc.Store.ListPins(ctx, agentID)

	// Estimate token counts
	soulTokens := 850 // typical system prompt
	memoryTokens := 0
	memoryCount := 0
	for _, mem := range memories {
		// Estimate tokens: (len(text) + 3) / 4
		tokens := (len(mem.Value) + 3) / 4
		memoryTokens += tokens
		memoryCount++
	}

	pinTokens := 0
	pinCount := 0
	for _, pin := range pins {
		pinTokens += pin.TokenCount
		pinCount++
	}

	// Get shared context
	sharedMemories, _ := cc.Store.GetSharedMemories(ctx, agentID)
	sharedPins, _ := cc.Store.GetSharedPinsForAgent(ctx, agentID)
	sharedTokens := 0
	sharedMemCount := 0
	sharedPinCount := 0
	for _, mem := range sharedMemories {
		tokens := (len(mem.Value) + 3) / 4
		sharedTokens += tokens
		sharedMemCount++
	}
	for _, pin := range sharedPins {
		sharedTokens += pin.TokenCount
		sharedPinCount++
	}

	// Estimate summary and message tokens (these would be computed during actual context building)
	summaryTokens := 0
	truncatedCount := 0
	messageTokens := 200 // rough estimate for recent messages
	messageCount := 5    // rough estimate

	totalUsed := soulTokens + memoryTokens + pinTokens + sharedTokens + summaryTokens + messageTokens
	available := modelLimit - outputBuffer
	remaining := available - totalUsed
	if remaining < 0 {
		remaining = 0
	}

	// Create and format budget
	budget := &memory.ContextBudget{
		ModelLimit:    modelLimit,
		OutputBuffer:  outputBuffer,
		Available:     available,
		SoulTokens:    soulTokens,
		MemoryTokens:  memoryTokens,
		PinTokens:     pinTokens,
		SharedTokens:  sharedTokens,
		SummaryTokens: summaryTokens,
		MessageTokens: messageTokens,
		TotalUsed:     totalUsed,
		Remaining:     remaining,
		MessageCount:  messageCount,
		TruncatedCount: truncatedCount,
		PinCount:      pinCount,
		SharedPinCount: sharedPinCount,
		MemoryCount:   memoryCount,
		SharedMemCount: sharedMemCount,
	}

	fmt.Fprintln(out)
	fmt.Fprint(out, budget.Format(agentID, modelName))
	fmt.Fprintln(out)
}
