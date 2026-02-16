package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/basket/go-claw/internal/agent"
	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/channels"
	"github.com/basket/go-claw/internal/config"
	"github.com/basket/go-claw/internal/coordinator"
	"github.com/basket/go-claw/internal/cron"
	"github.com/basket/go-claw/internal/engine"
	"github.com/basket/go-claw/internal/gateway"
	"github.com/basket/go-claw/internal/mcp"
	otelPkg "github.com/basket/go-claw/internal/otel"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
	"github.com/basket/go-claw/internal/sandbox/wasm"
	"github.com/basket/go-claw/internal/skills"
	"github.com/basket/go-claw/internal/telemetry"
	"github.com/basket/go-claw/internal/tools"
	"github.com/basket/go-claw/internal/tui"
	"github.com/google/uuid"
	"github.com/mattn/go-isatty"
	"gopkg.in/yaml.v3"
)

// Version is set via ldflags at build time: -ldflags "-X main.Version=..."
var Version = "v0.5-dev"

func printUsage() {
	fmt.Fprintf(os.Stderr, `Usage of %s:

INTERACTIVE MODE (default):
  %s                          Start the interactive chat TUI

DAEMON MODE:
  %s -daemon                  Start daemon (no TUI, logs to stdout)
  %s daemon start             Start daemon in background
  %s daemon stop              Stop running daemon
  %s daemon status            Check daemon status

SUBCOMMANDS:
  %s skill <action>           Manage WASM skills
                              Actions: install, list, remove, update, info
  %s status                   Show daemon health status (/healthz)
  %s pull <url>               Fetch agents from HTTPS URL
                              Example: goclaw pull https://example.com/agents.yaml
  %s import [options]         Import legacy .env file to config.yaml
                              Options: --path <file> (default: .env), --force
  %s doctor [-json]           Run diagnostic checks
                              Flags: -json for JSON output

FLAGS:
`, os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0])
	flag.PrintDefaults()
	fmt.Fprintf(os.Stderr, `
ENVIRONMENT VARIABLES:
  GOCLAW_HOME             Data directory (default: ~/.goclaw)
  GOCLAW_NO_TUI           Set to 1 to disable TUI (use with -daemon)
  GEMINI_API_KEY          Required for Gemini provider

EXAMPLES:
  Interactive chat:       %s
  Daemon mode:            %s -daemon
  Install a skill:        %s skill install https://github.com/user/repo
  Check daemon health:    %s status
  Run diagnostics:        %s doctor
`, os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0])
}

