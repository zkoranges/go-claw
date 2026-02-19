package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/basket/go-claw/internal/bus"
	"github.com/google/uuid"
)

// TotalEventCount returns the total number of task events in the store.
func (s *Store) TotalEventCount(ctx context.Context) (int64, error) {
	var count int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM task_events;`).Scan(&count); err != nil {
		return 0, fmt.Errorf("total event count: %w", err)
	}
	return count, nil
}

func (s *Store) TaskEventBounds(ctx context.Context, sessionID string) (minEventID, maxEventID int64, err error) {
	var min sql.NullInt64
	var max sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
		SELECT MIN(event_id), MAX(event_id)
		FROM task_events
		WHERE session_id = ?;
	`, sessionID).Scan(&min, &max); err != nil {
		return 0, 0, fmt.Errorf("task event bounds: %w", err)
	}
	if min.Valid {
		minEventID = min.Int64
	}
	if max.Valid {
		maxEventID = max.Int64
	}
	return minEventID, maxEventID, nil
}

func (s *Store) ListTaskEventsFrom(ctx context.Context, sessionID string, fromEventID int64, limit int) ([]TaskEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, task_id, session_id, event_type, COALESCE(run_id, ''), COALESCE(trace_id, session_id), state_from, state_to, payload_json, created_at
		FROM task_events
		WHERE session_id = ? AND event_id > ?
		ORDER BY event_id ASC
		LIMIT ?;
	`, sessionID, fromEventID, limit)
	if err != nil {
		return nil, fmt.Errorf("list task events: %w", err)
	}
	defer rows.Close()

	var out []TaskEvent
	for rows.Next() {
		var (
			event     TaskEvent
			stateFrom sql.NullString
		)
		if err := rows.Scan(
			&event.EventID,
			&event.TaskID,
			&event.SessionID,
			&event.EventType,
			&event.RunID,
			&event.TraceID,
			&stateFrom,
			&event.StateTo,
			&event.Payload,
			&event.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan task event: %w", err)
		}
		if stateFrom.Valid {
			event.StateFrom = TaskStatus(stateFrom.String)
		}
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("task event rows: %w", err)
	}
	return out, nil
}

func (s *Store) CreateTask(ctx context.Context, sessionID, payload string) (string, error) {
	return s.createTask(ctx, "", sessionID, payload)
}

func (s *Store) createTask(ctx context.Context, agentID, sessionID, payload string) (string, error) {
	taskID := uuid.NewString()
	// GC-SPEC-PER-002: Retry transient lock errors with bounded jitter.
	err := retryOnBusy(ctx, 5, func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin create task tx: %w", err)
		}
		defer func() { _ = tx.Rollback() }()

		agent := agentID
		if agent == "" {
			agent = "default"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tasks (
				id, session_id, type, status, attempt, max_attempts, available_at, agent_id, payload, created_at, updated_at
			)
			VALUES (?, ?, 'chat', ?, 0, ?, CURRENT_TIMESTAMP, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
		`, taskID, sessionID, TaskStatusQueued, defaultMaxAttempts, agent, payload); err != nil {
			return fmt.Errorf("create task: %w", err)
		}
		if err := s.appendTaskEventTx(ctx, tx, taskID, sessionID, "", TaskStatusQueued, "task.enqueued", `{"reason":"create_task"}`); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		return "", err
	}
	return taskID, nil
}

func (s *Store) ClaimNextPendingTask(ctx context.Context) (*Task, error) {
	return s.claimNextPendingTask(ctx, "")
}

func (s *Store) claimNextPendingTask(ctx context.Context, agentID string) (*Task, error) {
	var result *Task
	// GC-SPEC-PER-002: Retry transient lock errors with bounded jitter.
	err := retryOnBusy(ctx, 5, func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin claim tx: %w", err)
		}
		defer func() { _ = tx.Rollback() }()

		var task Task
		var query string
		var args []any
		if agentID == "" {
			query = `
				SELECT id, session_id, type, status, attempt, max_attempts, available_at,
					COALESCE(last_error_code, ''), poison_count, payload,
					COALESCE(result, ''), COALESCE(error, ''), COALESCE(lease_owner, ''),
					lease_expires_at, created_at, updated_at, COALESCE(agent_id, 'default')
				FROM tasks
				WHERE status = ? AND available_at <= CURRENT_TIMESTAMP
				ORDER BY priority DESC, created_at ASC, id ASC
				LIMIT 1;`
			args = []any{TaskStatusQueued}
		} else {
			query = `
				SELECT id, session_id, type, status, attempt, max_attempts, available_at,
					COALESCE(last_error_code, ''), poison_count, payload,
					COALESCE(result, ''), COALESCE(error, ''), COALESCE(lease_owner, ''),
					lease_expires_at, created_at, updated_at, COALESCE(agent_id, 'default')
				FROM tasks
				WHERE status = ? AND agent_id = ? AND available_at <= CURRENT_TIMESTAMP
				ORDER BY priority DESC, created_at ASC, id ASC
				LIMIT 1;`
			args = []any{TaskStatusQueued, agentID}
		}
		row := tx.QueryRowContext(ctx, query, args...)
		if scanErr := scanTask(row.Scan, &task); scanErr != nil {
			if errors.Is(scanErr, sql.ErrNoRows) {
				_ = tx.Rollback()
				result = nil
				return nil
			}
			return fmt.Errorf("select pending task: %w", scanErr)
		}

		ok, err := s.transitionTaskTx(ctx, tx, task.ID,
			[]TaskStatus{TaskStatusQueued}, TaskStatusClaimed,
			"task.claimed", `{"reason":"claim_next_pending"}`, nil, nil)
		if err != nil {
			return fmt.Errorf("claim task transition: %w", err)
		}
		if !ok {
			_ = tx.Rollback()
			result = nil
			return nil
		}
		leaseOwner := uuid.NewString()
		leaseExpiresAt := time.Now().UTC().Add(defaultLeaseDuration)
		if _, err := tx.ExecContext(ctx, `
			UPDATE tasks
			SET lease_owner = ?, lease_expires_at = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND status = ?;
		`, leaseOwner, leaseExpiresAt, task.ID, TaskStatusClaimed); err != nil {
			return fmt.Errorf("set claim lease: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit claim tx: %w", err)
		}
		task.Status = TaskStatusClaimed
		task.LeaseOwner = leaseOwner
		task.LeaseExpiresAt = &leaseExpiresAt
		result = &task
		return nil
	})
	return result, err
}

