package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/policy"
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
)

type ReaderInput struct {
	URL string `json:"url"`
}

type ReaderOutput struct {
	Content string `json:"content"`
}

const maxReadURLRedirects = 10

func registerReader(g *genkit.Genkit, reg *Registry) ai.Tool {
	return genkit.DefineTool(g, "read_url",
		"Fetch and read the content of a web page URL. Returns the page content as simplified text. Use this to read articles, documentation, or any web page.",
		func(ctx *ai.ToolContext, input ReaderInput) (ReaderOutput, error) {
			reg.publishToolCall(ctx, "read_url")
			return reg.Read(ctx, input.URL)
		},
	)
}

func readURL(ctx context.Context, rawURL string, pol policy.Checker) (ReaderOutput, error) {
	if rawURL == "" {
		return ReaderOutput{}, fmt.Errorf("empty URL")
	}
	if pol == nil || !pol.AllowCapability("tools.read_url") {
		pv := ""
		if pol != nil {
			pv = pol.PolicyVersion()
		}
		audit.Record("deny", "tools.read_url", "missing_capability", pv, rawURL)
		return ReaderOutput{}, fmt.Errorf("policy denied capability %q", "tools.read_url")
	}
	audit.Record("allow", "tools.read_url", "capability_granted", pol.PolicyVersion(), rawURL)
	if !pol.AllowHTTPURL(rawURL) {
		audit.Record("deny", "tools.read_url", "url_denied", pol.PolicyVersion(), rawURL)
		return ReaderOutput{}, fmt.Errorf("policy denied URL %q", rawURL)
	}
	audit.Record("allow", "tools.read_url", "url_allowed", pol.PolicyVersion(), rawURL)
	content, err := fetchAndSimplify(ctx, rawURL, pol)
	if err != nil {
		return ReaderOutput{}, fmt.Errorf("read URL: %w", err)
	}
	return ReaderOutput{Content: content}, nil
}

func fetchAndSimplify(ctx context.Context, rawURL string, pol policy.Checker) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "GoClaw/1.0 (autonomous agent)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain")

	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxReadURLRedirects {
				return fmt.Errorf("stopped after %d redirects", maxReadURLRedirects)
			}
			redirectURL := req.URL.String()
			policyVersion := ""
			if pol != nil {
				policyVersion = pol.PolicyVersion()
			}
			if pol == nil || !pol.AllowHTTPURL(redirectURL) {
				audit.Record("deny", "tools.read_url", "redirect_url_denied", policyVersion, redirectURL)
				return fmt.Errorf("policy denied redirect URL %q", redirectURL)
			}
			audit.Record("allow", "tools.read_url", "redirect_url_allowed", policyVersion, redirectURL)
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d for %s", resp.StatusCode, rawURL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2MB limit
	if err != nil {
		return "", err
	}

	content := htmlToText(string(body))

	// Truncate to reasonable size for LLM context
	if len(content) > 8000 {
		content = content[:8000] + "\n\n[Content truncated at 8000 characters]"
	}
	return content, nil
}

// htmlToText converts HTML to simplified plain text.
// No browser required (NG1 compliant).
func htmlToText(html string) string {
	// Remove script and style blocks
	reScript := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	html = reScript.ReplaceAllString(html, "")

	reStyle := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	html = reStyle.ReplaceAllString(html, "")

	// Remove HTML comments
	reComment := regexp.MustCompile(`(?s)<!--.*?-->`)
	html = reComment.ReplaceAllString(html, "")

	// Replace block-level tags with newlines
	blockTags := regexp.MustCompile(`(?i)</?(?:div|p|br|h[1-6]|li|tr|td|th|blockquote|pre|hr)[^>]*>`)
	html = blockTags.ReplaceAllString(html, "\n")

	// Remove all remaining HTML tags
	reTags := regexp.MustCompile(`<[^>]+>`)
	html = reTags.ReplaceAllString(html, "")

	// Decode common HTML entities
	html = strings.ReplaceAll(html, "&amp;", "&")
	html = strings.ReplaceAll(html, "&lt;", "<")
	html = strings.ReplaceAll(html, "&gt;", ">")
	html = strings.ReplaceAll(html, "&quot;", "\"")
	html = strings.ReplaceAll(html, "&#39;", "'")
	html = strings.ReplaceAll(html, "&nbsp;", " ")

	// Clean up whitespace
	reSpaces := regexp.MustCompile(`[ \t]+`)
	html = reSpaces.ReplaceAllString(html, " ")

	reNewlines := regexp.MustCompile(`\n{3,}`)
	html = reNewlines.ReplaceAllString(html, "\n\n")

	return strings.TrimSpace(html)
}