func main() {
	loadDotEnv(".env")

	interactive := isatty.IsTerminal(os.Stdout.Fd()) && os.Getenv("GOCLAW_NO_TUI") == ""
	daemon := flag.Bool("daemon", false, "run in daemon mode (no chat REPL, logs to stdout)")
	flag.Usage = printUsage
	flag.Parse()

	if *daemon {
		interactive = false
	}

	// Quiet logs (file-only) in interactive mode so the REPL stays clean.
	quietLogs := interactive

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// CLI subcommands (non-daemon actions).
	if args := flag.Args(); len(args) > 0 {
		switch strings.ToLower(strings.TrimSpace(args[0])) {
		case "help", "-h", "--help":
			printUsage()
			os.Exit(0)
		case "skill":
			os.Exit(runSkillCommand(ctx, args[1:]))
		case "status":
			os.Exit(runStatusCommand(ctx, args[1:]))
		case "import":
			os.Exit(runImportCommand(ctx, args[1:]))
		case "pull":
			os.Exit(runPullCommand(args[1:]))
		case "doctor":
			os.Exit(runDoctorCommand(ctx, args[1:]))
		case "daemon":
			mode, err := parseDaemonSubcommandArgs(args[1:])
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			if mode == daemonSubcommandHelp {
				printDaemonSubcommandUsage(os.Stdout)
				return
			}
			interactive = false
		}
	}

	cfg, err := config.Load()
	if err != nil {
		fatalStartup(nil, "E_CONFIG_LOAD", err)
	}
	if len(cfg.ContextLimits) > 0 {
		engine.SetContextLimitOverrides(cfg.ContextLimits)
	}

	// GC-SPEC-SEC-006: Initialize audit before logger so E_LOGGER_INIT failures are audited.
	// Audit only needs homeDir (available from config), not the logger itself.
	if err := audit.Init(cfg.HomeDir); err != nil {
		fatalStartup(nil, "E_AUDIT_INIT", err)
	}
	defer func() { _ = audit.Close() }()

	logger, closer, err := telemetry.NewLogger(cfg.HomeDir, cfg.LogLevel, quietLogs)
	if err != nil {
		fatalStartup(nil, "E_LOGGER_INIT", err)
	}
	defer closer.Close()
	slog.SetDefault(logger)
	logger.Info("startup phase", "phase", "config_loaded")
	if host, _, err := net.SplitHostPort(cfg.BindAddr); err == nil {
		h := strings.TrimSpace(strings.ToLower(host))
		loopback := h == "127.0.0.1" || h == "localhost" || h == "::1"
		if !loopback && len(cfg.AllowOrigins) == 0 {
			// TODO (Other Work): warn on non-localhost bind when origin allowlist isn't configured.
			logger.Warn("allow_origins is empty on non-loopback bind; cross-origin browser connections will be rejected (same-origin only)", "bind_addr", cfg.BindAddr)
		}
	}

	if cfg.NeedsGenesis {
		if interactive {
			result, err := tui.RunGenesis(ctx)
			if err != nil {
				logger.Info("genesis wizard cancelled", "error", err)
				fmt.Println("\n  Run GoClaw again to restart the setup wizard.")
				os.Exit(0)
			} else {
				if err := tui.WriteGenesisFiles(cfg.HomeDir, result); err != nil {
					fatalStartup(logger, "E_GENESIS_WRITE", err)
				}
				logger.Info("genesis wizard completed", "home", cfg.HomeDir)
				// Reload config with generated values
				cfg, err = config.Load()
				if err != nil {
					fatalStartup(logger, "E_CONFIG_RELOAD", err)
				}
				greeting := tui.GreetingFromSoul(cfg.SOUL)
				fmt.Println(greeting)
				logger.Info("genesis greeting", "message", greeting)
			}
		} else {
			// Fallback: write minimal config.yaml with starter agents in daemon mode
			if err := writeMinimalConfig(cfg.HomeDir); err != nil {
				fatalStartup(logger, "E_CONFIG_WRITE", err)
			}
			logger.Info("config.yaml written with starter agents", "home", cfg.HomeDir)
			// Reload config
			cfg, err = config.Load()
			if err != nil {
				fatalStartup(logger, "E_CONFIG_RELOAD", err)
			}
		}
	}

	// Create event bus early so it can be passed to the store.
	eventBus := bus.New()

	// Initialize OpenTelemetry (no-op when disabled, zero overhead).
	otelProvider, err := otelPkg.Init(ctx, otelPkg.Config{
		Enabled:        cfg.Telemetry.Enabled,
		Exporter:       cfg.Telemetry.Exporter,
		Endpoint:       cfg.Telemetry.Endpoint,
		ServiceName:    cfg.Telemetry.ServiceName,
		SampleRate:     cfg.Telemetry.SampleRate,
		MetricsEnabled: cfg.Telemetry.MetricsEnabled,
	})
	if err != nil {
		fatalStartup(logger, "E_OTEL_INIT", err)
	}
	defer otelProvider.Shutdown(ctx)

	dbPath := filepath.Join(cfg.HomeDir, "goclaw.db")
	store, err := persistence.Open(dbPath, eventBus)
	if err != nil {
		fatalStartup(logger, "E_STORE_OPEN", err)
	}
	defer store.Close()
	audit.SetDB(store.DB())
	logger.Info("startup phase", "phase", "schema_migrated")

	recoveredCount, err := store.RequeueExpiredLeases(ctx)
	if err != nil {
		fatalStartup(logger, "E_RECOVERY_SCAN", err)
	}
	// GC-SPEC-REL-006: Timed recovery with RPO/RTO metrics.
	recMetrics, recErr := store.RecoverRunningTasksTimed(ctx)
	if recErr != nil {
		fatalStartup(logger, "E_TASK_RECOVERY", recErr)
	}
	logger.Info("startup phase", "phase", "recovery_scan_completed",
		"requeued", recoveredCount,
		"tasks_recovered", recMetrics.RecoveredCount,
		"recovery_duration_ms", recMetrics.RecoveryDuration.Milliseconds())

	// GC-SPEC-PDR-v4-Phase-3: Plan resumption is now handled after executor init (in background goroutine)
	// This allows crashed plans to be resumed from their last checkpoint instead of marked as failed.

	policyPath := filepath.Join(cfg.HomeDir, "policy.yaml")
	if _, statErr := os.Stat(policyPath); os.IsNotExist(statErr) {
		if writeErr := os.WriteFile(policyPath, []byte(tui.DefaultPolicyYAML()), 0o644); writeErr != nil {
			fatalStartup(logger, "E_POLICY_BOOTSTRAP", writeErr)
		}
		logger.Info("policy.yaml bootstrapped with defaults", "path", policyPath)
	}
	polData, err := policy.Load(policyPath)
	if err != nil {
		fatalStartup(logger, "E_POLICY_LOAD", err)
	}
	pol := policy.NewLivePolicy(polData, policyPath)
	// GC-SPEC-CFG-006: Record policy version in DB on load.
	polVersion := pol.PolicyVersion()
	if err := store.RecordPolicyVersion(ctx, polVersion, polVersion, policyPath); err != nil {
		logger.Warn("failed to record policy version", "error", err)
	}
	logger.Info("startup phase", "phase", "policy_loaded")

	wasmHost, err := wasm.NewHost(ctx, wasm.Config{
		Store:  store,
		Policy: pol,
		Logger: logger,
	})
	if err != nil {
		fatalStartup(logger, "E_WASM_HOST_INIT", err)
	}
	defer wasmHost.Close(context.Background())

	userSkillsDir := filepath.Join(cfg.HomeDir, "skills")
	if err := os.MkdirAll(userSkillsDir, 0o755); err != nil {
		fatalStartup(logger, "E_SKILL_DIR_CREATE", err)
	}
	installedSkillsDir := filepath.Join(cfg.HomeDir, "installed")
	if err := os.MkdirAll(installedSkillsDir, 0o755); err != nil {
		fatalStartup(logger, "E_SKILL_DIR_CREATE", err)
	}

	// Ensure workspace dir exists
	workspaceDir := filepath.Join(cfg.HomeDir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		fatalStartup(logger, "E_WORKSPACE_CREATE", err)
	}

	// Create default HEARTBEAT.md if missing
	heartbeatPath := filepath.Join(workspaceDir, "HEARTBEAT.md")
	if _, err := os.Stat(heartbeatPath); os.IsNotExist(err) {
		defaultHeartbeat := `# Heartbeat Checklist

The system runs this checklist periodically to ensure health.

- [ ] Check for any high-priority tasks that are stuck.
- [ ] Review recent logs for errors.
- [ ] Ensure disk space is sufficient.
`
		if err := os.WriteFile(heartbeatPath, []byte(defaultHeartbeat), 0o644); err != nil {
			logger.Warn("failed to create default HEARTBEAT.md", "error", err)
		}
	}

	llmProvider, llmModel, llmAPIKey := cfg.ResolveLLMConfig()

	// Create AgentRegistry (replaces single brain+engine).
	registry := agent.NewRegistry(store, eventBus, pol, wasmHost, cfg.APIKeys)

	// Create default agent from global config (backward compat).
	defaultCfg := agent.AgentConfig{
		AgentID:              "default",
		DisplayName:          cfg.AgentName,
		Provider:             llmProvider,
		Model:                llmModel,
		APIKey:               llmAPIKey,
		Soul:                 cfg.SOUL,
		AgentEmoji:           cfg.AgentEmoji,
		WorkerCount:          cfg.WorkerCount,
		TaskTimeoutSeconds:   cfg.TaskTimeoutSeconds,
		MaxQueueDepth:        cfg.MaxQueueDepth,
		PreferredSearch:      cfg.PreferredSearch,
		OpenAICompatProvider: cfg.LLM.OpenAICompatibleProvider,
		OpenAICompatBaseURL:  cfg.LLM.OpenAICompatibleBaseURL,
	}
	if err := registry.CreateAgent(ctx, defaultCfg); err != nil {
		fatalStartup(logger, "E_DEFAULT_AGENT_CREATE", err)
	}
	defaultAgent := registry.GetAgent("default")

	// Create static agents from config.yaml.
	seenAgentIDs := map[string]bool{"default": true}
	for _, acfg := range cfg.Agents {
		if acfg.AgentID == "" {
			logger.Warn("skipping agent with empty agent_id in config.yaml")
			continue
		}
		if seenAgentIDs[acfg.AgentID] {
			logger.Warn("skipping duplicate agent_id in config.yaml", "agent_id", acfg.AgentID)
			continue
		}
		seenAgentIDs[acfg.AgentID] = true
		apiKey := os.Getenv(acfg.APIKeyEnv)
		soul := acfg.Soul
		if acfg.SoulFile != "" {
			if data, err := os.ReadFile(filepath.Join(cfg.HomeDir, acfg.SoulFile)); err == nil {
				soul = string(data)
			} else {
				logger.Warn("failed to read agent soul_file, using inline soul", "agent_id", acfg.AgentID, "soul_file", acfg.SoulFile, "error", err)
			}
		}
		if err := registry.CreateAgent(ctx, agent.AgentConfig{
			AgentID:            acfg.AgentID,
			DisplayName:        acfg.DisplayName,
			Provider:           acfg.Provider,
			Model:              acfg.Model,
			APIKey:             apiKey,
			APIKeyEnv:          acfg.APIKeyEnv,
			Soul:               soul,
			WorkerCount:        acfg.WorkerCount,
			TaskTimeoutSeconds: acfg.TaskTimeoutSeconds,
			MaxQueueDepth:      acfg.MaxQueueDepth,
			SkillsFilter:       acfg.SkillsFilter,
			PreferredSearch:    acfg.PreferredSearch,
		}); err != nil {
			logger.Error("failed to create agent from config", "agent_id", acfg.AgentID, "error", err)
		}
	}

	// Restore runtime-created agents from DB.
	if err := registry.RestorePersistedAgents(ctx); err != nil {
		logger.Warn("some persisted agents failed to restore", "error", err)
	}

	// Load plans from config (GC-SPEC-PDR-v4-Phase-4: Plan system).
	agentConfigs := registry.ListAgents()
	agentIDs := make([]string, 0, len(agentConfigs))
	for _, ac := range agentConfigs {
		agentIDs = append(agentIDs, ac.AgentID)
	}
	planSummaries, plansMap := loadPlans(cfg.Plans, agentIDs, logger)
	if len(planSummaries) > 0 {
		logger.Info("plans loaded from config", "count", len(planSummaries))
	}

	// Configure shell executor (Host vs Docker)
	var shellSandbox *tools.DockerSandbox
	if cfg.Tools.Shell.Sandbox {
		sb, err := tools.NewDockerSandbox(
			cfg.Tools.Shell.SandboxImage,
			cfg.Tools.Shell.SandboxMemory,
			cfg.Tools.Shell.SandboxNetwork,
			filepath.Join(cfg.HomeDir, "workspace"),
		)
		if err != nil {
			logger.Warn("failed to init docker sandbox, falling back to host", "error", err)
		} else {
			shellSandbox = sb
			for _, ra := range registry.ListRunningAgents() {
				if ra.Brain != nil {
					ra.Brain.Registry().ShellExecutor = shellSandbox
				}
			}
			defer shellSandbox.Close()
			logger.Info("shell sandbox enabled", "image", cfg.Tools.Shell.SandboxImage)
		}
	}

	// Initialize MCP Manager
	var mcpConfigs []mcp.ServerConfig
	for _, s := range cfg.MCP.Servers {
		mcpConfigs = append(mcpConfigs, mcp.ServerConfig{
			Name:    s.Name,
			Command: s.Command,
			Args:    s.Args,
			Env:     s.Env,
			Enabled: s.Enabled,
		})
	}

	mcpManager := mcp.NewManager(mcpConfigs, pol, logger)
	if err := mcpManager.Start(ctx); err != nil {
		logger.Warn("MCP manager start failed", "error", err)
	}
	defer func() { _ = mcpManager.Stop() }()

	// Register MCP tools on all agents and set delegation config (Phase 1.4 per-agent MCP).
	for _, ra := range registry.ListRunningAgents() {
		if ra.Brain != nil && ra.Brain.Genkit() != nil {
			agentID := ra.Config.AgentID

			// Connect per-agent MCP servers from config (Phase 1.4).
			agentCfg := findAgentConfig(cfg.Agents, agentID)
			if agentCfg != nil && len(agentCfg.MCPServers) > 0 {
				serverConfigs := make([]mcp.ServerConfig, 0, len(agentCfg.MCPServers))
				for _, ref := range agentCfg.MCPServers {
					serverConfigs = append(serverConfigs, mcp.ServerConfig{
						Name:      ref.Name,
						Command:   ref.Command,
						Args:      ref.Args,
						Env:       ref.Env,
						Transport: ref.Transport,
						URL:       ref.URL,
						Timeout:   ref.Timeout,
					})
				}
				if err := mcpManager.ConnectAgentServers(ctx, agentID, serverConfigs); err != nil {
					logger.Warn("failed to connect per-agent MCP servers", "agent_id", agentID, "error", err)
				}
			}

			// Register MCP tools for this agent via the new per-agent bridge.
			_ = tools.RegisterMCPTools(ra.Brain.Genkit(), agentID, mcpManager)
		}
		// Set delegation max hops from config.
		if ra.Brain != nil && cfg.DelegationMaxHops > 0 {
			ra.Brain.Registry().DelegationMaxHops = cfg.DelegationMaxHops
		}
	}

	// AUD-020: reloadMu serializes skill reload and WASM hot-swap registration.
	var reloadMu sync.Mutex

	wasmWatcher := wasm.NewWatcher(userSkillsDir, wasmHost, logger)
	wasmWatcher.OnToolLoaded(func(name string) {
		// AUD-020: Serialize with reloadSkills to prevent concurrent WASM module loading.
		reloadMu.Lock()
		defer reloadMu.Unlock()
		for _, ra := range registry.ListRunningAgents() {
			if ra.Brain != nil {
				ra.Brain.RegisterSkill(name)
			}
		}
		logger.Info("skill registered in all agents", "skill", name)
	})
	if err := wasmWatcher.Start(ctx); err != nil {
		fatalStartup(logger, "E_SKILL_WATCHER_START", err)
	}

	// Phase 5: Load SKILL.md skills after policy load and register eligible skills in Brain.
	projectSkillsDir := cfg.Skills.ProjectDir
	projectSkillsAbs, err := filepath.Abs(projectSkillsDir)
	if err != nil {
		fatalStartup(logger, "E_SKILL_DIR_CREATE", fmt.Errorf("abs project skills dir: %w", err))
	}

	var extraAbs []string
	for _, d := range cfg.Skills.ExtraDirs {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			logger.Warn("ignoring invalid skills.extra_dirs entry", "dir", d, "error", err)
			continue
		}
		extraAbs = append(extraAbs, abs)
	}

	type skillState struct {
		mu     sync.RWMutex
		loaded []skills.LoadedSkill
	}
	skillsState := &skillState{}

	loadAllSkillMD := func(loadCtx context.Context) ([]skills.LoadedSkill, error) {
		var errs []error
		out := make([]skills.LoadedSkill, 0, 16)
		seen := make(map[string]string) // canonical -> winner
		merge := func(items []skills.LoadedSkill, sourceLabel string) {
			for _, ls := range items {
				// AUD-005: Use shared CanonicalSkillKey for consistent collision detection.
				key := skills.CanonicalSkillKey(filepath.Base(ls.SourceDir))
				if key == "" || key == "." {
					key = skills.CanonicalSkillKey(ls.Skill.Name)
				}
				if key == "" {
					continue
				}
				if winner, ok := seen[key]; ok {
					logger.Info("skill collision: skipping lower-priority duplicate",
						"skill", key,
						"winner_source", winner,
						"skipped_source", sourceLabel,
					)
					continue
				}
				seen[key] = sourceLabel
				out = append(out, ls)
			}
		}

		// Base: project > user.
		baseLoader := skills.NewLoader(projectSkillsAbs, userSkillsDir, "", logger)
		base, err := baseLoader.LoadAll(loadCtx)
		if err != nil {
			errs = append(errs, err)
		}
		merge(base, "project/user")

		// Extras (lowest priority before installed).
		for _, extraDir := range extraAbs {
			extraLoader := skills.NewLoader(extraDir, "", "", logger)
			extra, err := extraLoader.LoadAll(loadCtx)
			if err != nil {
				errs = append(errs, err)
			}
			merge(extra, "extra")
		}

		// Installed (lowest).
		installedLoader := skills.NewLoader("", "", installedSkillsDir, logger)
		installed, err := installedLoader.LoadAll(loadCtx)
		if err != nil {
			errs = append(errs, err)
		}
		merge(installed, "installed")

		return out, errors.Join(errs...)
	}

	findWASMPathForSkill := func(skillName string, sourceDir string) (string, bool) {
		if strings.TrimSpace(sourceDir) == "" || strings.TrimSpace(skillName) == "" {
			return "", false
		}
		candidates := []string{
			filepath.Join(sourceDir, "skill.wasm"),
			filepath.Join(sourceDir, skillName+".wasm"),
		}
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
				return c, true
			}
		}
		// If there's exactly one wasm file in the directory, treat it as the skill module.
		entries, err := os.ReadDir(sourceDir)
		if err != nil {
			return "", false
		}
		var wasmFiles []string
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			if strings.HasSuffix(strings.ToLower(ent.Name()), ".wasm") {
				wasmFiles = append(wasmFiles, filepath.Join(sourceDir, ent.Name()))
			}
		}
		if len(wasmFiles) == 1 {
			return wasmFiles[0], true
		}
		return "", false
	}

	reloadSkills := func(loadCtx context.Context) {
		// AUD-020: Serialize with WASM watcher's OnToolLoaded callback.
		reloadMu.Lock()
		defer reloadMu.Unlock()

		loaded, err := loadAllSkillMD(loadCtx)
		if err != nil {
			logger.Warn("skill load completed with errors", "error", err)
		}

		// GC-SPEC-CFG-007: legacy mode must be explicitly enabled.
		if !cfg.Skills.LegacyMode {
			for idx := range loaded {
				if strings.TrimSpace(loaded[idx].Skill.Script) == "" {
					continue
				}
				loaded[idx].Eligible = false
				loaded[idx].Missing = append(loaded[idx].Missing, "legacy_mode disabled")
			}
		}

		skillsState.mu.Lock()
		skillsState.loaded = loaded
		skillsState.mu.Unlock()

		for _, ra := range registry.ListRunningAgents() {
			if ra.Brain != nil {
				ra.Brain.ReplaceLoadedSkills(loaded)
			}
		}

		// Register eligible WASM modules found under SKILL.md skill directories.
		for _, ls := range loaded {
			if !ls.Eligible {
				continue
			}
			name := filepath.Base(strings.TrimSpace(ls.SourceDir))
			if name == "" || name == "." {
				continue
			}
			if wasmPath, ok := findWASMPathForSkill(name, ls.SourceDir); ok {
				// Load under the canonical skill name, not the wasm filename.
				b, readErr := os.ReadFile(wasmPath)
				if readErr != nil {
					logger.Warn("failed to read wasm module for skill", "skill", name, "wasm", wasmPath, "error", readErr)
					continue
				}
				if err := wasmHost.LoadModuleFromBytes(loadCtx, name, b, wasmPath); err != nil {
					logger.Warn("failed to load wasm module for skill", "skill", name, "wasm", wasmPath, "error", err)
					continue
				}
				for _, ra := range registry.ListRunningAgents() {
					if ra.Brain != nil {
						ra.Brain.RegisterSkill(name)
					}
				}
			}
		}
	}

	// Initial load before ACP is exposed.
	reloadSkills(ctx)

	// C1 FIX: Register provisioning hook so runtime-created agents (via agent.create RPC)
	// receive skills, MCP tools, and shell executor — matching what startup agents get.
	registry.SetOnAgentCreated(func(ra *agent.RunningAgent) {
		if ra.Brain == nil {
			logger.Info("runtime agent has nil brain, skipping provisioning", "agent_id", ra.Config.AgentID)
			return
		}

		// Load current skills snapshot.
		skillsState.mu.RLock()
		loaded := append([]skills.LoadedSkill(nil), skillsState.loaded...)
		skillsState.mu.RUnlock()
		if len(loaded) > 0 {
			ra.Brain.ReplaceLoadedSkills(loaded)
		}

		// Register WASM skills.
		for _, ls := range loaded {
			if !ls.Eligible {
				continue
			}
			name := filepath.Base(strings.TrimSpace(ls.SourceDir))
			if name == "" || name == "." {
				continue
			}
			if wasmPath, ok := findWASMPathForSkill(name, ls.SourceDir); ok {
				b, readErr := os.ReadFile(wasmPath)
				if readErr != nil {
					logger.Warn("failed to read wasm for runtime agent", "skill", name, "error", readErr)
					continue
				}
				if err := wasmHost.LoadModuleFromBytes(ctx, name, b, wasmPath); err != nil {
					logger.Warn("failed to load wasm for runtime agent", "skill", name, "error", err)
					continue
				}
				ra.Brain.RegisterSkill(name)
			}
		}

		// Shell executor.
		if shellSandbox != nil {
			ra.Brain.Registry().ShellExecutor = shellSandbox
		}

		// Look up per-agent config entry (used for MCP, structured output, etc.).
		agentCfg := findAgentConfig(cfg.Agents, ra.Config.AgentID)

		// MCP tools (Phase 1.4 per-agent MCP).
		if ra.Brain.Genkit() != nil {
			agentID := ra.Config.AgentID

			// Connect per-agent MCP servers from config (Phase 1.4).
			if agentCfg != nil && len(agentCfg.MCPServers) > 0 {
				serverConfigs := make([]mcp.ServerConfig, 0, len(agentCfg.MCPServers))
				for _, ref := range agentCfg.MCPServers {
					serverConfigs = append(serverConfigs, mcp.ServerConfig{
						Name:      ref.Name,
						Command:   ref.Command,
						Args:      ref.Args,
						Env:       ref.Env,
						Transport: ref.Transport,
						URL:       ref.URL,
						Timeout:   ref.Timeout,
					})
				}
				if err := mcpManager.ConnectAgentServers(ctx, agentID, serverConfigs); err != nil {
					logger.Warn("failed to connect per-agent MCP servers on hot-reload", "agent_id", agentID, "error", err)
				}
			}

			// Register MCP tools for this agent via the new per-agent bridge.
			_ = tools.RegisterMCPTools(ra.Brain.Genkit(), agentID, mcpManager)
		}

		// Delegation max hops from config.
		if cfg.DelegationMaxHops > 0 {
			ra.Brain.Registry().DelegationMaxHops = cfg.DelegationMaxHops
		}

		// Wire structured output validator if agent config specifies a schema (v0.5).
		if agentCfg != nil && agentCfg.StructuredOutput != nil {
			schema := agentCfg.StructuredOutput.Schema
			if len(schema) == 0 && agentCfg.StructuredOutput.SchemaFile != "" {
				if data, err := os.ReadFile(filepath.Join(cfg.HomeDir, agentCfg.StructuredOutput.SchemaFile)); err == nil {
					schema = data
				} else {
					logger.Warn("failed to read structured output schema file", "agent_id", ra.Config.AgentID, "error", err)
				}
			}
			if len(schema) > 0 {
				v, err := engine.NewStructuredValidator(schema, agentCfg.StructuredOutput.MaxRetries, agentCfg.StructuredOutput.StrictMode)
				if err != nil {
					logger.Warn("failed to compile structured output schema", "agent_id", ra.Config.AgentID, "error", err)
				} else {
					ra.Brain.SetValidator(v)
				}
			}
		}

		logger.Info("runtime agent provisioned with skills/tools", "agent_id", ra.Config.AgentID)
	})

	// Watch all skill sources for hot-reload (SKILL.md + referenced files).
	skillWatcher := skills.NewWatcher(append([]string{projectSkillsAbs, userSkillsDir, installedSkillsDir}, extraAbs...), logger)
	if err := skillWatcher.Start(ctx); err != nil {
		fatalStartup(logger, "E_SKILL_WATCHER_START", err)
	}

	confWatcher := config.NewWatcher(cfg.HomeDir, logger)
	if err := confWatcher.Start(ctx); err != nil {
		fatalStartup(logger, "E_CONFIG_WATCHER_START", err)
	}
	go func() {
		for ev := range confWatcher.Events() {
			logger.Info("config hot-reload event", "path", ev.Path, "op", ev.Op.String())
			switch filepath.Base(ev.Path) {
			case "SOUL.md":
				data, err := os.ReadFile(ev.Path)
				if err == nil {
					cfg.SOUL = string(data)
					da := registry.GetAgent("default")
					if da != nil {
						da.Brain.UpdateSystemPrompt(cfg.SOUL)
					}
					logger.Info("SOUL.md hot-reloaded")
				}
			case "AGENTS.md":
				data, err := os.ReadFile(ev.Path)
				if err == nil {
					cfg.AGENTS = string(data)
					logger.Info("AGENTS.md hot-reloaded")
				}
			case "policy.yaml":
				if err := policy.ReloadFromFile(pol, ev.Path); err != nil {
					logger.Error("policy.yaml reload rejected; retaining previous policy", "error", err)
				} else {
					// GC-SPEC-CFG-006: Record new policy version on hot-reload.
					newVer := pol.PolicyVersion()
					_ = store.RecordPolicyVersion(context.Background(), newVer, newVer, ev.Path)
					logger.Info("policy.yaml hot-reloaded", "policy_version", newVer)
				}
			case "config.yaml":
				newCfg, err := config.Load()
				if err != nil {
					logger.Error("config.yaml reload failed", "error", err)
					break
				}

				// Reconcile agents (add new, remove deleted, recreate changed ones).
				// GC-SPEC-CFR-004: Agent hot-reload on config change.
				reconcileAgents(ctx, registry, newCfg.Agents, cfg.Agents, cfg.HomeDir, logger)
				cfg.Agents = newCfg.Agents

				// Reload plans from updated config.
				// GC-SPEC-PDR-v4-Phase-4: Plan hot-reload on config change.
				reloadAgentConfigs := registry.ListAgents()
				reloadAgentIDs := make([]string, 0, len(reloadAgentConfigs))
				for _, ac := range reloadAgentConfigs {
					reloadAgentIDs = append(reloadAgentIDs, ac.AgentID)
				}
				planSummaries, plansMap = loadPlans(newCfg.Plans, reloadAgentIDs, logger)
				if len(planSummaries) > 0 {
					logger.Info("plans reloaded from config", "count", len(planSummaries))
				}

				// Update other config fields that may have changed.
				if newCfg.DelegationMaxHops > 0 {
					for _, ra := range registry.ListRunningAgents() {
						if ra.Brain != nil && ra.Brain.Registry() != nil {
							ra.Brain.Registry().DelegationMaxHops = newCfg.DelegationMaxHops
						}
					}
				}

				logger.Info("config.yaml hot-reloaded")
			}
		}
	}()

	authToken, err := loadAuthToken(cfg.HomeDir)
	if err != nil {
		fatalStartup(logger, "E_AUTH_TOKEN_WRITE", err)
	}

	// Skill status closure used by system.status.
	skillsStatusFn := func(statusCtx context.Context) ([]tools.SkillStatus, error) {
		skillsState.mu.RLock()
		loaded := append([]skills.LoadedSkill(nil), skillsState.loaded...)
		skillsState.mu.RUnlock()

		eligibility := make(map[string]tools.SkillEligibility, len(loaded))
		for _, ls := range loaded {
			name := filepath.Base(strings.TrimSpace(ls.SourceDir))
			if name == "" || name == "." {
				name = strings.TrimSpace(ls.Skill.Name)
			}
			key := strings.ToLower(strings.TrimSpace(name))
			if key == "" {
				continue
			}
			eligibility[key] = tools.SkillEligibility{
				Eligible: ls.Eligible,
				Missing:  append([]string(nil), ls.Missing...),
			}
		}

		installedRecs, _ := store.ListInstalledSkills(statusCtx)
		installed := make(map[string]persistence.InstalledSkillRecord, len(installedRecs))
		for _, r := range installedRecs {
			installed[r.SkillID] = r
		}

		// Start with built-in tool catalog statuses (search providers + read_url).
		catalog := tools.FullCatalog(defaultAgent.Brain.Providers())
		out := tools.ResolveStatus(catalog, cfg.APIKeys, pol, eligibility)

		// Add SKILL.md-backed statuses.
		for _, ls := range loaded {
			name := filepath.Base(strings.TrimSpace(ls.SourceDir))
			if name == "" || name == "." {
				name = strings.TrimSpace(ls.Skill.Name)
			}
			if strings.TrimSpace(name) == "" {
				continue
			}
			typ := "instruction"
			caps := []string{"skill.inject"}
			if strings.TrimSpace(ls.Skill.Script) != "" {
				typ = "legacy"
				caps = []string{"legacy.run"}
			}

			source := strings.TrimSpace(ls.Source)
			sourceURL := ""
			if rec, ok := installed[name]; ok {
				if strings.TrimSpace(rec.Source) != "" {
					source = rec.Source
				}
				sourceURL = strings.TrimSpace(rec.SourceURL)
			}

			info := tools.SkillInfo{
				Name:         name,
				Description:  strings.TrimSpace(ls.Skill.Description),
				Type:         typ,
				Source:       source,
				SourceURL:    sourceURL,
				Capabilities: caps,
				SetupHint:    "Grant required capability in policy.yaml",
			}

			missing := append([]string(nil), ls.Missing...)
			enabled := true
			for _, cap := range caps {
				if pol == nil || !pol.AllowCapability(cap) {
					enabled = false
					missing = append(missing, "Capability: "+cap)
				}
			}
			out = append(out, tools.SkillStatus{
				Info:       info,
				Configured: true,
				Enabled:    enabled,
				Eligible:   ls.Eligible,
				Missing:    missing,
			})
		}
		return out, nil
	}

	// ToolsUpdated fan-in (WASM hot-swap + SKILL.md watcher).
	toolsUpdated := make(chan string, 32)
	forwardUpdates := func(ch <-chan string) {
		if ch == nil {
			return
		}
		go func() {
			for name := range ch {
				select {
				case toolsUpdated <- name:
				default:
					// AUD-021: Log dropped toolsUpdated events for diagnostics.
					logger.Debug("toolsUpdated event dropped (channel full)", "name", name)
				}
			}
		}()
	}
	forwardUpdates(wasmWatcher.ToolsUpdated())
	go func() {
		for name := range skillWatcher.Events() {
			reloadSkills(ctx)
			select {
			case toolsUpdated <- name:
			default:
				// AUD-021: Log dropped toolsUpdated events for diagnostics.
				logger.Debug("toolsUpdated event dropped (channel full)", "name", name)
			}
		}
	}()

	// Create plan executor (GC-SPEC-PDR-v4-Phase-4: Plan execution engine).
	waiter := coordinator.NewWaiter(eventBus, store)
	executor := coordinator.NewExecutor(registry, waiter, store, eventBus)

	// GC-SPEC-PDR-v4-Phase-3: Resume crashed plans in background
	go func() {
		recoveries, err := store.RecoverRunningPlans(context.Background())
		if err != nil {
			logger.Warn("failed to query running plans for resumption", "error", err)
			return
		}
		if len(recoveries) == 0 {
			return
		}

		logger.Info("attempting to resume crashed plans", "count", len(recoveries))
		for _, rec := range recoveries {
			plan, ok := plansMap[rec.PlanName]
			if !ok {
				logger.Warn("cannot resume plan: not found in config", "exec_id", rec.ID, "plan_name", rec.PlanName)
				// Mark as failed since we can't find the plan definition
				_ = store.CompletePlanExecution(context.Background(), rec.ID, "failed", 0)
				continue
			}

			// Attempt resume
			result, resumeErr := executor.Resume(context.Background(), rec.ID, plan)
			if resumeErr != nil {
				logger.Warn("failed to resume crashed plan", "exec_id", rec.ID, "error", resumeErr)
			} else {
				logger.Info("successfully resumed crashed plan",
					"exec_id", rec.ID,
					"plan_name", rec.PlanName,
					"final_cost", result.TotalCost())
			}
		}
	}()

	gw := gateway.New(gateway.Config{
		Store:             store,
		Registry:          registry,
		Policy:            pol,
		Bus:               eventBus,
		AuthToken:         authToken,
		AllowOrigins:      cfg.AllowOrigins,
		ConfigFingerprint: cfg.Fingerprint(),
		ToolsUpdated:      toolsUpdated,
		TinygoStatus:      wasmWatcher.TinygoStatus,
		SkillsStatus:      skillsStatusFn,
		Plans:             planSummaries,
		PlansMap:          plansMap,
		Executor:          executor,
		WasmHost:          wasmHost,
	})

	gw.StartBackgroundTasks(ctx)

	server := &http.Server{
		Addr:    cfg.BindAddr,
		Handler: gw.Handler(),
	}
	serverErr := make(chan error, 1)
	lc := &net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
			})
		},
	}
	ln, err := lc.Listen(ctx, "tcp", cfg.BindAddr)
	if err != nil {
		if isAddrInUse(err) {
			hint := portOccupantHint(cfg.BindAddr)
			fatalStartup(logger, "E_ACP_LISTENER_BIND", fmt.Errorf("%w\n\n  %s", err, hint))
		}
		fatalStartup(logger, "E_ACP_LISTENER_BIND", err)
	}
	logger.Info("startup phase", "phase", "acp_listener_bound", "addr", cfg.BindAddr)
	go func() {
		logger.Info("gateway listening", "addr", cfg.BindAddr, "ws", "/ws")
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// Engines are started by registry.CreateAgent; no separate eng.Start needed.
	logger.Info("startup phase", "phase", "scheduler_started")

	// Cron scheduler: fires due schedules by creating tasks.
	cronSched := cron.NewScheduler(cron.Config{Store: store, Logger: logger})
	cronSched.Start(ctx)
	defer cronSched.Stop()

	// Heartbeat system
	heartbeat := engine.NewHeartbeatManager(registry, store, cfg.HomeDir, cfg.HeartbeatIntervalMinutes, logger)
	heartbeat.Start(ctx)

	// Channels
	if cfg.Channels.Telegram.Enabled {
		if cfg.Channels.Telegram.Token == "" {
			logger.Warn("telegram channel enabled but token is missing")
		} else {
			tg := channels.NewTelegramChannel(
				cfg.Channels.Telegram.Token,
				cfg.Channels.Telegram.AllowedIDs,
				registry,
				store,
				logger,
				eventBus,
			)

			// GC-SPEC-PDR-v7-Phase-3: Subscribe to plan execution and HITL events
			tg.SubscribeToEvents()

			go func() {
				if err := tg.Start(ctx); err != nil {
					logger.Error("telegram channel failed", "error", err)
				}
			}()
		}
	}

	// GC-SPEC-DATA-005: Periodic retention job.
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				result, err := store.RunRetention(ctx,
					cfg.RetentionTaskEventsDays,
					cfg.RetentionAuditLogDays,
					cfg.RetentionMessagesDays,
				)
				if err != nil {
					logger.Error("retention job failed", "error", err)
				} else if result.PurgedTaskEvents+result.PurgedAuditLogs+result.PurgedMessages > 0 {
					logger.Info("retention job completed",
						"purged_task_events", result.PurgedTaskEvents,
						"purged_audit_logs", result.PurgedAuditLogs,
						"purged_messages", result.PurgedMessages,
					)
				}
			}
		}
	}()

	go func() {
		for ev := range wasmWatcher.Notifications() {
			logger.Info("skill watcher event", "level", ev.Level, "message", ev.Message)
		}
	}()

	if interactive {
		// Run the chat REPL. When it exits, cancel the context to shut down.
		go func() {
			if err := tui.RunChat(ctx, tui.ChatConfig{
				Brain:        defaultAgent.Brain,
				Store:        store,
				Policy:       pol,
				ModelName:    cfg.GeminiModel,
				HomeDir:      cfg.HomeDir,
				Cfg:          &cfg,
				CancelFunc:   stop,
				Providers:    defaultAgent.Brain.Providers(),
				AgentName:    cfg.AgentName,
				AgentEmoji:   cfg.AgentEmoji,
				Switcher:     &tuiAgentSwitcher{reg: registry},
				CurrentAgent: "default",
				EventBus:     eventBus,
			}); err != nil && ctx.Err() == nil {
				logger.Error("chat exited with error", "error", err)
			}
			stop()
		}()
	}

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-serverErr:
		logger.Error("gateway server error", "error", err)
	}

	// GC-SPEC-RUN-003: Graceful shutdown phases.
	// 1. Stop intake (HTTP server shutdown stops new connections).
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	// GC-SPEC-REL-005: Drain active lanes with bounded configurable timeout.
	drainTimeout := time.Duration(cfg.DrainTimeoutSeconds) * time.Second
	if drainTimeout <= 0 {
		drainTimeout = 5 * time.Second
	}
	registry.DrainAll(drainTimeout)
	// 3. Flush events + close DB handled by deferred store.Close().
	logger.Info("shutdown complete")
}

