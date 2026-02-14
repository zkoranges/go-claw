package tools

import (
	"context"

	"github.com/basket/go-claw/internal/policy"
)

// SearchProvider is the interface every search backend implements.
// Available() checks provider-specific readiness (e.g. API key present).
// Policy checks (capability + domain allowlists) are handled by the router.
type SearchProvider interface {
	Name() string        // e.g. "brave_search", "duckduckgo", "perplexity_search"
	Description() string // Human-readable label
	Domains() []string   // Required domain allowlist entries
	APIKeyReqs() []APIKeyReq
	Available() bool // Has credentials? (policy checked separately by router)
	Search(ctx context.Context, query string, pol policy.Checker) ([]SearchResult, error)
}
