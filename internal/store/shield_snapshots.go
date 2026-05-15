package store

import (
	"context"
	"fmt"
	"time"
)

// ShieldSnapshot is one row in the shield_snapshots table (migration 012).
//
// Each row is written by the UserPromptSubmit advisor when context fill
// reaches ≥70%, capturing the decisions, open files, and recent turns so
// the PreCompact hook can inject scoped context back into the next prompt
// after a klyne-blocked compact.
type ShieldSnapshot struct {
	ID             int64   `json:"id"`
	Ts             int64   `json:"ts"`
	SessionID      string  `json:"session_id,omitempty"`
	DecisionsJSON  string  `json:"decisions_json"`
	OpenFilesJSON  string  `json:"open_files_json"`
	TurnsJSON      string  `json:"turns_json"`
	ToolChainID    string  `json:"tool_chain_id,omitempty"`
	PreTokens      int64   `json:"pre_tokens"`
	FillPct        float64 `json:"fill_pct"`
	Blocked        bool    `json:"blocked"`
	BlockReason    string  `json:"block_reason,omitempty"`
}

// ShieldSnapshotFilter scopes ListShieldSnapshots queries.
type ShieldSnapshotFilter struct {
	SessionID string
	// OnlyBlocked limits results to rows where blocked=1.
	OnlyBlocked bool
	Limit       int
}

// InsertShieldSnapshot writes a new shield_snapshots row and sets s.ID to
// the auto-assigned row id.
func InsertShieldSnapshot(ctx context.Context, db *DB, s *ShieldSnapshot) error {
	if s.Ts <= 0 {
		s.Ts = time.Now().UnixMilli()
	}
	if s.DecisionsJSON == "" {
		s.DecisionsJSON = "[]"
	}
	if s.OpenFilesJSON == "" {
		s.OpenFilesJSON = "[]"
	}
	if s.TurnsJSON == "" {
		s.TurnsJSON = "[]"
	}

	blocked := 0
	if s.Blocked {
		blocked = 1
	}

	const q = `
INSERT INTO shield_snapshots
    (ts, session_id, decisions_json, open_files_json, turns_json, tool_chain_id, pre_tokens, fill_pct, blocked, block_reason)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := db.Write().ExecContext(ctx, q,
		s.Ts, s.SessionID, s.DecisionsJSON, s.OpenFilesJSON, s.TurnsJSON,
		s.ToolChainID, s.PreTokens, s.FillPct, blocked, s.BlockReason,
	)
	if err != nil {
		return fmt.Errorf("store: insert shield_snapshot: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: shield_snapshot last insert id: %w", err)
	}
	s.ID = id
	return nil
}

// GetShieldSnapshot fetches one row by its integer id.
// Returns sql.ErrNoRows (wrapped) when no row matches.
func GetShieldSnapshot(ctx context.Context, db *DB, id int64) (*ShieldSnapshot, error) {
	const q = `
SELECT id, ts, session_id, decisions_json, open_files_json, turns_json, tool_chain_id, pre_tokens, fill_pct, blocked, block_reason
FROM shield_snapshots WHERE id = ?`
	row := db.Read().QueryRowContext(ctx, q, id)
	return scanShieldSnapshot(row)
}

// LatestShieldSnapshot returns the most recent shield_snapshot for sessionID,
// or nil when none exists. Useful for the PreCompact handler to load the
// arming snapshot without knowing its id.
func LatestShieldSnapshot(ctx context.Context, db *DB, sessionID string) (*ShieldSnapshot, error) {
	const q = `
SELECT id, ts, session_id, decisions_json, open_files_json, turns_json, tool_chain_id, pre_tokens, fill_pct, blocked, block_reason
FROM shield_snapshots WHERE session_id = ? ORDER BY ts DESC LIMIT 1`
	row := db.Read().QueryRowContext(ctx, q, sessionID)
	s, err := scanShieldSnapshot(row)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// ListShieldSnapshots returns snapshots ordered by ts DESC. Limit defaults
// to 50 when zero.
func ListShieldSnapshots(ctx context.Context, db *DB, f ShieldSnapshotFilter) ([]ShieldSnapshot, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT id, ts, session_id, decisions_json, open_files_json, turns_json, tool_chain_id, pre_tokens, fill_pct, blocked, block_reason
          FROM shield_snapshots WHERE 1=1`
	args := make([]any, 0, 3)
	if f.SessionID != "" {
		q += " AND session_id = ?"
		args = append(args, f.SessionID)
	}
	if f.OnlyBlocked {
		q += " AND blocked = 1"
	}
	q += " ORDER BY ts DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.Read().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list shield_snapshots: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := make([]ShieldSnapshot, 0)
	for rows.Next() {
		s, err := scanShieldSnapshot(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan shield_snapshot: %w", err)
		}
		out = append(out, *s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: shield_snapshots rows: %w", err)
	}
	return out, nil
}

// CountShieldDecisions returns a summary of shield decisions for display in
// `klyne precompact --status`. It counts total snapshots, blocked snapshots,
// and fold decisions.
func CountShieldDecisions(ctx context.Context, db *DB, sessionID string) (total, blocked, folded int64, err error) {
	q := `SELECT COUNT(*), SUM(CASE WHEN blocked=1 THEN 1 ELSE 0 END), SUM(CASE WHEN block_reason='fold' THEN 1 ELSE 0 END)
          FROM shield_snapshots`
	args := make([]any, 0, 1)
	if sessionID != "" {
		q += " WHERE session_id = ?"
		args = append(args, sessionID)
	}
	row := db.Read().QueryRowContext(ctx, q, args...)
	if err = row.Scan(&total, &blocked, &folded); err != nil {
		return 0, 0, 0, fmt.Errorf("store: count shield_decisions: %w", err)
	}
	return total, blocked, folded, nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanShieldSnapshot(row rowScanner) (*ShieldSnapshot, error) {
	s := &ShieldSnapshot{}
	var blocked int
	err := row.Scan(
		&s.ID, &s.Ts, &s.SessionID, &s.DecisionsJSON, &s.OpenFilesJSON, &s.TurnsJSON,
		&s.ToolChainID, &s.PreTokens, &s.FillPct, &blocked, &s.BlockReason,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get shield_snapshot: %w", err)
	}
	s.Blocked = blocked == 1
	return s, nil
}

