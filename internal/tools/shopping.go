package tools

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
)

// PriceComparisonInput is the input for the price_comparison tool.
type PriceComparisonInput struct {
	Prompt    string `json:"prompt"`
	SessionID string `json:"session_id"`
}

// PriceComparisonOutput is the structured result of a price comparison.
type PriceComparisonOutput struct {
	ProductA string `json:"product_a"`
	ProductB string `json:"product_b"`
	PriceA   string `json:"price_a"`
	PriceB   string `json:"price_b"`
	SourceA  string `json:"source_a"`
	SourceB  string `json:"source_b"`
	Summary  string `json:"summary"`
}

// priceCompareRE matches structured comparison patterns like "iPhone 15 vs Galaxy S24".
var priceCompareRE = regexp.MustCompile(`(?i)\b([A-Za-z][\w-]*(?:\s+\d[\w-]*)?)\s+(?:vs\.?|versus)\s+([A-Za-z][\w-]*(?:\s+\d[\w-]*)?)\b`)

// productNameREs extracts plausible product identifiers from comparison prompts.
var productNameREs = []*regexp.Regexp{
	regexp.MustCompile(`\b([A-Za-z]{3,}\s+\d[\w-]*)\b`), // "RTX 5090", "GTX 1080"
	regexp.MustCompile(`\b([A-Za-z]+-\d[\w-]*)\b`),      // "i9-13900K"
	regexp.MustCompile(`\b(\d{4,}[A-Za-z]*)\b`),         // "4090", "5090Ti"
}

var dollarRE = regexp.MustCompile(`\$\s?[0-9][0-9,]*(?:\.[0-9]+)?`)

func registerShopping(g *genkit.Genkit, reg *Registry) ai.Tool {
	return genkit.DefineTool(g, "price_comparison",
		"Compare prices of two products by searching the web. Input should contain a comparison prompt like 'compare price of X vs Y'. Returns structured price data with sources.",
		func(ctx *ai.ToolContext, input PriceComparisonInput) (PriceComparisonOutput, error) {
			reg.publishToolCall(ctx, "price_comparison")
			return comparePrices(ctx, input, reg)
		},
	)
}

func comparePrices(ctx *ai.ToolContext, input PriceComparisonInput, reg *Registry) (PriceComparisonOutput, error) {
	if reg.Policy == nil || !reg.Policy.AllowCapability("tools.price_comparison") {
		pv := ""
		if reg.Policy != nil {
			pv = reg.Policy.PolicyVersion()
		}
		audit.Record("deny", "tools.price_comparison", "missing_capability", pv, "price_comparison")
		return PriceComparisonOutput{}, fmt.Errorf("policy denied capability %q", "tools.price_comparison")
	}

	productA, productB := ExtractComparisonProducts(input.Prompt)
	if productA == "" || productB == "" {
		return PriceComparisonOutput{}, fmt.Errorf("could not extract product names from comparison request")
	}

	slog.Info("price_comparison tool called", "product_a", productA, "product_b", productB)

	queryA := productA + " price"
	queryB := productB + " price"

	searchA, err := reg.Search(ctx, queryA)
	recordToolTask(reg.Store, ctx, input.SessionID, "Search", queryA, marshalSearchOutput(searchA), err)
	if err != nil {
		return PriceComparisonOutput{}, fmt.Errorf("search for %s: %w", productA, err)
	}
	searchB, err := reg.Search(ctx, queryB)
	recordToolTask(reg.Store, ctx, input.SessionID, "Search", queryB, marshalSearchOutput(searchB), err)
	if err != nil {
		return PriceComparisonOutput{}, fmt.Errorf("search for %s: %w", productB, err)
	}

	var readSnippets []string
	readOnce := func(results SearchOutput) {
		for _, r := range results.Results {
			if strings.TrimSpace(r.URL) == "" {
				continue
			}
			readOut, readErr := reg.Read(ctx, r.URL)
			recordToolTask(reg.Store, ctx, input.SessionID, "Read", r.URL, readOut.Content, readErr)
			if readErr == nil && readOut.Content != "" {
				readSnippets = append(readSnippets, readOut.Content)
			}
			break
		}
	}
	readOnce(searchA)
	readOnce(searchB)

	combined := marshalSearchOutput(searchA) + "\n" + marshalSearchOutput(searchB) + "\n" + strings.Join(readSnippets, "\n")
	prices := FindDollarNumbers(combined)
	quoteA := FirstPriceNear(combined, productA)
	quoteB := FirstPriceNear(combined, productB)

	if quoteA == "" && len(prices) > 0 {
		quoteA = prices[0]
	}
	if quoteB == "" && len(prices) > 1 {
		quoteB = prices[1]
	}

	srcA := firstSearchURL(searchA)
	srcB := firstSearchURL(searchB)
	if quoteA == "" || quoteB == "" {
		return PriceComparisonOutput{}, fmt.Errorf("could not extract both prices from research results")
	}

	summary := fmt.Sprintf(
		"Based on current fetched results, %s is around %s and %s is around %s.\nSources: %s (%s), %s (%s).",
		productA, quoteA, productB, quoteB,
		productA, srcA, productB, srcB,
	)

	return PriceComparisonOutput{
		ProductA: productA,
		ProductB: productB,
		PriceA:   quoteA,
		PriceB:   quoteB,
		SourceA:  srcA,
		SourceB:  srcB,
		Summary:  summary,
	}, nil
}

