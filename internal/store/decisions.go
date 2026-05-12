package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Decision is one row in the `decisions` table (migration 009).
//
// Decisions are short, immutable notes pinned to a project (and
// optionally a session) so the AI can recover load-bearing context
// across sessions without re-reading the JSONL. See
// internal/store/migrations/009_decisions.sql for the schema.
type Decision struct {
	ID          string   `json:"id"`
	Ts          int64    `json:"ts"`
	ProjectPath string   `json:"project_path,omitempty"`
	SessionID   string   `json:"session_id,omitempty"`
	Text        string   `json:"text"`
	Tags        []string `json:"tags,omitempty"`
}

// DecisionFilter scopes ListDecisions queries.
type DecisionFilter struct {
	ProjectPath string
	SessionID   string
	Tag         string
	Limit       int
}

// InsertDecision writes a new decision row. The id MUST already be set
// by the caller (typically a UUID); we don't generate one here so the
// caller can echo it back to the user.
func InsertDecision(ctx context.Context, db *DB, d *Decision) error {
	if d.ID == "" {
		return errors.New("store: decision id required")
	}
	if d.Text == "" {
		return errors.New("store: decision text required")
	}
	tags, err := json.Marshal(d.Tags)
	if err != nil {
		return fmt.Errorf("store: marshal tags for decision %q: %w", d.ID, err)
	}
	const q = `
INSERT INTO decisions (id, ts, project_path, session_id, text, tags_json)
VALUES (?, ?, ?, ?, ?, ?)`
	_, err = db.Write().ExecContext(ctx, q,
		d.ID, d.Ts, d.ProjectPath, d.SessionID, d.Text, string(tags),
	)
	if err != nil {
		return fmt.Errorf("store: insert decision %q: %w", d.ID, err)
	}
	return nil
}

// ListDecisions returns decisions matching f, sorted by ts DESC. Limit
// defaults to 100 when zero.
func ListDecisions(ctx context.Context, db *DB, f DecisionFilter) ([]Decision, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT id, ts, project_path, session_id, text, tags_json FROM decisions WHERE 1=1`
	args := make([]any, 0, 4)
	if f.ProjectPath != "" {
		q += " AND project_path = ?"
		args = append(args, f.ProjectPath)
	}
	if f.SessionID != "" {
		q += " AND session_id = ?"
		args = append(args, f.SessionID)
	}
	if f.Tag != "" {
		// JSON-array containment via LIKE; cheap and avoids json1 dependency.
		// "x" appears in '["x"]', '["x","y"]', '["y","x"]', etc.
		q += ` AND (',' || REPLACE(REPLACE(REPLACE(tags_json,'"',''),'[',''),']','') || ',') LIKE ?`
		args = append(args, "%,"+f.Tag+",%")
	}
	q += " ORDER BY ts DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.Read().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list decisions: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := make([]Decision, 0)
	for rows.Next() {
		var d Decision
		var tagsJSON string
		if err := rows.Scan(&d.ID, &d.Ts, &d.ProjectPath, &d.SessionID, &d.Text, &tagsJSON); err != nil {
			return nil, fmt.Errorf("store: scan decision: %w", err)
		}
		if tagsJSON != "" {
			_ = json.Unmarshal([]byte(tagsJSON), &d.Tags)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: decisions rows: %w", err)
	}
	return out, nil
}

// SearchDecisions returns decisions whose text contains q (case-insensitive).
// Limit defaults to 50.
func SearchDecisions(ctx context.Context, db *DB, q, projectPath string, limit int) ([]Decision, error) {
	if limit <= 0 {
		limit = 50
	}
	sqlQ := `SELECT id, ts, project_path, session_id, text, tags_json FROM decisions
              WHERE text LIKE ? COLLATE NOCASE`
	args := []any{"%" + q + "%"}
	if projectPath != "" {
		sqlQ += " AND project_path = ?"
		args = append(args, projectPath)
	}
	sqlQ += " ORDER BY ts DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.Read().QueryContext(ctx, sqlQ, args...)
	if err != nil {
		return nil, fmt.Errorf("store: search decisions: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := make([]Decision, 0)
	for rows.Next() {
		var d Decision
		var tagsJSON string
		if err := rows.Scan(&d.ID, &d.Ts, &d.ProjectPath, &d.SessionID, &d.Text, &tagsJSON); err != nil {
			return nil, fmt.Errorf("store: scan decision: %w", err)
		}
		if tagsJSON != "" {
			_ = json.Unmarshal([]byte(tagsJSON), &d.Tags)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: search decisions rows: %w", err)
	}
	return out, nil
}

// DeleteDecision removes one decision by id. Returns sql.ErrNoRows
// (wrapped) when no row matches — callers should map to a 404.
func DeleteDecision(ctx context.Context, db *DB, id string) error {
	res, err := db.Write().ExecContext(ctx, `DELETE FROM decisions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete decision %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete decision %q rows: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: decision %q: %w", id, sql.ErrNoRows)
	}
	return nil
}
