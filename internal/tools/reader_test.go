package tools

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/basket/go-claw/internal/policy"
)

func TestReadURL_DenyRedirectTargetOutsidePolicy(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer source.Close()

	host := testURLHost(t, source.URL)
	pol := policy.Policy{
		AllowDomains:      []string{host},
		AllowLoopback:     true,
		AllowCapabilities: []string{"tools.read_url"},
	}

	_, err := readURL(context.Background(), source.URL, pol)
	if err == nil || !strings.Contains(err.Error(), "policy denied redirect URL") {
		t.Fatalf("expected redirect policy denial, got: %v", err)
	}
}

func TestReadURL_AllowRedirectWithinPolicy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body><p>redirect target content</p></body></html>"))
	}))
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer source.Close()

	host := testURLHost(t, source.URL)
	pol := policy.Policy{
		AllowDomains:      []string{host},
		AllowLoopback:     true,
		AllowCapabilities: []string{"tools.read_url"},
	}

	out, err := readURL(context.Background(), source.URL, pol)
	if err != nil {
		t.Fatalf("readURL: %v", err)
	}
	if !strings.Contains(out.Content, "redirect target content") {
		t.Fatalf("expected redirected content, got: %q", out.Content)
	}
}

func testURLHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url %q: %v", raw, err)
	}
	host, _, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host port %q: %v", u.Host, err)
	}
	return host
}
