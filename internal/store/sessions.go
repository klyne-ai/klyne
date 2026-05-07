// Package store — sessions DAO.
//
// W2 deliverable. Owned path: internal/store/sessions.go.
package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mohitpatell/agentdeck/internal/connectors"
)

// SessionFilter scopes a ListSessions query.
//
//   - CLI         — empty string means "any CLI"
//   - ProjectPath — empty string means "any project"
//   - Limit       — 0 defaults to 50
//   - Before      — epoch-ms upper bound on last_msg_at; 0 means no upper bound
type SessionFilter struct {
	CLI         string
	ProjectPath string
	Limit       int
	Before      int64
}

const defaultSessionLimit = 50

// UpsertSession inserts a session row or updates the mutable fields if a row
// with the same id already exists.
//
// Preserved on conflict: msg_count, tokens_in, tokens_out, cost_usd,
// started_at (these are rolled up incrementally by InsertMessage).
// Updated on conflict:   last_msg_at, status, model, encoded_cwd, raw_path.
//
// The write handle must be used; the caller is responsible for upserting the
// session before calling InsertMessage (FK enforced by SQLite).
func UpsertSession(ctx context.Context, db *DB, s *connectors.Session) error {
	const q = `
INSERT INTO sessions
    (id, cli, project_path, encoded_cwd, started_at, last_msg_at,
     msg_count, tokens_in, tokens_out, cost_usd, model, status, raw_path)
VALUES
    (?, ?, ?, ?, ?, ?,
     ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    last_msg_at  = excluded.last_msg_at,
    status       = excluded.status,
    model        = excluded.model,
    encoded_cwd  = excluded.encoded_cwd,
    raw_path     = excluded.raw_path
`
	_, err := db.Write().ExecContext(ctx, q,
		s.ID,
		string(s.CLI),
		s.ProjectPath,
		s.EncodedCWD,
		s.StartedAt,
		s.LastMsgAt,
		s.MsgCount,
		s.TokensIn,
		s.TokensOut,
		s.CostUSD,
		s.Model,
		string(s.Status),
		s.RawPath,
	)
	if err != nil {
		return fmt.Errorf("store: upsert session %q: %w", s.ID, err)
	}
	return nil
}

// ListSessions returns sessions ordered by last_msg_at DESC, optionally
// filtered by CLI and/or ProjectPath.  Cursor-based pagination is supported
// via filter.Before (exclusive upper bound on last_msg_at).
//
// Uses the read handle.
func ListSessions(ctx context.Context, db *DB, filter SessionFilter) ([]*connectors.Session, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultSessionLimit
	}

	// Build query dynamically to avoid redundant WHERE clauses.
	q := `
SELECT id, cli, project_path, encoded_cwd, started_at, last_msg_at,
       msg_count, tokens_in, tokens_out, cost_usd, model, status, raw_path
FROM sessions
WHERE 1=1`

	args := make([]any, 0, 4)

	if filter.CLI != "" {
		q += " AND cli = ?"
		args = append(args, filter.CLI)
	}
	if filter.ProjectPath != "" {
		q += " AND project_path = ?"
		args = append(args, filter.ProjectPath)
	}
	if filter.Before > 0 {
		q += " AND last_msg_at < ?"
		args = append(args, filter.Before)
	}

	q += " ORDER BY last_msg_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.Read().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list sessions: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	return scanSessions(rows)
}

// GetSession fetches a single session by its primary key.
//
// Returns sql.ErrNoRows (wrapped) when no row exists — callers should map
// this to HTTP 404.
//
// Uses the read handle.
func GetSession(ctx context.Context, db *DB, id string) (*connectors.Session, error) {
	const q = `
SELECT id, cli, project_path, encoded_cwd, started_at, last_msg_at,
       msg_count, tokens_in, tokens_out, cost_usd, model, status, raw_path
FROM sessions
WHERE id = ?`

	row := db.Read().QueryRowContext(ctx, q, id)
	s, err := scanSession(row)
	if err != nil {
		return nil, fmt.Errorf("store: get session %q: %w", id, err)
	}
	return s, nil
}

// --- internal scan helpers ---------------------------------------------------

// sessionScanner is satisfied by both *sql.Row and (a row from) *sql.Rows.
type sessionScanner interface {
	Scan(dest ...any) error
}

func scanSession(r sessionScanner) (*connectors.Session, error) {
	var s connectors.Session
	var cli, status string
	err := r.Scan(
		&s.ID,
		&cli,
		&s.ProjectPath,
		&s.EncodedCWD,
		&s.StartedAt,
		&s.LastMsgAt,
		&s.MsgCount,
		&s.TokensIn,
		&s.TokensOut,
		&s.CostUSD,
		&s.Model,
		&status,
		&s.RawPath,
	)
	if err != nil {
		return nil, err
	}
	s.CLI = connectors.CLI(cli)
	s.Status = connectors.SessionStatus(status)
	return &s, nil
}

func scanSessions(rows *sql.Rows) ([]*connectors.Session, error) {
	var out []*connectors.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan session row: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: sessions rows: %w", err)
	}
	return out, nil
}
