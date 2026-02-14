package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/policy"
)

// PerplexityProvider implements SearchProvider using the Perplexity Sonar API.
type PerplexityProvider struct {
	apiKey string
}

// NewPerplexityProvider creates a Perplexity search provider.
func NewPerplexityProvider(apiKey string) *PerplexityProvider {
	return &PerplexityProvider{apiKey: apiKey}
}

func (p *PerplexityProvider) Name() string { return "perplexity_search" }
func (p *PerplexityProvider) Description() string {
	return "Perplexity Sonar — AI-powered search with citations"
}
func (p *PerplexityProvider) Available() bool { return p.apiKey != "" }

func (p *PerplexityProvider) Domains() []string {
	return []string{"api.perplexity.ai"}
}

func (p *PerplexityProvider) APIKeyReqs() []APIKeyReq {
	return []APIKeyReq{
		{
			ConfigKey:   "perplexity_search",
			EnvVar:      "PERPLEXITY_API_KEY",
			Description: "Perplexity API key",
			SignupURL:   "https://www.perplexity.ai/settings/api",
		},
	}
}

// perplexityRequest is the request body for the Perplexity chat/completions API.
type perplexityRequest struct {
	Model    string              `json:"model"`
	Messages []perplexityMessage `json:"messages"`
}

type perplexityMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// perplexityResponse matches the relevant fields of the Perplexity API response.
type perplexityResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Citations []string `json:"citations"`
}

func (p *PerplexityProvider) Search(ctx context.Context, query string, pol policy.Checker) ([]SearchResult, error) {
	apiURL := "https://api.perplexity.ai/chat/completions"
	if !pol.AllowHTTPURL(apiURL) {
		audit.Record("deny", "tools.web_search", "url_denied", pol.PolicyVersion(), apiURL)
		return nil, fmt.Errorf("policy denied search URL %q", apiURL)
	}
	audit.Record("allow", "tools.web_search", "url_allowed", pol.PolicyVersion(), apiURL)

	reqBody := perplexityRequest{
		Model: "sonar",
		Messages: []perplexityMessage{
			{Role: "system", Content: "Be precise and concise. Provide factual search results."},
			{Role: "user", Content: query},
		},
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal perplexity request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("perplexity API returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	return parsePerplexityResponse(body)
}

// parsePerplexityResponse extracts search results from a Perplexity API response.
func parsePerplexityResponse(data []byte) ([]SearchResult, error) {
	var resp perplexityResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse perplexity response: %w", err)
	}

	content := ""
	if len(resp.Choices) > 0 {
		content = resp.Choices[0].Message.Content
	}

	var results []SearchResult

	// Build results from citations with content snippets.
	for i, citation := range resp.Citations {
		if i >= 5 {
			break
		}
		snippet := ""
		if i == 0 {
			snippet = trimSnippet(content, 500)
		}
		results = append(results, SearchResult{
			Title:   citationTitle(citation),
			URL:     citation,
			Snippet: snippet,
		})
	}

	// If no citations but we have content, return it as a single result.
	if len(results) == 0 && content != "" {
		results = append(results, SearchResult{
			Title:   "Perplexity Search Result",
			URL:     "",
			Snippet: content,
		})
	}

	return results, nil
}

// citationTitle extracts a display title from a citation URL.
func citationTitle(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		return u.Host
	}
	parts := strings.Split(path, "/")
	last := parts[len(parts)-1]
	if last == "" {
		return u.Host
	}
	return strings.ReplaceAll(last, "-", " ") + " — " + u.Host
}

// trimSnippet returns s truncated to max characters with an ellipsis, or empty if s is empty.
func trimSnippet(s string, max int) string {
	if s == "" || len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
