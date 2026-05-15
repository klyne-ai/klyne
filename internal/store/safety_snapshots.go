package store

import (
	"context"
	"fmt"
	"time"
)

// SafetySnapshot is one row in the safety_snapshots table (migration 013).
//
// Each row records a risky-command interception: the command that matched,
// the policy pattern that fired, and the git stash SHA (or fallback directory)
// that captured the working-tree state at the moment of interception.
type SafetySnapshot struct {
	ID          int64  `json:"id"`
	Ts          int64  `json:"ts"`
	SessionID   string `json:"session_id,omitempty"`
	CWD         string `json:"cwd,omitempty"`
	Command     string `json:"command"`
	PatternID   string `json:"pattern_id"`
	Severity    string `json:"severity"`
	StashSHA    string `json:"stash_sha,omitempty"`
	FallbackDir string `json:"fallback_dir,omitempty"`
	FileCount   int    `json:"file_count"`
}

// SafetySnapshotFilter scopes ListSafetySnapshots queries.
type SafetySnapshotFilter struct {
	SessionID string
	Limit     int
}

// InsertSafetySnapshot writes a new safety_snapshots row and sets s.ID to
// the auto-assigned row id.
func InsertSafetySnapshot(ctx context.Context, db *DB, s *SafetySnapshot) error {
	if s.Command == "" {
		return fmt.Errorf("store: safety snapshot command required")
	}
	if s.PatternID == "" {
		return fmt.Errorf("store: safety snapshot pattern_id required")
	}
	if s.Ts <= 0 {
		s.Ts = time.Now().UnixMilli()
	}
	const q = `
INSERT INTO safety_snapshots
    (ts, session_id, cwd, command, pattern_id, severity, stash_sha, fallback_dir, file_count)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := db.Write().ExecContext(ctx, q,
		s.Ts, s.SessionID, s.CWD, s.Command, s.PatternID, s.Severity,
		s.StashSHA, s.FallbackDir, s.FileCount,
	)
	if err != nil {
		return fmt.Errorf("store: insert safety_snapshot: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: safety_snapshot last insert id: %w", err)
	}
	s.ID = id
	return nil
}

// GetSafetySnapshot fetches one row by its integer id.
// Returns sql.ErrNoRows (wrapped) when no row matches.
func GetSafetySnapshot(ctx context.Context, db *DB, id int64) (*SafetySnapshot, error) {
	const q = `
SELECT id, ts, session_id, cwd, command, pattern_id, severity, stash_sha, fallback_dir, file_count
FROM safety_snapshots WHERE id = ?`
	row := db.Read().QueryRowContext(ctx, q, id)
	s := &SafetySnapshot{}
	err := row.Scan(
		&s.ID, &s.Ts, &s.SessionID, &s.CWD, &s.Command,
		&s.PatternID, &s.Severity, &s.StashSHA, &s.FallbackDir, &s.FileCount,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get safety_snapshot %d: %w", id, err)
	}
	return s, nil
}

// ListSafetySnapshots returns snapshots ordered by ts DESC. Limit defaults
// to 50 when zero.
func ListSafetySnapshots(ctx context.Context, db *DB, f SafetySnapshotFilter) ([]SafetySnapshot, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT id, ts, session_id, cwd, command, pattern_id, severity, stash_sha, fallback_dir, file_count
          FROM safety_snapshots WHERE 1=1`
	args := make([]any, 0, 2)
	if f.SessionID != "" {
		q += " AND session_id = ?"
		args = append(args, f.SessionID)
	}
	q += " ORDER BY ts DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.Read().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list safety_snapshots: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := make([]SafetySnapshot, 0)
	for rows.Next() {
		var s SafetySnapshot
		if err := rows.Scan(
			&s.ID, &s.Ts, &s.SessionID, &s.CWD, &s.Command,
			&s.PatternID, &s.Severity, &s.StashSHA, &s.FallbackDir, &s.FileCount,
		); err != nil {
			return nil, fmt.Errorf("store: scan safety_snapshot: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: safety_snapshots rows: %w", err)
	}
	return out, nil
}
