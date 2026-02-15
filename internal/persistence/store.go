package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/shared"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

const (
	// v0.1 schema ledger constants used to gate startup safety.
	schemaVersionV2  = 2
	schemaChecksumV2 = "gc-v2-2026-02-11-spec-schema"

	// v0.1 schema v3: adds skill provenance columns (TODO Phase 3.3).
	schemaVersionV3  = 3
	schemaChecksumV3 = "gc-v3-2026-02-12-skill-provenance"

	// v0.1 schema v4: adds tasks.type column (TODO Other Work: task sub-types).
	schemaVersionV4  = 4
	schemaChecksumV4 = "gc-v4-2026-02-12-task-type"

	// v0.1 schema v5: adds schedules table + tasks.parent_task_id column.
	schemaVersionV5  = 5
	schemaChecksumV5 = "gc-v5-2026-02-13-cron-subtasks"

	// v0.1 schema v6: adds agents table + tasks.agent_id column.
	schemaVersionV6  = 6
	schemaChecksumV6 = "gc-v6-2026-02-14-multi-agent"

	// v0.1 schema v7: adds agent_messages table for inter-agent messaging.
	schemaVersionV7  = 7
	schemaChecksumV7 = "gc-v7-2026-02-14-agent-messages"

	// v0.1 schema v8: adds messages.agent_id for per-agent history isolation.
	schemaVersionV8  = 8
	schemaChecksumV8 = "gc-v8-2026-02-14-agent-history"

	// v0.2 schema v9: adds parent_task_id, token tracking, observability tables (PDR Phase 1).
	schemaVersionV9  = 9
	schemaChecksumV9 = "gc-v9-2026-02-14-coordination-foundation"

	schemaVersionLatest  = schemaVersionV9
	schemaChecksumLatest = schemaChecksumV9

	defaultLeaseDuration = 30 * time.Second

	defaultMaxAttempts = 3
	retryBaseDelay     = 1 * time.Second
	retryMaxDelay      = 30 * time.Second
	poisonThreshold    = 3
)

// GC-SPEC-REL-007: Deterministic reason codes for retry and terminal states.
const (
	ReasonRetryProcessorError   = "RETRY_PROCESSOR_ERROR"
	ReasonDeadLetterPoisonPill  = "DEAD_LETTER_POISON_PILL"
	ReasonDeadLetterMaxAttempts = "DEAD_LETTER_MAX_ATTEMPTS"
	ReasonAborted               = "ABORTED"
	ReasonTimeout               = "TIMEOUT"
	ReasonCanceled              = "CANCELED"
)

type TaskStatus string

const (
	// Canonical v0.1 states (SPEC §5.3).
	TaskStatusQueued     TaskStatus = "QUEUED"
	TaskStatusClaimed    TaskStatus = "CLAIMED"
	TaskStatusRunning    TaskStatus = "RUNNING"
	TaskStatusRetryWait  TaskStatus = "RETRY_WAIT"
	TaskStatusSucceeded  TaskStatus = "SUCCEEDED"
	TaskStatusFailed     TaskStatus = "FAILED"
	TaskStatusCanceled   TaskStatus = "CANCELED"
	TaskStatusDeadLetter TaskStatus = "DEAD_LETTER"
)

var allowedTransitions = map[TaskStatus]map[TaskStatus]struct{}{
	TaskStatusQueued: {
		TaskStatusClaimed:  {},
		TaskStatusCanceled: {},
	},
	TaskStatusClaimed: {
		TaskStatusRunning:  {},
		TaskStatusCanceled: {},
		TaskStatusQueued:   {}, // Recovery requeue.
	},
	TaskStatusRunning: {
		TaskStatusSucceeded: {},
		TaskStatusFailed:    {},
		TaskStatusRetryWait: {},
		TaskStatusCanceled:  {},
		TaskStatusQueued:    {}, // Crash recovery requeue.
	},
	TaskStatusRetryWait: {
		TaskStatusQueued:   {},
		TaskStatusFailed:   {},
		TaskStatusCanceled: {},
	},
	TaskStatusFailed: {
		TaskStatusDeadLetter: {},
		TaskStatusRetryWait:  {},
	},
}

type Task struct {
	ID             string     `json:"id"`
	SessionID      string     `json:"session_id"`
	Type           string     `json:"type"`
	Status         TaskStatus `json:"status"`
	Attempt        int        `json:"attempt"`
	MaxAttempts    int        `json:"max_attempts"`
	AvailableAt    time.Time  `json:"available_at"`
	LastErrorCode  string     `json:"last_error_code,omitempty"`
	PoisonCount    int        `json:"poison_count,omitempty"`
	PolicyVersion  string     `json:"policy_version,omitempty"` // GC-SPEC-SEC-003
	Payload        string     `json:"payload"`
	Result         string     `json:"result,omitempty"`
	Error          string     `json:"error,omitempty"`
	LeaseOwner     string     `json:"lease_owner,omitempty"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	AgentID        string     `json:"agent_id"`
}

// AgentRecord represents a row in the agents table.
type AgentRecord struct {
	AgentID            string    `json:"agent_id"`
	DisplayName        string    `json:"display_name"`
	Provider           string    `json:"provider"`
	Model              string    `json:"model"`
	Soul               string    `json:"soul"`
	WorkerCount        int       `json:"worker_count"`
	TaskTimeoutSeconds int       `json:"task_timeout_seconds"`
	MaxQueueDepth      int       `json:"max_queue_depth"`
	SkillsFilter       string    `json:"skills_filter"`
	PolicyOverrides    string    `json:"policy_overrides"`
	APIKeyEnv          string    `json:"api_key_env"`
	AgentEmoji         string    `json:"agent_emoji"`
	PreferredSearch    string    `json:"preferred_search"`
	Status             string    `json:"status"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type FailureOutcome string

const (
	FailureOutcomeRetried    FailureOutcome = "RETRIED"
	FailureOutcomeDeadLetter FailureOutcome = "DEAD_LETTER"
)

type FailureDecision struct {
	Outcome          FailureOutcome `json:"outcome"`
	Attempt          int            `json:"attempt"`
	MaxAttempts      int            `json:"max_attempts"`
	BackoffUntil     *time.Time     `json:"backoff_until,omitempty"`
	ReasonCode       string         `json:"reason_code"`
	ErrorFingerprint string         `json:"error_fingerprint"`
	PoisonCount      int            `json:"poison_count"`
}

type ToolTaskPayload struct {
	Kind  string `json:"kind"`
	Tool  string `json:"tool"`
	Input string `json:"input"`
}

type ToolTaskResult struct {
	Output string `json:"output,omitempty"`
}

type HistoryItem struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Text      string    `json:"text"` // GC-SPEC-ACP-009: OpenClaw backward-compat alias for content.
	Tokens    int       `json:"tokens"`
	CreatedAt time.Time `json:"created_at"`
}

type TaskEvent struct {
	EventID   int64      `json:"event_id"`
	TaskID    string     `json:"task_id"`
	SessionID string     `json:"session_id"`
	EventType string     `json:"event_type"`
	RunID     string     `json:"run_id,omitempty"`
	TraceID   string     `json:"trace_id,omitempty"`
	StateFrom TaskStatus `json:"state_from"`
	StateTo   TaskStatus `json:"state_to"`
	Payload   string     `json:"payload"`
	CreatedAt time.Time  `json:"created_at"`
}

type Store struct {
	db  *sql.DB
	bus *bus.Bus // may be nil in tests
}

func DefaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return filepath.Join(home, ".goclaw", "goclaw.db")
}

