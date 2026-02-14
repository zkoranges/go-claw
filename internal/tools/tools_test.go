package tools

import (
	"strings"
	"testing"
)

func TestBuildProviders_DefaultOrder(t *testing.T) {
	keys := map[string]string{
		"brave_search":      "bk",
		"perplexity_search": "pk",
	}
	providers := buildProviders(keys, "")

	if len(providers) != 3 {
		t.Fatalf("expected 3 providers, got %d", len(providers))
	}
	if providers[0].Name() != "brave_search" {
		t.Errorf("expected first=brave_search, got %q", providers[0].Name())
	}
	if providers[1].Name() != "perplexity_search" {
		t.Errorf("expected second=perplexity_search, got %q", providers[1].Name())
	}
	if providers[2].Name() != "duckduckgo" {
		t.Errorf("expected third=duckduckgo, got %q", providers[2].Name())
	}
}

func TestBuildProviders_PreferPerplexity(t *testing.T) {
	keys := map[string]string{"perplexity_search": "pk"}
	providers := buildProviders(keys, "perplexity_search")

	if providers[0].Name() != "perplexity_search" {
		t.Errorf("expected perplexity_search first, got %q", providers[0].Name())
	}
	if providers[1].Name() != "brave_search" {
		t.Errorf("expected brave_search second, got %q", providers[1].Name())
	}
	if providers[2].Name() != "duckduckgo" {
		t.Errorf("expected duckduckgo third, got %q", providers[2].Name())
	}
}

func TestBuildProviders_PreferDDG(t *testing.T) {
	providers := buildProviders(nil, "duckduckgo")

	if providers[0].Name() != "duckduckgo" {
		t.Errorf("expected duckduckgo first, got %q", providers[0].Name())
	}
	// Other two follow.
	if len(providers) != 3 {
		t.Fatalf("expected 3 providers, got %d", len(providers))
	}
}

func TestBuildProviders_PreferAlreadyFirst(t *testing.T) {
	providers := buildProviders(nil, "brave_search")
	if providers[0].Name() != "brave_search" {
		t.Errorf("expected brave_search first, got %q", providers[0].Name())
	}
}

func TestBuildProviders_UnknownPreference(t *testing.T) {
	providers := buildProviders(nil, "nonexistent")
	// Should fall back to default order.
	if providers[0].Name() != "brave_search" {
		t.Errorf("expected default order, got first=%q", providers[0].Name())
	}
}

func TestNewRegistry_BuildsProviders(t *testing.T) {
	keys := map[string]string{"brave_search": "bk"}
	reg := NewRegistry(nil, keys, "")
	if len(reg.Providers) != 3 {
		t.Fatalf("expected 3 providers, got %d", len(reg.Providers))
	}
	if reg.Providers[0].Name() != "brave_search" {
		t.Errorf("expected brave first, got %q", reg.Providers[0].Name())
	}
}

func TestHtmlToText(t *testing.T) {
	tests := []struct {
		name  string
		html  string
		check func(string) bool
		desc  string
	}{
		{
			name: "strips script tags",
			html: `<p>Hello</p><script>alert("xss")</script><p>World</p>`,
			check: func(s string) bool {
				return strings.Contains(s, "Hello") && strings.Contains(s, "World") && !strings.Contains(s, "alert")
			},
			desc: "should contain Hello+World but not alert",
		},
		{
			name:  "strips style tags",
			html:  `<style>.x{color:red}</style><p>Content</p>`,
			check: func(s string) bool { return strings.Contains(s, "Content") && !strings.Contains(s, "color") },
			desc:  "should contain Content but not color",
		},
		{
			name:  "decodes entities",
			html:  `<p>A &amp; B &lt; C &gt; D &quot;E&quot; F&#39;s</p>`,
			check: func(s string) bool { return strings.Contains(s, `A & B < C > D "E" F's`) },
			desc:  "should decode HTML entities",
		},
		{
			name:  "block tags become newlines",
			html:  `<div>Line1</div><div>Line2</div>`,
			check: func(s string) bool { return strings.Contains(s, "Line1") && strings.Contains(s, "Line2") },
			desc:  "should have both lines",
		},
		{
			name: "strips remaining tags",
			html: `<span class="x">Text</span><a href="url">Link</a>`,
			check: func(s string) bool {
				return strings.Contains(s, "Text") && strings.Contains(s, "Link") && !strings.Contains(s, "<")
			},
			desc: "should have text without any HTML tags",
		},
		{
			name:  "strips comments",
			html:  `<!-- hidden -->Visible`,
			check: func(s string) bool { return strings.Contains(s, "Visible") && !strings.Contains(s, "hidden") },
			desc:  "should strip comments",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := htmlToText(tt.html)
			if !tt.check(got) {
				t.Errorf("%s: %q", tt.desc, got)
			}
		})
	}
}
