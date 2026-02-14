package tools

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/policy"
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
)

type SearchInput struct {
	Query string `json:"query"`
}

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

type SearchOutput struct {
	Results  []SearchResult `json:"results"`
	Provider string         `json:"provider,omitempty"`
}

func registerSearch(g *genkit.Genkit, reg *Registry) ai.Tool {
	return genkit.DefineTool(g, "web_search",
		"Search the web for current information. Returns results with titles, URLs, and snippets. Use this tool immediately when the user asks to search or look something up — do not ask for confirmation.",
		func(ctx *ai.ToolContext, input SearchInput) (SearchOutput, error) {
			return reg.Search(ctx, input.Query)
		},
	)
}

// search routes a query through the ordered list of providers. It checks the
// tools.web_search capability once, then iterates providers: skip unavailable,
// try search, fall through on error. First success wins.
func search(ctx context.Context, query string, pol policy.Checker, providers []SearchProvider) (SearchOutput, error) {
	if query == "" {
		return SearchOutput{}, fmt.Errorf("empty search query")
	}
	if pol == nil || !pol.AllowCapability("tools.web_search") {
		pv := ""
		if pol != nil {
			pv = pol.PolicyVersion()
		}
		audit.Record("deny", "tools.web_search", "missing_capability", pv, "web_search")
		return SearchOutput{}, fmt.Errorf("policy denied capability %q", "tools.web_search")
	}
	audit.Record("allow", "tools.web_search", "capability_granted", pol.PolicyVersion(), "web_search")

	slog.Info("web_search tool called", "query", query)

	for _, p := range providers {
		if !p.Available() {
			continue
		}
		results, err := p.Search(ctx, query, pol)
		if err != nil {
			slog.Warn("search provider failed, trying next", "provider", p.Name(), "error", err)
			continue
		}
		if len(results) == 0 {
			return SearchOutput{Provider: p.Name(), Results: []SearchResult{{
				Title:   "No results found",
				Snippet: fmt.Sprintf("No results found for %q. Please answer using your training data.", query),
			}}}, nil
		}
		return SearchOutput{Provider: p.Name(), Results: results}, nil
	}

	return SearchOutput{Results: []SearchResult{{
		Title:   "Search unavailable",
		Snippet: fmt.Sprintf("Could not search for %q. Configure a search provider in config.yaml api_keys section or set BRAVE_API_KEY / PERPLEXITY_API_KEY env var.", query),
	}}}, nil
}
