package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

	// v0.3 schema v10: adds team_plans and team_plan_steps for team workflows (PDR Phase 4).
	schemaVersionV10  = 10
	schemaChecksumV10 = "gc-v10-2026-02-15-team-workflows"

	// v0.4 schema v11: adds experiments and experiment_samples for A/B testing and analytics (PDR Phase 5).
	schemaVersionV11  = 11
	schemaChecksumV11 = "gc-v11-2026-02-15-experiments-analytics"

	// v0.1.5 schema v12: adds plan_executions tables for crash recovery (Stabilization Sprint Phase 1).
	schemaVersionV12  = 12
	schemaChecksumV12 = "gc-v12-2026-02-15-plan-persistence"

	// v0.4 schema v13: adds delegations table for async inter-agent delegation (PDR v7 Phase 2).
	schemaVersionV13  = 13
	schemaChecksumV13 = "gc-v13-2026-02-16-async-delegation"

	// v0.5 schema v14: adds loop_checkpoints table for agent loop persistence (PDR v8 Phase 2).
	schemaVersionV14  = 14
	schemaChecksumV14 = "gc-v14-2026-02-16-loop-checkpoints"

	schemaVersionLatest  = schemaVersionV14
	schemaChecksumLatest = schemaChecksumV14

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
		{schemaVersionV10, schemaChecksumV10},
		{schemaVersionV11, schemaChecksumV11},
		{schemaVersionV12, schemaChecksumV12},
		{schemaVersionV13, schemaChecksumV13},
		{schemaVersionV14, schemaChecksumV14},
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
		// v13: delegations table for async inter-agent delegation (PDR v7 Phase 2).
		`CREATE TABLE IF NOT EXISTS delegations (
			id            TEXT PRIMARY KEY,
			task_id       TEXT,
			parent_agent  TEXT NOT NULL,
			child_agent   TEXT NOT NULL,
			prompt        TEXT NOT NULL,
			status        TEXT NOT NULL DEFAULT 'queued',
			result        TEXT,
			error_msg     TEXT,
			created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
			completed_at  DATETIME,
			injected      INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE INDEX IF NOT EXISTS idx_deleg_parent_pending
			ON delegations(parent_agent, injected) WHERE status IN ('completed','failed') AND injected = 0;`,
		`CREATE INDEX IF NOT EXISTS idx_deleg_task
			ON delegations(task_id);`,
		// v13: Agent memories with relevance scoring.
		`CREATE TABLE IF NOT EXISTS agent_memories (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_id        TEXT    NOT NULL,
			key             TEXT    NOT NULL,
			value           TEXT    NOT NULL,
			source          TEXT    DEFAULT 'user',
			relevance_score REAL    DEFAULT 1.0,
			access_count    INTEGER DEFAULT 0,
			created_at      TEXT    NOT NULL DEFAULT (datetime('now')),
			updated_at      TEXT    NOT NULL DEFAULT (datetime('now')),
			last_accessed   TEXT    NOT NULL DEFAULT (datetime('now')),
			UNIQUE(agent_id, key)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_agent_memories_agent ON agent_memories(agent_id);`,
		`CREATE INDEX IF NOT EXISTS idx_agent_memories_relevance ON agent_memories(agent_id, relevance_score DESC);`,
		// v14: Agent pinned files and text for context injection.
		`CREATE TABLE IF NOT EXISTS agent_pins (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_id    TEXT    NOT NULL,
			pin_type    TEXT    NOT NULL,
			source      TEXT    NOT NULL,
			content     TEXT    NOT NULL,
			token_count INTEGER DEFAULT 0,
			shared      INTEGER DEFAULT 0,
			last_read   TEXT    NOT NULL DEFAULT (datetime('now')),
			file_mtime  TEXT    DEFAULT '',
			created_at  TEXT    NOT NULL DEFAULT (datetime('now')),
			UNIQUE(agent_id, source)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_agent_pins_agent ON agent_pins(agent_id);`,
		`CREATE INDEX IF NOT EXISTS idx_agent_pins_shared ON agent_pins(shared) WHERE shared = 1;`,
		// v15: Agent shares for cross-agent knowledge access.
		`CREATE TABLE IF NOT EXISTS agent_shares (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			source_agent_id TEXT NOT NULL,
			target_agent_id TEXT NOT NULL,
			share_type      TEXT NOT NULL,
			item_key        TEXT DEFAULT '',
			created_at      TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE(source_agent_id, target_agent_id, share_type, item_key)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_agent_shares_target ON agent_shares(target_agent_id);`,
		// v14: Loop checkpoints for agent loop persistence (PDR v8 Phase 2).
		`CREATE TABLE IF NOT EXISTS loop_checkpoints (
			loop_id      TEXT PRIMARY KEY,
			task_id      TEXT NOT NULL,
			agent_id     TEXT NOT NULL,
			current_step INTEGER NOT NULL DEFAULT 0,
			max_steps    INTEGER NOT NULL DEFAULT 0,
			tokens_used  INTEGER NOT NULL DEFAULT 0,
			max_tokens   INTEGER NOT NULL DEFAULT 0,
			started_at   DATETIME NOT NULL,
			max_duration INTEGER NOT NULL DEFAULT 0,
			status       TEXT NOT NULL DEFAULT 'running',
			messages     TEXT NOT NULL DEFAULT '[]',
			created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP
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

	// v10: Team workflow plans and execution steps
	v10Statements := []string{
		`CREATE TABLE IF NOT EXISTS team_plans (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			execution_strategy TEXT NOT NULL CHECK(execution_strategy IN ('sequential', 'parallel', 'round_robin')),
			max_retries INTEGER NOT NULL DEFAULT 3,
			session_id TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(session_id) REFERENCES sessions(id)
		);`,
		`CREATE TABLE IF NOT EXISTS team_plan_steps (
			id TEXT PRIMARY KEY,
			plan_id TEXT NOT NULL,
			step_index INTEGER NOT NULL,
			agent_id TEXT NOT NULL,
			prompt TEXT NOT NULL,
			status TEXT NOT NULL CHECK(status IN ('pending', 'running', 'succeeded', 'failed')) DEFAULT 'pending',
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(plan_id) REFERENCES team_plans(id),
			UNIQUE(plan_id, step_index)
		);`,
	}
	for _, stmt := range v10Statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil && !strings.Contains(err.Error(), "already exists") {
			// Create tables if they don't exist, ignore "table already exists" errors
			_, _ = tx.ExecContext(ctx, stmt)
		}
	}

	// v11: Experiments and samples for A/B testing and analytics.
	v11Statements := []string{
		`CREATE TABLE IF NOT EXISTS experiments (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			status TEXT NOT NULL CHECK(status IN ('planning', 'running', 'completed', 'canceled')) DEFAULT 'planning',
			hypothesis TEXT,
			control_agent TEXT NOT NULL,
			treatment_agent TEXT,
			session_id TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			completed_at TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(session_id) REFERENCES sessions(id)
		);`,
		`CREATE TABLE IF NOT EXISTS experiment_samples (
			id TEXT PRIMARY KEY,
			experiment_id TEXT NOT NULL,
			variant TEXT NOT NULL CHECK(variant IN ('control', 'treatment')),
			task_id TEXT,
			success INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER,
			cost_usd REAL NOT NULL DEFAULT 0.0,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(experiment_id) REFERENCES experiments(id),
			FOREIGN KEY(task_id) REFERENCES tasks(id)
		);`,
	}
	for _, stmt := range v11Statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil && !strings.Contains(err.Error(), "already exists") {
			// Create tables if they don't exist, ignore "table already exists" errors
			_, _ = tx.ExecContext(ctx, stmt)
		}
	}

	// v12: Plan executions for crash recovery.
	v12Statements := []string{
		`CREATE TABLE IF NOT EXISTS plan_executions (
			id TEXT PRIMARY KEY,
			plan_name TEXT NOT NULL,
			session_id TEXT NOT NULL REFERENCES sessions(id),
			status TEXT NOT NULL CHECK(status IN ('running', 'succeeded', 'failed', 'canceled')) DEFAULT 'running',
			total_steps INTEGER NOT NULL,
			completed_steps INTEGER NOT NULL DEFAULT 0,
			current_wave INTEGER NOT NULL DEFAULT 0,
			total_cost_usd REAL NOT NULL DEFAULT 0.0,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			completed_at TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS plan_execution_steps (
			id TEXT PRIMARY KEY,
			execution_id TEXT NOT NULL REFERENCES plan_executions(id) ON DELETE CASCADE,
			step_id TEXT NOT NULL,
			step_index INTEGER NOT NULL,
			wave_number INTEGER NOT NULL,
			agent_id TEXT NOT NULL,
			prompt TEXT NOT NULL,
			task_id TEXT REFERENCES tasks(id) ON DELETE SET NULL,
			status TEXT NOT NULL CHECK(status IN ('pending', 'running', 'succeeded', 'failed')) DEFAULT 'pending',
			result TEXT,
			error TEXT,
			cost_usd REAL NOT NULL DEFAULT 0.0,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			completed_at TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
	}
	for _, stmt := range v12Statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec v12 migration: %w", err)
		}
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
		// v10: Indexes for team plans
		`CREATE INDEX IF NOT EXISTS idx_team_plans_session ON team_plans(session_id, created_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_team_plan_steps_plan ON team_plan_steps(plan_id, step_index);`,
		`CREATE INDEX IF NOT EXISTS idx_team_plan_steps_status ON team_plan_steps(status, created_at DESC);`,
		// v11: Indexes for experiments
		`CREATE INDEX IF NOT EXISTS idx_experiments_session ON experiments(session_id, created_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_experiments_status ON experiments(status, created_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_experiment_samples_experiment ON experiment_samples(experiment_id, variant);`,
		`CREATE INDEX IF NOT EXISTS idx_experiment_samples_task ON experiment_samples(task_id);`,
		// v12: Indexes for plan executions
		`CREATE INDEX IF NOT EXISTS idx_plan_executions_session ON plan_executions(session_id, created_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_plan_executions_status ON plan_executions(status, updated_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_plan_steps_execution ON plan_execution_steps(execution_id, step_index);`,
		`CREATE INDEX IF NOT EXISTS idx_plan_steps_wave ON plan_execution_steps(execution_id, wave_number, status);`,
		`CREATE INDEX IF NOT EXISTS idx_plan_steps_task ON plan_execution_steps(task_id);`,
		// v14: Indexes for loop checkpoints
		`CREATE INDEX IF NOT EXISTS idx_loop_checkpoints_task ON loop_checkpoints(task_id);`,
		`CREATE INDEX IF NOT EXISTS idx_loop_checkpoints_status ON loop_checkpoints(status);`,
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

