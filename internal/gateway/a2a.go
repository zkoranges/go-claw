package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// AgentCard follows the A2A agent card schema.
// Reference: https://google.github.io/A2A/#/documentation?id=agent-card
// NOTE: The A2A spec is evolving. This implements the schema as of early 2025.
type AgentCard struct {
	Name               string       `json:"name"`
	Description        string       `json:"description"`
	URL                string       `json:"url"`
	Version            string       `json:"version"`
	Capabilities       Capabilities `json:"capabilities"`
	DefaultInputModes  []string     `json:"defaultInputModes"`
	DefaultOutputModes []string     `json:"defaultOutputModes"`
	Skills             []A2ASkill   `json:"skills"`
}

// Capabilities describes what the agent can do.
type Capabilities struct {
	Streaming              bool `json:"streaming"`
	PushNotifications      bool `json:"pushNotifications"`
	StateTransitionHistory bool `json:"stateTransitionHistory"`
}

// A2ASkill represents an available skill/agent.
type A2ASkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
}

// handleAgentCard handles GET /.well-known/agent.json requests.
func (s *Server) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if A2A is enabled; default to true if not configured
	a2aEnabled := true
	if s.cfg.Cfg != nil && s.cfg.Cfg.A2A.Enabled != nil {
		a2aEnabled = *s.cfg.Cfg.A2A.Enabled
	}

	if !a2aEnabled {
		http.NotFound(w, r)
		return
	}

	agents := s.cfg.Registry.ListAgents()
	skills := make([]A2ASkill, 0, len(agents))
	for _, a := range agents {
		skills = append(skills, A2ASkill{
			ID:          a.AgentID,
			Name:        a.DisplayName,
			Description: "", // Keep minimal — don't leak soul prompts
			Tags:        a.SkillsFilter,
		})
	}

	card := AgentCard{
		Name:               "GoClaw",
		Description:        "Multi-agent runtime with durable task execution",
		URL:                fmt.Sprintf("http://localhost:%d", 18789), // Default port
		Version:            "v0.4",
		Capabilities:       Capabilities{StateTransitionHistory: true},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Skills:             skills,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	json.NewEncoder(w).Encode(card)
}
