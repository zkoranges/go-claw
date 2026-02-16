package gateway

import "testing"

// A2A Protocol Tests (Phase 4)

func TestA2A_AgentCardRoute(t *testing.T) {
	t.Skip("/.well-known/agent.json endpoint in Phase 4")
}

func TestA2A_ValidJSON(t *testing.T) {
	t.Skip("valid A2A agent card JSON validation in Phase 4")
}

func TestA2A_ContentType(t *testing.T) {
	t.Skip("application/json content-type in Phase 4")
}

func TestA2A_MethodNotAllowed(t *testing.T) {
	t.Skip("POST/PUT/DELETE return 405 in Phase 4")
}

func TestA2A_DisabledReturns404(t *testing.T) {
	t.Skip("disabled endpoint returns 404 in Phase 4")
}

func TestA2A_AgentsListed(t *testing.T) {
	t.Skip("agents listed as skills in Phase 4")
}
