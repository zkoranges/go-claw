package persistence_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/basket/go-claw/internal/persistence"
)

// BenchmarkStartup measures cold-start time: Open + schema migration (V-PERF-001).
func BenchmarkStartup(b *testing.B) {
	for i := 0; i < b.N; i++ {
		dir := b.TempDir()
		dbPath := filepath.Join(dir, "goclaw.db")
		store, err := persistence.Open(dbPath)
		if err != nil {
			b.Fatalf("open: %v", err)
		}
		_ = store.Close()
	}
}

// BenchmarkClaimLatency measures the p95-targeted claim/update path (V-PERF-004).
func BenchmarkClaimLatency(b *testing.B) {
	dir := b.TempDir()
	store, err := persistence.Open(filepath.Join(dir, "goclaw.db"))
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	sessionID := "b0000000-0000-0000-0000-000000000001"
	if err := store.EnsureSession(ctx, sessionID); err != nil {
		b.Fatalf("ensure session: %v", err)
	}

	// Pre-populate tasks.
	for i := 0; i < 100; i++ {
		if _, err := store.CreateTask(ctx, sessionID, fmt.Sprintf(`{"content":"bench-%d"}`, i)); err != nil {
			b.Fatalf("create task: %v", err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		task, err := store.ClaimNextPendingTask(ctx)
		if err != nil {
			b.Fatalf("claim: %v", err)
		}
		if task == nil {
			// Refill queue.
			b.StopTimer()
			for j := 0; j < 100; j++ {
				if _, err := store.CreateTask(ctx, sessionID, fmt.Sprintf(`{"content":"bench-refill-%d-%d"}`, i, j)); err != nil {
					b.Fatalf("refill: %v", err)
				}
			}
			b.StartTimer()
			continue
		}
		if err := store.StartTaskRun(ctx, task.ID, task.LeaseOwner, ""); err != nil {
			b.Fatalf("start run: %v", err)
		}
		if err := store.CompleteTask(ctx, task.ID, `{"reply":"ok"}`); err != nil {
			b.Fatalf("complete: %v", err)
		}
	}
}

// BenchmarkConcurrentSessions exercises 10 sessions with concurrent claim operations (V-PERF-003).
func BenchmarkConcurrentSessions(b *testing.B) {
	dir := b.TempDir()
	store, err := persistence.Open(filepath.Join(dir, "goclaw.db"))
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	const numSessions = 10
	sessions := make([]string, numSessions)
	for i := 0; i < numSessions; i++ {
		sessions[i] = fmt.Sprintf("b0000000-0000-0000-0000-0000000000%02d", i)
		if err := store.EnsureSession(ctx, sessions[i]); err != nil {
			b.Fatalf("ensure session: %v", err)
		}
		for j := 0; j < 10; j++ {
			if _, err := store.CreateTask(ctx, sessions[i], fmt.Sprintf(`{"content":"c-%d-%d"}`, i, j)); err != nil {
				b.Fatalf("create task: %v", err)
			}
		}
	}

	b.ResetTimer()
	var wg sync.WaitGroup
	for i := 0; i < b.N; i++ {
		for s := 0; s < numSessions; s++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				task, err := store.ClaimNextPendingTask(ctx)
				if err != nil || task == nil {
					return
				}
				_ = store.StartTaskRun(ctx, task.ID, task.LeaseOwner, "")
				_ = store.CompleteTask(ctx, task.ID, `{"reply":"ok"}`)
			}()
		}
		wg.Wait()
	}
}
