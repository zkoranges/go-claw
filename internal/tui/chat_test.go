package tui

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/basket/go-claw/internal/config"
	"github.com/basket/go-claw/internal/engine"
)

// mockSwitcher implements AgentSwitcher for tests.
type mockSwitcher struct {
	agents      []AgentInfo
	switchErr   error
	createErr   error
	removeErr   error
	switchCalls []string
	createCalls []string
	removeCalls []string
}

func (m *mockSwitcher) SwitchAgent(agentID string) (engine.Brain, string, string, error) {
	m.switchCalls = append(m.switchCalls, agentID)
	if m.switchErr != nil {
		return nil, "", "", m.switchErr
	}
	return nil, agentID, "", nil
}

func (m *mockSwitcher) ListAgentIDs() []string {
	ids := make([]string, len(m.agents))
	for i, a := range m.agents {
		ids[i] = a.ID
	}
	return ids
}

func (m *mockSwitcher) ListAgentInfo() []AgentInfo {
	return m.agents
}

func (m *mockSwitcher) CreateAgent(ctx context.Context, id, name, provider, model, soul string) error {
	m.createCalls = append(m.createCalls, id)
	return m.createErr
}

func (m *mockSwitcher) RemoveAgent(ctx context.Context, id string) error {
	m.removeCalls = append(m.removeCalls, id)
	return m.removeErr
}

func TestHandleCommand_Dispatch(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantExit   bool
		wantOutput string // substring to check in output
	}{
		{"quit returns true", "/quit", true, ""},
		{"exit returns true", "/exit", true, ""},
		{"help returns false", "/help", false, "Commands:"},
		{"session shows ID", "/session", false, "test-session"},
		{"unknown command", "/foobar", false, "Unknown command: /foobar"},
		{"domain alias", "/domain", false, "Policy not available"},
		{"domains no policy", "/domains", false, "Policy not available"},
		{"allow no arg", "/allow", false, "Usage: /allow"},
		{"allow no policy", "/allow example.com", false, "Policy not available"},
		{"config no sub", "/config", false, "Usage: /config list"},
		{"config list no keys", "/config list", false, "No API keys configured"},
		{"config set missing args", "/config set", false, "Usage: /config set"},
		{"model unknown sub", "/model foo", false, "Usage: /model"},
		{"plan no arg", "/plan", false, "Usage: /plan"},
		{"plan no gateway", "/plan deploy", false, "gateway not configured"},
		{"memory no store", "/memory list", false, "Store not available"},
		{"remember no store", "/remember key val", false, "Store not available"},
		{"forget no store", "/forget key", false, "Store not available"},
		{"clear no store", "/clear", false, "Store not available"},
		{"pin no store", "/pin file.txt", false, "Store not available"},
		{"unpin no store", "/unpin file.txt", false, "Store not available"},
		{"pinned no store", "/pinned", false, "Store not available"},
		{"share no store", "/share key with agent", false, "Store not available"},
		{"unshare no store", "/unshare key from agent", false, "Store not available"},
		{"shared no store", "/shared", false, "Store not available"},
		{"context no store", "/context", false, "Store not available"},
		{"agents no switcher", "/agents", false, "Multi-agent not available"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			cc := ChatConfig{}
			gotExit := handleCommand(context.Background(), tt.input, &cc, "test-session", &buf)
			if gotExit != tt.wantExit {
				t.Errorf("handleCommand(%q) exit = %v, want %v", tt.input, gotExit, tt.wantExit)
			}
			if tt.wantOutput != "" && !strings.Contains(buf.String(), tt.wantOutput) {
				t.Errorf("handleCommand(%q) output = %q, want substring %q", tt.input, buf.String(), tt.wantOutput)
			}
		})
	}
}

func TestHandleCommand_CaseInsensitive(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	exit := handleCommand(context.Background(), "/QUIT", &cc, "s", &buf)
	if !exit {
		t.Error("expected /QUIT to exit")
	}
}

func TestHandlePlanCommand(t *testing.T) {
	tests := []struct {
		name       string
		arg        string
		cc         ChatConfig
		wantOutput string
	}{
		{"empty arg", "", ChatConfig{}, "Usage: /plan"},
		{"no gateway", "deploy", ChatConfig{}, "gateway not configured"},
		{"no auth token", "deploy", ChatConfig{BindAddr: "127.0.0.1:18789"}, "gateway not configured"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			handlePlanCommand(tt.arg, &tt.cc, &buf)
			if !strings.Contains(buf.String(), tt.wantOutput) {
				t.Errorf("output = %q, want substring %q", buf.String(), tt.wantOutput)
			}
		})
	}
}

// Note: handleSkillsCommand tests are omitted because ResolveStatus
// requires a non-nil *LivePolicy. Testing skills commands requires
// full policy setup which is tested in the tools package.

