package tui

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/config"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/shared"
	"github.com/basket/go-claw/internal/tokenutil"
)

type chatRole string

const (
	chatRoleUser      chatRole = "user"
	chatRoleAssistant chatRole = "assistant"
	chatRoleSystem    chatRole = "system"
)

type chatEntry struct {
	role chatRole
	text string
}

type brainReplyMsg struct {
	reply string
	err   error
}

type ctxDoneMsg struct{}

type spinnerTickMsg struct{}

// statusTickMsg triggers a periodic refresh of operational metrics (GC-SPEC-TUI-002).
type statusTickMsg struct{}

// planEventMsg delivers a plan event from the bus subscription to the TUI update loop.
type planEventMsg struct {
	event bus.Event
}

// PlanExecutionState tracks an active plan execution for display in the TUI.
type PlanExecutionState struct {
	ExecutionID    string
	PlanName       string
	Status         string // "running", "succeeded", "failed"
	TotalSteps     int
	CompletedSteps int
	StartedAt      time.Time
}

// planTracker is a shared, pointer-based container for plan execution state.
// Bubbletea passes models by value, so a mutex cannot live directly on chatModel.
type planTracker struct {
	mu         sync.RWMutex
	executions map[string]*PlanExecutionState
}

// GC-SPEC-PDR-v4-Phase-5: Delegation and plan progress tracking types.
type delegationStatus struct {
	TaskID      string
	TargetAgent string
	StartedAt   time.Time
}

type planStepStatus struct {
	ID       string
	AgentID  string
	Status   string
	Duration time.Duration
	CostUSD  float64
}

type activeDelegation struct {
	TaskID string
	Status delegationStatus
}

type activePlan struct {
	Name  string
	Steps []planStepStatus
}

type chatMode int

const (
	chatModeChat chatMode = iota
	chatModeModelSelector
	chatModeAgentSelector
	chatModePlanView
)

type chatModel struct {
	ctx context.Context
	cc  ChatConfig

	sessionID   string
	agentPrefix string
	modelName   string

	width  int
	height int

	history    []chatEntry
	thinking   bool
	spinnerIdx int

	mode          chatMode
	selector      selectorModel
	agentSelector agentSelectorModel

	input  []rune
	cursor int // rune index within input

	// Input history navigation (Up/Down).
	inputHistory []string
	histIdx      int    // 0..len(inputHistory); len = editing new line
	histSaved    string // current draft before entering history

	// GC-SPEC-TUI-002: Operational status bar.
	metrics   persistence.MetricsCounts
	denyCount int64

	// Plan execution tracking (GC-SPEC-PDR-v4-Phase-5).
	plans   *planTracker
	planSub *bus.Subscription
}

func newChatModel(ctx context.Context, cc ChatConfig, sessionID, agentPrefix, modelName string) chatModel {
	m := chatModel{
		ctx:         ctx,
		cc:          cc,
		sessionID:   sessionID,
		agentPrefix: agentPrefix,
		modelName:   modelName,
		mode:        chatModeChat,
		plans:       &planTracker{executions: make(map[string]*PlanExecutionState)},
	}
	// Subscribe to plan events from the event bus.
	if cc.EventBus != nil {
		m.planSub = cc.EventBus.Subscribe("plan.")
	}
	// Small intro line inside the UI (kept minimal; avoids printing to stdout).
	m.history = append(m.history, chatEntry{
		role: chatRoleSystem,
		text: fmt.Sprintf("%s is online. Type /help for commands.", agentPrefix),
	})
	m.histIdx = 0
	return m
}

func runChatTUI(ctx context.Context, m chatModel, cancel context.CancelFunc) error {
	// BubbleTea should restore the terminal on exit, but if the process is
	// interrupted at an unfortunate time it's easy to end up with ICRNL off
	// (Enter appears as ^M/+M and line-based prompts stop working). This is a
	// best-effort safety net.
	defer bestEffortResetTTY()

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	_, err := p.Run()
	if cancel != nil {
		cancel()
	}
	if err != nil && ctx.Err() != nil {
		// If the parent context is cancelled we don't care about the renderer error.
		return nil
	}
	return err
}

func (m chatModel) Init() tea.Cmd {
	cmds := []tea.Cmd{waitCtxDone(m.ctx), statusTickCmd()}
	if m.planSub != nil {
		cmds = append(cmds, waitForPlanEvent(m.planSub))
	}
	return tea.Batch(cmds...)
}

func statusTickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return statusTickMsg{} })
}