// TeamPlan represents a multi-agent workflow plan (PDR Phase 4).
type TeamPlan struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Description       string    `json:"description"`
	ExecutionStrategy string    `json:"execution_strategy"` // sequential, parallel, round_robin
	MaxRetries        int       `json:"max_retries"`
	SessionID         string    `json:"session_id"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// TeamPlanStep represents a single step in a team plan.
type TeamPlanStep struct {
	ID        string    `json:"id"`
	PlanID    string    `json:"plan_id"`
	StepIndex int       `json:"step_index"`
	AgentID   string    `json:"agent_id"`
	Prompt    string    `json:"prompt"`
	Status    string    `json:"status"` // pending, running, succeeded, failed
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateTeamPlan creates a new team plan.
func (s *Store) CreateTeamPlan(ctx context.Context, plan *TeamPlan) error {
	plan.ID = uuid.NewString()
	plan.CreatedAt = time.Now()
	plan.UpdatedAt = plan.CreatedAt
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO team_plans (id, name, description, execution_strategy, max_retries, session_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		plan.ID, plan.Name, plan.Description, plan.ExecutionStrategy, plan.MaxRetries, plan.SessionID, plan.CreatedAt, plan.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create team plan: %w", err)
	}
	return nil
}

// ListTeamPlansBySession lists all team plans for a session.
func (s *Store) ListTeamPlansBySession(ctx context.Context, sessionID string) ([]*TeamPlan, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, description, execution_strategy, max_retries, session_id, created_at, updated_at
		FROM team_plans
		WHERE session_id = ?
		ORDER BY created_at DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query team plans: %w", err)
	}
	defer rows.Close()

	var plans []*TeamPlan
	for rows.Next() {
		var p TeamPlan
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.ExecutionStrategy, &p.MaxRetries, &p.SessionID, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan team plan: %w", err)
		}
		plans = append(plans, &p)
	}
	return plans, rows.Err()
}

// GetTeamPlanSteps retrieves all steps for a team plan.
func (s *Store) GetTeamPlanSteps(ctx context.Context, planID string) ([]*TeamPlanStep, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, plan_id, step_index, agent_id, prompt, status, created_at, updated_at
		FROM team_plan_steps
		WHERE plan_id = ?
		ORDER BY step_index ASC`, planID)
	if err != nil {
		return nil, fmt.Errorf("query team plan steps: %w", err)
	}
	defer rows.Close()

	var steps []*TeamPlanStep
	for rows.Next() {
		var s TeamPlanStep
		if err := rows.Scan(&s.ID, &s.PlanID, &s.StepIndex, &s.AgentID, &s.Prompt, &s.Status, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan team plan step: %w", err)
		}
		steps = append(steps, &s)
	}
	return steps, rows.Err()
}