// reconcileAgents reconciles the running agents with the new config.yaml agents section.
func reconcileAgents(ctx context.Context, reg *agent.Registry,
	newAgents, oldAgents []config.AgentConfigEntry, homeDir string, logger *slog.Logger) {
	newMap := make(map[string]config.AgentConfigEntry)
	for _, a := range newAgents {
		newMap[a.AgentID] = a
	}
	oldMap := make(map[string]config.AgentConfigEntry)
	for _, a := range oldAgents {
		oldMap[a.AgentID] = a
	}

	// Remove agents no longer in config (skip "default").
	for id := range oldMap {
		if _, still := newMap[id]; !still && id != "default" {
			if err := reg.RemoveAgent(ctx, id, 5*time.Second); err != nil {
				logger.Warn("failed to remove agent during reconcile", "agent_id", id, "error", err)
			}
		}
	}
	// Add new agents.
	for id, acfg := range newMap {
		if _, existed := oldMap[id]; !existed {
			if err := reg.CreateAgent(ctx, buildAgentConfig(acfg, homeDir)); err != nil {
				logger.Warn("failed to create agent during reconcile", "agent_id", id, "error", err)
			}
		}
	}
	// Changed agents: remove + re-create (skip "default").
	for id, acfg := range newMap {
		if old, existed := oldMap[id]; existed && !agentConfigEqual(acfg, old) && id != "default" {
			if err := reg.RemoveAgent(ctx, id, 5*time.Second); err != nil {
				logger.Warn("failed to remove changed agent during reconcile", "agent_id", id, "error", err)
			}
			if err := reg.CreateAgent(ctx, buildAgentConfig(acfg, homeDir)); err != nil {
				logger.Warn("failed to re-create changed agent during reconcile", "agent_id", id, "error", err)
			}
		}
	}
}

