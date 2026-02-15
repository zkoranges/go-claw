package tools

import (
	"context"

	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
)

// Registry holds all Genkit tool definitions and the policy engine
// used to enforce access control on external calls.
type Registry struct {
	Policy            policy.Checker
	APIKeys           map[string]string
	Tools             []ai.ToolRef
	Providers         []SearchProvider   // Ordered by preference
	ShellExecutor     Executor           // Optional override for shell execution
	Store             *persistence.Store // Optional: enables spawn_task tool
	DelegationMaxHops int                // Max delegation chain depth (default 2)
}

// NewRegistry builds a Registry with providers ordered by preference.
// Default order: Brave → Perplexity → DDG. If preferredSearch matches a
// provider name, that provider is moved to the front.
func NewRegistry(pol policy.Checker, apiKeys map[string]string, preferredSearch string, store ...*persistence.Store) *Registry {
	r := &Registry{
		Policy:            pol,
		APIKeys:           apiKeys,
		DelegationMaxHops: 2, // Default max hops (safe for 3+ workers); override from config if needed
	}
	if len(store) > 0 && store[0] != nil {
		r.Store = store[0]
	}
	r.Providers = buildProviders(apiKeys, preferredSearch)
	return r
}

// buildProviders constructs the provider list with optional preference reordering.
func buildProviders(apiKeys map[string]string, preferredSearch string) []SearchProvider {
	brave := NewBraveProvider(apiKeys["brave_search"])
	perplexity := NewPerplexityProvider(apiKeys["perplexity_search"])
	ddg := NewDDGProvider()

	// Default order: Brave → Perplexity → DDG.
	providers := []SearchProvider{brave, perplexity, ddg}

	if preferredSearch == "" {
		return providers
	}

	// Move preferred provider to the front.
	for i, p := range providers {
		if p.Name() == preferredSearch {
			if i == 0 {
				return providers
			}
			reordered := make([]SearchProvider, 0, len(providers))
			reordered = append(reordered, p)
			reordered = append(reordered, providers[:i]...)
			reordered = append(reordered, providers[i+1:]...)
			return reordered
		}
	}

	return providers
}

// RegisterAll creates and registers all built-in tools with the Genkit instance.
// Returns the registry with tools populated for use in Generate calls.
func (r *Registry) RegisterAll(g *genkit.Genkit) {
	searchTool := registerSearch(g, r)
	readerTool := registerReader(g, r)
	fileTools := registerFileTools(g, r)
	shellTool := registerShell(g, r)
	memTools := registerMemoryTools(g, r)
	shoppingTool := registerShopping(g, r)
	r.Tools = []ai.ToolRef{searchTool, readerTool}
	r.Tools = append(r.Tools, fileTools...)
	r.Tools = append(r.Tools, shellTool)
	r.Tools = append(r.Tools, memTools...)
	r.Tools = append(r.Tools, shoppingTool)
	if r.Store != nil {
		spawnTool := registerSpawn(g, r)
		r.Tools = append(r.Tools, spawnTool)
		delegateTool := registerDelegate(g, r)
		r.Tools = append(r.Tools, delegateTool)
		msgTools := registerMessaging(g, r)
		r.Tools = append(r.Tools, msgTools...)
	}
}

func (r *Registry) Search(ctx context.Context, query string) (SearchOutput, error) {
	return search(ctx, query, r.Policy, r.Providers)
}

func (r *Registry) Read(ctx context.Context, rawURL string) (ReaderOutput, error) {
	return readURL(ctx, rawURL, r.Policy)
}
