package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/engine"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
	"github.com/basket/go-claw/internal/sandbox/wasm"
)

// AgentConfig holds the configuration needed to create and run an agent.
type AgentConfig struct {
	AgentID              string
	DisplayName          string
	Provider             string // "google", "anthropic", "openai", etc.
	Model                string
	APIKey               string // in-memory only, never persisted
	APIKeyEnv            string // env var name for persistence
	Soul                 string // system prompt
	AgentEmoji           string
	WorkerCount          int
	TaskTimeoutSeconds   int
	MaxQueueDepth        int
	SkillsFilter         []string // empty = all skills
	PolicyOverrides      *policy.Policy
	PreferredSearch      string
	OpenAICompatProvider string
	OpenAICompatBaseURL  string
}

// RunningAgent holds a running agent's brain, engine, and lifecycle state.
type RunningAgent struct {
	Config    AgentConfig
	Engine    *engine.Engine
	Brain     *engine.GenkitBrain
	cancel    context.CancelFunc
	startedAt time.Time
}

// Registry manages the lifecycle of multiple named agents.
type Registry struct {
	mu             sync.RWMutex
	agents         map[string]*RunningAgent
	store          *persistence.Store
	bus            *bus.Bus
	policy         policy.Checker
	wasm           *wasm.Host
	apiKeys        map[string]string      // shared tool API keys (brave, perplexity, etc.)
	onAgentCreated func(ra *RunningAgent) // optional provisioning callback for runtime-created agents
}

// RegisterTestAgent registers a pre-built engine as a named agent.
// This is intended for testing scenarios where a custom processor is needed
// instead of GenkitBrain.
func (r *Registry) RegisterTestAgent(agentID string, eng *engine.Engine) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[agentID] = &RunningAgent{
		Config:    AgentConfig{AgentID: agentID},
		Engine:    eng,
		cancel:    func() {},
		startedAt: time.Now(),
	}
}

// NewRegistry creates a Registry that manages agent lifecycles.
func NewRegistry(store *persistence.Store, b *bus.Bus, pol policy.Checker,
	wasmHost *wasm.Host, apiKeys map[string]string) *Registry {
	return &Registry{
		agents:  make(map[string]*RunningAgent),
		store:   store,
		bus:     b,
		policy:  pol,
		wasm:    wasmHost,
		apiKeys: apiKeys,
	}
}

// SetOnAgentCreated registers a callback invoked after each new agent is created.
// This allows main.go to provision skills, MCP tools, shell executor, etc.
// on agents created at runtime via the agent.create RPC.
func (r *Registry) SetOnAgentCreated(fn func(ra *RunningAgent)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onAgentCreated = fn
}