// loadPlans converts config plan entries into summaries and full plan definitions.
// It returns the loaded summaries and plan map, logging a warning on error.
func loadPlans(planConfigs []config.PlanConfig, agentIDs []string, logger *slog.Logger) (map[string]gateway.PlanSummary, map[string]*coordinator.Plan) {
	summaries := make(map[string]gateway.PlanSummary)
	plansMap := make(map[string]*coordinator.Plan)
	if len(planConfigs) == 0 {
		return summaries, plansMap
	}
	plans, err := coordinator.LoadPlansFromConfig(planConfigs, agentIDs)
	if err != nil {
		logger.Warn("failed to load plans from config", "error", err)
		return summaries, plansMap
	}
	for name, p := range plans {
		planCopy := p
		plansMap[name] = &planCopy

		agents := make(map[string]bool)
		for _, s := range p.Steps {
			agents[s.AgentID] = true
		}
		agentList := make([]string, 0, len(agents))
		for a := range agents {
			agentList = append(agentList, a)
		}
		summaries[name] = gateway.PlanSummary{
			Name:      name,
			StepCount: len(p.Steps),
			AgentIDs:  agentList,
		}
	}
	return summaries, plansMap
}

// buildAgentConfig constructs an agent.AgentConfig from config.AgentConfigEntry.
func buildAgentConfig(acfg config.AgentConfigEntry, homeDir string) agent.AgentConfig {
	apiKey := os.Getenv(acfg.APIKeyEnv)
	soul := acfg.Soul
	if acfg.SoulFile != "" {
		if data, err := os.ReadFile(filepath.Join(homeDir, acfg.SoulFile)); err == nil {
			soul = string(data)
		}
	}
	return agent.AgentConfig{
		AgentID:            acfg.AgentID,
		DisplayName:        acfg.DisplayName,
		Provider:           acfg.Provider,
		Model:              acfg.Model,
		APIKey:             apiKey,
		APIKeyEnv:          acfg.APIKeyEnv,
		Soul:               soul,
		WorkerCount:        acfg.WorkerCount,
		TaskTimeoutSeconds: acfg.TaskTimeoutSeconds,
		MaxQueueDepth:      acfg.MaxQueueDepth,
		SkillsFilter:       acfg.SkillsFilter,
		PreferredSearch:    acfg.PreferredSearch,
	}
}

