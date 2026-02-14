package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/basket/go-claw/internal/config"
	"github.com/basket/go-claw/internal/doctor"
)

func runDoctorCommand(ctx context.Context, args []string) int {
	// Parse args? json output flag?
	jsonOutput := false
	for _, arg := range args {
		if arg == "-json" || arg == "--json" {
			jsonOutput = true
		}
	}

	cfg, err := config.Load()
	if err != nil && !cfg.NeedsGenesis {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		// We continue anyway to diagnose why
	}

	diag := doctor.Run(ctx, &cfg, Version)

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(diag); err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding json: %v\n", err)
			return 1
		}
		return 0
	}

	fmt.Printf("GoClaw Doctor Report (%s)\n", diag.Timestamp.Format(time.RFC3339))
	fmt.Printf("System: %s/%s (%s)\n", diag.System.OS, diag.System.Arch, diag.System.Go)
	fmt.Println("---")

	failCount := 0
	for _, res := range diag.Results {
		icon := "✅"
		if res.Status == "FAIL" {
			icon = "❌"
			failCount++
		} else if res.Status == "WARN" {
			icon = "⚠️ "
		} else if res.Status == "SKIP" {
			icon = "⏩"
		}

		fmt.Printf("%s %-15s: %s\n", icon, res.Name, res.Message)
		if res.Detail != "" {
			fmt.Printf("    %s\n", res.Detail)
		}
	}

	if failCount > 0 {
		return 1
	}
	return 0
}
