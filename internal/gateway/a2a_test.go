package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/basket/go-claw/internal/agent"
	"github.com/basket/go-claw/internal/config"
	"github.com/basket/go-claw/internal/persistence"
)

// newTestRegistry creates a test registry with a temporary store.
func newTestRegistry(t *testing.T) *agent.Registry {
	t.Helper()
	store, err := persistence.Open(":memory:", nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return agent.NewRegistry(store, nil, nil, nil, nil)
}

// TestA2A_HandleAgentCard_ValidJSON tests that GET returns valid JSON with required fields.
func TestA2A_HandleAgentCard_ValidJSON(t *testing.T) {
	reg := newTestRegistry(t)
	cfg := Config{
		Registry: reg,
	}
	srv := New(cfg)

	req := httptest.NewRequest("GET", "/.well-known/agent.json", nil)
	w := httptest.NewRecorder()
	srv.handleAgentCard(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", contentType)
	}

	var card AgentCard
	if err := json.Unmarshal(w.Body.Bytes(), &card); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if card.Name != "GoClaw" {
		t.Errorf("expected Name 'GoClaw', got %q", card.Name)
	}
	if card.Version == "" {
		t.Error("expected Version to be non-empty")
	}
	if card.URL == "" {
		t.Error("expected URL to be non-empty")
	}
	if card.Capabilities == (Capabilities{}) {
		t.Error("expected Capabilities to be populated")
	}
	if len(card.DefaultInputModes) == 0 {
		t.Error("expected DefaultInputModes to be non-empty")
	}
	if len(card.DefaultOutputModes) == 0 {
		t.Error("expected DefaultOutputModes to be non-empty")
	}
}

// TestA2A_HandleAgentCard_AgentsListedAsSkills tests that agents appear as skills.
func TestA2A_HandleAgentCard_AgentsListedAsSkills(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	// Add test agents
	reg.CreateAgent(ctx, agent.AgentConfig{
		AgentID:     "test-coder",
		DisplayName: "Test Coder",
		SkillsFilter: []string{"code", "test"},
	})
	reg.CreateAgent(ctx, agent.AgentConfig{
		AgentID:     "test-researcher",
		DisplayName: "Test Researcher",
		SkillsFilter: []string{"search", "analyze"},
	})

	cfg := Config{
		Registry: reg,
	}
	srv := New(cfg)

	req := httptest.NewRequest("GET", "/.well-known/agent.json", nil)
	w := httptest.NewRecorder()
	srv.handleAgentCard(w, req)

	var card AgentCard
	json.Unmarshal(w.Body.Bytes(), &card)

	if len(card.Skills) != 2 {
		t.Errorf("expected 2 skills, got %d", len(card.Skills))
	}

	// Check agents (order is not guaranteed, so check both are present)
	skillsMap := make(map[string]A2ASkill)
	for _, s := range card.Skills {
		skillsMap[s.ID] = s
	}

	coder, hasCoder := skillsMap["test-coder"]
	if !hasCoder || coder.Name != "Test Coder" {
		t.Errorf("expected test-coder agent, got %+v", coder)
	}

	researcher, hasResearcher := skillsMap["test-researcher"]
	if !hasResearcher || researcher.Name != "Test Researcher" {
		t.Errorf("expected test-researcher agent, got %+v", researcher)
	}

	// Verify tags match SkillsFilter
	if len(coder.Tags) != 2 {
		t.Errorf("expected 2 tags for coder, got %v", coder.Tags)
	}
	if len(researcher.Tags) != 2 {
		t.Errorf("expected 2 tags for researcher, got %v", researcher.Tags)
	}
}

// TestA2A_HandleAgentCard_PostMethodNotAllowed tests that POST returns 405.
func TestA2A_HandleAgentCard_PostMethodNotAllowed(t *testing.T) {
	reg := newTestRegistry(t)
	cfg := Config{
		Registry: reg,
	}
	srv := New(cfg)

	req := httptest.NewRequest("POST", "/.well-known/agent.json", nil)
	w := httptest.NewRecorder()
	srv.handleAgentCard(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

// TestA2A_HandleAgentCard_DisabledReturns404 tests that disabled returns 404.
func TestA2A_HandleAgentCard_DisabledReturns404(t *testing.T) {
	reg := newTestRegistry(t)
	cfg := Config{
		Registry: reg,
	}
	srv := New(cfg)
	srv.cfg.Cfg = &config.Config{}
	disabled := false
	srv.cfg.Cfg.A2A.Enabled = &disabled

	req := httptest.NewRequest("GET", "/.well-known/agent.json", nil)
	w := httptest.NewRecorder()
	srv.handleAgentCard(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

// TestA2A_HandleAgentCard_ContentTypeHeader tests that Content-Type is set correctly.
func TestA2A_HandleAgentCard_ContentTypeHeader(t *testing.T) {
	reg := newTestRegistry(t)
	cfg := Config{
		Registry: reg,
	}
	srv := New(cfg)

	req := httptest.NewRequest("GET", "/.well-known/agent.json", nil)
	w := httptest.NewRecorder()
	srv.handleAgentCard(w, req)

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", contentType)
	}

	cacheControl := w.Header().Get("Cache-Control")
	if cacheControl != "public, max-age=300" {
		t.Errorf("expected Cache-Control public, max-age=300, got %q", cacheControl)
	}
}

// TestA2A_HandleAgentCard_EmptyAgentList tests that empty agent list is handled gracefully.
func TestA2A_HandleAgentCard_EmptyAgentList(t *testing.T) {
	reg := newTestRegistry(t)
	cfg := Config{
		Registry: reg,
	}
	srv := New(cfg)

	req := httptest.NewRequest("GET", "/.well-known/agent.json", nil)
	w := httptest.NewRecorder()
	srv.handleAgentCard(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var card AgentCard
	json.Unmarshal(w.Body.Bytes(), &card)

	if card.Skills == nil || len(card.Skills) != 0 {
		t.Errorf("expected empty skills slice, got %v", card.Skills)
	}
}