// agentConfigEqual compares two AgentConfigEntry values for equality.
func agentConfigEqual(a, b config.AgentConfigEntry) bool {
	return a.AgentID == b.AgentID &&
		a.DisplayName == b.DisplayName &&
		a.Provider == b.Provider &&
		a.Model == b.Model &&
		a.APIKeyEnv == b.APIKeyEnv &&
		a.Soul == b.Soul &&
		a.SoulFile == b.SoulFile &&
		a.WorkerCount == b.WorkerCount &&
		a.TaskTimeoutSeconds == b.TaskTimeoutSeconds &&
		a.MaxQueueDepth == b.MaxQueueDepth &&
		a.PreferredSearch == b.PreferredSearch
}

// tuiAgentSwitcher adapts agent.Registry for the tui.AgentSwitcher interface.
type tuiAgentSwitcher struct {
	reg *agent.Registry
}

func (s *tuiAgentSwitcher) SwitchAgent(id string) (engine.Brain, string, string, error) {
	ra := s.reg.GetAgent(id)
	if ra == nil {
		return nil, "", "", fmt.Errorf("agent %q not found", id)
	}
	return ra.Brain, ra.Config.DisplayName, ra.Config.AgentEmoji, nil
}

func (s *tuiAgentSwitcher) ListAgentIDs() []string {
	configs := s.reg.ListAgents()
	ids := make([]string, len(configs))
	for i, c := range configs {
		ids[i] = c.AgentID
	}
	return ids
}

