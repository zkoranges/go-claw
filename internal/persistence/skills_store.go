package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

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
