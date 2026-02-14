package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/basket/go-claw/internal/persistence"
)

const (
	maxEvents = 64
	maxLogs   = 32
)

type bundle struct {
	SessionID   string                    `json:"session_id"`
	ExportedAt  time.Time                 `json:"exported_at"`
	ConfigHash  string                    `json:"config_hash"`
	EventCount  int                       `json:"event_count"`
	LogCount    int                       `json:"log_count"`
	History     []persistence.HistoryItem `json:"history"`
	Events      []persistence.TaskEvent   `json:"events"`
	RedactedLog []string                  `json:"redacted_logs"`
}

func main() {
	ctx := context.Background()
	home, err := os.MkdirTemp("", "goclaw-incident-export-*")
	if err != nil {
		fmt.Printf("mktemp_error=%v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(home)

	logDir := filepath.Join(home, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		fmt.Printf("mkdir_logs_error=%v\n", err)
		os.Exit(1)
	}

	cfgPath := filepath.Join(home, "config.yaml")
	cfgBody := []byte("worker_count: 1\nbind_addr: \"127.0.0.1:18900\"\nlog_level: \"info\"\n")
	if err := os.WriteFile(cfgPath, cfgBody, 0o644); err != nil {
		fmt.Printf("write_config_error=%v\n", err)
		os.Exit(1)
	}
	logPath := filepath.Join(logDir, "system.jsonl")
	logLines := []string{
		`{"timestamp":"2026-02-11T00:00:00Z","level":"INFO","msg":"startup phase","component":"runtime","trace_id":"-"}`,
		`{"timestamp":"2026-02-11T00:00:01Z","level":"WARN","msg":"api token used","token":"[REDACTED]","trace_id":"abc"}`,
		`{"timestamp":"2026-02-11T00:00:02Z","level":"INFO","msg":"task complete","trace_id":"abc","task_id":"t1"}`,
	}
	if err := os.WriteFile(logPath, []byte(strings.Join(logLines, "\n")+"\n"), 0o644); err != nil {
		fmt.Printf("write_log_error=%v\n", err)
		os.Exit(1)
	}

	dbPath := filepath.Join(home, "goclaw.db")
	store, err := persistence.Open(dbPath)
	if err != nil {
		fmt.Printf("open_store_error=%v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	sessionID := "2b7f85b0-3f00-4f2d-ac65-a8a0f3af4a9b"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		fmt.Printf("ensure_session_error=%v\n", err)
		os.Exit(1)
	}
	if err := store.AddHistory(ctx, sessionID, "user", "create incident bundle", 10); err != nil {
		fmt.Printf("add_history_user_error=%v\n", err)
		os.Exit(1)
	}
	if err := store.AddHistory(ctx, sessionID, "assistant", "incident bundle acknowledged", 12); err != nil {
		fmt.Printf("add_history_assistant_error=%v\n", err)
		os.Exit(1)
	}

	for i := 0; i < 10; i++ {
		taskID, err := store.CreateTask(ctx, sessionID, fmt.Sprintf(`{"content":"incident-%d"}`, i))
		if err != nil {
			fmt.Printf("create_task_error=%v\n", err)
			os.Exit(1)
		}
		task, err := store.ClaimNextPendingTask(ctx)
		if err != nil || task == nil {
			fmt.Printf("claim_task_error=%v task_nil=%v\n", err, task == nil)
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

	history, err := store.ListHistory(ctx, sessionID, 50)
	if err != nil {
		fmt.Printf("list_history_error=%v\n", err)
		os.Exit(1)
	}
	events, err := store.ListTaskEventsFrom(ctx, sessionID, 0, maxEvents)
	if err != nil {
		fmt.Printf("list_events_error=%v\n", err)
		os.Exit(1)
	}
	logs, err := tailLines(logPath, maxLogs)
	if err != nil {
		fmt.Printf("tail_logs_error=%v\n", err)
		os.Exit(1)
	}
	cfgHash, err := sha256File(cfgPath)
	if err != nil {
		fmt.Printf("config_hash_error=%v\n", err)
		os.Exit(1)
	}

	b := bundle{
		SessionID:   sessionID,
		ExportedAt:  time.Now().UTC(),
		ConfigHash:  cfgHash,
		EventCount:  len(events),
		LogCount:    len(logs),
		History:     history,
		Events:      events,
		RedactedLog: logs,
	}

	bundlePath := filepath.Join(home, "incident_bundle.json")
	encoded, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		fmt.Printf("marshal_bundle_error=%v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(bundlePath, encoded, 0o644); err != nil {
		fmt.Printf("write_bundle_error=%v\n", err)
		os.Exit(1)
	}

	fmt.Printf("bundle_path=%s\n", bundlePath)
	fmt.Printf("config_hash=%s\n", cfgHash)
	fmt.Printf("events=%d max_events=%d\n", len(events), maxEvents)
	fmt.Printf("logs=%d max_logs=%d\n", len(logs), maxLogs)
	fmt.Printf("history=%d\n", len(history))
	if len(events) == 0 || len(logs) == 0 || len(events) > maxEvents || len(logs) > maxLogs {
		fmt.Println("VERDICT FAIL")
		os.Exit(1)
	}
	fmt.Println("VERDICT PASS")
}

func tailLines(path string, limit int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if limit <= 0 {
		limit = 1
	}
	lines := make([]string, 0, limit)
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		lines = append(lines, line)
		if len(lines) > limit {
			lines = lines[1:]
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

func sha256File(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