// StartTaskRun transitions a claimed task to running and pins the policy version (GC-SPEC-SEC-003).
func (s *Store) StartTaskRun(ctx context.Context, taskID, leaseOwner, policyVersion string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin start task tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var currentLeaseOwner string
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(lease_owner, '')
		FROM tasks
		WHERE id = ? AND status = ?;
	`, taskID, TaskStatusClaimed).Scan(&currentLeaseOwner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return fmt.Errorf("read claimed lease owner: %w", err)
	}
	if currentLeaseOwner == "" || currentLeaseOwner != leaseOwner {
		return sql.ErrNoRows
	}
	ok, err := s.transitionTaskTx(
		ctx,
		tx,
		taskID,
		[]TaskStatus{TaskStatusClaimed},
		TaskStatusRunning,
		"task.running",
		`{"reason":"worker_start"}`,
		nil,
		nil,
	)
	if err != nil {
		return err
	}
	if !ok {
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET lease_expires_at = ?, policy_version = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND lease_owner = ? AND status = ?;
	`, time.Now().UTC().Add(defaultLeaseDuration), policyVersion, taskID, leaseOwner, TaskStatusRunning); err != nil {
		return fmt.Errorf("extend lease on start run: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit start task tx: %w", err)
	}
	return nil
}

func (s *Store) HeartbeatLease(ctx context.Context, taskID, leaseOwner string) (bool, error) {
	if leaseOwner == "" {
		return false, nil
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		SET lease_expires_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND lease_owner = ? AND status IN (?, ?);
	`, time.Now().UTC().Add(defaultLeaseDuration), taskID, leaseOwner, TaskStatusClaimed, TaskStatusRunning)
	if err != nil {
		return false, fmt.Errorf("heartbeat lease: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("heartbeat rows affected: %w", err)
	}
	return n == 1, nil
}

func (s *Store) RequeueExpiredLeases(ctx context.Context) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin requeue expired leases tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM tasks
		WHERE status IN (?, ?)
		  AND lease_expires_at IS NOT NULL
		  AND lease_expires_at <= CURRENT_TIMESTAMP;
	`, TaskStatusClaimed, TaskStatusRunning)
	if err != nil {
		return 0, fmt.Errorf("query expired leases: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, fmt.Errorf("scan expired lease task: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate expired lease tasks: %w", err)
	}

	var reclaimed int64
	for _, id := range ids {
		ok, err := s.transitionTaskTx(
			ctx,
			tx,
			id,
			[]TaskStatus{TaskStatusClaimed, TaskStatusRunning},
			TaskStatusQueued,
			"task.lease_expired_requeued",
			`{"reason":"lease_expired"}`,
			nil,
			nil,
		)
		if err != nil {
			return 0, fmt.Errorf("requeue expired transition: %w", err)
		}
		if !ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE tasks
			SET lease_owner = NULL, lease_expires_at = NULL, updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND status = ?;
		`, id, TaskStatusQueued); err != nil {
			return 0, fmt.Errorf("clear lease after requeue: %w", err)
		}
		reclaimed++
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit requeue expired leases tx: %w", err)
	}
	return reclaimed, nil
}

// AgeQueuedPriorities bumps priority for QUEUED tasks that have been waiting
// longer than ageThreshold, preventing session starvation (GC-SPEC-QUE-007).
// The maxPriority cap prevents unbounded growth. Returns the number of tasks aged.
func (s *Store) AgeQueuedPriorities(ctx context.Context, ageThreshold time.Duration, maxPriority int) (int64, error) {
	cutoff := time.Now().UTC().Add(-ageThreshold)
	res, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		SET priority = MIN(priority + 1, ?),
		    updated_at = CURRENT_TIMESTAMP
		WHERE status = ?
		  AND available_at <= CURRENT_TIMESTAMP
		  AND updated_at < ?
		  AND priority < ?;
	`, maxPriority, TaskStatusQueued, cutoff, maxPriority)
	if err != nil {
		return 0, fmt.Errorf("age queued priorities: %w", err)
	}
	return res.RowsAffected()
}

func (s *Store) CompleteTask(ctx context.Context, taskID, result string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin complete task tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	ok, err := s.transitionTaskTx(
		ctx,
		tx,
		taskID,
		[]TaskStatus{TaskStatusRunning},
		TaskStatusSucceeded,
		"task.succeeded",
		`{"reason":"processor_success"}`,
		&result,
		nil,
	)
	if err != nil {
		return fmt.Errorf("complete task transition: %w", err)
	}
	if !ok {
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET lease_owner = NULL, lease_expires_at = NULL, error = NULL, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND status = ?;
	`, taskID, TaskStatusSucceeded); err != nil {
		return fmt.Errorf("clear lease on complete: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit complete task tx: %w", err)
	}

	// Publish completion event (best-effort, ignore errors).
	if s.bus != nil {
		task, err := s.GetTask(ctx, taskID)
		if err == nil && task != nil {
			s.bus.Publish("task.completed", map[string]interface{}{
				"task_id":    taskID,
				"session_id": task.SessionID,
				"status":     TaskStatusSucceeded,
			})
		}
	}

	// Snapshot metrics on completion (best-effort)
	_ = s.RecordTaskMetrics(ctx, taskID)
	return nil
}

func (s *Store) FailTask(ctx context.Context, taskID, errMsg string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin fail task tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	ok, err := s.transitionTaskTx(
		ctx,
		tx,
		taskID,
		[]TaskStatus{TaskStatusQueued, TaskStatusClaimed, TaskStatusRunning, TaskStatusRetryWait},
		TaskStatusFailed,
		"task.failed",
		fmt.Sprintf(`{"reason":"processor_error","error":%q}`, errMsg),
		nil,
		&errMsg,
	)
	if err != nil {
		return fmt.Errorf("fail task transition: %w", err)
	}
	if !ok {
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET lease_owner = NULL, lease_expires_at = NULL, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND status = ?;
	`, taskID, TaskStatusFailed); err != nil {
		return fmt.Errorf("clear lease on fail: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit fail task tx: %w", err)
	}

	// Publish failure event (best-effort, ignore errors).
	if s.bus != nil {
		task, err := s.GetTask(ctx, taskID)
		if err == nil && task != nil {
			s.bus.Publish("task.failed", map[string]interface{}{
				"task_id":    taskID,
				"session_id": task.SessionID,
				"error":      errMsg,
			})
		}
	}

	// Snapshot metrics on completion (best-effort)
	_ = s.RecordTaskMetrics(ctx, taskID)
	return nil
}

func hashString(input string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(input))
	return strconv.FormatUint(h.Sum64(), 16)
}