func (s *tuiAgentSwitcher) ListAgentInfo() []tui.AgentInfo {
	configs := s.reg.ListAgents()
	infos := make([]tui.AgentInfo, len(configs))
	for i, c := range configs {
		infos[i] = tui.AgentInfo{
			ID:          c.AgentID,
			DisplayName: c.DisplayName,
			Emoji:       c.AgentEmoji,
			Model:       c.Model,
		}
	}
	return infos
}

func (s *tuiAgentSwitcher) CreateAgent(ctx context.Context, id, name, provider, model, soul string) error {
	return s.reg.CreateAgent(ctx, agent.AgentConfig{
		AgentID:     id,
		DisplayName: name,
		Provider:    provider,
		Model:       model,
		Soul:        soul,
	})
}

func (s *tuiAgentSwitcher) RemoveAgent(ctx context.Context, id string) error {
	return s.reg.RemoveAgent(ctx, id, 5*time.Second)
}

func fatalStartup(logger *slog.Logger, reasonCode string, err error) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	// GC-SPEC-RUN-005: Produce structured fatal event with explicit reason code.
	audit.Record("fatal", "runtime.startup", reasonCode, "", message)

	if logger != nil {
		logger.Error("startup failure", "reason_code", reasonCode, "error", message)
	} else {
		fmt.Fprintf(
			os.Stderr,
			`{"timestamp":"%s","level":"ERROR","component":"runtime","trace_id":"-","msg":"startup failure","reason_code":%q,"error":%q}`+"\n",
			time.Now().UTC().Format(time.RFC3339Nano),
			reasonCode,
			message,
		)
	}
	os.Exit(1)
}