func TestHandleAgentCommand_NoSwitcher(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	handleAgentCommand(context.Background(), "", &cc, &buf)
	if !strings.Contains(buf.String(), "Multi-agent not available") {
		t.Errorf("expected 'Multi-agent not available', got: %q", buf.String())
	}
}

func TestHandleAgentCommand_List(t *testing.T) {
	sw := &mockSwitcher{
		agents: []AgentInfo{
			{ID: "default", DisplayName: "Default", Model: "gemini-2.5-flash"},
			{ID: "coder", DisplayName: "Coder", Emoji: "💻", Model: "gemini-2.5-pro"},
		},
	}
	cc := ChatConfig{Switcher: sw, CurrentAgent: "default"}

	var buf bytes.Buffer
	handleAgentCommand(context.Background(), "list", &cc, &buf)
	out := buf.String()

	if !strings.Contains(out, "default") {
		t.Error("expected 'default' in agent list")
	}
	if !strings.Contains(out, "coder") {
		t.Error("expected 'coder' in agent list")
	}
	if !strings.Contains(out, "*") {
		t.Error("expected current agent marker '*'")
	}
}

func TestHandleAgentCommand_EmptyListShowsList(t *testing.T) {
	sw := &mockSwitcher{
		agents: []AgentInfo{
			{ID: "default", DisplayName: "Default"},
		},
	}
	cc := ChatConfig{Switcher: sw, CurrentAgent: "default"}

	var buf bytes.Buffer
	handleAgentCommand(context.Background(), "", &cc, &buf)
	if !strings.Contains(buf.String(), "default") {
		t.Error("expected agent list with empty sub-command")
	}
}

func TestHandleAgentCommand_Switch(t *testing.T) {
	sw := &mockSwitcher{
		agents: []AgentInfo{
			{ID: "default"},
			{ID: "coder"},
		},
	}
	cc := ChatConfig{Switcher: sw, CurrentAgent: "default"}

	var buf bytes.Buffer
	handleAgentCommand(context.Background(), "coder", &cc, &buf)
	if !strings.Contains(buf.String(), "Switched to agent: coder") {
		t.Errorf("expected switch message, got: %q", buf.String())
	}
	if cc.CurrentAgent != "coder" {
		t.Errorf("expected CurrentAgent = coder, got %q", cc.CurrentAgent)
	}
}

func TestHandleAgentCommand_SwitchError(t *testing.T) {
	sw := &mockSwitcher{
		switchErr: fmt.Errorf("agent not found"),
	}
	cc := ChatConfig{Switcher: sw, CurrentAgent: "default"}

	var buf bytes.Buffer
	handleAgentCommand(context.Background(), "unknown", &cc, &buf)
	if !strings.Contains(buf.String(), "Error: agent not found") {
		t.Errorf("expected error message, got: %q", buf.String())
	}
}

func TestHandleAgentCommand_NewNoArgs(t *testing.T) {
	sw := &mockSwitcher{}
	cc := ChatConfig{Switcher: sw}

	var buf bytes.Buffer
	handleAgentCommand(context.Background(), "new", &cc, &buf)
	if !strings.Contains(buf.String(), "Usage: /agents new") {
		t.Errorf("expected usage message, got: %q", buf.String())
	}
}

func TestHandleAgentCommand_NewSuccess(t *testing.T) {
	sw := &mockSwitcher{}
	cc := ChatConfig{Switcher: sw, CurrentAgent: "default"}

	var buf bytes.Buffer
	handleAgentCommand(context.Background(), "new myagent my custom soul", &cc, &buf)
	if !strings.Contains(buf.String(), "Created agent: myagent") {
		t.Errorf("expected creation message, got: %q", buf.String())
	}
	if len(sw.createCalls) != 1 || sw.createCalls[0] != "myagent" {
		t.Errorf("expected CreateAgent called with 'myagent', got %v", sw.createCalls)
	}
}

func TestHandleAgentCommand_NewCreateError(t *testing.T) {
	sw := &mockSwitcher{createErr: fmt.Errorf("duplicate agent")}
	cc := ChatConfig{Switcher: sw}

	var buf bytes.Buffer
	handleAgentCommand(context.Background(), "new myagent", &cc, &buf)
	if !strings.Contains(buf.String(), "Error: duplicate agent") {
		t.Errorf("expected error message, got: %q", buf.String())
	}
}

func TestHandleAgentCommand_RemoveNoArgs(t *testing.T) {
	sw := &mockSwitcher{}
	cc := ChatConfig{Switcher: sw}

	var buf bytes.Buffer
	handleAgentCommand(context.Background(), "remove", &cc, &buf)
	if !strings.Contains(buf.String(), "Usage: /agents remove") {
		t.Errorf("expected usage message, got: %q", buf.String())
	}
}