func waitCtxDone(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		<-ctx.Done()
		return ctxDoneMsg{}
	}
}

func (m chatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case ctxDoneMsg:
		return m, tea.Quit

	case planEventMsg:
		m.plans.handleEvent(msg.event)
		var cmd tea.Cmd
		if m.planSub != nil {
			cmd = waitForPlanEvent(m.planSub)
		}
		return m, cmd

	case statusTickMsg:
		// GC-SPEC-TUI-002: Refresh operational metrics for the status bar.
		if m.cc.Store != nil {
			if mc, err := m.cc.Store.MetricsCounts(m.ctx); err == nil {
				m.metrics = mc
			}
		}
		m.denyCount = audit.DenyCount()
		m.plans.cleanup()
		return m, statusTickCmd()

	case tea.KeyMsg:
		if m.mode == chatModeModelSelector {
			updated, cmd := m.selector.Update(msg)
			sm, ok := updated.(selectorModel)
			if ok {
				m.selector = sm
			}
			if m.selector.quitting {
				m.mode = chatModeChat
				m.selector = selectorModel{}
				return m, nil
			}
			if m.selector.done {
				provider := strings.TrimSpace(m.selector.selectedProvider)
				modelID := strings.TrimSpace(m.selector.selectedModel)
				if provider != "" && modelID != "" && m.cc.Cfg != nil {
					if err := config.SetModel(m.cc.HomeDir, provider, modelID); err != nil {
						m.history = append(m.history, chatEntry{role: chatRoleSystem, text: fmt.Sprintf("Error saving config: %v", err)})
					} else {
						m.cc.Cfg.LLMProvider = provider
						m.cc.Cfg.GeminiModel = modelID
						m.history = append(m.history, chatEntry{role: chatRoleSystem, text: fmt.Sprintf("Model set to: %s (provider: %s)\nRestart GoClaw for the change to take effect.", modelID, provider)})
					}
				}
				m.mode = chatModeChat
				m.selector = selectorModel{}
				return m, nil
			}
			return m, cmd
		}

		if m.mode == chatModeAgentSelector {
			updated, cmd := m.agentSelector.Update(msg)
			as, ok := updated.(agentSelectorModel)
			if ok {
				m.agentSelector = as
			}
			if m.agentSelector.quit {
				m.mode = chatModeChat
				m.agentSelector = agentSelectorModel{}
				return m, nil
			}
			if m.agentSelector.done {
				agentID := m.agentSelector.selectedID
				if agentID != "" && m.cc.Switcher != nil {
					brain, name, emoji, err := m.cc.Switcher.SwitchAgent(agentID)
					if err != nil {
						m.history = append(m.history, chatEntry{role: chatRoleSystem, text: fmt.Sprintf("Error: %v", err)})
					} else {
						m.cc.Brain = brain
						m.cc.AgentName = name
						m.cc.AgentEmoji = emoji
						m.cc.CurrentAgent = agentID
						if name != "" && emoji != "" {
							m.agentPrefix = fmt.Sprintf("%s %s", emoji, name)
						} else if name != "" {
							m.agentPrefix = name
						} else {
							m.agentPrefix = agentID
						}
						m.history = append(m.history, chatEntry{role: chatRoleSystem, text: fmt.Sprintf("Switched to agent: %s", agentID)})
					}
				}
				m.mode = chatModeChat
				m.agentSelector = agentSelectorModel{}
				return m, nil
			}
			return m, cmd
		}

		if m.mode == chatModePlanView {
			switch msg.String() {
			case "ctrl+c", "ctrl+d":
				return m, tea.Quit
			default:
				// Any key exits plan view back to chat.
				m.mode = chatModeChat
				return m, nil
			}
		}

		switch msg.String() {
		case "ctrl+c", "ctrl+d":
			return m, tea.Quit

		case "enter", "ctrl+m", "ctrl+j":
			if m.thinking {
				return m, nil
			}
			line := strings.TrimSpace(string(m.input))
			m.input = nil
			m.cursor = 0
			m.histIdx = len(m.inputHistory)
			m.histSaved = ""
			if line == "" {
				return m, nil
			}

			// Save to input history (commands and prompts).
			m.inputHistory = append(m.inputHistory, line)
			m.histIdx = len(m.inputHistory)

			// Slash commands.
			if strings.HasPrefix(line, "/") {
				trimmed := strings.TrimSpace(line)

				// /plans toggles the plan execution view.
				if trimmed == "/plans" {
					m.mode = chatModePlanView
					return m, nil
				}

				// /agents (no args) is interactive; embed the agent selector.
				if (trimmed == "/agents" || trimmed == "/agent") && m.cc.Switcher != nil {
					infos := m.cc.Switcher.ListAgentInfo()
					if len(infos) > 0 {
						m.agentSelector = newAgentSelector(infos, m.cc.CurrentAgent)
						m.mode = chatModeAgentSelector
						return m, nil
					}
				}

				// /model (no args) is interactive; embed the selector in this program.
				if trimmed == "/model" {
					if m.cc.Cfg == nil {
						m.history = append(m.history, chatEntry{role: chatRoleSystem, text: "Config not available for interactive selector."})
						return m, nil
					}
					providers, mergedModels := buildSelectorData(m.cc.Cfg)
					m.selector = selectorModel{
						step:            stepSelectProvider,
						providers:       providers,
						allModels:       mergedModels,
						currentProvider: m.cc.Cfg.LLMProvider,
						currentModel:    m.cc.Cfg.GeminiModel,
						embedded:        true,
					}
					m.mode = chatModeModelSelector
					return m, nil
				}

				var buf bytes.Buffer
				shouldExit := handleCommand(line, &m.cc, m.sessionID, &buf)
				out := strings.TrimSpace(buf.String())
				if out != "" {
					m.history = append(m.history, chatEntry{role: chatRoleSystem, text: out})
				}
				if shouldExit {
					return m, tea.Quit
				}
				// Update agentPrefix if agent was switched via /agent command.
				if m.cc.AgentName != "" && m.cc.AgentEmoji != "" {
					m.agentPrefix = fmt.Sprintf("%s %s", m.cc.AgentEmoji, m.cc.AgentName)
				} else if m.cc.AgentName != "" {
					m.agentPrefix = m.cc.AgentName
				}
				return m, nil
			}


		// Parse @mentions. Path B: all mentions use sticky switch (v0.2).
		// TODO(v0.2.1): implement Path A single-message routing when agentID can override.
		mention := ParseMention(line)
		if mention.AgentID != "" {
			// Validate agent exists
			agentIDs := m.cc.Switcher.ListAgentIDs()
			found := false
			for _, id := range agentIDs {
				if id == mention.AgentID {
					found = true
					break
				}
			}
			if !found {
				available := strings.Join(agentIDs, ", @")
				m.history = append(m.history, chatEntry{role: chatRoleSystem, text: fmt.Sprintf("Unknown agent: @%s. Available: @%s", mention.AgentID, available)})
				return m, nil
			}

			// Switch to target agent
			brain, name, emoji, err := m.cc.Switcher.SwitchAgent(mention.AgentID)
			if err != nil {
				m.history = append(m.history, chatEntry{role: chatRoleSystem, text: fmt.Sprintf("Error switching to @%s: %v", mention.AgentID, err)})
				return m, nil
			}
			m.cc.Brain = brain
			m.cc.AgentName = name
			m.cc.AgentEmoji = emoji
			m.cc.CurrentAgent = mention.AgentID
			if name != "" && emoji != "" {
				m.agentPrefix = fmt.Sprintf("%s %s", emoji, name)
			} else if name != "" {
				m.agentPrefix = name
			} else {
				m.agentPrefix = mention.AgentID
			}

			// Send message if provided
			if mention.Message == "" {
				// Bare @agent — just switched, no message to send
				m.history = append(m.history, chatEntry{role: chatRoleSystem, text: fmt.Sprintf("Switched to agent: @%s", mention.AgentID)})
				return m, nil
			}

			line = mention.Message // Update line to the actual message
		}
			// User message.
			m.history = append(m.history, chatEntry{role: chatRoleUser, text: line})
			if m.cc.Store != nil {
				_ = m.cc.Store.AddHistory(m.ctx, m.sessionID, m.cc.CurrentAgent, "user", line, tokenutil.EstimateTokens(line))
			}
			m.thinking = true
			return m, tea.Batch(respondCmd(m.ctx, m.cc, m.sessionID, line), waitForSpinner())

		case "up", "ctrl+p":
			m = m.historyPrev()
			return m, nil
		case "down", "ctrl+n":
			m = m.historyNext()
			return m, nil

		case "backspace":
			m.input, m.cursor = deleteRuneLeft(m.input, m.cursor)
			return m, nil
		case "delete":
			m.input, m.cursor = deleteRuneRight(m.input, m.cursor)
			return m, nil
		case "tab":
			m.input, m.cursor = insertRunes(m.input, m.cursor, []rune{'\t'})
			return m, nil
		case " ":
			// Some terminals report space as KeySpace (not KeyRunes).
			m.input, m.cursor = insertRunes(m.input, m.cursor, []rune{' '})
			return m, nil

		case "left":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "right":
			if m.cursor < len(m.input) {
				m.cursor++
			}
			return m, nil
		case "home":
			m.cursor = 0
			return m, nil
		case "end":
			m.cursor = len(m.input)
			return m, nil

		// Terminal mappings vary; Cmd+Backspace is commonly configured as ctrl+u (kill line) or ctrl+w (backward-kill-word).
		case "ctrl+a":
			m.cursor = 0
			return m, nil
		case "ctrl+e":
			m.cursor = len(m.input)
			return m, nil
		case "ctrl+b":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "ctrl+f":
			if m.cursor < len(m.input) {
				m.cursor++
			}
			return m, nil
		case "ctrl+k":
			if m.cursor < len(m.input) {
				m.input = append([]rune(nil), m.input[:m.cursor]...)
			}
			return m, nil
		case "ctrl+u":
			m.input = nil
			m.cursor = 0
			return m, nil
		case "ctrl+w", "alt+backspace":
			m.input, m.cursor = deleteWordLeft(m.input, m.cursor)
			return m, nil
		}

		// Allow typing even while the agent is thinking; Enter is still blocked.
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
			// Ignore control characters that some terminals may report as runes
			// (notably Enter as '\r', which would show up as ^M/+M in the input).
			filtered := make([]rune, 0, len(msg.Runes))
			for _, r := range msg.Runes {
				switch r {
				case '\r', '\n':
					// treat as non-insertable; handled above in the "enter/ctrl+m/ctrl+j" cases
					continue
				case '\t':
					filtered = append(filtered, r)
				default:
					if r < 0x20 {
						continue
					}
					filtered = append(filtered, r)
				}
			}
			if len(filtered) > 0 {
				m.input, m.cursor = insertRunes(m.input, m.cursor, filtered)
			}
			return m, nil
		}

	case brainReplyMsg:
		m.thinking = false
		if msg.err != nil {
			// Context cancellation is a normal shutdown path.
			if m.ctx.Err() != nil {
				return m, tea.Quit
			}
			m.history = append(m.history, chatEntry{role: chatRoleSystem, text: fmt.Sprintf("Error: %v", msg.err)})
			return m, nil
		}

		m.history = append(m.history, chatEntry{role: chatRoleAssistant, text: msg.reply})
		if m.cc.Store != nil {
			_ = m.cc.Store.AddHistory(m.ctx, m.sessionID, m.cc.CurrentAgent, "assistant", msg.reply, tokenutil.EstimateTokens(msg.reply))
		}
		return m, nil

	case spinnerTickMsg:
		if m.thinking {
			m.spinnerIdx++
			return m, waitForSpinner()
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	}

	return m, nil
}

