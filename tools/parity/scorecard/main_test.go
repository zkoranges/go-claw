package main

import "testing"

func TestValidateCatalog_RejectsMissingRequiredMetadata(t *testing.T) {
	c := Catalog{
		Version: 1,
		Sections: []Section{{
			ID:            "gateway",
			Title:         "Gateway System",
			Owner:         "runtime",
			TargetRelease: "v0.2",
			DefaultRisk:   "medium",
			Items: []Item{{
				Feature:  "Gateway control plane",
				OpenClaw: "implemented",
				GoClaw:   "implemented",
				Verified: true,
				// Missing traceability/spec/evidence.
			}},
		}},
	}
	if err := validateCatalog(c); err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestScorecardRows_CountsStatuses(t *testing.T) {
	c := Catalog{
		Version: 1,
		Sections: []Section{{
			ID:                      "security",
			Title:                   "Security Features",
			Owner:                   "security",
			TargetRelease:           "v0.2",
			DefaultRisk:             "high",
			DefaultSpecRefs:         []string{"GC-SPEC-SEC-001"},
			DefaultTraceabilityRefs: []string{"GC-SPEC-SEC-001"},
			DefaultEvidence:         []string{"docs/EVIDENCE/S-006/policy.txt"},
			Items: []Item{
				{Feature: "A", OpenClaw: "implemented", GoClaw: "implemented", Verified: true},
				{Feature: "B", OpenClaw: "partial", GoClaw: "goclaw_only", Verified: false},
				{Feature: "C", OpenClaw: "not_implemented", GoClaw: "not_implemented", Verified: false},
			},
		}},
	}
	if err := validateCatalog(c); err != nil {
		t.Fatalf("validateCatalog: %v", err)
	}

	rows := scorecardRows(c)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	if row.OpenCount != 2 {
		t.Fatalf("expected OpenCount=2, got %d", row.OpenCount)
	}
	if row.GoCount != 2 {
		t.Fatalf("expected GoCount=2, got %d", row.GoCount)
	}
	if row.GoOnlyCount != 1 {
		t.Fatalf("expected GoOnlyCount=1, got %d", row.GoOnlyCount)
	}
	if row.Verified != 1 {
		t.Fatalf("expected Verified=1, got %d", row.Verified)
	}
	if row.Total != 3 {
		t.Fatalf("expected Total=3, got %d", row.Total)
	}
}

func TestValidateCatalog_RejectsDuplicateSectionIDAndFeature(t *testing.T) {
	c := Catalog{
		Version: 1,
		Sections: []Section{
			{
				ID:                      "gateway",
				Title:                   "Gateway A",
				Owner:                   "runtime",
				TargetRelease:           "v0.2",
				DefaultRisk:             "medium",
				DefaultSpecRefs:         []string{"GC-SPEC-ACP-001"},
				DefaultTraceabilityRefs: []string{"GC-SPEC-ACP-001"},
				DefaultEvidence:         []string{"docs/TRACEABILITY.md"},
				Items: []Item{
					{Feature: "Gateway control plane", OpenClaw: "implemented", GoClaw: "implemented"},
					{Feature: "Gateway control plane", OpenClaw: "implemented", GoClaw: "implemented"},
				},
			},
			{
				ID:                      "gateway",
				Title:                   "Gateway B",
				Owner:                   "runtime",
				TargetRelease:           "v0.2",
				DefaultRisk:             "medium",
				DefaultSpecRefs:         []string{"GC-SPEC-ACP-001"},
				DefaultTraceabilityRefs: []string{"GC-SPEC-ACP-001"},
				DefaultEvidence:         []string{"docs/TRACEABILITY.md"},
				Items: []Item{
					{Feature: "Other", OpenClaw: "implemented", GoClaw: "implemented"},
				},
			},
		},
	}
	if err := validateCatalog(c); err == nil {
		t.Fatalf("expected duplicate validation error")
	}
}
