package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/policy"
)

// BraveProvider implements SearchProvider using the Brave Search API.
type BraveProvider struct {
	apiKey string
}

// NewBraveProvider creates a Brave search provider.
func NewBraveProvider(apiKey string) *BraveProvider {
	return &BraveProvider{apiKey: apiKey}
}

func (b *BraveProvider) Name() string { return "brave_search" }
func (b *BraveProvider) Description() string {
	return "Brave Search — fast, privacy-focused web search"
}
func (b *BraveProvider) Available() bool { return b.apiKey != "" }

func (b *BraveProvider) Domains() []string {
	return []string{"api.search.brave.com"}
}

func (b *BraveProvider) APIKeyReqs() []APIKeyReq {
	return []APIKeyReq{
		{
			ConfigKey:   "brave_search",
			EnvVar:      "BRAVE_API_KEY",
			Description: "Brave Search API key",
			SignupURL:   "https://brave.com/search/api/",
		},
	}
}

func (b *BraveProvider) Search(ctx context.Context, query string, pol policy.Checker) ([]SearchResult, error) {
	braveURL := "https://api.search.brave.com/res/v1/web/search?q=" + url.QueryEscape(query) + "&count=5"
	if !pol.AllowHTTPURL(braveURL) {
		audit.Record("deny", "tools.web_search", "url_denied", pol.PolicyVersion(), braveURL)
		return nil, fmt.Errorf("policy denied search URL %q", braveURL)
	}
	audit.Record("allow", "tools.web_search", "url_allowed", pol.PolicyVersion(), braveURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, braveURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", b.apiKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("brave API returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	return parseBraveJSON(body)
}

// braveResponse matches the relevant fields of the Brave Search API response.
type braveResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

// parseBraveJSON extracts search results from a Brave API JSON response.
func parseBraveJSON(data []byte) ([]SearchResult, error) {
	var resp braveResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse brave response: %w", err)
	}
	var results []SearchResult
	for _, r := range resp.Web.Results {
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Description,
		})
		if len(results) >= 5 {
			break
		}
	}
	return results, nil
}