// Experiment represents an A/B test or experiment (PDR Phase 5).
type Experiment struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	Status         string     `json:"status"` // planning, running, completed, canceled
	Hypothesis     string     `json:"hypothesis"`
	ControlAgent   string     `json:"control_agent"`
	TreatmentAgent string     `json:"treatment_agent"`
	SessionID      string     `json:"session_id"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// ExperimentSample represents a single sample/trial in an experiment.
type ExperimentSample struct {
	ID           string    `json:"id"`
	ExperimentID string    `json:"experiment_id"`
	Variant      string    `json:"variant"` // control or treatment
	TaskID       string    `json:"task_id"`
	Success      int       `json:"success"` // 0 or 1
	DurationMs   int       `json:"duration_ms"`
	CostUSD      float64   `json:"cost_usd"`
	CreatedAt    time.Time `json:"created_at"`
}

// CreateExperiment creates a new experiment.
func (s *Store) CreateExperiment(ctx context.Context, exp *Experiment) error {
	exp.ID = uuid.NewString()
	exp.CreatedAt = time.Now()
	exp.UpdatedAt = exp.CreatedAt
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO experiments (id, name, description, status, hypothesis, control_agent, treatment_agent, session_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		exp.ID, exp.Name, exp.Description, exp.Status, exp.Hypothesis, exp.ControlAgent, exp.TreatmentAgent, exp.SessionID, exp.CreatedAt, exp.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create experiment: %w", err)
	}
	return nil
}

// RecordExperimentSample records a trial outcome for an experiment.
func (s *Store) RecordExperimentSample(ctx context.Context, sample *ExperimentSample) error {
	sample.ID = uuid.NewString()
	sample.CreatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO experiment_samples (id, experiment_id, variant, task_id, success, duration_ms, cost_usd, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sample.ID, sample.ExperimentID, sample.Variant, sample.TaskID, sample.Success, sample.DurationMs, sample.CostUSD, sample.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("record experiment sample: %w", err)
	}
	return nil
}

// ListExperimentsBySession lists all experiments for a session.
func (s *Store) ListExperimentsBySession(ctx context.Context, sessionID string) ([]*Experiment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, description, status, hypothesis, control_agent, treatment_agent, session_id, created_at, completed_at, updated_at
		FROM experiments
		WHERE session_id = ?
		ORDER BY created_at DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query experiments: %w", err)
	}
	defer rows.Close()

	var exps []*Experiment
	for rows.Next() {
		var e Experiment
		if err := rows.Scan(&e.ID, &e.Name, &e.Description, &e.Status, &e.Hypothesis, &e.ControlAgent, &e.TreatmentAgent, &e.SessionID, &e.CreatedAt, &e.CompletedAt, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan experiment: %w", err)
		}
		exps = append(exps, &e)
	}
	return exps, rows.Err()
}

// GetExperimentSamples retrieves all samples for an experiment.
func (s *Store) GetExperimentSamples(ctx context.Context, expID string) ([]*ExperimentSample, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, experiment_id, variant, task_id, success, duration_ms, cost_usd, created_at
		FROM experiment_samples
		WHERE experiment_id = ?
		ORDER BY created_at ASC`, expID)
	if err != nil {
		return nil, fmt.Errorf("query experiment samples: %w", err)
	}
	defer rows.Close()

	var samples []*ExperimentSample
	for rows.Next() {
		var s ExperimentSample
		if err := rows.Scan(&s.ID, &s.ExperimentID, &s.Variant, &s.TaskID, &s.Success, &s.DurationMs, &s.CostUSD, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan experiment sample: %w", err)
		}
		samples = append(samples, &s)
	}
	return samples, rows.Err()
}