func errorFingerprint(errMsg string) string {
	normalized := strings.ToLower(strings.TrimSpace(errMsg))
	if len(normalized) > 512 {
		normalized = normalized[:512]
	}
	return hashString(normalized)
}

func retryDelay(taskID string, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	base := retryBaseDelay
	for i := 1; i < attempt; i++ {
		base *= 2
		if base >= retryMaxDelay {
			base = retryMaxDelay
			break
		}
	}
	if base > retryMaxDelay {
		base = retryMaxDelay
	}
	jitterMax := base / 2
	if jitterMax <= 0 {
		jitterMax = time.Millisecond
	}
	jitterHash := hashString(taskID + ":" + strconv.Itoa(attempt))
	jitterSource, _ := strconv.ParseUint(jitterHash[:min(len(jitterHash), 8)], 16, 64)
	jitter := time.Duration(int64(jitterSource % uint64(jitterMax)))
	delay := base + jitter
	if delay > retryMaxDelay {
		delay = retryMaxDelay
	}
	return delay
}

// HandleTaskFailure applies retry/backoff/DLQ decisions for a RUNNING task.
func (s *Store) HandleTaskFailure(ctx context.Context, taskID, errMsg string) (FailureDecision, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FailureDecision{}, fmt.Errorf("begin handle failure tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		status          TaskStatus
		attempt         int
		maxAttempts     int
		lastFingerprint string
		poisonCount     int
		sessionID       string
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT status, attempt, max_attempts, COALESCE(last_error_fingerprint, ''), poison_count, session_id
		FROM tasks
		WHERE id = ?;
	`, taskID).Scan(&status, &attempt, &maxAttempts, &lastFingerprint, &poisonCount, &sessionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FailureDecision{}, sql.ErrNoRows
		}
		return FailureDecision{}, fmt.Errorf("select task for failure handling: %w", err)
	}
	if status != TaskStatusRunning {
		return FailureDecision{}, sql.ErrNoRows
	}
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}

	nextAttempt := attempt + 1
	fingerprint := errorFingerprint(errMsg)
	nextPoison := 1
	if lastFingerprint != "" && lastFingerprint == fingerprint {
		nextPoison = poisonCount + 1
	}

	decision := FailureDecision{
		Attempt:          nextAttempt,
		MaxAttempts:      maxAttempts,
		ErrorFingerprint: fingerprint,
		PoisonCount:      nextPoison,
	}

	reasonCode := ReasonRetryProcessorError
	moveToDeadLetter := false
	if nextPoison >= poisonThreshold {
		reasonCode = ReasonDeadLetterPoisonPill
		moveToDeadLetter = true
	}
	if nextAttempt >= maxAttempts {
		reasonCode = ReasonDeadLetterMaxAttempts
		moveToDeadLetter = true
	}
	decision.ReasonCode = reasonCode

	if moveToDeadLetter {
		ok, err := s.transitionTaskTx(
			ctx,
			tx,
			taskID,
			[]TaskStatus{TaskStatusRunning},
			TaskStatusFailed,
			"task.failed",
			fmt.Sprintf(`{"reason":"processor_error","reason_code":%q,"attempt":%d,"max_attempts":%d}`, reasonCode, nextAttempt, maxAttempts),
			nil,
			&errMsg,
		)
		if err != nil {
			return FailureDecision{}, fmt.Errorf("transition to failed: %w", err)
		}
		if !ok {
			return FailureDecision{}, sql.ErrNoRows
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE tasks
			SET
				attempt = ?,
				max_attempts = ?,
				last_error_code = ?,
				last_error_fingerprint = ?,
				poison_count = ?,
				lease_owner = NULL,
				lease_expires_at = NULL,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND status = ?;
		`, nextAttempt, maxAttempts, reasonCode, fingerprint, nextPoison, taskID, TaskStatusFailed); err != nil {
			return FailureDecision{}, fmt.Errorf("update failed metadata: %w", err)
		}
		ok, err = s.transitionTaskTx(
			ctx,
			tx,
			taskID,
			[]TaskStatus{TaskStatusFailed},
			TaskStatusDeadLetter,
			"task.dead_letter",
			fmt.Sprintf(`{"reason":"terminal_failure","reason_code":%q}`, reasonCode),
			nil,
			nil,
		)
		if err != nil {
			return FailureDecision{}, fmt.Errorf("transition to dead_letter: %w", err)
		}
		if !ok {
			return FailureDecision{}, sql.ErrNoRows
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE tasks
			SET lease_owner = NULL, lease_expires_at = NULL, updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND status = ?;
		`, taskID, TaskStatusDeadLetter); err != nil {
			return FailureDecision{}, fmt.Errorf("clear lease dead_letter: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return FailureDecision{}, fmt.Errorf("commit dead_letter tx: %w", err)
		}
		decision.Outcome = FailureOutcomeDeadLetter
		return decision, nil
	}

	delay := retryDelay(taskID, nextAttempt)
	availableAt := time.Now().UTC().Add(delay)
	decision.Outcome = FailureOutcomeRetried
	decision.BackoffUntil = &availableAt

	ok, err := s.transitionTaskTx(
		ctx,
		tx,
		taskID,
		[]TaskStatus{TaskStatusRunning},
		TaskStatusRetryWait,
		"task.retry_wait",
		fmt.Sprintf(`{"reason":"retry_scheduled","reason_code":%q,"attempt":%d,"max_attempts":%d,"delay_ms":%d}`, reasonCode, nextAttempt, maxAttempts, delay.Milliseconds()),
		nil,
		&errMsg,
	)
	if err != nil {
		return FailureDecision{}, fmt.Errorf("transition to retry_wait: %w", err)
	}
	if !ok {
		return FailureDecision{}, sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET
			attempt = ?,
			max_attempts = ?,
			available_at = ?,
			last_error_code = ?,
			last_error_fingerprint = ?,
			poison_count = ?,
			lease_owner = NULL,
			lease_expires_at = NULL,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND status = ?;
	`, nextAttempt, maxAttempts, availableAt, reasonCode, fingerprint, nextPoison, taskID, TaskStatusRetryWait); err != nil {
		return FailureDecision{}, fmt.Errorf("update retry metadata: %w", err)
	}
	ok, err = s.transitionTaskTx(
		ctx,
		tx,
		taskID,
		[]TaskStatus{TaskStatusRetryWait},
		TaskStatusQueued,
		"task.requeued",
		fmt.Sprintf(`{"reason":"ready_for_retry","reason_code":%q}`, reasonCode),
		nil,
		nil,
	)
	if err != nil {
		return FailureDecision{}, fmt.Errorf("transition to queued after retry wait: %w", err)
	}
	if !ok {
		return FailureDecision{}, sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return FailureDecision{}, fmt.Errorf("commit retry tx: %w", err)
	}
	_ = sessionID
	return decision, nil
}

