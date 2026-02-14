// Package cron provides a periodic scheduler that fires due cron schedules
// by creating tasks in the persistence store.
package cron

import (
	"context"
	"log/slog"
	"sync"
	"time"

	cronlib "github.com/robfig/cron/v3"

	"github.com/basket/go-claw/internal/persistence"
)

// cronParser parses standard 5-field cron expressions (minute, hour, dom, month, dow).
var cronParser = cronlib.NewParser(
	cronlib.Minute | cronlib.Hour | cronlib.Dom | cronlib.Month | cronlib.Dow,
)

// Config holds the dependencies for the cron scheduler.
type Config struct {
	Store    *persistence.Store
	Logger   *slog.Logger
	Interval time.Duration // tick interval; defaults to 1 minute if zero
}

// Scheduler periodically queries the store for due cron schedules
// and creates tasks for each one.
type Scheduler struct {
	store    *persistence.Store
	logger   *slog.Logger
	interval time.Duration

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewScheduler creates a new Scheduler with the given config.
func NewScheduler(cfg Config) *Scheduler {
	interval := cfg.Interval
	if interval <= 0 {
		interval = 1 * time.Minute
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{
		store:    cfg.Store,
		logger:   logger,
		interval: interval,
	}
}

// Start begins the scheduler loop. It runs in a background goroutine
// and respects the provided context for shutdown.
func (s *Scheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Add(1)
	go s.loop(ctx)
	s.logger.Info("cron scheduler started", "interval", s.interval)
}

// Stop cancels the scheduler loop and waits for it to exit.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	s.logger.Info("cron scheduler stopped")
}

// loop is the main scheduler loop. It ticks at the configured interval,
// queries for due schedules, and fires each one.
func (s *Scheduler) loop(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Fire immediately on startup, then on each tick.
	s.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// tick queries for due schedules and fires each one.
func (s *Scheduler) tick(ctx context.Context) {
	now := time.Now()
	due, err := s.store.DueSchedules(ctx, now)
	if err != nil {
		s.logger.Error("cron: failed to query due schedules", "error", err)
		return
	}
	for _, sched := range due {
		s.fire(ctx, sched, now)
	}
}

// fire creates a task for the given schedule and updates its run timestamps.
func (s *Scheduler) fire(ctx context.Context, sched persistence.Schedule, now time.Time) {
	taskID, err := s.store.CreateTask(ctx, sched.SessionID, sched.Payload)
	if err != nil {
		s.logger.Error("cron: failed to create task for schedule",
			"schedule_id", sched.ID,
			"schedule_name", sched.Name,
			"error", err,
		)
		return
	}

	nextRun, err := NextRunTime(sched.CronExpr, now)
	if err != nil {
		s.logger.Error("cron: failed to compute next run time",
			"schedule_id", sched.ID,
			"cron_expr", sched.CronExpr,
			"error", err,
		)
		return
	}

	if err := s.store.UpdateScheduleRun(ctx, sched.ID, now, nextRun); err != nil {
		s.logger.Error("cron: failed to update schedule run",
			"schedule_id", sched.ID,
			"error", err,
		)
		return
	}

	s.logger.Info("cron: schedule fired",
		"schedule_id", sched.ID,
		"schedule_name", sched.Name,
		"task_id", taskID,
		"next_run_at", nextRun,
	)
}

// NextRunTime parses the cron expression and returns the next run time after the given time.
func NextRunTime(cronExpr string, after time.Time) (time.Time, error) {
	sched, err := cronParser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(after), nil
}