func Open(path string, eventBus *bus.Bus) (*Store, error) {
	if path == "" {
		path = DefaultDBPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	dsn := fmt.Sprintf("%s?_busy_timeout=5000&_foreign_keys=on", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite3: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &Store{db: db, bus: eventBus}
	if err := store.configurePragmas(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.initSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) Close() error {
	return s.db.Close()
}

// GC-SPEC-PER-002: retryOnBusy retries f when SQLite returns BUSY or LOCKED,
// using exponential backoff with bounded jitter. maxRetries=5 gives ~3s total
// wait on top of the driver's busy_timeout (5s).
func retryOnBusy(ctx context.Context, maxRetries int, f func() error) error {
	const baseDelay = 50 * time.Millisecond
	const maxDelay = 500 * time.Millisecond

	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		err = f()
		if err == nil {
			return nil
		}
		if !isSQLiteBusy(err) {
			return err
		}
		if attempt == maxRetries {
			return err
		}
		// Exponential backoff: 50ms, 100ms, 200ms, 400ms, 500ms (capped).
		delay := baseDelay << uint(attempt)
		if delay > maxDelay {
			delay = maxDelay
		}
		// Add jitter: ±25% of delay.
		jitter := time.Duration(rand.IntN(int(delay / 2)))
		delay = delay - delay/4 + jitter

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return err
}

// isSQLiteBusy checks if an error is a SQLite BUSY (5) or LOCKED (6) error.
func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	// mattn/go-sqlite3 wraps errors as sqlite3.Error with Code field.
	// Check the error string for the code to avoid a direct dependency
	// on the sqlite3 package in non-CGO-importing code paths.
	msg := err.Error()
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked") ||
		strings.Contains(msg, "(5)") || // SQLITE_BUSY
		strings.Contains(msg, "(6)") // SQLITE_LOCKED
}

func (s *Store) configurePragmas(ctx context.Context) error {
	pragma := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=FULL;",
	}
	for _, q := range pragma {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("set pragma %q: %w", q, err)
		}
	}
	return nil
}

