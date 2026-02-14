package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Catalog struct {
	Version  int       `yaml:"version"`
	Sections []Section `yaml:"sections"`
}

type Section struct {
	ID                      string   `yaml:"id"`
	Title                   string   `yaml:"title"`
	Owner                   string   `yaml:"owner"`
	TargetRelease           string   `yaml:"target_release"`
	DefaultRisk             string   `yaml:"default_risk"`
	DefaultSpecRefs         []string `yaml:"default_spec_refs"`
	DefaultTraceabilityRefs []string `yaml:"default_traceability_refs"`
	DefaultEvidence         []string `yaml:"default_evidence"`
	Items                   []Item   `yaml:"items"`
}

type Item struct {
	Feature          string   `yaml:"feature"`
	OpenClaw         string   `yaml:"openclaw"`
	GoClaw           string   `yaml:"goclaw"`
	Priority         string   `yaml:"priority"`
	Owner            string   `yaml:"owner"`
	TargetRelease    string   `yaml:"target_release"`
	Risk             string   `yaml:"risk"`
	Verified         bool     `yaml:"verified"`
	SpecRefs         []string `yaml:"spec_refs"`
	TraceabilityRefs []string `yaml:"traceability_refs"`
	Evidence         []string `yaml:"evidence"`
	Dependencies     []string `yaml:"dependencies"`
	Notes            string   `yaml:"notes"`
}

type scoreRow struct {
	Category    string
	OpenCount   int
	GoCount     int
	GoOnlyCount int
	Verified    int
	Total       int
}

func main() {
	var inPath string
	var outPath string
	flag.StringVar(&inPath, "in", "docs/parity/parity.yaml", "input parity catalog yaml")
	flag.StringVar(&outPath, "out", "-", "output markdown path (or - for stdout)")
	flag.Parse()

	catalog, err := loadCatalog(inPath)
	if err != nil {
		fatal(err)
	}
	if err := validateCatalog(catalog); err != nil {
		fatal(err)
	}

	rows := scorecardRows(catalog)
	out := renderScorecard(rows)
	if outPath == "" || outPath == "-" {
		fmt.Print(out)
		return
	}
	if err := os.WriteFile(outPath, []byte(out), 0o644); err != nil {
		fatal(fmt.Errorf("write output: %w", err))
	}
}

func loadCatalog(path string) (Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, fmt.Errorf("read catalog: %w", err)
	}
	var c Catalog
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Catalog{}, fmt.Errorf("parse catalog: %w", err)
	}
	return c, nil
}

func validateCatalog(c Catalog) error {
	if c.Version <= 0 {
		return fmt.Errorf("version must be > 0")
	}
	if len(c.Sections) == 0 {
		return fmt.Errorf("sections must not be empty")
	}
	var errs []error
	sectionIDs := make(map[string]struct{}, len(c.Sections))
	for _, s := range c.Sections {
		sectionID := strings.TrimSpace(s.ID)
		if sectionID == "" {
			errs = append(errs, fmt.Errorf("section missing id: %q", s.Title))
		} else {
			if _, exists := sectionIDs[sectionID]; exists {
				errs = append(errs, fmt.Errorf("duplicate section id %q", sectionID))
			} else {
				sectionIDs[sectionID] = struct{}{}
			}
		}
		if strings.TrimSpace(s.Title) == "" {
			errs = append(errs, fmt.Errorf("section %q missing title", s.ID))
		}
		if len(s.Items) == 0 {
			errs = append(errs, fmt.Errorf("section %q has no items", s.ID))
			continue
		}
		features := make(map[string]struct{}, len(s.Items))
		for _, item := range s.Items {
			feature := strings.TrimSpace(item.Feature)
			if feature == "" {
				errs = append(errs, fmt.Errorf("section %q has item with empty feature", s.ID))
				continue
			}
			key := strings.ToLower(feature)
			if _, exists := features[key]; exists {
				errs = append(errs, fmt.Errorf("section %q has duplicate feature %q", s.ID, feature))
			} else {
				features[key] = struct{}{}
			}
			if !isValidStatus(item.OpenClaw) {
				errs = append(errs, fmt.Errorf("section %q feature %q has invalid openclaw status %q", s.ID, item.Feature, item.OpenClaw))
			}
			if !isValidStatus(item.GoClaw) {
				errs = append(errs, fmt.Errorf("section %q feature %q has invalid goclaw status %q", s.ID, item.Feature, item.GoClaw))
			}
			if strings.TrimSpace(firstNonEmpty(item.Owner, s.Owner)) == "" {
				errs = append(errs, fmt.Errorf("section %q feature %q missing owner", s.ID, item.Feature))
			}
			if strings.TrimSpace(firstNonEmpty(item.TargetRelease, s.TargetRelease)) == "" {
				errs = append(errs, fmt.Errorf("section %q feature %q missing target_release", s.ID, item.Feature))
			}
			if strings.TrimSpace(firstNonEmpty(item.Risk, s.DefaultRisk)) == "" {
				errs = append(errs, fmt.Errorf("section %q feature %q missing risk", s.ID, item.Feature))
			}
			if len(firstNonEmptySlice(item.SpecRefs, s.DefaultSpecRefs)) == 0 {
				errs = append(errs, fmt.Errorf("section %q feature %q missing spec_refs", s.ID, item.Feature))
			}
			if len(firstNonEmptySlice(item.TraceabilityRefs, s.DefaultTraceabilityRefs)) == 0 {
				errs = append(errs, fmt.Errorf("section %q feature %q missing traceability_refs", s.ID, item.Feature))
			}
			if item.Verified && len(firstNonEmptySlice(item.Evidence, s.DefaultEvidence)) == 0 {
				errs = append(errs, fmt.Errorf("section %q feature %q is verified but missing evidence", s.ID, item.Feature))
			}
		}
	}
	return errors.Join(errs...)
}

func scorecardRows(c Catalog) []scoreRow {
	rows := make([]scoreRow, 0, len(c.Sections))
	for _, s := range c.Sections {
		row := scoreRow{
			Category: s.Title,
			Total:    len(s.Items),
		}
		for _, item := range s.Items {
			open := normalizeStatus(item.OpenClaw)
			goStatus := normalizeStatus(item.GoClaw)
			if open == "implemented" || open == "partial" {
				row.OpenCount++
			}
			if goStatus == "implemented" || goStatus == "partial" || goStatus == "goclaw_only" {
				row.GoCount++
			}
			if goStatus == "goclaw_only" {
				row.GoOnlyCount++
			}
			if item.Verified {
				row.Verified++
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Category < rows[j].Category })
	return rows
}

func renderScorecard(rows []scoreRow) string {
	var b strings.Builder
	b.WriteString("| Category | OpenClaw | GoClaw | GoClaw-Only | Verified | Total |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, row := range rows {
		b.WriteString(fmt.Sprintf(
			"| %s | %d/%d | %d/%d | %d | %d/%d | %d |\n",
			row.Category,
			row.OpenCount, row.Total,
			row.GoCount, row.Total,
			row.GoOnlyCount,
			row.Verified, row.Total,
			row.Total,
		))
	}
	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstNonEmptySlice(values ...[]string) []string {
	for _, v := range values {
		if len(v) > 0 {
			return v
		}
	}
	return nil
}

func normalizeStatus(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	return strings.ReplaceAll(s, "-", "_")
}

func isValidStatus(raw string) bool {
	switch normalizeStatus(raw) {
	case "implemented", "partial", "not_implemented", "planned", "out_of_scope", "na", "goclaw_only":
		return true
	default:
		return false
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
