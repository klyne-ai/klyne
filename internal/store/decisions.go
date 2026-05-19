package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

// FindDecisionByText returns the most recent decision in projectPath
// whose text exactly matches body (after caller-side TrimSpace). Returns
// (nil, nil) when no match exists. Used by HandleRecordDecision to make
// record_decision idempotent against same-text re-records within one
// project — guards against the model accidentally creating parallel
// runbook entries on a topic the project already has.
func FindDecisionByText(ctx context.Context, db *DB, projectPath, body string) (*Decision, error) {
	const q = `SELECT id, ts, project_path, session_id, text, tags_json
FROM decisions
WHERE project_path = ? AND text = ?
ORDER BY ts DESC
LIMIT 1`
	row := db.Read().QueryRowContext(ctx, q, projectPath, body)
	var d Decision
	var tagsJSON string
	if err := row.Scan(&d.ID, &d.Ts, &d.ProjectPath, &d.SessionID, &d.Text, &tagsJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: find decision by text: %w", err)
	}
	if tagsJSON != "" {
		_ = json.Unmarshal([]byte(tagsJSON), &d.Tags)
	}
	return &d, nil
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

// ListGlobalDecisions returns global decisions (project_path = '')
// sorted by ts DESC. Limit defaults to 100 when zero. Use this
// instead of ListDecisions with empty ProjectPath when you only
// want globals — ListDecisions with ProjectPath="" returns ALL
// decisions across all projects (no filter), which forces callers
// to filter client-side. This primitive does the filter in SQL.
func ListGlobalDecisions(ctx context.Context, db *DB, limit int) ([]Decision, error) {
	if limit <= 0 {
		limit = 100
	}
	const q = `SELECT id, ts, project_path, session_id, text, tags_json
FROM decisions
WHERE project_path = ''
ORDER BY ts DESC
LIMIT ?`
	rows, err := db.Read().QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list global decisions: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := make([]Decision, 0)
	for rows.Next() {
		var d Decision
		var tagsJSON string
		if err := rows.Scan(&d.ID, &d.Ts, &d.ProjectPath, &d.SessionID, &d.Text, &tagsJSON); err != nil {
			return nil, fmt.Errorf("store: scan global decision: %w", err)
		}
		if tagsJSON != "" {
			_ = json.Unmarshal([]byte(tagsJSON), &d.Tags)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iter global decisions: %w", err)
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

// UpdateDecision patches one or both of text / tags on an existing
// decision row. updateText / updateTags act as presence flags so the
// caller can distinguish "leave unchanged" from "set to empty".
//
// Behavior:
//   - updateText=true with whitespace-only text → error (mirrors
//     InsertDecision's text-required contract).
//   - updateTags=true with a nil slice writes `[]` (clear tags).
//   - Neither flag set → error: at least one field must be patched.
//   - Returns sql.ErrNoRows (wrapped) when no row matches the id.
func UpdateDecision(ctx context.Context, db *DB, id string, text string, tags []string, updateText, updateTags bool) error {
	if id == "" {
		return errors.New("store: decision id required")
	}
	if !updateText && !updateTags {
		return errors.New("store: update decision: at least one of text or tags must be set")
	}

	sets := make([]string, 0, 2)
	args := make([]any, 0, 3)
	if updateText {
		if strings.TrimSpace(text) == "" {
			return errors.New("store: decision text required")
		}
		sets = append(sets, "text = ?")
		args = append(args, text)
	}
	if updateTags {
		if tags == nil {
			tags = []string{}
		}
		raw, err := json.Marshal(tags)
		if err != nil {
			return fmt.Errorf("store: marshal tags for decision %q: %w", id, err)
		}
		sets = append(sets, "tags_json = ?")
		args = append(args, string(raw))
	}
	args = append(args, id)

	q := "UPDATE decisions SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	res, err := db.Write().ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("store: update decision %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update decision %q rows: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: decision %q: %w", id, sql.ErrNoRows)
	}
	return nil
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