func respondCmd(ctx context.Context, cc ChatConfig, sessionID, prompt string) tea.Cmd {
	return func() tea.Msg {
		if cc.Brain == nil {
			return brainReplyMsg{err: fmt.Errorf("brain not configured")}
		}
		// GC-SPEC-RUN-004: Inject agent ID, trace ID, and run ID into context.
		traceID := shared.NewTraceID()
		runID := shared.NewRunID()
		agentCtx := shared.WithAgentID(ctx, cc.CurrentAgent)
		agentCtx = shared.WithTraceID(agentCtx, traceID)
		agentCtx = shared.WithRunID(agentCtx, runID)
		slog.Debug("tui: chat request", "agent_id", cc.CurrentAgent, "session_id", sessionID, "trace_id", traceID, "run_id", runID)
		reply, err := cc.Brain.Respond(agentCtx, sessionID, prompt)
		if err != nil {
			slog.Warn("tui: chat response error", "agent_id", cc.CurrentAgent, "session_id", sessionID, "trace_id", traceID, "run_id", runID, "error", err)
		} else {
			slog.Debug("tui: chat response ok", "agent_id", cc.CurrentAgent, "session_id", sessionID, "trace_id", traceID, "run_id", runID)
		}
		return brainReplyMsg{reply: reply, err: err}
	}
}