func (s *Store) initSchema(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			checksum TEXT NOT NULL,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	var maxVersion int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations;`).Scan(&maxVersion); err != nil {
		return fmt.Errorf("read migration max version: %w", err)
	}
	if maxVersion > schemaVersionLatest {
		return fmt.Errorf("db schema version %d is newer than supported %d", maxVersion, schemaVersionLatest)
	}

	// Known predecessor checksums that can be upgraded to current schema.
	knownPriorChecksumsV2 := map[string]bool{
		"gc-v1-2026-02-11-lease": true,
	}

	// If we're already at the latest schema, verify checksum and apply backfills only.
	if maxVersion == schemaVersionLatest {
		var existingChecksum string
		if err := tx.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE version = ?;`, schemaVersionLatest).Scan(&existingChecksum); err != nil {
			return fmt.Errorf("read schema migration checksum: %w", err)
		}
		if existingChecksum != schemaChecksumLatest {
			return fmt.Errorf("schema checksum mismatch for version %d: got %q want %q", schemaVersionLatest, existingChecksum, schemaChecksumLatest)
		}
		if err := s.applyBackfillsTx(ctx, tx); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration tx: %w", err)
		}
		return nil
	}

	// Upgrading from an earlier schema. Validate the checksum for the maxVersion we are upgrading from.
	versionChecksums := []struct {
		version  int
		checksum string
	}{
		{schemaVersionV2, schemaChecksumV2},
		{schemaVersionV3, schemaChecksumV3},
		{schemaVersionV4, schemaChecksumV4},
		{schemaVersionV5, schemaChecksumV5},
		{schemaVersionV6, schemaChecksumV6},
		{schemaVersionV7, schemaChecksumV7},
		{schemaVersionV8, schemaChecksumV8},
		{schemaVersionV9, schemaChecksumV9},
	}
	matched := false
	for _, vc := range versionChecksums {
		if maxVersion != vc.version {
			continue
		}
		matched = true
		var existingChecksum string
		if err := tx.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE version = ?;`, vc.version).Scan(&existingChecksum); err != nil {
			return fmt.Errorf("read schema migration checksum: %w", err)
		}
		if existingChecksum != vc.checksum && !knownPriorChecksumsV2[existingChecksum] {
			return fmt.Errorf("schema checksum mismatch for version %d: got %q want %q", vc.version, existingChecksum, vc.checksum)
		}
		break
	}
	if !matched && maxVersion != 0 {
		return fmt.Errorf("db schema version %d is older than supported minimum %d", maxVersion, schemaVersionV2)
	}

	// Phase 1: Create tables (without indexes).
	// Table names and columns aligned to SPEC Section 6.1.
	tableStatements := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			soul_hash TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL REFERENCES sessions(id),
			agent_id TEXT NOT NULL DEFAULT 'default',
			role TEXT NOT NULL CHECK(role IN ('system', 'user', 'assistant', 'tool')),
			content TEXT NOT NULL,
			tokens INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			archived_at DATETIME
		);`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(id),
			type TEXT NOT NULL DEFAULT 'chat',
			status TEXT NOT NULL CHECK(status IN ('QUEUED', 'CLAIMED', 'RUNNING', 'RETRY_WAIT', 'SUCCEEDED', 'FAILED', 'CANCELED', 'DEAD_LETTER')),
			priority INTEGER NOT NULL DEFAULT 0,
			attempt INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 3,
			available_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			cancel_requested INTEGER NOT NULL DEFAULT 0,
			last_error_code TEXT,
			last_error_fingerprint TEXT,
			poison_count INTEGER NOT NULL DEFAULT 0,
			policy_version TEXT,
			lease_owner TEXT,
			lease_expires_at DATETIME,
			payload JSON NOT NULL,
			result JSON,
			error TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS task_events (
			event_id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL REFERENCES tasks(id),
			session_id TEXT NOT NULL REFERENCES sessions(id),
			run_id TEXT,
			trace_id TEXT,
			event_type TEXT NOT NULL,
			state_from TEXT,
			state_to TEXT NOT NULL,
			payload_json TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS kv_store (
			key TEXT PRIMARY KEY,
			value TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS tool_call_dedup (
			idempotency_key TEXT PRIMARY KEY,
			tool_name TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			side_effect_status TEXT NOT NULL CHECK(side_effect_status IN ('PENDING', 'SUCCEEDED', 'FAILED')),
			result_hash TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS skill_registry (
			skill_id TEXT PRIMARY KEY,
			version TEXT NOT NULL,
			abi_version TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active', 'quarantined')),
			source TEXT DEFAULT 'local',
			source_url TEXT DEFAULT '',
			ref TEXT DEFAULT '',
			installed_at DATETIME,
			last_fault_at DATETIME,
			fault_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS policy_versions (
			policy_version TEXT PRIMARY KEY,
			checksum TEXT NOT NULL,
			loaded_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			source TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS approvals (
			approval_id TEXT PRIMARY KEY,
			task_id TEXT,
			capability TEXT,
			resource TEXT,
			status TEXT NOT NULL DEFAULT 'PENDING',
			expires_at DATETIME,
			resolved_at DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS audit_log (
			audit_id INTEGER PRIMARY KEY AUTOINCREMENT,
			trace_id TEXT,
			subject TEXT,
			action TEXT NOT NULL,
			decision TEXT NOT NULL,
			reason TEXT,
			policy_version TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		// Schedules table for cron-triggered task creation.
		`CREATE TABLE IF NOT EXISTS schedules (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			cron_expr TEXT NOT NULL,
			payload JSON NOT NULL DEFAULT '{}',
			session_id TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			next_run_at DATETIME,
			last_run_at DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		// GC-SPEC-DATA-007: Redaction metadata proves sanitization without retaining secrets.
		`CREATE TABLE IF NOT EXISTS data_redactions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			entity_type TEXT NOT NULL,
			entity_id TEXT NOT NULL,
			field_name TEXT NOT NULL,
			redaction_reason TEXT NOT NULL,
			policy_version TEXT,
			redacted_by TEXT NOT NULL DEFAULT 'system',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		// v6: agents table for multi-agent support.
		`CREATE TABLE IF NOT EXISTS agents (
			agent_id TEXT PRIMARY KEY,
			display_name TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT 'google',
			model TEXT NOT NULL DEFAULT '',
			soul TEXT NOT NULL DEFAULT '',
			worker_count INTEGER NOT NULL DEFAULT 4,
			task_timeout_seconds INTEGER NOT NULL DEFAULT 600,
			max_queue_depth INTEGER NOT NULL DEFAULT 0,
			skills_filter TEXT NOT NULL DEFAULT '',
			policy_overrides TEXT NOT NULL DEFAULT '',
			api_key_env TEXT NOT NULL DEFAULT '',
			agent_emoji TEXT NOT NULL DEFAULT '',
			preferred_search TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'stopped', 'draining')),
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		// v7: agent_messages table for inter-agent messaging.
		`CREATE TABLE IF NOT EXISTS agent_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			from_agent TEXT NOT NULL,
			to_agent TEXT NOT NULL,
			content TEXT NOT NULL,
			read_at DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		// v9: Observability tables for metrics and activity logging.
		`CREATE TABLE IF NOT EXISTS task_metrics (
			task_id       TEXT PRIMARY KEY,
			agent_id      TEXT NOT NULL,
			session_id    TEXT NOT NULL,
			parent_task_id TEXT,
			created_at    DATETIME NOT NULL,
			started_at    DATETIME,
			completed_at  DATETIME,
			duration_ms   INTEGER,
			status        TEXT NOT NULL,
			prompt_tokens     INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens      INTEGER NOT NULL DEFAULT 0,
			estimated_cost_usd REAL NOT NULL DEFAULT 0.0,
			error_message TEXT,
			FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS agent_activity_log (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_id      TEXT NOT NULL,
			activity_type TEXT NOT NULL,
			task_id       TEXT,
			session_id    TEXT,
			details       TEXT NOT NULL DEFAULT '{}',
			created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS agent_collaboration_metrics (
			from_agent    TEXT NOT NULL,
			to_agent      TEXT NOT NULL,
			metric_type   TEXT NOT NULL,
			count         INTEGER NOT NULL DEFAULT 0,
			total_duration_ms INTEGER NOT NULL DEFAULT 0,
			last_updated  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(from_agent, to_agent, metric_type)
		);`,
		`CREATE TABLE IF NOT EXISTS task_context (
			task_root_id TEXT NOT NULL,
			key       TEXT NOT NULL,
			value     TEXT NOT NULL,
			UNIQUE(task_root_id, key),
			FOREIGN KEY (task_root_id) REFERENCES tasks(id) ON DELETE CASCADE
		);`,
	}

	for _, stmt := range tableStatements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec migration: %w", err)
		}
	}

	// Phase 2: Backfills (ALTER TABLE for legacy DBs) — must run before indexes.
	if err := s.applyBackfillsTx(ctx, tx); err != nil {
		return err
	}

	// Phase 2b: V9 migrations - add token tracking and observability columns (idempotent).
	// These are applied to existing tables, so we catch errors gracefully.
	v9Statements := []string{
		// Add token/cost tracking to tasks table (if not already present)
		"ALTER TABLE tasks ADD COLUMN parent_task_id TEXT",
		"ALTER TABLE tasks ADD COLUMN prompt_tokens INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE tasks ADD COLUMN completion_tokens INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE tasks ADD COLUMN total_tokens INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE tasks ADD COLUMN estimated_cost_usd REAL NOT NULL DEFAULT 0.0",
		"ALTER TABLE tasks ADD COLUMN agent_id TEXT",
	}
	for _, stmt := range v9Statements {
		_, _ = tx.ExecContext(ctx, stmt) // Idempotent: ignore "column already exists" errors
	}

	// Phase 3: Indexes (may reference columns added by backfills).
	indexStatements := []string{
		`CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_available ON tasks(status, available_at, priority, created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_lease_expires ON tasks(lease_expires_at);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id, id);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session_agent ON messages(session_id, agent_id, id);`,
		`CREATE INDEX IF NOT EXISTS idx_task_events_session_event_id ON task_events(session_id, event_id);`,
		`CREATE INDEX IF NOT EXISTS idx_task_events_task_event_id ON task_events(task_id, event_id);`,
		`CREATE INDEX IF NOT EXISTS idx_approvals_status ON approvals(status, expires_at);`,
		`CREATE INDEX IF NOT EXISTS idx_data_redactions_entity ON data_redactions(entity_type, entity_id);`,
		`CREATE INDEX IF NOT EXISTS idx_schedules_next_run ON schedules(enabled, next_run_at);`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_parent ON tasks(parent_task_id);`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_agent_status ON tasks(agent_id, status, available_at);`,
		`CREATE INDEX IF NOT EXISTS idx_agent_messages_to ON agent_messages(to_agent, read_at);`,
		`CREATE INDEX IF NOT EXISTS idx_agent_messages_from ON agent_messages(from_agent);`,
		// v9: Indexes for observability tables
		`CREATE INDEX IF NOT EXISTS idx_metrics_agent_time ON task_metrics(agent_id, completed_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_activity_agent_time ON agent_activity_log(agent_id, created_at DESC);`,
	}

	for _, stmt := range indexStatements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec migration index: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO schema_migrations (version, checksum)
		VALUES (?, ?);
	`, schemaVersionLatest, schemaChecksumLatest); err != nil {
		return fmt.Errorf("insert schema migration ledger: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration tx: %w", err)
	}

	// GC-SPEC-DATA-003: Emit migration audit event after successful migration.
	audit.Record("allow", "data.migration", "migration_applied", "",
		fmt.Sprintf("schema migrated from v%d to v%d (checksum %s)", maxVersion, schemaVersionLatest, schemaChecksumLatest))
	return nil
}

func (s *Store) applyBackfillsTx(ctx context.Context, tx *sql.Tx) error {
	// Detect legacy tasks table with old CHECK constraint (4-state model).
	// SQLite cannot ALTER CHECK constraints, so we must rebuild the table.
	var taskSQL string
	if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='tasks';`).Scan(&taskSQL); err == nil {
		if strings.Contains(taskSQL, "'PENDING'") && !strings.Contains(taskSQL, "'QUEUED'") {
			if err := s.rebuildLegacyTasksTx(ctx, tx); err != nil {
				return err
			}
			return nil
		}
	}

	// Repair broken FK references from a prior migration that used RENAME.
	// SQLite cascades RENAME TO into FK definitions in dependent tables.
	if err := s.repairBrokenFKsTx(ctx, tx); err != nil {
		return err
	}

	// Rename history -> messages (SPEC Section 6.1 / 12.3).
	var historyExists int
	_ = tx.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type='table' AND name='history';`).Scan(&historyExists)
	if historyExists == 1 {
		var messagesExists int
		_ = tx.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type='table' AND name='messages';`).Scan(&messagesExists)
		if messagesExists == 1 {
			// Phase 1 already created empty messages table — copy data and drop history.
			if _, err := tx.ExecContext(ctx, `INSERT INTO messages (id, session_id, role, content, tokens, created_at) SELECT id, session_id, role, content, tokens, created_at FROM history;`); err != nil {
				return fmt.Errorf("copy history to messages: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `DROP TABLE history;`); err != nil {
				return fmt.Errorf("drop legacy history table: %w", err)
			}
		} else {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE history RENAME TO messages;`); err != nil {
				return fmt.Errorf("rename history to messages: %w", err)
			}
		}
		// Drop old index (indexes survive RENAME but keep old name).
		_, _ = tx.ExecContext(ctx, `DROP INDEX IF EXISTS idx_history_session_id;`)
	}

	// Non-legacy path: add missing columns to existing tables.
	alterStatements := []struct {
		stmt string
		desc string
	}{
		{stmt: `ALTER TABLE tasks ADD COLUMN type TEXT NOT NULL DEFAULT 'chat';`, desc: "tasks.type"},
		{stmt: `ALTER TABLE tasks ADD COLUMN lease_owner TEXT;`, desc: "tasks.lease_owner"},
		{stmt: `ALTER TABLE tasks ADD COLUMN lease_expires_at DATETIME;`, desc: "tasks.lease_expires_at"},
		{stmt: `ALTER TABLE tasks ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0;`, desc: "tasks.attempt"},
		{stmt: `ALTER TABLE tasks ADD COLUMN max_attempts INTEGER NOT NULL DEFAULT 3;`, desc: "tasks.max_attempts"},
		{stmt: `ALTER TABLE tasks ADD COLUMN available_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP;`, desc: "tasks.available_at"},
		{stmt: `ALTER TABLE tasks ADD COLUMN last_error_code TEXT;`, desc: "tasks.last_error_code"},
		{stmt: `ALTER TABLE tasks ADD COLUMN last_error_fingerprint TEXT;`, desc: "tasks.last_error_fingerprint"},
		{stmt: `ALTER TABLE tasks ADD COLUMN poison_count INTEGER NOT NULL DEFAULT 0;`, desc: "tasks.poison_count"},
		{stmt: `ALTER TABLE tasks ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;`, desc: "tasks.priority"},
		{stmt: `ALTER TABLE tasks ADD COLUMN cancel_requested INTEGER NOT NULL DEFAULT 0;`, desc: "tasks.cancel_requested"},
		{stmt: `ALTER TABLE sessions ADD COLUMN updated_at DATETIME;`, desc: "sessions.updated_at"},
		{stmt: `ALTER TABLE messages ADD COLUMN archived_at DATETIME;`, desc: "messages.archived_at"},
		{stmt: `ALTER TABLE task_events ADD COLUMN trace_id TEXT;`, desc: "task_events.trace_id"},
		{stmt: `ALTER TABLE tasks ADD COLUMN policy_version TEXT;`, desc: "tasks.policy_version"},
		// TODO Phase 3.3: skill provenance columns.
		{stmt: `ALTER TABLE skill_registry ADD COLUMN source TEXT DEFAULT 'local';`, desc: "skill_registry.source"},
		{stmt: `ALTER TABLE skill_registry ADD COLUMN source_url TEXT DEFAULT '';`, desc: "skill_registry.source_url"},
		{stmt: `ALTER TABLE skill_registry ADD COLUMN ref TEXT DEFAULT '';`, desc: "skill_registry.ref"},
		{stmt: `ALTER TABLE skill_registry ADD COLUMN installed_at DATETIME;`, desc: "skill_registry.installed_at"},
		// v5: parent_task_id for subtask delegation.
		{stmt: `ALTER TABLE tasks ADD COLUMN parent_task_id TEXT;`, desc: "tasks.parent_task_id"},
		// v6: agent_id for multi-agent support.
		{stmt: `ALTER TABLE tasks ADD COLUMN agent_id TEXT NOT NULL DEFAULT 'default';`, desc: "tasks.agent_id"},
		// v6: additional agent config fields for restore fidelity.
		{stmt: `ALTER TABLE agents ADD COLUMN agent_emoji TEXT NOT NULL DEFAULT '';`, desc: "agents.agent_emoji"},
		{stmt: `ALTER TABLE agents ADD COLUMN preferred_search TEXT NOT NULL DEFAULT '';`, desc: "agents.preferred_search"},
		// v8: per-agent history isolation.
		{stmt: `ALTER TABLE messages ADD COLUMN agent_id TEXT NOT NULL DEFAULT 'default';`, desc: "messages.agent_id"},
	}
	for _, a := range alterStatements {
		if _, err := tx.ExecContext(ctx, a.stmt); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("add %s: %w", a.desc, err)
		}
	}
	// AUD-011: skill_registry.state CHECK constraint.
	// New DBs get the CHECK via CREATE TABLE. SQLite does not support
	// ALTER TABLE ADD CONSTRAINT, so existing DBs rely on application-level
	// validation (UpsertSkill/IncrementSkillFault/ReenableSkill only write
	// 'active' or 'quarantined').

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS tool_call_dedup (
			idempotency_key TEXT PRIMARY KEY,
			tool_name TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			side_effect_status TEXT NOT NULL CHECK(side_effect_status IN ('PENDING', 'SUCCEEDED', 'FAILED')),
			result_hash TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		return fmt.Errorf("create tool_call_dedup: %w", err)
	}
	return nil
}

// rebuildLegacyTasksTx recreates the tasks table with the v1 8-state schema,
// and rebuilds task_events to avoid the SQLite FK cascade issue where RENAME
// propagates into foreign key definitions of dependent tables.
func (s *Store) rebuildLegacyTasksTx(ctx context.Context, tx *sql.Tx) error {
	stateMap := map[string]string{
		"PENDING":   "QUEUED",
		"RUNNING":   "RUNNING",
		"COMPLETED": "SUCCEEDED",
		"FAILED":    "FAILED",
	}

	// Step 1: Create new tasks table under a temp name (avoids FK cascade).
	if _, err := tx.ExecContext(ctx, `DROP INDEX IF EXISTS idx_tasks_status;`); err != nil {
		return fmt.Errorf("drop legacy tasks index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE _tasks_v1 (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(id),
			type TEXT NOT NULL DEFAULT 'chat',
			status TEXT NOT NULL CHECK(status IN ('QUEUED', 'CLAIMED', 'RUNNING', 'RETRY_WAIT', 'SUCCEEDED', 'FAILED', 'CANCELED', 'DEAD_LETTER')),
			attempt INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 3,
			available_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_error_code TEXT,
			last_error_fingerprint TEXT,
			poison_count INTEGER NOT NULL DEFAULT 0,
			lease_owner TEXT,
			lease_expires_at DATETIME,
			payload JSON NOT NULL,
			result JSON,
			error TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		return fmt.Errorf("create _tasks_v1: %w", err)
	}

	// Step 2: Copy data with state mapping.
	caseParts := make([]string, 0, len(stateMap))
	for old, new_ := range stateMap {
		caseParts = append(caseParts, fmt.Sprintf("WHEN '%s' THEN '%s'", old, new_))
	}
	caseExpr := "CASE status " + strings.Join(caseParts, " ") + " ELSE 'FAILED' END"
	copySQL := fmt.Sprintf(`
		INSERT INTO _tasks_v1 (id, session_id, status, payload, result, error, created_at, updated_at)
		SELECT id, session_id, %s, payload, result, error, created_at, updated_at
		FROM tasks;
	`, caseExpr)
	if _, err := tx.ExecContext(ctx, copySQL); err != nil {
		return fmt.Errorf("copy legacy tasks: %w", err)
	}

	// Step 3: Drop old table, rename new. The DROP removes the FK target
	// and RENAME won't affect task_events since it pointed at the old 'tasks'.
	if _, err := tx.ExecContext(ctx, `DROP TABLE tasks;`); err != nil {
		return fmt.Errorf("drop old tasks: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE _tasks_v1 RENAME TO tasks;`); err != nil {
		return fmt.Errorf("rename _tasks_v1 to tasks: %w", err)
	}

	// Step 4: Rebuild task_events to ensure FK references 'tasks' correctly.
	if err := s.rebuildTaskEventsFKTx(ctx, tx); err != nil {
		return err
	}

	return nil
}

// repairBrokenFKsTx detects and fixes task_events tables with broken FK
// references (e.g. pointing to "_tasks_legacy" instead of "tasks").
func (s *Store) repairBrokenFKsTx(ctx context.Context, tx *sql.Tx) error {
	var eventsSQL string
	err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='task_events';`).Scan(&eventsSQL)
	if err != nil {
		return nil // table doesn't exist yet, nothing to repair
	}
	if !strings.Contains(eventsSQL, "_tasks_legacy") {
		return nil // FK is fine
	}
	return s.rebuildTaskEventsFKTx(ctx, tx)
}

// rebuildTaskEventsFKTx recreates task_events with correct FK to tasks.
func (s *Store) rebuildTaskEventsFKTx(ctx context.Context, tx *sql.Tx) error {
	// Check if task_events exists.
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name='task_events';`).Scan(&count); err != nil || count == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DROP INDEX IF EXISTS idx_task_events_session_event_id;`); err != nil {
		return fmt.Errorf("drop task_events index 1: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP INDEX IF EXISTS idx_task_events_task_event_id;`); err != nil {
		return fmt.Errorf("drop task_events index 2: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE task_events RENAME TO _task_events_old;`); err != nil {
		return fmt.Errorf("rename task_events: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE task_events (
			event_id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL REFERENCES tasks(id),
			session_id TEXT NOT NULL REFERENCES sessions(id),
			run_id TEXT,
			event_type TEXT NOT NULL,
			state_from TEXT,
			state_to TEXT NOT NULL,
			payload_json TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`); err != nil {
		return fmt.Errorf("create new task_events: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO task_events (event_id, task_id, session_id, run_id, event_type, state_from, state_to, payload_json, created_at)
		SELECT event_id, task_id, session_id, run_id, event_type, state_from, state_to, payload_json, created_at
		FROM _task_events_old;
	`); err != nil {
		return fmt.Errorf("copy task_events: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE _task_events_old;`); err != nil {
		return fmt.Errorf("drop _task_events_old: %w", err)
	}
	return nil
}

func canTransition(from, to TaskStatus) bool {
	next, ok := allowedTransitions[from]
	if !ok {
		return false
	}
	_, ok = next[to]
	return ok
}

func scanTask(scanFn func(dest ...any) error, task *Task) error {
	var leaseExpires sql.NullTime
	var lastErrorCode sql.NullString
	if err := scanFn(
		&task.ID,
		&task.SessionID,
		&task.Type,
		&task.Status,
		&task.Attempt,
		&task.MaxAttempts,
		&task.AvailableAt,
		&lastErrorCode,
		&task.PoisonCount,
		&task.Payload,
		&task.Result,
		&task.Error,
		&task.LeaseOwner,
		&leaseExpires,
		&task.CreatedAt,
		&task.UpdatedAt,
		&task.AgentID,
	); err != nil {
		return err
	}
	if leaseExpires.Valid {
		t := leaseExpires.Time
		task.LeaseExpiresAt = &t
	} else {
		task.LeaseExpiresAt = nil
	}
	if lastErrorCode.Valid {
		task.LastErrorCode = lastErrorCode.String
	}
	return nil
}

func (s *Store) appendTaskEventTx(ctx context.Context, tx *sql.Tx, taskID, sessionID string, from, to TaskStatus, eventType, payload string) error {
	if payload == "" {
		payload = "{}"
	}
	// GC-SPEC-RUN-004: Use trace_id from context, fall back to session_id.
	traceID := shared.TraceID(ctx)
	if traceID == "-" {
		traceID = sessionID
	}
	// GC-SPEC-RUN-004: Propagate run_id into task events.
	runID := shared.RunID(ctx)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO task_events (task_id, session_id, run_id, trace_id, event_type, state_from, state_to, payload_json, created_at)
		VALUES (?, ?, NULLIF(?, ''), ?, ?, NULLIF(?, ''), ?, ?, CURRENT_TIMESTAMP);
	`, taskID, sessionID, runID, traceID, eventType, string(from), string(to), payload)
	if err != nil {
		return fmt.Errorf("insert task_event: %w", err)
	}
	return nil
}

func (s *Store) transitionTaskTx(
	ctx context.Context,
	tx *sql.Tx,
	taskID string,
	allowedFrom []TaskStatus,
	to TaskStatus,
	eventType string,
	payload string,
	result *string,
	errMsg *string,
) (bool, error) {
	var current TaskStatus
	var sessionID string
	if err := tx.QueryRowContext(ctx, `
		SELECT status, session_id
		FROM tasks
		WHERE id = ?;
	`, taskID).Scan(&current, &sessionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("select task for transition: %w", err)
	}
	if !slices.Contains(allowedFrom, current) {
		return false, nil
	}
	if !canTransition(current, to) {
		return false, fmt.Errorf("illegal transition %s -> %s", current, to)
	}

	resValue := sql.NullString{}
	if result != nil {
		resValue.Valid = true
		resValue.String = *result
	}
	errValue := sql.NullString{}
	if errMsg != nil {
		errValue.Valid = true
		errValue.String = *errMsg
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?,
			result = CASE WHEN ? THEN ? ELSE result END,
			error = CASE WHEN ? THEN ? ELSE error END,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND status = ?;
	`, to, resValue.Valid, resValue.String, errValue.Valid, errValue.String, taskID, current)
	if err != nil {
		return false, fmt.Errorf("update task transition: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("transition rows affected: %w", err)
	}
	if affected != 1 {
		return false, nil
	}
	if err := s.appendTaskEventTx(ctx, tx, taskID, sessionID, current, to, eventType, payload); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) EnsureSession(ctx context.Context, sessionID string) error {
	if _, err := uuid.Parse(sessionID); err != nil {
		return fmt.Errorf("invalid session_id: %w", err)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, created_at)
		VALUES (?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO NOTHING;
	`, sessionID)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (s *Store) AddHistory(ctx context.Context, sessionID, agentID, role, content string, tokens int) error {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "system", "user", "assistant", "tool":
	default:
		return fmt.Errorf("invalid role %q", role)
	}
	if agentID == "" {
		agentID = "default"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (session_id, agent_id, role, content, tokens, created_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP);
	`, sessionID, agentID, role, content, tokens)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}
	return nil
}

func (s *Store) ListHistory(ctx context.Context, sessionID, agentID string, limit int) ([]HistoryItem, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var rows *sql.Rows
	var err error
	if agentID != "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, session_id, role, content, tokens, created_at
			FROM messages
			WHERE session_id = ? AND agent_id = ? AND archived_at IS NULL
			ORDER BY id ASC
			LIMIT ?;
		`, sessionID, agentID, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, session_id, role, content, tokens, created_at
			FROM messages
			WHERE session_id = ? AND archived_at IS NULL
			ORDER BY id ASC
			LIMIT ?;
		`, sessionID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var out []HistoryItem
	for rows.Next() {
		var item HistoryItem
		if err := rows.Scan(&item.ID, &item.SessionID, &item.Role, &item.Content, &item.Tokens, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		item.Text = item.Content // GC-SPEC-ACP-009: populate alias.
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("message rows: %w", err)
	}
	return out, nil
}

func (s *Store) ArchiveMessages(ctx context.Context, sessionID string, beforeID int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE messages
		SET archived_at = CURRENT_TIMESTAMP
		WHERE session_id = ? AND id <= ? AND archived_at IS NULL;
	`, sessionID, beforeID)
	if err != nil {
		return fmt.Errorf("archive messages: %w", err)
	}
	return nil
}

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
				"task_id":   taskID,
				"session_id": task.SessionID,
				"status":    TaskStatusSucceeded,
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
				"task_id":   taskID,
				"session_id": task.SessionID,
				"error":     errMsg,
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
				"task_id":   taskID,
				"session_id": task.SessionID,
				"reason":    "abort_request",
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

// ListSkills returns all registered WASM skills from the skill_registry.
func (s *Store) ListSkills(ctx context.Context) ([]SkillRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT skill_id, version, abi_version, COALESCE(state, 'active'), fault_count
		FROM skill_registry
		WHERE COALESCE(abi_version, '') != 'n/a'
		ORDER BY skill_id ASC;
	`)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	defer rows.Close()

	var result []SkillRecord
	for rows.Next() {
		var r SkillRecord
		if err := rows.Scan(&r.SkillID, &r.Version, &r.ABIVersion, &r.State, &r.FaultCount); err != nil {
			return nil, fmt.Errorf("scan skill: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// SkillRecord holds fields returned from the skill_registry table.
type SkillRecord struct {
	SkillID    string
	Version    string
	ABIVersion string
	State      string
	FaultCount int
}

// InstalledSkillRecord holds provenance fields for a skill installed from an external source.
type InstalledSkillRecord struct {
	SkillID     string
	Source      string
	SourceURL   string
	Ref         string
	InstalledAt *time.Time
}

// DefaultQuarantineThreshold is the fault count that triggers auto-quarantine (GC-SPEC-SKL-007).
const DefaultQuarantineThreshold = 5

// UpsertSkill registers or updates a skill in the skill_registry.
func (s *Store) UpsertSkill(ctx context.Context, skillID, version, abiVersion, contentHash string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO skill_registry (skill_id, version, abi_version, content_hash, state, fault_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'active', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(skill_id) DO UPDATE SET
			version = excluded.version,
			abi_version = excluded.abi_version,
			content_hash = excluded.content_hash,
			state = 'active',
			fault_count = 0,
			updated_at = CURRENT_TIMESTAMP;
	`, skillID, version, abiVersion, contentHash)
	if err != nil {
		return fmt.Errorf("upsert skill: %w", err)
	}
	return nil
}

// RegisterInstalledSkill records an installed skill's provenance in skill_registry (TODO Phase 3.3).
// This does not grant any capabilities; policy remains default-deny unless explicitly configured.
func (s *Store) RegisterInstalledSkill(ctx context.Context, skillID, source, sourceURL, ref string) error {
	if strings.TrimSpace(skillID) == "" {
		return fmt.Errorf("empty skillID")
	}
	if strings.TrimSpace(source) == "" {
		source = "local"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO skill_registry (skill_id, version, abi_version, content_hash, state, source, source_url, ref, installed_at, created_at, updated_at)
		VALUES (?, 'installed', 'n/a', '', 'active', ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(skill_id) DO UPDATE SET
			source = excluded.source,
			source_url = excluded.source_url,
			ref = excluded.ref,
			installed_at = excluded.installed_at,
			updated_at = CURRENT_TIMESTAMP;
	`, skillID, source, sourceURL, ref)
	if err != nil {
		return fmt.Errorf("register installed skill: %w", err)
	}
	return nil
}

// ListInstalledSkills returns all records registered by the installer (TODO Phase 3.3).
func (s *Store) ListInstalledSkills(ctx context.Context) ([]InstalledSkillRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT skill_id, COALESCE(source, ''), COALESCE(source_url, ''), COALESCE(ref, ''), installed_at
		FROM skill_registry
		WHERE installed_at IS NOT NULL
		ORDER BY skill_id ASC;
	`)
	if err != nil {
		return nil, fmt.Errorf("list installed skills: %w", err)
	}
	defer rows.Close()

	var out []InstalledSkillRecord
	for rows.Next() {
		var r InstalledSkillRecord
		var nt sql.NullTime
		if err := rows.Scan(&r.SkillID, &r.Source, &r.SourceURL, &r.Ref, &nt); err != nil {
			return nil, fmt.Errorf("scan installed skill: %w", err)
		}
		if nt.Valid {
			t := nt.Time
			r.InstalledAt = &t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RemoveInstalledSkill removes the installer provenance record (TODO Phase 3.3).
func (s *Store) RemoveInstalledSkill(ctx context.Context, skillID string) error {
	if strings.TrimSpace(skillID) == "" {
		return fmt.Errorf("empty skillID")
	}
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM skill_registry
		WHERE skill_id = ? AND installed_at IS NOT NULL;
	`, skillID)
	if err != nil {
		return fmt.Errorf("remove installed skill: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("installed skill not found: %s", skillID)
	}
	return nil
}

// IncrementSkillFault increments the fault count for a skill and auto-quarantines
// if the threshold is exceeded (GC-SPEC-SKL-007).
// Returns true if the skill was quarantined by this call.
func (s *Store) IncrementSkillFault(ctx context.Context, skillID string, threshold int) (quarantined bool, err error) {
	if threshold <= 0 {
		threshold = DefaultQuarantineThreshold
	}
	// AUD-010: Use RETURNING to atomically get the post-update state,
	// eliminating the TOCTOU race between UPDATE and SELECT.
	var state string
	err = s.db.QueryRowContext(ctx, `
		UPDATE skill_registry
		SET fault_count = fault_count + 1,
			last_fault_at = CURRENT_TIMESTAMP,
			state = CASE WHEN fault_count + 1 >= ? THEN 'quarantined' ELSE state END,
			updated_at = CURRENT_TIMESTAMP
		WHERE skill_id = ?
		RETURNING state;
	`, threshold, skillID).Scan(&state)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("increment skill fault: %w", err)
	}
	return state == "quarantined", nil
}

// IsSkillQuarantined checks if a skill is quarantined (GC-SPEC-SKL-007).
func (s *Store) IsSkillQuarantined(ctx context.Context, skillID string) (bool, error) {
	var state string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(state, 'active') FROM skill_registry WHERE skill_id = ?;`, skillID).Scan(&state)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil // Unknown skill = not quarantined
		}
		return false, fmt.Errorf("check skill quarantine: %w", err)
	}
	return state == "quarantined", nil
}

// ReenableSkill resets a quarantined skill to active with zero fault count (GC-SPEC-SKL-007).
func (s *Store) ReenableSkill(ctx context.Context, skillID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE skill_registry
		SET state = 'active', fault_count = 0, updated_at = CURRENT_TIMESTAMP
		WHERE skill_id = ?;
	`, skillID)
	if err != nil {
		return fmt.Errorf("reenable skill: %w", err)
	}
	return nil
}

// DataRedaction represents a record proving that a field was redacted (GC-SPEC-DATA-007).
type DataRedaction struct {
	ID              int       `json:"id"`
	EntityType      string    `json:"entity_type"`
	EntityID        string    `json:"entity_id"`
	FieldName       string    `json:"field_name"`
	RedactionReason string    `json:"redaction_reason"`
	PolicyVersion   string    `json:"policy_version,omitempty"`
	RedactedBy      string    `json:"redacted_by"`
	CreatedAt       time.Time `json:"created_at"`
}

// RecordRedaction inserts a redaction metadata record (GC-SPEC-DATA-007).
func (s *Store) RecordRedaction(ctx context.Context, entityType, entityID, fieldName, reason, policyVersion, redactedBy string) error {
	if redactedBy == "" {
		redactedBy = "system"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO data_redactions (entity_type, entity_id, field_name, redaction_reason, policy_version, redacted_by)
		VALUES (?, ?, ?, ?, ?, ?);
	`, entityType, entityID, fieldName, reason, policyVersion, redactedBy)
	if err != nil {
		return fmt.Errorf("record redaction: %w", err)
	}
	return nil
}

// ListRedactions returns redaction metadata for the given entity (GC-SPEC-DATA-007).
func (s *Store) ListRedactions(ctx context.Context, entityType, entityID string) ([]DataRedaction, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, entity_type, entity_id, field_name, redaction_reason,
		       COALESCE(policy_version, ''), redacted_by, created_at
		FROM data_redactions
		WHERE entity_type = ? AND entity_id = ?
		ORDER BY created_at ASC;
	`, entityType, entityID)
	if err != nil {
		return nil, fmt.Errorf("list redactions: %w", err)
	}
	defer rows.Close()

	var result []DataRedaction
	for rows.Next() {
		var r DataRedaction
		if err := rows.Scan(&r.ID, &r.EntityType, &r.EntityID, &r.FieldName,
			&r.RedactionReason, &r.PolicyVersion, &r.RedactedBy, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan redaction: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// PurgeResult holds counts from a PII purge operation.
type PurgeResult struct {
	MessagesDeleted    int64 `json:"messages_deleted"`
	TaskPayloadsTombed int64 `json:"task_payloads_tombstoned"`
	TaskEventsTombed   int64 `json:"task_events_tombstoned"`
	RedactionsRecorded int   `json:"redactions_recorded"`
}

// PurgeSessionPII removes or tombstones PII-bearing records for a session (GC-SPEC-DATA-006).
// Messages are deleted. Task payloads and results are replaced with [REDACTED].
// Redaction metadata is recorded per GC-SPEC-DATA-007.
func (s *Store) PurgeSessionPII(ctx context.Context, sessionID, policyVersion, actor string) (PurgeResult, error) {
	var result PurgeResult
	if actor == "" {
		actor = "system"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin purge tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 1. Delete messages and record redaction metadata.
	var msgIDs []string
	rows, err := tx.QueryContext(ctx, `SELECT id FROM messages WHERE session_id = ?;`, sessionID)
	if err != nil {
		return result, fmt.Errorf("select messages for purge: %w", err)
	}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return result, fmt.Errorf("scan message id: %w", err)
		}
		msgIDs = append(msgIDs, strconv.Itoa(id))
	}
	rows.Close()

	res, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE session_id = ?;`, sessionID)
	if err != nil {
		return result, fmt.Errorf("delete messages: %w", err)
	}
	result.MessagesDeleted, _ = res.RowsAffected()

	for _, mid := range msgIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO data_redactions (entity_type, entity_id, field_name, redaction_reason, policy_version, redacted_by)
			VALUES ('message', ?, 'content', 'pii_purge', ?, ?);
		`, mid, policyVersion, actor); err != nil {
			return result, fmt.Errorf("record message redaction: %w", err)
		}
		result.RedactionsRecorded++
	}

	// 2. Tombstone task payloads and results.
	res, err = tx.ExecContext(ctx, `
		UPDATE tasks
		SET payload = '[REDACTED]',
		    result = CASE WHEN result != '' THEN '[REDACTED]' ELSE result END,
		    updated_at = CURRENT_TIMESTAMP
		WHERE session_id = ?;
	`, sessionID)
	if err != nil {
		return result, fmt.Errorf("tombstone task payloads: %w", err)
	}
	result.TaskPayloadsTombed, _ = res.RowsAffected()

	// Record redaction for each tombstoned task.
	taskRows, err := tx.QueryContext(ctx, `SELECT id FROM tasks WHERE session_id = ?;`, sessionID)
	if err != nil {
		return result, fmt.Errorf("select tasks for redaction records: %w", err)
	}
	var taskIDs []string
	for taskRows.Next() {
		var tid string
		if err := taskRows.Scan(&tid); err != nil {
			taskRows.Close()
			return result, fmt.Errorf("scan task id: %w", err)
		}
		taskIDs = append(taskIDs, tid)
	}
	taskRows.Close()

	for _, tid := range taskIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO data_redactions (entity_type, entity_id, field_name, redaction_reason, policy_version, redacted_by)
			VALUES ('task', ?, 'payload', 'pii_purge', ?, ?);
		`, tid, policyVersion, actor); err != nil {
			return result, fmt.Errorf("record task redaction: %w", err)
		}
		result.RedactionsRecorded++
	}

	// 3. Tombstone task_events payload_json.
	res, err = tx.ExecContext(ctx, `
		UPDATE task_events
		SET payload_json = '[REDACTED]'
		WHERE session_id = ?;
	`, sessionID)
	if err != nil {
		return result, fmt.Errorf("tombstone task_events: %w", err)
	}
	result.TaskEventsTombed, _ = res.RowsAffected()

	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit purge tx: %w", err)
	}
	return result, nil
}

// --- Schedule CRUD (cron scheduler support) ---

// Schedule represents a cron-triggered task template.
type Schedule struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	CronExpr  string     `json:"cron_expr"`
	Payload   string     `json:"payload"`
	SessionID string     `json:"session_id"`
	Enabled   bool       `json:"enabled"`
	NextRunAt *time.Time `json:"next_run_at,omitempty"`
	LastRunAt *time.Time `json:"last_run_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// InsertSchedule creates a new cron schedule.
func (s *Store) InsertSchedule(ctx context.Context, sched Schedule) error {
	if sched.ID == "" {
		sched.ID = uuid.NewString()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO schedules (id, name, cron_expr, payload, session_id, enabled, next_run_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
	`, sched.ID, sched.Name, sched.CronExpr, sched.Payload, sched.SessionID, boolToInt(sched.Enabled), sched.NextRunAt)
	if err != nil {
		return fmt.Errorf("insert schedule: %w", err)
	}
	return nil
}

// DeleteSchedule removes a schedule by ID.
func (s *Store) DeleteSchedule(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM schedules WHERE id = ?;`, id)
	if err != nil {
		return fmt.Errorf("delete schedule: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("schedule not found: %s", id)
	}
	return nil
}

// ListSchedules returns all schedules ordered by name.
func (s *Store) ListSchedules(ctx context.Context) ([]Schedule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, cron_expr, payload, session_id, enabled, next_run_at, last_run_at, created_at, updated_at
		FROM schedules ORDER BY name ASC;
	`)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		var sc Schedule
		var enabled int
		var nextRun, lastRun sql.NullTime
		if err := rows.Scan(&sc.ID, &sc.Name, &sc.CronExpr, &sc.Payload, &sc.SessionID, &enabled, &nextRun, &lastRun, &sc.CreatedAt, &sc.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan schedule: %w", err)
		}
		sc.Enabled = enabled != 0
		if nextRun.Valid {
			t := nextRun.Time
			sc.NextRunAt = &t
		}
		if lastRun.Valid {
			t := lastRun.Time
			sc.LastRunAt = &t
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// UpdateScheduleRun updates the last_run_at and next_run_at for a schedule after firing.
func (s *Store) UpdateScheduleRun(ctx context.Context, id string, lastRun, nextRun time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE schedules SET last_run_at = ?, next_run_at = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;
	`, lastRun, nextRun, id)
	if err != nil {
		return fmt.Errorf("update schedule run: %w", err)
	}
	return nil
}

// EnableSchedule sets a schedule's enabled flag.
func (s *Store) EnableSchedule(ctx context.Context, id string, enabled bool) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE schedules SET enabled = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;
	`, boolToInt(enabled), id)
	if err != nil {
		return fmt.Errorf("enable schedule: %w", err)
	}
	return nil
}

// DueSchedules returns enabled schedules with next_run_at <= now.
func (s *Store) DueSchedules(ctx context.Context, now time.Time) ([]Schedule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, cron_expr, payload, session_id, enabled, next_run_at, last_run_at, created_at, updated_at
		FROM schedules WHERE enabled = 1 AND next_run_at <= ?
		ORDER BY next_run_at ASC;
	`, now)
	if err != nil {
		return nil, fmt.Errorf("due schedules: %w", err)
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		var sc Schedule
		var enabled int
		var nextRun, lastRun sql.NullTime
		if err := rows.Scan(&sc.ID, &sc.Name, &sc.CronExpr, &sc.Payload, &sc.SessionID, &enabled, &nextRun, &lastRun, &sc.CreatedAt, &sc.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan due schedule: %w", err)
		}
		sc.Enabled = enabled != 0
		if nextRun.Valid {
			t := nextRun.Time
			sc.NextRunAt = &t
		}
		if lastRun.Valid {
			t := lastRun.Time
			sc.LastRunAt = &t
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
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

// --- Agent CRUD (multi-agent support) ---

// CreateAgent persists a new agent record to the agents table.
func (s *Store) CreateAgent(ctx context.Context, rec AgentRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agents (agent_id, display_name, provider, model, soul, worker_count,
			task_timeout_seconds, max_queue_depth, skills_filter, policy_overrides,
			api_key_env, agent_emoji, preferred_search, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
	`, rec.AgentID, rec.DisplayName, rec.Provider, rec.Model, rec.Soul, rec.WorkerCount,
		rec.TaskTimeoutSeconds, rec.MaxQueueDepth, rec.SkillsFilter, rec.PolicyOverrides,
		rec.APIKeyEnv, rec.AgentEmoji, rec.PreferredSearch, rec.Status)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	return nil
}

// GetAgent returns the agent record for the given ID, or nil if not found.
func (s *Store) GetAgent(ctx context.Context, agentID string) (*AgentRecord, error) {
	var rec AgentRecord
	err := s.db.QueryRowContext(ctx, `
		SELECT agent_id, display_name, provider, model, soul, worker_count,
			task_timeout_seconds, max_queue_depth, skills_filter, policy_overrides,
			api_key_env, agent_emoji, preferred_search, status, created_at, updated_at
		FROM agents WHERE agent_id = ?;
	`, agentID).Scan(&rec.AgentID, &rec.DisplayName, &rec.Provider, &rec.Model, &rec.Soul,
		&rec.WorkerCount, &rec.TaskTimeoutSeconds, &rec.MaxQueueDepth, &rec.SkillsFilter,
		&rec.PolicyOverrides, &rec.APIKeyEnv, &rec.AgentEmoji, &rec.PreferredSearch,
		&rec.Status, &rec.CreatedAt, &rec.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get agent: %w", err)
	}
	return &rec, nil
}

// ListAgents returns all agent records ordered by creation time.
func (s *Store) ListAgents(ctx context.Context) ([]AgentRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT agent_id, display_name, provider, model, soul, worker_count,
			task_timeout_seconds, max_queue_depth, skills_filter, policy_overrides,
			api_key_env, agent_emoji, preferred_search, status, created_at, updated_at
		FROM agents ORDER BY created_at ASC;
	`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()
	var out []AgentRecord
	for rows.Next() {
		var rec AgentRecord
		if err := rows.Scan(&rec.AgentID, &rec.DisplayName, &rec.Provider, &rec.Model, &rec.Soul,
			&rec.WorkerCount, &rec.TaskTimeoutSeconds, &rec.MaxQueueDepth, &rec.SkillsFilter,
			&rec.PolicyOverrides, &rec.APIKeyEnv, &rec.AgentEmoji, &rec.PreferredSearch,
			&rec.Status, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list agents: iterate: %w", err)
	}
	return out, nil
}

// UpdateAgentStatus sets the status field for the given agent (e.g. "active", "stopped").
func (s *Store) UpdateAgentStatus(ctx context.Context, agentID, status string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE agents SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE agent_id = ?;
	`, status, agentID)
	if err != nil {
		return fmt.Errorf("update agent status: %w", err)
	}
	n, rowsErr := res.RowsAffected()
	if rowsErr != nil {
		return fmt.Errorf("update agent status: rows affected: %w", rowsErr)
	}
	if n == 0 {
		return fmt.Errorf("agent %q not found", agentID)
	}
	return nil
}

// DeleteAgent removes an agent and its inter-agent messages in a single transaction.
func (s *Store) DeleteAgent(ctx context.Context, agentID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete agent: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `DELETE FROM agents WHERE agent_id = ?;`, agentID)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	n, rowsErr := res.RowsAffected()
	if rowsErr != nil {
		return fmt.Errorf("delete agent: rows affected: %w", rowsErr)
	}
	if n == 0 {
		return fmt.Errorf("agent %q not found", agentID)
	}

	// Clean up inter-agent messages to/from the deleted agent.
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_messages WHERE from_agent = ? OR to_agent = ?;`, agentID, agentID); err != nil {
		return fmt.Errorf("delete agent messages: %w", err)
	}

	// Cancel orphaned tasks (QUEUED or CLAIMED) belonging to the deleted agent.
	// Running tasks are left to their engine's drain/timeout. Terminal tasks are kept for audit.
	if _, err := tx.ExecContext(ctx, `
		UPDATE tasks SET status = ?, error = 'agent_deleted', last_error_code = ?,
			updated_at = CURRENT_TIMESTAMP, lease_owner = NULL, lease_expires_at = NULL
		WHERE agent_id = ? AND status IN (?, ?);
	`, TaskStatusCanceled, ReasonCanceled, agentID, TaskStatusQueued, TaskStatusClaimed); err != nil {
		return fmt.Errorf("cancel orphaned tasks: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete agent: commit: %w", err)
	}
	return nil
}

// CreateTaskForAgent creates a new task scoped to the specified agent.
func (s *Store) CreateTaskForAgent(ctx context.Context, agentID, sessionID, payload string) (string, error) {
	return s.createTask(ctx, agentID, sessionID, payload)
}

// ClaimNextPendingTaskForAgent claims the highest-priority pending task for the given agent.
func (s *Store) ClaimNextPendingTaskForAgent(ctx context.Context, agentID string) (*Task, error) {
	return s.claimNextPendingTask(ctx, agentID)
}

// QueueDepthForAgent returns the number of queued tasks for a specific agent.
func (s *Store) QueueDepthForAgent(ctx context.Context, agentID string) (int, error) {
	var pending int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM tasks WHERE status=? AND agent_id=?;`, TaskStatusQueued, agentID).Scan(&pending); err != nil {
		return 0, fmt.Errorf("queue depth for agent: %w", err)
	}
	return pending, nil
}

// AgentMessage represents a row in the agent_messages table.
type AgentMessage struct {
	ID        int64     `json:"id"`
	FromAgent string    `json:"from_agent"`
	ToAgent   string    `json:"to_agent"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// SendAgentMessage stores a message from one agent to another.
func (s *Store) SendAgentMessage(ctx context.Context, from, to, content string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_messages (from_agent, to_agent, content) VALUES (?, ?, ?);
	`, from, to, content)
	if err != nil {
		return fmt.Errorf("send agent message: %w", err)
	}
	return nil
}

// ReadAgentMessages returns unread messages for an agent and marks them as read.
// Uses a transaction to prevent duplicate delivery under concurrent access.
func (s *Store) ReadAgentMessages(ctx context.Context, agentID string, limit int) ([]AgentMessage, error) {
	if limit <= 0 {
		limit = 10
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("read agent messages: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, from_agent, to_agent, content, created_at
		FROM agent_messages
		WHERE to_agent = ? AND read_at IS NULL
		ORDER BY created_at ASC
		LIMIT ?;
	`, agentID, limit)
	if err != nil {
		return nil, fmt.Errorf("read agent messages: %w", err)
	}
	defer rows.Close()

	var msgs []AgentMessage
	var idArgs []any
	for rows.Next() {
		var m AgentMessage
		if err := rows.Scan(&m.ID, &m.FromAgent, &m.ToAgent, &m.Content, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan agent message: %w", err)
		}
		msgs = append(msgs, m)
		idArgs = append(idArgs, m.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent messages: %w", err)
	}

	// Mark as read within the same transaction using parameterized query.
	if len(idArgs) > 0 {
		placeholders := strings.Repeat("?,", len(idArgs))
		placeholders = placeholders[:len(placeholders)-1] // trim trailing comma
		query := `UPDATE agent_messages SET read_at = CURRENT_TIMESTAMP WHERE id IN (` + placeholders + `);`
		if _, err := tx.ExecContext(ctx, query, idArgs...); err != nil {
			return nil, fmt.Errorf("mark messages read: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("read agent messages: commit: %w", err)
	}

	return msgs, nil
}

// PeekAgentMessages returns the count of unread messages for an agent.
func (s *Store) PeekAgentMessages(ctx context.Context, agentID string) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM agent_messages WHERE to_agent = ? AND read_at IS NULL;
	`, agentID).Scan(&count); err != nil {
		return 0, fmt.Errorf("peek agent messages: %w", err)
	}
	return count, nil
}

// TotalAgentMessageCount returns the total number of inter-agent messages (GC-SPEC-OBS-004).
func (s *Store) TotalAgentMessageCount(ctx context.Context) (int64, error) {
	var count int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM agent_messages;
	`).Scan(&count); err != nil {
		return 0, fmt.Errorf("total agent message count: %w", err)
	}
	return count, nil
}

// TotalDelegationCount returns the total number of delegate_task invocations recorded in the audit log.
func (s *Store) TotalDelegationCount(ctx context.Context) (int64, error) {
	var count int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM audit_log WHERE action='tools.delegate_task' AND reason='task_delegated';
	`).Scan(&count); err != nil {
		return 0, fmt.Errorf("total delegation count: %w", err)
	}
	return count, nil
}

// IncidentBundle is a bounded run bundle for offline debugging (GC-SPEC-OBS-006).
type IncidentBundle struct {
	TaskID     string       `json:"task_id"`
	Task       *Task        `json:"task"`
	Events     []TaskEvent  `json:"events"`
	AuditTrail []AuditEntry `json:"audit_trail"`
	ConfigHash string       `json:"config_hash"`
	ExportedAt time.Time    `json:"exported_at"`
}

// AuditEntry represents a row from the audit_log table.
type AuditEntry struct {
	AuditID       int64     `json:"audit_id"`
	TraceID       string    `json:"trace_id"`
	Subject       string    `json:"subject"`
	Action        string    `json:"action"`
	Decision      string    `json:"decision"`
	Reason        string    `json:"reason"`
	PolicyVersion string    `json:"policy_version"`
	CreatedAt     time.Time `json:"created_at"`
}

// ExportIncident bundles task events + redacted audit entries + config hash
// for a given task into an IncidentBundle (GC-SPEC-OBS-006).
func (s *Store) ExportIncident(ctx context.Context, taskID, configHash string) (*IncidentBundle, error) {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("export incident: get task: %w", err)
	}

	// Fetch all task events for this task.
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, task_id, session_id, event_type,
			COALESCE(run_id, ''), COALESCE(trace_id, session_id),
			COALESCE(state_from, ''), COALESCE(state_to, ''), COALESCE(payload_json, ''), created_at
		FROM task_events
		WHERE task_id = ?
		ORDER BY event_id ASC
		LIMIT 1000;
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("export incident: list events: %w", err)
	}
	defer rows.Close()
	var events []TaskEvent
	for rows.Next() {
		var ev TaskEvent
		if err := rows.Scan(&ev.EventID, &ev.TaskID, &ev.SessionID, &ev.EventType,
			&ev.RunID, &ev.TraceID, &ev.StateFrom, &ev.StateTo, &ev.Payload, &ev.CreatedAt); err != nil {
			return nil, fmt.Errorf("export incident: scan event: %w", err)
		}
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("export incident: rows: %w", err)
	}

	// Collect unique trace IDs from the events to query related audit entries.
	traceIDs := make(map[string]struct{})
	for _, ev := range events {
		if ev.TraceID != "" {
			traceIDs[ev.TraceID] = struct{}{}
		}
	}

	var auditTrail []AuditEntry
	for tid := range traceIDs {
		aRows, err := s.db.QueryContext(ctx, `
			SELECT audit_id, COALESCE(trace_id, ''), COALESCE(subject, ''),
				action, decision, COALESCE(reason, ''), COALESCE(policy_version, ''), created_at
			FROM audit_log
			WHERE trace_id = ?
			ORDER BY audit_id ASC
			LIMIT 200;
		`, tid)
		if err != nil {
			continue
		}
		for aRows.Next() {
			var ae AuditEntry
			if err := aRows.Scan(&ae.AuditID, &ae.TraceID, &ae.Subject,
				&ae.Action, &ae.Decision, &ae.Reason, &ae.PolicyVersion, &ae.CreatedAt); err != nil {
				continue
			}
			auditTrail = append(auditTrail, ae)
		}
		aRows.Close()
	}

	return &IncidentBundle{
		TaskID:     taskID,
		Task:       task,
		Events:     events,
		AuditTrail: auditTrail,
		ConfigHash: configHash,
		ExportedAt: time.Now().UTC(),
	}, nil
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
