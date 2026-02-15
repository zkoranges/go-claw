package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/basket/go-claw/internal/config"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/skills"
)

type installedSkillLister interface {
	ListInstalledSkills(ctx context.Context) ([]persistence.InstalledSkillRecord, error)
}

func lookupInstalledSkill(ctx context.Context, lister installedSkillLister, name string) (*persistence.InstalledSkillRecord, error) {
	recs, err := lister.ListInstalledSkills(ctx)
	if err != nil {
		return nil, fmt.Errorf("list installed skills: %w", err)
	}
	for idx := range recs {
		if recs[idx].SkillID == name {
			return &recs[idx], nil
		}
	}
	return nil, nil
}

func runSkillCommand(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: goclaw skill <install|list|remove|update|info> ...")
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config load: %v\n", err)
		return 1
	}

	dbPath := filepath.Join(cfg.HomeDir, "goclaw.db")
	store, err := persistence.Open(dbPath, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		return 1
	}
	defer store.Close()

	installer := skills.NewInstaller(cfg.HomeDir, store, slog.Default())

	sub := strings.ToLower(strings.TrimSpace(args[0]))
	switch sub {
	case "install":
		fs := flag.NewFlagSet("goclaw skill install", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		ref := fs.String("ref", "", "branch or tag")
		force := fs.Bool("force", false, "overwrite existing install")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		rest := fs.Args()
		if len(rest) != 1 {
			fmt.Fprintln(os.Stderr, "usage: goclaw skill install <github-url> [--ref <branch|tag>] [--force]")
			return 2
		}
		url := rest[0]
		if *force {
			owner, repo, err := skills.ParseInstallName(url)
			if err == nil {
				_ = installer.Remove(ctx, owner+"-"+repo)
			}
		}
		if err := installer.Install(ctx, url, *ref); err != nil {
			fmt.Fprintf(os.Stderr, "install failed: %v\n", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "installed")
		return 0

	case "list":
		// Show installed records from DB (provenance).
		recs, err := store.ListInstalledSkills(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "list installed: %v\n", err)
			return 1
		}
		if len(recs) == 0 {
			fmt.Fprintln(os.Stdout, "no installed skills")
			return 0
		}
		for _, r := range recs {
			ref := strings.TrimSpace(r.Ref)
			if ref == "" {
				ref = "(default)"
			}
			fmt.Fprintf(os.Stdout, "%s\t%s\t%s\n", r.SkillID, r.SourceURL, ref)
		}
		return 0

	case "remove":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: goclaw skill remove <name>")
			return 2
		}
		name := strings.TrimSpace(args[1])
		if err := installer.Remove(ctx, name); err != nil {
			fmt.Fprintf(os.Stderr, "remove failed: %v\n", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "removed")
		return 0

	case "update":
		fs := flag.NewFlagSet("goclaw skill update", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		all := fs.Bool("all", false, "update all installed skills")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		rest := fs.Args()
		if *all {
			if err := installer.UpdateAll(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "update-all failed: %v\n", err)
				return 1
			}
			fmt.Fprintln(os.Stdout, "updated")
			return 0
		}
		if len(rest) != 1 {
			fmt.Fprintln(os.Stderr, "usage: goclaw skill update <name> | --all")
			return 2
		}
		if err := installer.Update(ctx, rest[0]); err != nil {
			fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "updated")
		return 0

	case "info":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: goclaw skill info <name>")
			return 2
		}
		name := strings.TrimSpace(args[1])
		rec, err := lookupInstalledSkill(ctx, store, name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skill info failed: %v\n", err)
			return 1
		}
		if rec == nil {
			fmt.Fprintf(os.Stderr, "skill not found: %s\n", name)
			return 1
		}
		fmt.Fprintf(os.Stdout, "name: %s\nsource: %s\nurl: %s\nref: %s\n", rec.SkillID, rec.Source, rec.SourceURL, rec.Ref)
		return 0

	default:
		fmt.Fprintf(os.Stderr, "unknown skill subcommand: %s\n", sub)
		return 2
	}
}