func (m chatModel) View() string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("%s — %s\n", m.agentPrefix, m.modelName))
	b.WriteString("Type a message. /help for commands, Ctrl+D or /quit to exit.\n")
	b.WriteString("\n")

	if m.mode == chatModeModelSelector {
		// Selector view already includes its own help footer.
		b.WriteString(m.selector.View())
		return b.String()
	}

	if m.mode == chatModeAgentSelector {
		b.WriteString(m.agentSelector.View())
		return b.String()
	}

	if m.mode == chatModePlanView {
		b.WriteString(m.renderPlanView())
		return b.String()
	}

	// Render history, clipped to window height (best-effort; no expensive wrapping).
	hLines := m.renderHistoryLines()
	available := m.height - 6 // header + instructions + blank + input + spinner + status bar
	if available < 3 {
		available = 3
	}
	if len(hLines) > available {
		hLines = hLines[len(hLines)-available:]
	}
	for _, l := range hLines {
		b.WriteString(l)
		b.WriteString("\n")
	}

	// Input. Show current agent ID in prompt (e.g., "coder> " or "@coder> ").
	b.WriteString("\n")
	if m.cc.CurrentAgent != "" && m.cc.CurrentAgent != "default" {
		b.WriteString(fmt.Sprintf("@%s> ", m.cc.CurrentAgent))
	} else {
		b.WriteString("> ")
	}
	b.WriteString(renderCursor(string(m.input), m.cursor))
	b.WriteString("\n")
	if m.thinking {
		spin := []string{"|", "/", "-", "\\"}[m.spinnerIdx%4]
		b.WriteString(fmt.Sprintf("%s thinking...\n", spin))
	} else {
		b.WriteString("\n")
	}

	// GC-SPEC-TUI-002 / TUI-003: Operational status bar.
	statusBar := fmt.Sprintf("[Q:%d R:%d Retry:%d DLQ:%d Deny:%d]",
		m.metrics.Pending, m.metrics.Running, m.metrics.RetryWait,
		m.metrics.DeadLetter, m.denyCount)
	b.WriteString(statusBar)
	b.WriteString("\n")

	return b.String()
}

