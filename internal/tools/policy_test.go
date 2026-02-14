package tools

import (
	"context"
	"strings"
	"testing"
)

type fakePolicy struct {
	allowURL bool
	allowCap map[string]bool
}

func (f fakePolicy) AllowHTTPURL(raw string) bool {
	return f.allowURL
}

func (f fakePolicy) AllowCapability(capability string) bool {
	if f.allowCap == nil {
		return false
	}
	return f.allowCap[strings.ToLower(strings.TrimSpace(capability))]
}

func (f fakePolicy) AllowPath(string) bool { return true }

func (f fakePolicy) PolicyVersion() string {
	return "test-policy"
}

func TestSearch_DefaultDenyWhenCapabilityMissing(t *testing.T) {
	providers := []SearchProvider{NewDDGProvider()}
	_, err := search(context.Background(), "goclaw", fakePolicy{
		allowURL: true,
		allowCap: map[string]bool{"tools.read_url": true},
	}, providers)
	if err == nil || !strings.Contains(err.Error(), "tools.web_search") {
		t.Fatalf("expected tools.web_search capability denial, got err=%v", err)
	}
}

func TestReader_DefaultDenyWhenCapabilityMissing(t *testing.T) {
	_, err := readURL(context.Background(), "https://example.com", fakePolicy{
		allowURL: true,
		allowCap: map[string]bool{"tools.web_search": true},
	})
	if err == nil || !strings.Contains(err.Error(), "tools.read_url") {
		t.Fatalf("expected tools.read_url capability denial, got err=%v", err)
	}
}

func TestReader_DenyWhenURLNotAllowlisted(t *testing.T) {
	_, err := readURL(context.Background(), "https://example.com", fakePolicy{
		allowURL: false,
		allowCap: map[string]bool{"tools.read_url": true},
	})
	if err == nil || !strings.Contains(err.Error(), "policy denied URL") {
		t.Fatalf("expected URL policy denial, got err=%v", err)
	}
}