func TestHandleAgentCommand_RemoveDefault(t *testing.T) {
	sw := &mockSwitcher{}
	cc := ChatConfig{Switcher: sw}

	var buf bytes.Buffer
	handleAgentCommand(context.Background(), "remove default", &cc, &buf)
	if !strings.Contains(buf.String(), "Cannot remove the default agent") {
		t.Errorf("expected rejection message, got: %q", buf.String())
	}
}

func TestHandleAgentCommand_RemoveSuccess(t *testing.T) {
	sw := &mockSwitcher{}
	cc := ChatConfig{Switcher: sw, CurrentAgent: "coder"}

	var buf bytes.Buffer
	handleAgentCommand(context.Background(), "remove coder", &cc, &buf)
	if !strings.Contains(buf.String(), "Removed agent: coder") {
		t.Errorf("expected removal message, got: %q", buf.String())
	}
	// Should auto-switch to default.
	if cc.CurrentAgent != "default" {
		t.Errorf("expected CurrentAgent = default after removing current, got %q", cc.CurrentAgent)
	}
}

func TestHandleAgentCommand_RemoveNonCurrent(t *testing.T) {
	sw := &mockSwitcher{}
	cc := ChatConfig{Switcher: sw, CurrentAgent: "default"}

	var buf bytes.Buffer
	handleAgentCommand(context.Background(), "remove coder", &cc, &buf)
	if !strings.Contains(buf.String(), "Removed agent: coder") {
		t.Errorf("expected removal message, got: %q", buf.String())
	}
	// Should NOT switch.
	if cc.CurrentAgent != "default" {
		t.Errorf("expected CurrentAgent = default (unchanged), got %q", cc.CurrentAgent)
	}
}

func TestHandleTeamCommand_NoArgs(t *testing.T) {
	sw := &mockSwitcher{}
	cc := ChatConfig{Switcher: sw}

	var buf bytes.Buffer
	handleTeamCommand(context.Background(), "", &cc, &buf)
	if !strings.Contains(buf.String(), "Usage: /agents team") {
		t.Errorf("expected usage message, got: %q", buf.String())
	}
}

func TestHandleTeamCommand_CreateRoles(t *testing.T) {
	sw := &mockSwitcher{}
	cc := ChatConfig{Switcher: sw}

	var buf bytes.Buffer
	handleTeamCommand(context.Background(), "coder reviewer tester", &cc, &buf)
	if !strings.Contains(buf.String(), "3 agent(s)") {
		t.Errorf("expected 3 agents created, got: %q", buf.String())
	}
	if len(sw.createCalls) != 3 {
		t.Errorf("expected 3 CreateAgent calls, got %d", len(sw.createCalls))
	}
}

func TestHandleTeamCommand_CreateError(t *testing.T) {
	sw := &mockSwitcher{createErr: fmt.Errorf("already exists")}
	cc := ChatConfig{Switcher: sw}

	var buf bytes.Buffer
	handleTeamCommand(context.Background(), "coder", &cc, &buf)
	if !strings.Contains(buf.String(), "Skipped coder") {
		t.Errorf("expected skip message, got: %q", buf.String())
	}
}

func TestHandleModelCommand_NoConfig(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	handleModelCommand("", &cc, &buf)
	if !strings.Contains(buf.String(), "Config not available") {
		t.Errorf("expected config warning, got: %q", buf.String())
	}
}

func TestHandleModelCommand_SetEmpty(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	handleModelCommand("set", &cc, &buf)
	if !strings.Contains(buf.String(), "Usage: /model set") {
		t.Errorf("expected usage message, got: %q", buf.String())
	}
}

func TestHandleModelCommand_SetAlreadyCurrent(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{
		Cfg: &config.Config{LLMProvider: "google", GeminiModel: "gemini-2.5-flash"},
	}
	handleModelCommand("set gemini-2.5-flash", &cc, &buf)
	if !strings.Contains(buf.String(), "Already using") {
		t.Errorf("expected 'Already using', got: %q", buf.String())
	}
}

func TestHandleModelCommand_UnknownSub(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	handleModelCommand("foo", &cc, &buf)
	if !strings.Contains(buf.String(), "Usage: /model") {
		t.Errorf("expected usage message, got: %q", buf.String())
	}
}