func (m chatModel) renderHistoryLines() []string {
	lines := make([]string, 0, len(m.history)*2)
	for _, e := range m.history {
		prefix := ""
		text := e.text
		switch e.role {
		case chatRoleUser:
			prefix = "You: "
			// Highlight @mentions in cyan
			text = HighlightMention(text)
		case chatRoleAssistant:
			prefix = m.agentPrefix + ": "
		}

		lines = append(lines, m.wrapWithPrefix(text, prefix)...)
	}
	return lines
}

func (m chatModel) wrapWithPrefix(text, prefix string) []string {
	if m.width <= 0 {
		return appendPrefixToLines(text, prefix)
	}

	availableWidth := m.width - len(prefix)
	if availableWidth < 10 {
		availableWidth = 10
	}

	var result []string
	for _, line := range strings.Split(text, "\n") {
		for len(line) > availableWidth {
			result = append(result, prefix+line[:availableWidth])
			line = line[availableWidth:]
		}
		result = append(result, prefix+line)
	}
	return result
}

func appendPrefixToLines(text, prefix string) []string {
	var result []string
	for _, line := range strings.Split(text, "\n") {
		result = append(result, prefix+line)
	}
	return result
}

func (m chatModel) historyPrev() chatModel {
	if len(m.inputHistory) == 0 {
		return m
	}
	// First time entering history: capture the current draft.
	if m.histIdx == len(m.inputHistory) {
		m.histSaved = string(m.input)
	}
	if m.histIdx > 0 {
		m.histIdx--
		m.input = []rune(m.inputHistory[m.histIdx])
		m.cursor = len(m.input)
	}
	return m
}

func (m chatModel) historyNext() chatModel {
	if len(m.inputHistory) == 0 {
		return m
	}
	if m.histIdx < len(m.inputHistory)-1 {
		m.histIdx++
		m.input = []rune(m.inputHistory[m.histIdx])
		m.cursor = len(m.input)
		return m
	}
	// Move back to the draft line.
	if m.histIdx == len(m.inputHistory)-1 {
		m.histIdx = len(m.inputHistory)
		m.input = []rune(m.histSaved)
		m.cursor = len(m.input)
	}
	return m
}

func insertRunes(in []rune, cursor int, r []rune) ([]rune, int) {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(in) {
		cursor = len(in)
	}
	out := make([]rune, 0, len(in)+len(r))
	out = append(out, in[:cursor]...)
	out = append(out, r...)
	out = append(out, in[cursor:]...)
	return out, cursor + len(r)
}