// PlanExecution represents a running or completed plan.
// GC-SPEC-PDR-v4-Phase-3: Plan execution tracking.
type PlanExecution struct {
	ID             string
	PlanName       string
	SessionID      string
	Status         string
	TotalSteps     int
	CompletedSteps int
	CurrentWave    int
	TotalCostUSD   float64
	CreatedAt      time.Time
	CompletedAt    *time.Time
	UpdatedAt      *time.Time
}

// CreatePlanExecution records the start of a plan execution.
func (s *Store) CreatePlanExecution(ctx context.Context, id, planName, sessionID string, totalSteps int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO plan_executions (id, plan_name, session_id, status, total_steps, created_at, updated_at)
		VALUES (?, ?, ?, 'running', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		id, planName, sessionID, totalSteps,
	)
	if err != nil {
		return fmt.Errorf("insert plan_execution: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	if s.bus != nil {
		s.bus.Publish(bus.TopicPlanExecutionStarted, map[string]interface{}{
			"execution_id": id,
			"plan_name":    planName,
			"session_id":   sessionID,
			"total_steps":  totalSteps,
		})
	}
	return nil
}

// CompletePlanExecution marks a plan execution as finished.
// The totalCostUSD parameter is accepted for API compatibility but ignored;
// actual cost is recalculated from plan_execution_steps for crash-recovery consistency.
func (s *Store) CompletePlanExecution(ctx context.Context, id, status string, totalCostUSD float64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Calculate total cost from steps for consistency (handles crash recovery).
	var actualCost float64
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(cost_usd), 0) FROM plan_execution_steps WHERE execution_id = ?`,
		id,
	).Scan(&actualCost)
	if err != nil {
		return fmt.Errorf("calculate cost: %w", err)
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE plan_executions
		SET status = ?, total_cost_usd = ?, completed_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		status, actualCost, id,
	)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}

	if rows, _ := res.RowsAffected(); rows == 0 {
		return fmt.Errorf("plan_execution %s not found", id)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	if s.bus != nil {
		s.bus.Publish(bus.TopicPlanExecutionCompleted, map[string]interface{}{
			"execution_id": id,
			"status":       status,
			"cost":         actualCost,
		})
	}
	return nil
}

// InitializePlanSteps creates step records for all steps in a plan execution.
// GC-SPEC-PDR-v4-Phase-2: Step persistence initialization.
func (s *Store) InitializePlanSteps(ctx context.Context, execID string, steps []PlanExecutionStep) error {
	if len(steps) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO plan_execution_steps
		(id, execution_id, step_id, step_index, wave_number, agent_id, prompt, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		return fmt.Errorf("prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, step := range steps {
		stepRecordID := uuid.New().String()
		_, err := stmt.ExecContext(ctx,
			stepRecordID, execID, step.StepID, step.StepIndex, step.WaveNumber,
			step.AgentID, step.Prompt,
		)
		if err != nil {
			return fmt.Errorf("insert step: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// UpdatePlanWave updates the current wave number after wave completion.
// GC-SPEC-PDR-v4-Phase-2: Wave tracking for resumption.
func (s *Store) UpdatePlanWave(ctx context.Context, execID string, waveNum int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE plan_executions
		SET current_wave = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		waveNum, execID,
	)
	if err != nil {
		return fmt.Errorf("update wave: %w", err)
	}
	return nil
}

// GetPlanExecution retrieves a plan execution for hydration during resumption.
// GC-SPEC-PDR-v4-Phase-3: Plan state recovery.
func (s *Store) GetPlanExecution(ctx context.Context, execID string) (*PlanExecution, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, plan_name, session_id, status, total_steps, completed_steps, current_wave,
		       total_cost_usd, created_at, completed_at, updated_at
		FROM plan_executions
		WHERE id = ?`,
		execID,
	)

	var exec PlanExecution
	err := row.Scan(
		&exec.ID, &exec.PlanName, &exec.SessionID, &exec.Status,
		&exec.TotalSteps, &exec.CompletedSteps, &exec.CurrentWave,
		&exec.TotalCostUSD, &exec.CreatedAt, &exec.CompletedAt, &exec.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan plan_execution: %w", err)
	}

	return &exec, nil
}

