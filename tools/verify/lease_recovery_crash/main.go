package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/basket/go-claw/internal/persistence"
)

const sessionID = "11111111-2222-3333-4444-555555555555"

func main() {
	mode := flag.String("mode", "", "prepare|claim-sleep|recover")
	dbPath := flag.String("db", "", "path to sqlite db")
	flag.Parse()

	if *mode == "" || *dbPath == "" {
		fmt.Fprintln(os.Stderr, "mode and db are required")
		os.Exit(2)
	}

	ctx := context.Background()
	store, err := persistence.Open(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	switch *mode {
	case "prepare":
		if err := store.EnsureSession(ctx, sessionID); err != nil {
			fmt.Fprintf(os.Stderr, "ensure session: %v\n", err)
			os.Exit(1)
		}
		taskID, err := store.CreateTask(ctx, sessionID, `{"content":"lease-crash"}`)
		if err != nil {
			fmt.Fprintf(os.Stderr, "create task: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("PREPARED_TASK_ID=%s\n", taskID)
	case "claim-sleep":
		task, err := store.ClaimNextPendingTask(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "claim task: %v\n", err)
			os.Exit(1)
		}
		if task == nil {
			fmt.Fprintln(os.Stderr, "no claimable task")
			os.Exit(1)
		}
		if err := store.StartTaskRun(ctx, task.ID, task.LeaseOwner, ""); err != nil {
			fmt.Fprintf(os.Stderr, "start task run: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("CLAIMED_TASK_ID=%s\n", task.ID)
		fmt.Printf("LEASE_OWNER=%s\n", task.LeaseOwner)
		for {
			time.Sleep(1 * time.Second)
		}
	case "recover":
		recovered, err := store.RecoverRunningTasks(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "recover running tasks: %v\n", err)
			os.Exit(1)
		}
		tasks, err := store.ListTasksBySession(ctx, sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "list tasks by session: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("RECOVERED=%d\n", recovered)
		pass := true
		for _, task := range tasks {
			fmt.Printf("TASK_STATUS id=%s status=%s lease_owner=%q\n", task.ID, task.Status, task.LeaseOwner)
			if task.Status == "RUNNING" {
				pass = false
			}
		}
		if pass {
			fmt.Println("VERDICT PASS")
		} else {
			fmt.Println("VERDICT FAIL — tasks still in RUNNING state after recovery")
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q\n", *mode)
		os.Exit(2)
	}
}