func deleteRuneLeft(in []rune, cursor int) ([]rune, int) {
	if cursor <= 0 || len(in) == 0 {
		return in, 0
	}
	if cursor > len(in) {
		cursor = len(in)
	}
	out := append([]rune(nil), in[:cursor-1]...)
	out = append(out, in[cursor:]...)
	return out, cursor - 1
}

func deleteRuneRight(in []rune, cursor int) ([]rune, int) {
	if len(in) == 0 {
		return in, 0
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(in) {
		return in, len(in)
	}
	out := append([]rune(nil), in[:cursor]...)
	out = append(out, in[cursor+1:]...)
	return out, cursor
}

func deleteWordLeft(in []rune, cursor int) ([]rune, int) {
	if len(in) == 0 || cursor <= 0 {
		return in, 0
	}
	if cursor > len(in) {
		cursor = len(in)
	}

	i := cursor
	// Skip any spaces just before the cursor.
	for i > 0 && isSpace(in[i-1]) {
		i--
	}
	// Then delete the word characters.
	for i > 0 && !isSpace(in[i-1]) {
		i--
	}

	out := append([]rune(nil), in[:i]...)
	out = append(out, in[cursor:]...)
	return out, i
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

func waitForSpinner() tea.Cmd {
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

// waitForPlanEvent blocks until a plan event arrives on the subscription channel.
func waitForPlanEvent(sub *bus.Subscription) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-sub.Ch()
		if !ok {
			return nil // channel closed
		}
		return planEventMsg{event: event}
	}
}

// handlePlanEvent processes plan bus events and updates the planTracker.
func (pt *planTracker) handleEvent(event bus.Event) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	payload, ok := event.Payload.(map[string]interface{})
	if !ok {
		return
	}

	switch event.Topic {
	case bus.TopicPlanExecutionStarted:
		execID, _ := payload["execution_id"].(string)
		planName, _ := payload["plan_name"].(string)
		totalSteps, _ := payload["total_steps"].(int)
		if execID == "" {
			return
		}
		pt.executions[execID] = &PlanExecutionState{
			ExecutionID: execID,
			PlanName:    planName,
			Status:      "running",
			TotalSteps:  totalSteps,
			StartedAt:   time.Now(),
		}

	case bus.TopicPlanExecutionCompleted:
		execID, _ := payload["execution_id"].(string)
		status, _ := payload["status"].(string)
		if execID == "" {
			return
		}
		if pe, exists := pt.executions[execID]; exists {
			pe.Status = status
			pe.CompletedSteps = pe.TotalSteps
		}
	}
}

// cleanup removes completed plans older than 2 seconds.
func (pt *planTracker) cleanup() {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	now := time.Now()
	for id, pe := range pt.executions {
		if pe.Status != "running" && now.Sub(pe.StartedAt) > 2*time.Second {
			delete(pt.executions, id)
		}
	}
}

// renderPlanView renders the plan execution view.
func (m chatModel) renderPlanView() string {
	m.plans.mu.RLock()
	defer m.plans.mu.RUnlock()

	var b strings.Builder
	b.WriteString("Plan Executions  [any key: back to chat]\n")
	b.WriteString(strings.Repeat("-", 40))
	b.WriteString("\n\n")

	if len(m.plans.executions) == 0 {
		b.WriteString("No active plans.\n")
		return b.String()
	}

	for _, pe := range m.plans.executions {
		barWidth := 20
		filled := 0
		if pe.TotalSteps > 0 {
			filled = (pe.CompletedSteps * barWidth) / pe.TotalSteps
		}
		if filled > barWidth {
			filled = barWidth
		}
		empty := barWidth - filled
		bar := "[" + strings.Repeat("#", filled) + strings.Repeat(".", empty) + "]"

		duration := time.Since(pe.StartedAt).Truncate(time.Second)

		statusLabel := pe.Status
		switch pe.Status {
		case "running":
			statusLabel = "RUNNING"
		case "succeeded":
			statusLabel = "OK"
		case "failed":
			statusLabel = "FAILED"
		}

		b.WriteString(fmt.Sprintf("  %s\n", pe.PlanName))
		b.WriteString(fmt.Sprintf("    %s %d/%d steps  %s  %s\n",
			bar, pe.CompletedSteps, pe.TotalSteps, statusLabel, duration))
		b.WriteString(fmt.Sprintf("    ID: %s\n\n", pe.ExecutionID))
	}

	return b.String()
}