// GetPlanSteps retrieves all steps for a plan execution, ordered by wave and index.
// GC-SPEC-PDR-v4-Phase-3: Step recovery for resumption.
func (s *Store) GetPlanSteps(ctx context.Context, execID string) ([]PlanExecutionStep, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, execution_id, step_id, step_index, wave_number, agent_id, prompt, task_id,
		       status, result, error, cost_usd, created_at, completed_at
		FROM plan_execution_steps
		WHERE execution_id = ?
		ORDER BY wave_number ASC, step_index ASC`,
		execID,
	)
	if err != nil {
		return nil, fmt.Errorf("query steps: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var steps []PlanExecutionStep
	for rows.Next() {
		var step PlanExecutionStep
		var taskID sql.NullString
		var result sql.NullString
		var errMsg sql.NullString
		var completedAt sql.NullTime
		err := rows.Scan(
			&step.ID, &step.ExecutionID, &step.StepID, &step.StepIndex, &step.WaveNumber,
			&step.AgentID, &step.Prompt, &taskID, &step.Status, &result, &errMsg,
			&step.CostUSD, &step.CreatedAt, &completedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan step: %w", err)
		}
		step.TaskID = taskID.String
		step.Result = result.String
		step.Error = errMsg.String
		if completedAt.Valid {
			step.CompletedAt = &completedAt.Time
		}
		steps = append(steps, step)
	}

	return steps, rows.Err()
}

// RecoverRunningPlans finds plans in 'running' state for crash recovery.
func (s *Store) RecoverRunningPlans(ctx context.Context) ([]PlanExecutionRecovery, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, plan_name, session_id, current_wave, completed_steps, total_steps
		FROM plan_executions
		WHERE status = 'running'
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var recoveries []PlanExecutionRecovery
	for rows.Next() {
		var r PlanExecutionRecovery
		if err := rows.Scan(&r.ID, &r.PlanName, &r.SessionID, &r.CurrentWave, &r.CompletedSteps, &r.TotalSteps); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		recoveries = append(recoveries, r)
	}
	return recoveries, rows.Err()
}

// RecordStepComplete updates step status and increments completed_steps.
// GC-SPEC-PDR-v4-Phase-2: Persistent step tracking with event publishing.
func (s *Store) RecordStepComplete(ctx context.Context, execID, stepID, status, result, errMsg string, costUSD float64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
		UPDATE plan_execution_steps
		SET status = ?, result = ?, error = ?, cost_usd = ?, completed_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE execution_id = ? AND step_id = ?`,
		status, result, errMsg, costUSD, execID, stepID,
	)
	if err != nil {
		return fmt.Errorf("update step: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE plan_executions
		SET completed_steps = completed_steps + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		execID,
	)
	if err != nil {
		return fmt.Errorf("increment completed_steps: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	// Publish step completion event
	if s.bus != nil {
		s.bus.Publish(bus.TopicPlanStepCompleted, map[string]interface{}{
			"execution_id": execID,
			"step_id":      stepID,
			"status":       status,
			"result":       result,
			"error":        errMsg,
			"cost_usd":     costUSD,
		})
	}

	return nil
}

// Bus returns the event bus for publishing.
func (s *Store) Bus() *bus.Bus {
	return s.bus
}

// PlanExecutionRecovery holds data to resume crashed plans.
type PlanExecutionRecovery struct {
	ID             string
	PlanName       string
	SessionID      string
	CurrentWave    int
	CompletedSteps int
	TotalSteps     int
}

// PlanExecutionStep represents a single step in a plan execution.
type PlanExecutionStep struct {
	ID          string
	ExecutionID string
	StepID      string
	StepIndex   int
	WaveNumber  int
	AgentID     string
	Prompt      string
	TaskID      string
	Status      string
	Result      string
	Error       string
	CostUSD     float64
	CreatedAt   time.Time
	CompletedAt *time.Time
}