func isAddrInUse(err error) bool {
	if opErr, ok := err.(*net.OpError); ok {
		if sysErr, ok := opErr.Err.(*os.SyscallError); ok {
			return sysErr.Err == syscall.EADDRINUSE
		}
	}
	return strings.Contains(err.Error(), "address already in use")
}

func portOccupantHint(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Sprintf("Another process is using %s. Stop it first or change bind_addr in config.yaml.", addr)
	}
	// Try lsof to identify the occupying process (macOS/Linux).
	out, err := execCommand("lsof", "-ti", ":"+port)
	if err == nil && strings.TrimSpace(out) != "" {
		pids := strings.TrimSpace(out)
		return fmt.Sprintf("Port %s is occupied by PID %s. Kill it with: kill %s", port, pids, pids)
	}
	return fmt.Sprintf("Port %s is already in use. Stop the existing process or change bind_addr in config.yaml.", port)
}

func execCommand(name string, args ...string) (string, error) {
	cmd := execCommandFunc(name, args...)
	out, err := cmd.Output()
	return string(out), err
}

var execCommandFunc = newExecCommand

func newExecCommand(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		_ = os.Setenv(key, val)
	}
}

func loadAuthToken(homeDir string) (string, error) {
	if raw := strings.TrimSpace(os.Getenv("GOCLAW_AUTH_TOKEN")); raw != "" {
		return raw, nil
	}
	tokenPath := filepath.Join(homeDir, "auth.token")
	b, err := os.ReadFile(tokenPath)
	if err == nil {
		if tok := strings.TrimSpace(string(b)); tok != "" {
			return tok, nil
		}
	}
	// GC-SPEC-CFG-004: generate auth.token on first run if missing.
	token := uuid.NewString()
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("failed to persist auth token: %w", err)
	}
	slog.Info("auth.token generated", "path", tokenPath)
	return token, nil
}

