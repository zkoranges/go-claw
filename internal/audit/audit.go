package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/basket/go-claw/internal/shared"
)

type entry struct {
	Timestamp     string `json:"timestamp"`
	Decision      string `json:"decision"`
	Capability    string `json:"capability"`
	Reason        string `json:"reason"`
	PolicyVersion string `json:"policy_version"`
	Subject       string `json:"subject,omitempty"`
}

var (
	mu        sync.Mutex
	file      *os.File
	db        *sql.DB
	denyCount atomic.Int64
)

func Init(homeDir string) error {
	mu.Lock()
	defer mu.Unlock()
	if file != nil {
		return nil
	}
	logDir := filepath.Join(homeDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(logDir, "audit.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	file = f
	return nil
}

// SetDB configures the database for audit_log table writes (GC-SPEC-OBS-003).
func SetDB(d *sql.DB) {
	mu.Lock()
	defer mu.Unlock()
	db = d
}

func Close() error {
	mu.Lock()
	defer mu.Unlock()
	if file == nil {
		return nil
	}
	err := file.Close()
	file = nil
	return err
}

// DenyCount returns the total number of deny decisions since startup.
func DenyCount() int64 {
	return denyCount.Load()
}

func Record(decision, capability, reason, policyVersion, subject string) {
	if decision == "deny" {
		denyCount.Add(1)
	}

	// GC-SPEC-SEC-005: Redact secrets before persistence.
	reason = shared.Redact(reason)
	subject = shared.Redact(subject)

	mu.Lock()
	defer mu.Unlock()

	// Write to JSONL file.
	if file != nil {
		ev := entry{
			Timestamp:     time.Now().UTC().Format(time.RFC3339Nano),
			Decision:      decision,
			Capability:    capability,
			Reason:        reason,
			PolicyVersion: policyVersion,
			Subject:       subject,
		}
		b, err := json.Marshal(ev)
		if err == nil {
			_, _ = file.Write(append(b, '\n'))
		}
	}

	// Write to audit_log table (GC-SPEC-OBS-003).
	if db != nil {
		_, _ = db.ExecContext(context.Background(), `
			INSERT INTO audit_log (trace_id, subject, action, decision, reason, policy_version)
			VALUES (?, ?, ?, ?, ?, ?);
		`, "", subject, capability, decision, reason, policyVersion)
	}
}
