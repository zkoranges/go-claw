package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/basket/go-claw/internal/persistence"
)

func main() {
	ctx := context.Background()
	baseDir, err := os.MkdirTemp("", "goclaw-backup-drill-*")
	if err != nil {
		fmt.Printf("mktemp_error=%v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(baseDir)

	dbPath := filepath.Join(baseDir, "goclaw.db")
	backupPath := filepath.Join(baseDir, "backup.db")
	restorePath := filepath.Join(baseDir, "restore.db")

	store, err := persistence.Open(dbPath)
	if err != nil {
		fmt.Printf("open_store_error=%v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	sessionID := "6a2a1f8e-0087-4ca2-b229-80539598d91d"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		fmt.Printf("ensure_session_error=%v\n", err)
		os.Exit(1)
	}
	for i := 0; i < 40; i++ {
		taskID, err := store.CreateTask(ctx, sessionID, fmt.Sprintf(`{"content":"backup-%d"}`, i))
		if err != nil {
			fmt.Printf("create_task_error=%v\n", err)
			os.Exit(1)
		}
		task, err := store.ClaimNextPendingTask(ctx)
		if err != nil || task == nil {
			fmt.Printf("claim_task_error=%v task=%v\n", err, task == nil)
			os.Exit(1)
		}
		if err := store.StartTaskRun(ctx, taskID, task.LeaseOwner, ""); err != nil {
			fmt.Printf("start_task_error=%v\n", err)
			os.Exit(1)
		}
		if err := store.CompleteTask(ctx, taskID, `{"reply":"ok"}`); err != nil {
			fmt.Printf("complete_task_error=%v\n", err)
			os.Exit(1)
		}
	}

	backupStart := time.Now().UTC()
	if _, err := store.DB().ExecContext(ctx, `VACUUM INTO ?;`, backupPath); err != nil {
		fmt.Printf("backup_error=%v\n", err)
		os.Exit(1)
	}
	backupEnd := time.Now().UTC()

	backupBytes, err := os.ReadFile(backupPath)
	if err != nil {
		fmt.Printf("read_backup_error=%v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(restorePath, backupBytes, 0o644); err != nil {
		fmt.Printf("write_restore_error=%v\n", err)
		os.Exit(1)
	}
	restoreStart := time.Now().UTC()
	restoreStore, err := persistence.Open(restorePath)
	if err != nil {
		fmt.Printf("open_restore_error=%v\n", err)
		os.Exit(1)
	}
	defer restoreStore.Close()
	restoreEnd := time.Now().UTC()

	var tasksCount, eventCount int
	if err := restoreStore.DB().QueryRowContext(ctx, `SELECT COUNT(1) FROM tasks;`).Scan(&tasksCount); err != nil {
		fmt.Printf("count_tasks_error=%v\n", err)
		os.Exit(1)
	}
	if err := restoreStore.DB().QueryRowContext(ctx, `SELECT COUNT(1) FROM task_events;`).Scan(&eventCount); err != nil {
		fmt.Printf("count_events_error=%v\n", err)
		os.Exit(1)
	}

	rpo := backupEnd.Sub(backupStart)
	rto := restoreEnd.Sub(restoreStart)
	fmt.Printf("backup_started=%s\n", backupStart.Format(time.RFC3339Nano))
	fmt.Printf("backup_completed=%s\n", backupEnd.Format(time.RFC3339Nano))
	fmt.Printf("restore_started=%s\n", restoreStart.Format(time.RFC3339Nano))
	fmt.Printf("restore_completed=%s\n", restoreEnd.Format(time.RFC3339Nano))
	fmt.Printf("rpo_duration=%s\n", rpo)
	fmt.Printf("rto_duration=%s\n", rto)
	fmt.Printf("restored_tasks=%d\n", tasksCount)
	fmt.Printf("restored_task_events=%d\n", eventCount)

	if tasksCount < 40 || eventCount == 0 {
		fmt.Println("VERDICT FAIL")
		os.Exit(1)
	}
	fmt.Println("VERDICT PASS")
}
