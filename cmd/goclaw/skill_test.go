package main

import (
	"context"
	"errors"
	"testing"

	"github.com/basket/go-claw/internal/persistence"
)

type fakeSkillLister struct {
	recs []persistence.InstalledSkillRecord
	err  error
}

func (f fakeSkillLister) ListInstalledSkills(_ context.Context) ([]persistence.InstalledSkillRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.recs, nil
}

func TestLookupInstalledSkill_ReturnsMatch(t *testing.T) {
	ctx := context.Background()
	lister := fakeSkillLister{
		recs: []persistence.InstalledSkillRecord{
			{SkillID: "alpha", Source: "github", SourceURL: "https://example.com/a"},
			{SkillID: "beta", Source: "github", SourceURL: "https://example.com/b"},
		},
	}

	got, err := lookupInstalledSkill(ctx, lister, "beta")
	if err != nil {
		t.Fatalf("lookupInstalledSkill error: %v", err)
	}
	if got == nil {
		t.Fatal("expected match, got nil")
	}
	if got.SkillID != "beta" {
		t.Fatalf("expected beta, got %q", got.SkillID)
	}
}

func TestLookupInstalledSkill_ReturnsNilWhenNotFound(t *testing.T) {
	ctx := context.Background()
	lister := fakeSkillLister{
		recs: []persistence.InstalledSkillRecord{
			{SkillID: "alpha"},
		},
	}

	got, err := lookupInstalledSkill(ctx, lister, "missing")
	if err != nil {
		t.Fatalf("lookupInstalledSkill error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing skill, got %#v", got)
	}
}

func TestLookupInstalledSkill_PropagatesListError(t *testing.T) {
	ctx := context.Background()
	lister := fakeSkillLister{err: errors.New("db offline")}

	got, err := lookupInstalledSkill(ctx, lister, "alpha")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got != nil {
		t.Fatalf("expected nil result on error, got %#v", got)
	}
	if err.Error() != "list installed skills: db offline" {
		t.Fatalf("unexpected error: %v", err)
	}
}
