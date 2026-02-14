package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/basket/go-claw/internal/config"
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

type chatMode int

const (
	chatModeChat chatMode = iota
	chatModeModelSelector
	chatModeAgentSelector
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
}

func newChatModel(ctx context.Context, cc ChatConfig, sessionID, agentPrefix, modelName string) chatModel {
	m := chatModel{
		ctx:         ctx,
		cc:          cc,
		sessionID:   sessionID,
		agentPrefix: agentPrefix,
		modelName:   modelName,
		mode:        chatModeChat,
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
	return waitCtxDone(m.ctx)
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
				// /agents (no args) is interactive; embed the agent selector.
				trimmed := strings.TrimSpace(line)
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

			// User message.
			m.history = append(m.history, chatEntry{role: chatRoleUser, text: line})
			if m.cc.Store != nil {
				_ = m.cc.Store.AddHistory(m.ctx, m.sessionID, "user", line, tokenutil.EstimateTokens(line))
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
			_ = m.cc.Store.AddHistory(m.ctx, m.sessionID, "assistant", msg.reply, tokenutil.EstimateTokens(msg.reply))
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
		// Inject agent ID into context so tools know which agent is calling.
		agentCtx := shared.WithAgentID(ctx, cc.CurrentAgent)
		reply, err := cc.Brain.Respond(agentCtx, sessionID, prompt)
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

	// Render history, clipped to window height (best-effort; no expensive wrapping).
	hLines := m.renderHistoryLines()
	available := m.height - 5 // header + instructions + blank + input + status
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

	// Input.
	b.WriteString("\n")
	b.WriteString("> ")
	b.WriteString(renderCursor(string(m.input), m.cursor))
	b.WriteString("\n")
	if m.thinking {
		spin := []string{"|", "/", "-", "\\"}[m.spinnerIdx%4]
		b.WriteString(fmt.Sprintf("%s thinking...\n", spin))
	} else {
		b.WriteString("\n")
	}

	return b.String()
}

func (m chatModel) renderHistoryLines() []string {
	lines := make([]string, 0, len(m.history)*2)
	for _, e := range m.history {
		prefix := ""
		switch e.role {
		case chatRoleUser:
			prefix = "You: "
		case chatRoleAssistant:
			prefix = m.agentPrefix + ": "
		}

		lines = append(lines, m.wrapWithPrefix(e.text, prefix)...)
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
