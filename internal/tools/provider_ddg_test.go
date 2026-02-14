package tools

import "testing"

func TestDDGProvider_Metadata(t *testing.T) {
	p := NewDDGProvider()
	if p.Name() != "duckduckgo" {
		t.Errorf("expected name duckduckgo, got %q", p.Name())
	}
	if !p.Available() {
		t.Error("expected Available()=true always")
	}
	if len(p.Domains()) == 0 || p.Domains()[0] != "html.duckduckgo.com" {
		t.Errorf("unexpected domains: %v", p.Domains())
	}
	if p.APIKeyReqs() != nil {
		t.Errorf("expected nil API key reqs, got: %v", p.APIKeyReqs())
	}
}

func TestDDGProvider_ParseHTMLResults(t *testing.T) {
	html := `<a class="result__a" href="https://example.com">Example Title</a>
		<a class="result__snippet">Example snippet text</a>
		<a class="result__a" href="https://other.com">Other Title</a>
		<a class="result__snippet">Other snippet</a>`

	results := parseHTMLResults(html)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Title != "Example Title" {
		t.Errorf("expected title 'Example Title', got %q", results[0].Title)
	}
	if results[0].URL != "https://example.com" {
		t.Errorf("expected url 'https://example.com', got %q", results[0].URL)
	}
	if results[0].Snippet != "Example snippet text" {
		t.Errorf("expected snippet 'Example snippet text', got %q", results[0].Snippet)
	}
}

func TestDDGProvider_ParseHTMLResults_UDDGRedirect(t *testing.T) {
	html := `<a class="result__a" href="/l/?uddg=https%3A%2F%2Freal.com%2Fpage">Title</a>
		<a class="result__snippet">Snippet</a>`

	results := parseHTMLResults(html)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].URL != "https://real.com/page" {
		t.Errorf("expected uddg-extracted URL, got %q", results[0].URL)
	}
}
