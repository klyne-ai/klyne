package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Summary represents a single rolling-summary row from the session_summaries
// table. Version is monotonically increasing per session_id; the highest
// version is the most recent summary.
type Summary struct {
	SessionID string `json:"session_id"`
	Version   int    `json:"version"`
	Text      string `json:"text"`
	Model     string `json:"model"`
	TS        int64  `json:"ts"`
}

// InsertSummary inserts a new summary for the given session, automatically
// assigning the next version number.
//
// Version assignment is atomic: a transaction reads MAX(version) for the
// session and inserts version+1. The PRIMARY KEY (session_id, version)
// constraint prevents duplicates even under concurrent inserts. When a unique
// constraint violation is detected (two goroutines raced and assigned the same
// version) the function retries once. Callers should treat any returned error
// as terminal for that call; they may retry at a higher level if needed.
func InsertSummary(ctx context.Context, db *DB, s *Summary) error {
	const maxRetries = 5
	for attempt := 0; attempt < maxRetries; attempt++ {
		err := insertSummaryOnce(ctx, db, s)
		if err == nil {
			return nil
		}
		if isUniqueConstraintErr(err) {
			// Another goroutine raced us to the same version — retry.
			continue
		}
		return err
	}
	return fmt.Errorf("store: insert summary: too many version conflicts for session %s", s.SessionID)
}

// insertSummaryOnce attempts a single transactional insert. It reads the
// current MAX(version) for the session inside the transaction, increments it,
// and inserts. Returns the raw error (including unique-constraint violations)
// so the caller can decide whether to retry.
func insertSummaryOnce(ctx context.Context, db *DB, s *Summary) error {
	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	// Read current max version inside the transaction.
	var maxVer sql.NullInt64
	row := tx.QueryRowContext(ctx,
		`SELECT MAX(version) FROM session_summaries WHERE session_id = ?`,
		s.SessionID,
	)
	if err := row.Scan(&maxVer); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("select max version: %w", err)
	}

	nextVer := 1
	if maxVer.Valid {
		nextVer = int(maxVer.Int64) + 1
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO session_summaries (session_id, version, text, model, ts)
		 VALUES (?, ?, ?, ?, ?)`,
		s.SessionID, nextVer, s.Text, s.Model, s.TS,
	)
	if err != nil {
		_ = tx.Rollback()
		return err // may be a unique constraint violation — caller checks
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	// Reflect the assigned version back into the caller's struct.
	s.Version = nextVer
	return nil
}

// LatestSummary returns the summary with the highest version for the given
// session. It returns sql.ErrNoRows (unwrapped so errors.Is works) when no
// summary exists for the session.
func LatestSummary(ctx context.Context, db *DB, sessionID string) (*Summary, error) {
	const q = `
SELECT session_id, version, text, model, ts
FROM session_summaries
WHERE session_id = ?
ORDER BY version DESC
LIMIT 1`

	row := db.Read().QueryRowContext(ctx, q, sessionID)

	var s Summary
	err := row.Scan(&s.SessionID, &s.Version, &s.Text, &s.Model, &s.TS)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("store: latest summary scan: %w", err)
	}
	return &s, nil
}

// ListSummaries returns all summaries for the given session ordered by version
// descending (newest first). An empty slice (not nil) is returned when the
// session has no summaries.
func ListSummaries(ctx context.Context, db *DB, sessionID string) ([]*Summary, error) {
	const q = `
SELECT session_id, version, text, model, ts
FROM session_summaries
WHERE session_id = ?
ORDER BY version DESC`

	rows, err := db.Read().QueryContext(ctx, q, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: list summaries query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var summaries []*Summary
	for rows.Next() {
		var s Summary
		if err := rows.Scan(&s.SessionID, &s.Version, &s.Text, &s.Model, &s.TS); err != nil {
			return nil, fmt.Errorf("store: list summaries scan: %w", err)
		}
		summaries = append(summaries, &s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list summaries rows: %w", err)
	}

	if summaries == nil {
		summaries = []*Summary{}
	}
	return summaries, nil
}

// isUniqueConstraintErr returns true when err is a SQLite UNIQUE constraint
// violation. modernc.org/sqlite surfaces this as an error message containing
// "UNIQUE constraint failed".
func isUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