// CreateAgent validates, initializes, and starts an agent, persisting it to DB.
func (r *Registry) CreateAgent(ctx context.Context, cfg AgentConfig) error {
	if cfg.AgentID == "" {
		return fmt.Errorf("agent_id must be non-empty")
	}

	r.mu.RLock()
	_, exists := r.agents[cfg.AgentID]
	r.mu.RUnlock()
	if exists {
		return fmt.Errorf("agent %q already exists", cfg.AgentID)
	}

	// Normalize defaults.
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 4
	}
	if cfg.TaskTimeoutSeconds <= 0 {
		cfg.TaskTimeoutSeconds = 600
	}

	// Resolve API key: in-memory value -> env var -> empty.
	apiKey := cfg.APIKey
	if apiKey == "" && cfg.APIKeyEnv != "" {
		apiKey = os.Getenv(cfg.APIKeyEnv)
	}

	// Resolve policy: per-agent overrides or global.
	var agentPolicy policy.Checker
	if cfg.PolicyOverrides != nil {
		agentPolicy = policy.NewLivePolicy(*cfg.PolicyOverrides, "")
	} else {
		agentPolicy = r.policy
	}

	// Create GenkitBrain.
	brain := engine.NewGenkitBrain(ctx, r.store, engine.BrainConfig{
		Provider:                 cfg.Provider,
		Model:                    cfg.Model,
		APIKey:                   apiKey,
		Soul:                     cfg.Soul,
		AgentName:                cfg.DisplayName,
		AgentEmoji:               cfg.AgentEmoji,
		Policy:                   agentPolicy,
		APIKeys:                  r.apiKeys,
		PreferredSearch:          cfg.PreferredSearch,
		OpenAICompatibleProvider: cfg.OpenAICompatProvider,
		OpenAICompatibleBaseURL:  cfg.OpenAICompatBaseURL,
	})

	// Set WASM host if available.
	if r.wasm != nil {
		brain.SetWASMHost(r.wasm)
	}

	// Create Engine with agent scoping.
	eng := engine.New(r.store, engine.EchoProcessor{Brain: brain}, engine.Config{
		AgentID:       cfg.AgentID,
		WorkerCount:   cfg.WorkerCount,
		PollInterval:  50 * time.Millisecond,
		TaskTimeout:   time.Duration(cfg.TaskTimeoutSeconds) * time.Second,
		MaxQueueDepth: cfg.MaxQueueDepth,
		Bus:           r.bus,
	}, agentPolicy)

	// Start engine.
	agentCtx, cancel := context.WithCancel(ctx)
	eng.Start(agentCtx)

	// Persist to DB. If the agent already exists (e.g. restore path), update status.
	rec := persistence.AgentRecord{
		AgentID:            cfg.AgentID,
		DisplayName:        cfg.DisplayName,
		Provider:           cfg.Provider,
		Model:              cfg.Model,
		Soul:               cfg.Soul,
		WorkerCount:        cfg.WorkerCount,
		TaskTimeoutSeconds: cfg.TaskTimeoutSeconds,
		MaxQueueDepth:      cfg.MaxQueueDepth,
		APIKeyEnv:          cfg.APIKeyEnv,
		AgentEmoji:         cfg.AgentEmoji,
		PreferredSearch:    cfg.PreferredSearch,
		Status:             "active",
	}
	if err := r.store.CreateAgent(ctx, rec); err != nil {
		// Only treat as duplicate if it's a UNIQUE constraint violation.
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			_ = r.store.UpdateAgentStatus(ctx, cfg.AgentID, "active")
			slog.Info("agent already in DB, reactivated", "agent_id", cfg.AgentID)
		} else {
			cancel()
			eng.Drain(2 * time.Second)
			return fmt.Errorf("create agent record: %w", err)
		}
	}

	// Store in map. Re-check under write lock to prevent race between
	// concurrent CreateAgent calls for the same agent_id.
	// Capture callback and agent ref under lock so we don't read them after unlock.
	r.mu.Lock()
	if _, dup := r.agents[cfg.AgentID]; dup {
		r.mu.Unlock()
		cancel()
		eng.Drain(2 * time.Second)
		return fmt.Errorf("agent %q already exists (concurrent create)", cfg.AgentID)
	}
	ra := &RunningAgent{
		Config:    cfg,
		Engine:    eng,
		Brain:     brain,
		cancel:    cancel,
		startedAt: time.Now(),
	}
	r.agents[cfg.AgentID] = ra
	cb := r.onAgentCreated
	r.mu.Unlock()

	slog.Info("agent created", "agent_id", cfg.AgentID, "provider", cfg.Provider, "workers", cfg.WorkerCount)

	// Invoke provisioning hook so runtime-created agents receive skills, MCP tools,
	// shell executor, etc. The hook is set by main.go after initial startup.
	if cb != nil {
		cb(ra)
	}

	return nil
}

// RemoveAgent stops and removes a non-default agent, draining its engine.
func (r *Registry) RemoveAgent(ctx context.Context, agentID string, drainTimeout time.Duration) error {
	if agentID == "default" {
		return fmt.Errorf("cannot remove default agent")
	}

	r.mu.Lock()
	agent, ok := r.agents[agentID]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("agent %q not found", agentID)
	}
	delete(r.agents, agentID)
	r.mu.Unlock()

	agent.cancel()
	agent.Engine.Drain(drainTimeout)

	if err := r.store.UpdateAgentStatus(ctx, agentID, "stopped"); err != nil {
		slog.Warn("failed to update agent status in DB", "agent_id", agentID, "error", err)
	}

	slog.Info("agent removed", "agent_id", agentID)
	return nil
}

// GetAgent returns a running agent by ID, or nil if not found.
func (r *Registry) GetAgent(agentID string) *RunningAgent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.agents[agentID]
}

// ListAgents returns the configurations of all running agents.
func (r *Registry) ListAgents() []AgentConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	configs := make([]AgentConfig, 0, len(r.agents))
	for _, a := range r.agents {
		configs = append(configs, a.Config)
	}
	return configs
}