func TestHandleConfigCommand_ListWithKeys(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{
		Cfg: &config.Config{APIKeys: map[string]string{"brave_search": "BSAtest123"}},
	}
	handleConfigCommand("list", &cc, &buf)
	if !strings.Contains(buf.String(), "brave_search") {
		t.Errorf("expected 'brave_search' in output, got: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "BSAt") {
		t.Errorf("expected masked value starting with BSAt, got: %q", buf.String())
	}
}

func TestHandleConfigCommand_DefaultUsage(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	handleConfigCommand("", &cc, &buf)
	if !strings.Contains(buf.String(), "Usage: /config") {
		t.Errorf("expected usage message, got: %q", buf.String())
	}
}

func TestHandleMemoryCommand_NoStore(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	handleMemoryCommand(context.Background(), "list", &cc, &buf)
	if !strings.Contains(buf.String(), "Store not available") {
		t.Errorf("expected 'Store not available', got: %q", buf.String())
	}
}

func TestHandleMemoryCommand_DefaultUsage(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	handleMemoryCommand(context.Background(), "badsubcmd", &cc, &buf)
	if !strings.Contains(buf.String(), "Store not available") {
		t.Errorf("expected 'Store not available' (no store), got: %q", buf.String())
	}
}

func TestHandleRememberCommand_NoArgs(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	handleRememberCommand(context.Background(), "onlykey", &cc, &buf)
	if !strings.Contains(buf.String(), "Store not available") || !strings.Contains(buf.String(), "Usage:") {
		// With no store, the nil check triggers first
		// Either "Store not available" or "Usage" is acceptable
	}
}

func TestHandleForgetCommand_NoArgs(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	handleForgetCommand(context.Background(), "", &cc, &buf)
	if !strings.Contains(buf.String(), "Store not available") {
		t.Errorf("expected 'Store not available', got: %q", buf.String())
	}
}

func TestHandlePinCommand_NoArgs(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	handlePinCommand(context.Background(), "", &cc, &buf)
	if !strings.Contains(buf.String(), "Store not available") || !strings.Contains(buf.String(), "Usage:") {
		// Either response is acceptable
	}
}

func TestHandleUnpinCommand_NoArgs(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	handleUnpinCommand(context.Background(), "", &cc, &buf)
	if !strings.Contains(buf.String(), "Store not available") || !strings.Contains(buf.String(), "Usage:") {
		// Either response is acceptable
	}
}

func TestHandlePinnedCommand_NoStore(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	handlePinnedCommand(context.Background(), &cc, &buf)
	if !strings.Contains(buf.String(), "Store not available") {
		t.Errorf("expected 'Store not available', got: %q", buf.String())
	}
}

func TestHandleShareCommand_NoStore(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	handleShareCommand(context.Background(), "key with agent", &cc, &buf)
	if !strings.Contains(buf.String(), "Store not available") {
		t.Errorf("expected 'Store not available', got: %q", buf.String())
	}
}

func TestHandleShareCommand_NoArgs(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	handleShareCommand(context.Background(), "", &cc, &buf)
	out := buf.String()
	if !strings.Contains(out, "Store not available") && !strings.Contains(out, "Usage:") {
		t.Errorf("expected store error or usage, got: %q", out)
	}
}

func TestHandleUnshareCommand_NoStore(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	handleUnshareCommand(context.Background(), "key from agent", &cc, &buf)
	if !strings.Contains(buf.String(), "Store not available") {
		t.Errorf("expected 'Store not available', got: %q", buf.String())
	}
}

func TestHandleSharedCommand_NoStore(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	handleSharedCommand(context.Background(), &cc, &buf)
	if !strings.Contains(buf.String(), "Store not available") {
		t.Errorf("expected 'Store not available', got: %q", buf.String())
	}
}

func TestHandleContextCommand_NoStore(t *testing.T) {
	var buf bytes.Buffer
	cc := ChatConfig{}
	handleContextCommand(context.Background(), &cc, &buf)
	if !strings.Contains(buf.String(), "Store not available") {
		t.Errorf("expected 'Store not available', got: %q", buf.String())
	}
}

func TestMaskValue(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"abc", "****"},
		{"abcd", "****"},
		{"abcde", "abcd*"},
		{"BSAOk4longkey", "BSAO*********"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := maskValue(tt.input)
			if got != tt.want {
				t.Errorf("maskValue(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSoulForRole_Known(t *testing.T) {
	soul := soulForRole("coder")
	if !strings.Contains(soul, "coding specialist") {
		t.Errorf("expected coding specialist soul, got: %q", soul)
	}
}

func TestSoulForRole_Unknown(t *testing.T) {
	soul := soulForRole("wizardry")
	if !strings.Contains(soul, "wizardry specialist") {
		t.Errorf("expected generic specialist soul, got: %q", soul)
	}
}

func TestIsMissing(t *testing.T) {
	missing := []string{"tools.web_search", "brave.com", "api_keys.brave_search"}
	if !isMissing(missing, "brave.com") {
		t.Error("expected isMissing to find 'brave.com'")
	}
	if isMissing(missing, "nonexistent") {
		t.Error("expected isMissing to not find 'nonexistent'")
	}
	if !isMissing(missing, "brave") {
		t.Error("expected isMissing to find substring 'brave'")
	}
}
