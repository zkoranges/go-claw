package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/policy"
)

// DDGProvider implements SearchProvider using DuckDuckGo HTML search.
type DDGProvider struct{}

// NewDDGProvider creates a DuckDuckGo search provider.
func NewDDGProvider() *DDGProvider {
	return &DDGProvider{}
}

func (d *DDGProvider) Name() string            { return "duckduckgo" }
func (d *DDGProvider) Description() string     { return "DuckDuckGo — free, no API key required" }
func (d *DDGProvider) Available() bool         { return true }
func (d *DDGProvider) APIKeyReqs() []APIKeyReq { return nil }

func (d *DDGProvider) Domains() []string {
	return []string{"html.duckduckgo.com"}
}

func (d *DDGProvider) Search(ctx context.Context, query string, pol policy.Checker) ([]SearchResult, error) {
	ddgURL := searchEndpoint(query)
	if !pol.AllowHTTPURL(ddgURL) {
		audit.Record("deny", "tools.web_search", "url_denied", pol.PolicyVersion(), ddgURL)
		return nil, fmt.Errorf("policy denied search URL %q", ddgURL)
	}
	audit.Record("allow", "tools.web_search", "url_allowed", pol.PolicyVersion(), ddgURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ddgURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "GoClaw/1.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	return parseHTMLResults(string(body)), nil
}

func searchEndpoint(query string) string {
	if endpoint := os.Getenv("GOCLAW_SEARCH_ENDPOINT"); endpoint != "" {
		u, err := url.Parse(endpoint)
		if err == nil {
			q := u.Query()
			q.Set("q", query)
			u.RawQuery = q.Encode()
			return u.String()
		}
	}
	return "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
}

// reResultBlock matches each <div class="result...">...</div> or the
// <a class="result__a" ...> + <a class="result__snippet" ...> pattern
// in the DuckDuckGo HTML response.
var (
	reResultLink    = regexp.MustCompile(`(?i)<a[^>]+class="result__a"[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	reResultSnippet = regexp.MustCompile(`(?i)<a[^>]+class="result__snippet"[^>]*>(.*?)</a>`)
	reTag           = regexp.MustCompile(`<[^>]+>`)
)

func parseHTMLResults(html string) []SearchResult {
	links := reResultLink.FindAllStringSubmatch(html, 10)
	snippets := reResultSnippet.FindAllStringSubmatch(html, 10)

	var results []SearchResult
	for i, link := range links {
		if len(link) < 3 {
			continue
		}
		rawURL := link[1]
		title := stripTags(link[2])

		// DuckDuckGo wraps URLs in a redirect; extract the actual URL.
		if u, err := url.Parse(rawURL); err == nil {
			if actual := u.Query().Get("uddg"); actual != "" {
				rawURL = actual
			}
		}

		snippet := ""
		if i < len(snippets) && len(snippets[i]) >= 2 {
			snippet = stripTags(snippets[i][1])
		}

		results = append(results, SearchResult{
			Title:   strings.TrimSpace(title),
			URL:     rawURL,
			Snippet: strings.TrimSpace(snippet),
		})

		if len(results) >= 5 {
			break
		}
	}
	return results
}

func stripTags(s string) string {
	return strings.TrimSpace(reTag.ReplaceAllString(s, ""))
}