// ListRunningAgents returns all running agent instances.
func (r *Registry) ListRunningAgents() []*RunningAgent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	agents := make([]*RunningAgent, 0, len(r.agents))
	for _, a := range r.agents {
		agents = append(agents, a)
	}
	return agents
}

// AgentStatus returns the engine status for a running agent.
func (r *Registry) AgentStatus(agentID string) (*engine.Status, error) {
	r.mu.RLock()
	agent, ok := r.agents[agentID]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("agent %q not found", agentID)
	}
	st := agent.Engine.Status()
	return &st, nil
}

// CreateChatTask routes a chat task to the specified agent's engine.
func (r *Registry) CreateChatTask(ctx context.Context, agentID, sessionID, content string) (string, error) {
	r.mu.RLock()
	agent, ok := r.agents[agentID]
	r.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("agent %q not found", agentID)
	}
	return agent.Engine.CreateChatTaskForAgent(ctx, agentID, sessionID, content)
}

// CreateMessageTask creates a task with inter-agent message depth for loop prevention.
func (r *Registry) CreateMessageTask(ctx context.Context, agentID, sessionID, content string, depth int) (string, error) {
	r.mu.RLock()
	agent, ok := r.agents[agentID]
	r.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("agent %q not found", agentID)
	}
	return agent.Engine.CreateMessageTaskForAgent(ctx, agentID, sessionID, content, depth)
}

// StreamChatTask routes a streaming chat task to the specified agent's engine.
func (r *Registry) StreamChatTask(ctx context.Context, agentID, sessionID, content string, onChunk func(string) error) (string, error) {
	r.mu.RLock()
	agent, ok := r.agents[agentID]
	r.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("agent %q not found", agentID)
	}
	return agent.Engine.StreamChatTaskForAgent(ctx, agentID, sessionID, content, onChunk)
}

// AbortTask finds the agent owning a task and aborts it.
func (r *Registry) AbortTask(ctx context.Context, taskID string) (bool, error) {
	task, err := r.store.GetTask(ctx, taskID)
	if err != nil {
		return false, err
	}
	if task == nil {
		return false, fmt.Errorf("task %q not found", taskID)
	}
	r.mu.RLock()
	agent, ok := r.agents[task.AgentID]
	r.mu.RUnlock()
	if !ok {
		return r.store.AbortTask(ctx, taskID)
	}
	return agent.Engine.AbortTask(ctx, taskID)
}

// DrainAll cancels and drains all running agents in parallel.
func (r *Registry) DrainAll(timeout time.Duration) {
	r.mu.RLock()
	agents := make([]*RunningAgent, 0, len(r.agents))
	for _, a := range r.agents {
		agents = append(agents, a)
	}
	r.mu.RUnlock()

	var wg sync.WaitGroup
	for _, a := range agents {
		wg.Add(1)
		go func(agent *RunningAgent) {
			defer wg.Done()
			agent.cancel()
			agent.Engine.Drain(timeout)
		}(a)
	}
	wg.Wait()
}

// RestorePersistedAgents re-creates agents from DB records with status "active".
func (r *Registry) RestorePersistedAgents(ctx context.Context) error {
	records, err := r.store.ListAgents(ctx)
	if err != nil {
		return fmt.Errorf("list persisted agents: %w", err)
	}

	var errs []error
	for _, rec := range records {
		if rec.Status != "active" {
			continue
		}
		// Skip agents already running (e.g. "default").
		r.mu.RLock()
		_, exists := r.agents[rec.AgentID]
		r.mu.RUnlock()
		if exists {
			continue
		}

		cfg := AgentConfig{
			AgentID:            rec.AgentID,
			DisplayName:        rec.DisplayName,
			Provider:           rec.Provider,
			Model:              rec.Model,
			APIKeyEnv:          rec.APIKeyEnv,
			Soul:               rec.Soul,
			AgentEmoji:         rec.AgentEmoji,
			WorkerCount:        rec.WorkerCount,
			TaskTimeoutSeconds: rec.TaskTimeoutSeconds,
			MaxQueueDepth:      rec.MaxQueueDepth,
			PreferredSearch:    rec.PreferredSearch,
		}

		if err := r.CreateAgent(ctx, cfg); err != nil {
			slog.Warn("failed to restore agent", "agent_id", rec.AgentID, "error", err)
			errs = append(errs, fmt.Errorf("restore %q: %w", rec.AgentID, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("some agents failed to restore: %v", errs)
	}
	return nil
}
