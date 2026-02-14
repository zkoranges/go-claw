package tools

import (
	"context"
	"testing"
)

func TestBraveProvider_Metadata(t *testing.T) {
	p := NewBraveProvider("test-key")
	if p.Name() != "brave_search" {
		t.Errorf("expected name brave_search, got %q", p.Name())
	}
	if !p.Available() {
		t.Error("expected Available()=true with API key")
	}
	if len(p.Domains()) == 0 || p.Domains()[0] != "api.search.brave.com" {
		t.Errorf("unexpected domains: %v", p.Domains())
	}
	if len(p.APIKeyReqs()) != 1 || p.APIKeyReqs()[0].ConfigKey != "brave_search" {
		t.Errorf("unexpected API key reqs: %v", p.APIKeyReqs())
	}
}

func TestBraveProvider_AvailableWithoutKey(t *testing.T) {
	p := NewBraveProvider("")
	if p.Available() {
		t.Error("expected Available()=false without API key")
	}
}

func TestBraveProvider_Search_PolicyDenied(t *testing.T) {
	p := NewBraveProvider("test-key")
	pol := fakePolicy{
		allowURL: false,
		allowCap: map[string]bool{"tools.web_search": true},
	}
	_, err := p.Search(context.Background(), "test", pol)
	if err == nil {
		t.Fatal("expected policy denial error")
	}
}