// ExtractComparisonProducts extracts two product names from a comparison prompt.
// It first tries structured patterns (X vs Y), then falls back to product-like identifiers.
func ExtractComparisonProducts(prompt string) (string, string) {
	if matches := priceCompareRE.FindStringSubmatch(prompt); len(matches) >= 3 {
		a := strings.TrimSpace(matches[1])
		b := strings.TrimSpace(matches[2])
		if a != "" && b != "" {
			return a, b
		}
	}
	var compounds, standalones []string
	for _, m := range productNameREs[0].FindAllString(prompt, -1) {
		compounds = append(compounds, strings.TrimSpace(m))
	}
	for _, m := range productNameREs[1].FindAllString(prompt, -1) {
		compounds = append(compounds, strings.TrimSpace(m))
	}
	for _, m := range productNameREs[2].FindAllString(prompt, -1) {
		m = strings.TrimSpace(m)
		subsumed := false
		for _, c := range compounds {
			if strings.Contains(c, m) {
				subsumed = true
				break
			}
		}
		if !subsumed {
			standalones = append(standalones, m)
		}
	}
	products := append(compounds, standalones...)
	if len(products) >= 2 {
		return products[0], products[1]
	}
	return "", ""
}

// FindDollarNumbers extracts dollar amounts from text.
func FindDollarNumbers(in string) []string {
	return dollarRE.FindAllString(in, -1)
}

// FirstPriceNear finds the first dollar amount on a line containing the anchor text.
func FirstPriceNear(in, anchor string) string {
	lines := strings.Split(in, "\n")
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), strings.ToLower(anchor)) {
			if prices := FindDollarNumbers(line); len(prices) > 0 {
				return prices[0]
			}
		}
	}
	return ""
}

func marshalSearchOutput(out SearchOutput) string {
	b, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(b)
}

func firstSearchURL(out SearchOutput) string {
	for _, r := range out.Results {
		if strings.TrimSpace(r.URL) != "" {
			return r.URL
		}
	}
	return "n/a"
}

// recordToolTask is a helper that records a tool invocation if a store is available.
func recordToolTask(store *persistence.Store, ctx *ai.ToolContext, sessionID, toolName, input, output string, callErr error) {
	if store == nil || sessionID == "" {
		return
	}
	_, _ = store.RecordToolTask(ctx, sessionID, toolName, input, output, callErr)
}