// CheckToolCallDedup checks whether a tool call has already been recorded as
// successful. It does NOT insert a new record — use RegisterSuccessfulToolCall
// after the side effect succeeds. Returns true if a matching SUCCEEDED record exists.
func (s *Store) CheckToolCallDedup(ctx context.Context, idempotencyKey, requestHash string) (bool, error) {
	var existingStatus, existingRequestHash string
	err := s.db.QueryRowContext(ctx, `
		SELECT side_effect_status, request_hash
		FROM tool_call_dedup
		WHERE idempotency_key = ?;
	`, idempotencyKey).Scan(&existingStatus, &existingRequestHash)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check dedupe row: %w", err)
	}
	return existingStatus == "SUCCEEDED" && existingRequestHash == requestHash, nil
}

// RegisterSuccessfulToolCall enforces at-most-once success semantics per idempotency key.
// It returns deduped=true when a prior successful record already exists for the key.
func (s *Store) RegisterSuccessfulToolCall(ctx context.Context, idempotencyKey, toolName, requestHash, resultHash string) (deduped bool, err error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return false, fmt.Errorf("idempotency key required")
	}
	if strings.TrimSpace(toolName) == "" {
		return false, fmt.Errorf("tool name required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin dedupe tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		existingStatus      string
		existingRequestHash string
	)
	switch err := tx.QueryRowContext(ctx, `
		SELECT side_effect_status, request_hash
		FROM tool_call_dedup
		WHERE idempotency_key = ?;
	`, idempotencyKey).Scan(&existingStatus, &existingRequestHash); {
	case err == nil:
		if existingStatus == "SUCCEEDED" && existingRequestHash == requestHash {
			if err := tx.Commit(); err != nil {
				return false, fmt.Errorf("commit dedupe read tx: %w", err)
			}
			return true, nil
		}
		return false, fmt.Errorf("idempotency key conflict for %q", idempotencyKey)
	case errors.Is(err, sql.ErrNoRows):
		// continue and insert
	default:
		return false, fmt.Errorf("select dedupe row: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO tool_call_dedup (idempotency_key, tool_name, request_hash, side_effect_status, result_hash, created_at, updated_at)
		VALUES (?, ?, ?, 'SUCCEEDED', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
	`, idempotencyKey, toolName, requestHash, resultHash); err != nil {
		return false, fmt.Errorf("insert dedupe row: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit dedupe tx: %w", err)
	}
	return false, nil
}

func (s *Store) RecoverRunningTasks(ctx context.Context) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin recover tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, session_id, status, updated_at
		FROM tasks
		WHERE status IN (?, ?);
	`, TaskStatusRunning, TaskStatusClaimed)
	if err != nil {
		return 0, fmt.Errorf("query recoverable tasks: %w", err)
	}
	defer rows.Close()

	type recoverableTask struct {
		id        string
		sessionID string
		status    string
		updatedAt string
	}
	var tasks []recoverableTask
	for rows.Next() {
		var t recoverableTask
		if err := rows.Scan(&t.id, &t.sessionID, &t.status, &t.updatedAt); err != nil {
			return 0, fmt.Errorf("scan recoverable task: %w", err)
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate recoverable tasks: %w", err)
	}

	var recovered int64
	for _, t := range tasks {
		ok, err := s.transitionTaskTx(
			ctx,
			tx,
			t.id,
			[]TaskStatus{TaskStatusClaimed, TaskStatusRunning},
			TaskStatusQueued,
			"task.recovered",
			`{"reason":"startup_recovery"}`,
			nil,
			nil,
		)
		if err != nil {
			return 0, fmt.Errorf("recover task transition: %w", err)
		}
		if ok {
			if _, err := tx.ExecContext(ctx, `
				UPDATE tasks
				SET lease_owner = NULL, lease_expires_at = NULL, updated_at = CURRENT_TIMESTAMP
				WHERE id = ? AND status = ?;
			`, t.id, TaskStatusQueued); err != nil {
				return 0, fmt.Errorf("clear lease on recovery requeue: %w", err)
			}
			// Clean up orphaned assistant messages from the crashed run.
			// Tasks in RUNNING state may have saved partial assistant responses
			// before the crash; remove them to prevent duplicated context on retry.
			if t.status == string(TaskStatusRunning) {
				if _, err := tx.ExecContext(ctx, `
					DELETE FROM messages
					WHERE session_id = ? AND role = 'assistant' AND created_at >= ?;
				`, t.sessionID, t.updatedAt); err != nil {
					return 0, fmt.Errorf("clean orphaned messages: %w", err)
				}
			}
			recovered++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit recover tx: %w", err)
	}
	return recovered, nil
}

// RecoveryMetrics holds RPO/RTO-related measurements (GC-SPEC-REL-006).
type RecoveryMetrics struct {
	// StaleRunning is the count of tasks in RUNNING/CLAIMED state (at-risk on crash).
	StaleRunning int `json:"stale_running"`
	// OldestStaleAge is the age of the oldest in-flight task (RPO proxy).
	OldestStaleAge time.Duration `json:"oldest_stale_age_ms"`
	// RecoveredCount is how many tasks were recovered on last startup.
	RecoveredCount int64 `json:"recovered_count"`
	// RecoveryDuration measures how long the recovery scan took (RTO proxy).
	RecoveryDuration time.Duration `json:"recovery_duration_ms"`
}

// MeasureRecoveryMetrics returns RPO/RTO measurements for operational monitoring (GC-SPEC-REL-006).
func (s *Store) MeasureRecoveryMetrics(ctx context.Context) (RecoveryMetrics, error) {
	var m RecoveryMetrics
	// Count in-flight tasks (RPO: these are at risk on crash).
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM tasks WHERE status IN (?, ?);
	`, TaskStatusClaimed, TaskStatusRunning).Scan(&m.StaleRunning); err != nil {
		return m, fmt.Errorf("count stale: %w", err)
	}
	// Find oldest in-flight task age.
	var oldest sql.NullString
	if err := s.db.QueryRowContext(ctx, `
		SELECT MIN(updated_at) FROM tasks WHERE status IN (?, ?);
	`, TaskStatusClaimed, TaskStatusRunning).Scan(&oldest); err != nil {
		return m, fmt.Errorf("oldest stale: %w", err)
	}
	if oldest.Valid && oldest.String != "" {
		if t, err := time.Parse("2006-01-02 15:04:05", oldest.String); err == nil {
			m.OldestStaleAge = time.Since(t)
		}
	}
	return m, nil
}

// RecoverRunningTasksTimed wraps RecoverRunningTasks and records recovery duration (GC-SPEC-REL-006).
func (s *Store) RecoverRunningTasksTimed(ctx context.Context) (RecoveryMetrics, error) {
	start := time.Now()
	recovered, err := s.RecoverRunningTasks(ctx)
	if err != nil {
		return RecoveryMetrics{}, err
	}
	return RecoveryMetrics{
		RecoveredCount:   recovered,
		RecoveryDuration: time.Since(start),
	}, nil
}

// RequestCancel sets cancel_requested=1 for cooperative cancellation (GC-SPEC-STM-005).
// Returns true if the task was updated. Workers should check IsCancelRequested before/after tool calls.
func (s *Store) RequestCancel(ctx context.Context, taskID string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		SET cancel_requested = 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND status IN (?, ?, ?);
	`, taskID, TaskStatusClaimed, TaskStatusRunning, TaskStatusRetryWait)
	if err != nil {
		return false, fmt.Errorf("request cancel: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// IsCancelRequested checks the cancel_requested flag for a task.
func (s *Store) IsCancelRequested(ctx context.Context, taskID string) (bool, error) {
	var flag int
	err := s.db.QueryRowContext(ctx, `SELECT cancel_requested FROM tasks WHERE id = ?;`, taskID).Scan(&flag)
	if err != nil {
		return false, err
	}
	return flag == 1, nil
}

func (s *Store) AbortTask(ctx context.Context, taskID string) (bool, error) {
	// Set cancel_requested first for cooperative cancellation visibility.
	_, _ = s.RequestCancel(ctx, taskID)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin abort task tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	errMsg := "aborted"
	ok, err := s.transitionTaskTx(
		ctx,
		tx,
		taskID,
		[]TaskStatus{TaskStatusQueued, TaskStatusClaimed, TaskStatusRunning, TaskStatusRetryWait},
		TaskStatusCanceled,
		"task.canceled",
		`{"reason":"abort_request"}`,
		nil,
		&errMsg,
	)
	if err != nil {
		return false, fmt.Errorf("abort task transition: %w", err)
	}
	if !ok {
		return false, nil
	}
	// GC-SPEC-REL-007: Record deterministic reason code on cancellation.
	if _, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET lease_owner = NULL, lease_expires_at = NULL, last_error_code = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND status = ?;
	`, ReasonAborted, taskID, TaskStatusCanceled); err != nil {
		return false, fmt.Errorf("clear lease on cancel: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit abort task tx: %w", err)
	}

	// Publish cancellation event (best-effort, ignore errors).
	if s.bus != nil {
		task, err := s.GetTask(ctx, taskID)
		if err == nil && task != nil {
			s.bus.Publish("task.canceled", map[string]interface{}{
				"task_id":    taskID,
				"session_id": task.SessionID,
				"reason":     "abort_request",
			})
		}
	}

	return true, nil
}

func (s *Store) GetTask(ctx context.Context, taskID string) (*Task, error) {
	var task Task
	err := scanTask(s.db.QueryRowContext(ctx, `
		SELECT
			id,
			session_id,
			type,
			status,
			attempt,
			max_attempts,
			available_at,
			COALESCE(last_error_code, ''),
			poison_count,
			payload,
			COALESCE(result, ''),
			COALESCE(error, ''),
			COALESCE(lease_owner, ''),
			lease_expires_at,
			created_at,
			updated_at,
			COALESCE(agent_id, 'default')
		FROM tasks
		WHERE id = ?;
	`, taskID).Scan, &task)
	if err != nil {
		return nil, err
	}
	return &task, nil
}

func (s *Store) TaskCounts(ctx context.Context) (pending, running int, err error) {
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM tasks WHERE status=?;`, TaskStatusQueued).Scan(&pending); err != nil {
		return 0, 0, fmt.Errorf("count pending: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM tasks WHERE status IN (?, ?);`, TaskStatusClaimed, TaskStatusRunning).Scan(&running); err != nil {
		return 0, 0, fmt.Errorf("count running: %w", err)
	}
	return pending, running, nil
}

func (s *Store) QueueDepth(ctx context.Context) (int, error) {
	var pending int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM tasks WHERE status=?;`, TaskStatusQueued).Scan(&pending); err != nil {
		return 0, fmt.Errorf("queue depth: %w", err)
	}
	return pending, nil
}

// MetricsCounts returns counts required by GC-SPEC-OBS-004.
type MetricsCounts struct {
	Pending       int `json:"pending_tasks"`
	Running       int `json:"running_tasks"`
	RetryWait     int `json:"retry_wait_tasks"`
	DeadLetter    int `json:"dlq_size"`
	LeaseExpiries int `json:"lease_expiries"`
}

func (s *Store) MetricsCounts(ctx context.Context) (MetricsCounts, error) {
	var m MetricsCounts
	row := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN status = 'QUEUED' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status IN ('CLAIMED', 'RUNNING') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'RETRY_WAIT' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'DEAD_LETTER' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN lease_expires_at IS NOT NULL AND lease_expires_at <= CURRENT_TIMESTAMP AND status IN ('CLAIMED', 'RUNNING') THEN 1 ELSE 0 END), 0)
		FROM tasks;
	`)
	if err := row.Scan(&m.Pending, &m.Running, &m.RetryWait, &m.DeadLetter, &m.LeaseExpiries); err != nil {
		return m, fmt.Errorf("metrics counts: %w", err)
	}
	return m, nil
}

// RecordPolicyVersion persists a policy version snapshot (GC-SPEC-CFG-006).
func (s *Store) RecordPolicyVersion(ctx context.Context, policyVersion, checksum, source string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO policy_versions (policy_version, checksum, loaded_at, source)
		VALUES (?, ?, CURRENT_TIMESTAMP, ?)
		ON CONFLICT(policy_version) DO UPDATE SET loaded_at = CURRENT_TIMESTAMP, source = excluded.source;
	`, policyVersion, checksum, source)
	if err != nil {
		return fmt.Errorf("record policy version: %w", err)
	}
	return nil
}

// Backup creates an online-consistent backup of the database (GC-SPEC-PER-005).
// Uses VACUUM INTO which creates a complete, consistent copy without blocking writes.
func (s *Store) Backup(ctx context.Context, destPath string) error {
	if destPath == "" {
		return fmt.Errorf("backup destination path required")
	}
	if _, err := os.Stat(destPath); err == nil {
		return fmt.Errorf("backup destination already exists: %s", destPath)
	}
	_, err := s.db.ExecContext(ctx, `VACUUM INTO ?;`, destPath)
	if err != nil {
		return fmt.Errorf("backup (VACUUM INTO): %w", err)
	}
	return nil
}

type Session struct {
	ID        string    `json:"id"`
	SoulHash  string    `json:"soul_hash,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) ListSessions(ctx context.Context, limit int) ([]Session, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(soul_hash, ''), created_at
		FROM sessions
		ORDER BY created_at DESC
		LIMIT ?;
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.SoulHash, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sessions rows: %w", err)
	}
	return out, nil
}

func (s *Store) KVSet(ctx context.Context, key, val string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO kv_store (key, value, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=CURRENT_TIMESTAMP;
	`, key, val)
	if err != nil {
		return fmt.Errorf("kv set: %w", err)
	}
	return nil
}

// KVGet retrieves a value from the kv_store. Returns empty string if key not found.
func (s *Store) KVGet(ctx context.Context, key string) (string, error) {
	var val string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM kv_store WHERE key = ?`, key).Scan(&val)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("kv_get: %w", err)
	}
	return val, nil
}

func (s *Store) RecordToolTask(ctx context.Context, sessionID, toolName, input, output string, callErr error) (string, error) {
	payloadJSON, err := json.Marshal(ToolTaskPayload{
		Kind:  "TOOL",
		Tool:  toolName,
		Input: input,
	})
	if err != nil {
		return "", fmt.Errorf("marshal tool payload: %w", err)
	}
	resultJSON, err := json.Marshal(ToolTaskResult{
		Output: output,
	})
	if err != nil {
		return "", fmt.Errorf("marshal tool result: %w", err)
	}

	taskID := uuid.NewString()
	status := TaskStatusSucceeded
	errMsg := ""
	if callErr != nil {
		status = TaskStatusFailed
		errMsg = callErr.Error()
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO tasks (
			id, session_id, type, status, attempt, max_attempts, available_at, payload, result, error, created_at, updated_at
		)
		VALUES (?, ?, 'tool', ?, 0, ?, CURRENT_TIMESTAMP, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
	`, taskID, sessionID, status, defaultMaxAttempts, string(payloadJSON), string(resultJSON), errMsg)
	if err != nil {
		return "", fmt.Errorf("record tool task: %w", err)
	}
	return taskID, nil
}

func (s *Store) ListTasksBySession(ctx context.Context, sessionID string) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			id,
			session_id,
			type,
			status,
			attempt,
			max_attempts,
			available_at,
			COALESCE(last_error_code, ''),
			poison_count,
			payload,
			COALESCE(result,''),
			COALESCE(error,''),
			COALESCE(lease_owner, ''),
			lease_expires_at,
			created_at,
			updated_at,
			COALESCE(agent_id, 'default')
		FROM tasks
		WHERE session_id = ?
		ORDER BY created_at ASC, id ASC;
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list tasks by session: %w", err)
	}
	defer rows.Close()

	var out []Task
	for rows.Next() {
		var task Task
		if err := scanTask(rows.Scan, &task); err != nil {
			return nil, fmt.Errorf("scan task by session: %w", err)
		}
		out = append(out, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tasks by session rows: %w", err)
	}
	return out, nil
}

// --- Subtask support ---

// GetSubtasks returns all tasks that have the given parent_task_id.
func (s *Store) GetSubtasks(ctx context.Context, parentTaskID string) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, type, status, attempt, max_attempts, available_at,
		       last_error_code, poison_count, payload, COALESCE(result,''), COALESCE(error,''),
		       COALESCE(lease_owner,''), lease_expires_at, created_at, updated_at,
		       COALESCE(agent_id, 'default')
		FROM tasks WHERE parent_task_id = ?
		ORDER BY created_at ASC;
	`, parentTaskID)
	if err != nil {
		return nil, fmt.Errorf("get subtasks: %w", err)
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		if err := scanTask(rows.Scan, &t); err != nil {
			return nil, fmt.Errorf("scan subtask: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CreateSubtask creates a task linked to a parent task.
func (s *Store) CreateSubtask(ctx context.Context, parentTaskID, sessionID, payload string, priority int) (string, error) {
	taskID := uuid.NewString()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tasks (id, session_id, type, status, priority, payload, parent_task_id, created_at, updated_at)
		VALUES (?, ?, 'subtask', 'QUEUED', ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
	`, taskID, sessionID, priority, payload, parentTaskID)
	if err != nil {
		return "", fmt.Errorf("create subtask: %w", err)
	}
	return taskID, nil
}

// --- Pagination support ---

// ListTasksPaginated returns tasks with optional status filter and cursor-based pagination.
func (s *Store) ListTasksPaginated(ctx context.Context, statusFilter string, limit, offset int) ([]Task, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	var totalCount int
	var countErr error
	if statusFilter != "" {
		countErr = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE status = ?;`, statusFilter).Scan(&totalCount)
	} else {
		countErr = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks;`).Scan(&totalCount)
	}
	if countErr != nil {
		return nil, 0, fmt.Errorf("count tasks: %w", countErr)
	}

	var query string
	var args []any
	if statusFilter != "" {
		query = `SELECT id, session_id, type, status, attempt, max_attempts, available_at,
		         last_error_code, poison_count, payload, COALESCE(result,''), COALESCE(error,''),
		         COALESCE(lease_owner,''), lease_expires_at, created_at, updated_at,
		         COALESCE(agent_id, 'default')
		         FROM tasks WHERE status = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;`
		args = []any{statusFilter, limit, offset}
	} else {
		query = `SELECT id, session_id, type, status, attempt, max_attempts, available_at,
		         last_error_code, poison_count, payload, COALESCE(result,''), COALESCE(error,''),
		         COALESCE(lease_owner,''), lease_expires_at, created_at, updated_at,
		         COALESCE(agent_id, 'default')
		         FROM tasks ORDER BY created_at DESC LIMIT ? OFFSET ?;`
		args = []any{limit, offset}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list tasks paginated: %w", err)
	}
	defer rows.Close()

	var out []Task
	for rows.Next() {
		var t Task
		if err := scanTask(rows.Scan, &t); err != nil {
			return nil, 0, fmt.Errorf("scan task: %w", err)
		}
		out = append(out, t)
	}
	return out, totalCount, rows.Err()
}

// UpdateTaskTokens records token usage for a task.
func (s *Store) UpdateTaskTokens(ctx context.Context, taskID string, promptTokens, completionTokens int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		SET prompt_tokens = ?, completion_tokens = ?, total_tokens = ?, estimated_cost_usd = 0.0
		WHERE id = ?`,
		promptTokens, completionTokens, promptTokens+completionTokens, taskID,
	)
	if err != nil {
		return fmt.Errorf("update task tokens %s: %w", taskID, err)
	}

	// Publish token update event (best-effort, ignore errors).
	if s.bus != nil {
		s.bus.Publish(bus.TopicTaskTokens, bus.TaskTokensEvent{
			TaskID:           taskID,
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
		})
	}

	return nil
}

// RecordTaskMetrics snapshots final metrics for a completed task.
func (s *Store) RecordTaskMetrics(ctx context.Context, taskID string) error {
	// Fetch task metrics before recording for event publishing.
	var promptTokens, completionTokens, totalTokens int
	var estimatedCost float64
	var sessionID string
	err := s.db.QueryRowContext(ctx, `
		SELECT session_id, prompt_tokens, completion_tokens, total_tokens, estimated_cost_usd
		FROM tasks WHERE id = ?`, taskID).
		Scan(&sessionID, &promptTokens, &completionTokens, &totalTokens, &estimatedCost)
	if err != nil && err != sql.ErrNoRows {
		// Continue anyway — metrics recording is best-effort.
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO task_metrics (
			task_id, agent_id, session_id, parent_task_id, created_at, completed_at,
			status, prompt_tokens, completion_tokens, total_tokens, estimated_cost_usd
		)
		SELECT
			id, agent_id, session_id, parent_task_id, created_at, CURRENT_TIMESTAMP,
			status, prompt_tokens, completion_tokens, total_tokens, estimated_cost_usd
		FROM tasks WHERE id = ?
		ON CONFLICT(task_id) DO NOTHING`,
		taskID,
	)
	if err != nil {
		return fmt.Errorf("record task metrics %s: %w", taskID, err)
	}

	// Publish metrics event (best-effort, ignore errors).
	if s.bus != nil && sessionID != "" {
		s.bus.Publish(bus.TopicTaskMetrics, bus.TaskMetricsEvent{
			TaskID:           taskID,
			InputTokens:      promptTokens,
			OutputTokens:     completionTokens,
			TotalTokens:      totalTokens,
			EstimatedCostUSD: estimatedCost,
		})
	}

	return nil
}

// SetParentTask sets the parent task ID for a child task (task trees).
func (s *Store) SetParentTask(ctx context.Context, childTaskID, parentTaskID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		SET parent_task_id = ?
		WHERE id = ?`,
		parentTaskID, childTaskID,
	)
	if err != nil {
		return fmt.Errorf("set parent task: %w", err)
	}
	return nil
}

// SetTaskContext writes a key-value pair to the shared context for a task tree.
// GC-SPEC-PDR-v4-Phase-1: Shared context for multi-agent task trees.
func (s *Store) SetTaskContext(ctx context.Context, taskRootID, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO task_context (task_root_id, key, value)
		VALUES (?, ?, ?)
		ON CONFLICT(task_root_id, key) DO UPDATE SET value = excluded.value`,
		taskRootID, key, value,
	)
	if err != nil {
		return fmt.Errorf("set task context %s/%s: %w", taskRootID, key, err)
	}
	return nil
}

// GetTaskContext reads a value from the shared context for a task tree.
// Returns empty string and no error if the key does not exist.
// GC-SPEC-PDR-v4-Phase-1: Shared context for multi-agent task trees.
func (s *Store) GetTaskContext(ctx context.Context, taskRootID, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `
		SELECT value FROM task_context WHERE task_root_id = ? AND key = ?`,
		taskRootID, key,
	).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get task context %s/%s: %w", taskRootID, key, err)
	}
	return value, nil
}

// GetAllTaskContext returns all key-value pairs for a task tree.
// GC-SPEC-PDR-v4-Phase-1: Shared context for multi-agent task trees.
func (s *Store) GetAllTaskContext(ctx context.Context, taskRootID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT key, value FROM task_context WHERE task_root_id = ?`, taskRootID)
	if err != nil {
		return nil, fmt.Errorf("get all task context %s: %w", taskRootID, err)
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan task context: %w", err)
		}
		result[k] = v
	}
	return result, rows.Err()
}
