package cron_test

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/cron"
	"github.com/basket/go-claw/internal/persistence"
)

// waitFor polls check at short intervals until it returns true or the deadline
// elapses. This avoids fixed time.Sleep calls that cause flaky tests.
func waitFor(t *testing.T, deadline time.Duration, check func() bool) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

func openTestStore(t *testing.T) *persistence.Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "goclaw.db")
	store, err := persistence.Open(dbPath, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func insertTestSchedule(t *testing.T, store *persistence.Store, sessionID string, cronExpr string, payload string, enabled bool, nextRunAt *time.Time) string {
	t.Helper()
	id := "sched-" + t.Name()
	sched := persistence.Schedule{
		ID:        id,
		Name:      "test-" + t.Name(),
		CronExpr:  cronExpr,
		Payload:   payload,
		SessionID: sessionID,
		Enabled:   enabled,
		NextRunAt: nextRunAt,
	}
	if err := store.InsertSchedule(context.Background(), sched); err != nil {
		t.Fatalf("insert schedule: %v", err)
	}
	return id
}

func TestScheduler_FiresOnTime(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	sessionID := "a0a0a0a0-1111-2222-3333-444444444444"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	// Schedule with next_run_at in the past should fire immediately.
	past := time.Now().Add(-5 * time.Minute)
	insertTestSchedule(t, store, sessionID, "*/5 * * * *", `{"msg":"hello"}`, true, &past)

	// Create scheduler with a short interval; start it and poll for the task.
	sched := cron.NewScheduler(cron.Config{
		Store:    store,
		Logger:   slog.Default(),
		Interval: 50 * time.Millisecond,
	})
	sched.Start(ctx)
	defer sched.Stop()

	// Poll until the scheduler fires and creates a task.
	waitFor(t, 3*time.Second, func() bool {
		tasks, err := store.ListTasksBySession(ctx, sessionID)
		return err == nil && len(tasks) > 0
	})
}

func TestScheduler_DisabledSkipped(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	sessionID := "b0b0b0b0-1111-2222-3333-555555555555"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	// Disabled schedule should NOT fire even with past next_run_at.
	past := time.Now().Add(-5 * time.Minute)
	insertTestSchedule(t, store, sessionID, "*/5 * * * *", `{"msg":"nope"}`, false, &past)

	sched := cron.NewScheduler(cron.Config{
		Store:    store,
		Logger:   slog.Default(),
		Interval: 50 * time.Millisecond,
	})
	sched.Start(ctx)

	// Give the scheduler enough ticks to have processed the schedule, then
	// verify no task was created. We still need a brief wait here because we
	// are asserting a negative (nothing happened), but we keep it short.
	time.Sleep(200 * time.Millisecond)
	sched.Stop()

	tasks, err := store.ListTasksBySession(ctx, sessionID)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks for disabled schedule, got %d", len(tasks))
	}
}

func TestScheduler_EnqueuesTask(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	sessionID := "c0c0c0c0-1111-2222-3333-666666666666"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	payload := `{"action":"run-report","target":"daily"}`
	past := time.Now().Add(-1 * time.Minute)
	insertTestSchedule(t, store, sessionID, "0 9 * * *", payload, true, &past)

	sched := cron.NewScheduler(cron.Config{
		Store:    store,
		Logger:   slog.Default(),
		Interval: 50 * time.Millisecond,
	})
	sched.Start(ctx)
	defer sched.Stop()

	// Poll until the task is enqueued.
	var tasks []persistence.Task
	waitFor(t, 3*time.Second, func() bool {
		var err error
		tasks, err = store.ListTasksBySession(ctx, sessionID)
		return err == nil && len(tasks) > 0
	})

	task := tasks[0]
	if task.SessionID != sessionID {
		t.Fatalf("expected session_id=%s, got %s", sessionID, task.SessionID)
	}
	if task.Payload != payload {
		t.Fatalf("expected payload=%s, got %s", payload, task.Payload)
	}
	if task.Status != persistence.TaskStatusQueued {
		t.Fatalf("expected status=%s, got %s", persistence.TaskStatusQueued, task.Status)
	}
}

func TestScheduler_NextRunUpdated(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	sessionID := "d0d0d0d0-1111-2222-3333-777777777777"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	past := time.Now().Add(-1 * time.Minute)
	schedID := insertTestSchedule(t, store, sessionID, "*/10 * * * *", `{"msg":"tick"}`, true, &past)

	sched := cron.NewScheduler(cron.Config{
		Store:    store,
		Logger:   slog.Default(),
		Interval: 50 * time.Millisecond,
	})
	sched.Start(ctx)
	defer sched.Stop()

	// Poll until last_run_at is set (schedule has fired).
	var found *persistence.Schedule
	waitFor(t, 3*time.Second, func() bool {
		schedules, err := store.ListSchedules(ctx)
		if err != nil {
			return false
		}
		for i := range schedules {
			if schedules[i].ID == schedID && schedules[i].LastRunAt != nil {
				found = &schedules[i]
				return true
			}
		}
		return false
	})

	if found.NextRunAt == nil {
		t.Fatal("expected next_run_at to be set after firing")
	}

	// The next run should be in the future (after the original past time).
	if !found.NextRunAt.After(past) {
		t.Fatalf("expected next_run_at (%v) to be after original past time (%v)", found.NextRunAt, past)
	}

	// Verify next_run_at is roughly aligned to a 10-minute boundary.
	if found.NextRunAt.Minute()%10 != 0 {
		t.Fatalf("expected next_run_at minute to be a multiple of 10, got %d", found.NextRunAt.Minute())
	}
}
