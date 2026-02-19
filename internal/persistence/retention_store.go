package persistence

import (
	"context"
	"fmt"
	"time"
)

// RetentionResult holds counts of purged records from a retention run.
type RetentionResult struct {
	PurgedTaskEvents    int64 `json:"purged_task_events"`
	PurgedAuditLogs     int64 `json:"purged_audit_logs"`
	PurgedMessages      int64 `json:"purged_messages"`
	PurgedAgentMessages int64 `json:"purged_agent_messages"`
}

// RunRetention deletes records older than the configured retention windows (GC-SPEC-DATA-005).
// Each category uses a separate DELETE with its own cutoff. The job is idempotent (GC-SPEC-DATA-008).
func (s *Store) RunRetention(ctx context.Context, taskEventDays, auditLogDays, messageDays int) (RetentionResult, error) {
	var result RetentionResult

	if taskEventDays > 0 {
		cutoff := time.Now().UTC().AddDate(0, 0, -taskEventDays)
		res, err := s.db.ExecContext(ctx, `DELETE FROM task_events WHERE created_at < ?;`, cutoff)
		if err != nil {
			return result, fmt.Errorf("purge task_events: %w", err)
		}
		result.PurgedTaskEvents, _ = res.RowsAffected()
	}

	if auditLogDays > 0 {
		cutoff := time.Now().UTC().AddDate(0, 0, -auditLogDays)
		res, err := s.db.ExecContext(ctx, `DELETE FROM audit_log WHERE created_at < ?;`, cutoff)
		if err != nil {
			return result, fmt.Errorf("purge audit_log: %w", err)
		}
		result.PurgedAuditLogs, _ = res.RowsAffected()
	}

	if messageDays > 0 {
		cutoff := time.Now().UTC().AddDate(0, 0, -messageDays)
		res, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE created_at < ?;`, cutoff)
		if err != nil {
			return result, fmt.Errorf("purge messages: %w", err)
		}
		result.PurgedMessages, _ = res.RowsAffected()

		// Purge read inter-agent messages older than the same retention window.
		// Only messages that have been read (read_at IS NOT NULL) are eligible for purge.
		res, err = s.db.ExecContext(ctx, `DELETE FROM agent_messages WHERE read_at IS NOT NULL AND created_at < ?;`, cutoff)
		if err != nil {
			return result, fmt.Errorf("purge agent_messages: %w", err)
		}
		result.PurgedAgentMessages, _ = res.RowsAffected()
	}

	return result, nil
}