// writeMinimalConfig writes a minimal config.yaml with starter agents to disk.
// Used as fallback when daemon mode is started without an existing config.yaml.
func writeMinimalConfig(homeDir string) error {
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return fmt.Errorf("create home: %w", err)
	}

	// Create a config struct with defaults and starter agents
	cfg := config.Config{
		WorkerCount:              16,
		TaskTimeoutSeconds:       int((10 * time.Minute).Seconds()),
		BindAddr:                 "127.0.0.1:18789",
		LogLevel:                 "info",
		MaxQueueDepth:            100,
		DrainTimeoutSeconds:      5,
		RetentionTaskEventsDays:  90,
		RetentionAuditLogDays:    365,
		RetentionMessagesDays:    90,
		HeartbeatIntervalMinutes: 30,
		Skills: config.SkillsConfig{
			ProjectDir: "./skills",
		},
		Agents: config.StarterAgents(), // Generate 3 starter agents
	}

	// Marshal to YAML
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	// Write to config.yaml
	configPath := filepath.Join(homeDir, "config.yaml")
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return fmt.Errorf("write config.yaml: %w", err)
	}

	return nil
}

type daemonSubcommandMode int

const (
	daemonSubcommandRun daemonSubcommandMode = iota
	daemonSubcommandHelp
)

func parseDaemonSubcommandArgs(args []string) (daemonSubcommandMode, error) {
	if len(args) == 0 {
		return daemonSubcommandRun, nil
	}
	if len(args) == 1 && isHelpArg(args[0]) {
		return daemonSubcommandHelp, nil
	}
	return daemonSubcommandRun, fmt.Errorf("usage: goclaw daemon [--help]")
}

func isHelpArg(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "-h", "--help", "help":
		return true
	default:
		return false
	}
}

func printDaemonSubcommandUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: goclaw daemon [--help]")
	fmt.Fprintln(w, "       goclaw -daemon")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Runs GoClaw in daemon mode (no interactive chat TUI).")
}

// findAgentConfig finds an agent config by ID in the agents list.
func findAgentConfig(agents []config.AgentConfigEntry, agentID string) *config.AgentConfigEntry {
	for i := range agents {
		if agents[i].AgentID == agentID {
			return &agents[i]
		}
	}
	return nil
}
