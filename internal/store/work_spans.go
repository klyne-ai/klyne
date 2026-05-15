package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// WorkSpan is one row in the work_spans table (migration 014).
//
// A span covers the sequence of messages between a session start (or the
// previous close event) and a git commit, GitHub PR creation (v1), or the
// end-of-stream fallback (exploration bucket). Token fields carry raw counts
// so the display layer can compute USD via the live pricing table without
// persisting a cost that would drift as rate cards change.
type WorkSpan struct {
	ID               int64    `json:"id"`
	CommitSHA        string   `json:"commit_sha,omitempty"`
	PRNumber         int      `json:"pr_number,omitempty"`
	ExplorationID    string   `json:"exploration_id,omitempty"`
	Bucket           string   `json:"bucket"` // "commit" | "exploration"
	ProjectPath      string   `json:"project_path,omitempty"`
	GitBranch        string   `json:"git_branch,omitempty"`
	SessionIDs       []string `json:"session_ids,omitempty"`
	DecisionIDs      []string `json:"decision_ids,omitempty"`
	WasteClasses     []string `json:"waste_classes,omitempty"`
	WasteMeta        any      `json:"waste_meta,omitempty"`
	TokensFresh      int64    `json:"tokens_fresh"`
	TokensCacheRead  int64    `json:"tokens_cache_read"`
	TokensCacheWrite int64    `json:"tokens_cache_write"`
	TokensOut        int64    `json:"tokens_out"`
	MsgCount         int      `json:"msg_count"`
	OpenedAt         int64    `json:"opened_at"`
	ClosedAt         int64    `json:"closed_at"`
}

// WorkSpanFilter scopes ListWorkSpans queries.
type WorkSpanFilter struct {
	ProjectPath string
	Since       int64 // epoch-ms; 0 = all time
	Until       int64 // epoch-ms; 0 = now
	Limit       int
}

// InsertWorkSpan writes a new work_spans row and sets s.ID to the
// auto-assigned row id.
func InsertWorkSpan(ctx context.Context, db *DB, s *WorkSpan) error {
	if s.Bucket == "" {
		return fmt.Errorf("store: work span bucket required")
	}
	if s.OpenedAt <= 0 {
		s.OpenedAt = time.Now().UnixMilli()
	}
	if s.ClosedAt <= 0 {
		s.ClosedAt = s.OpenedAt
	}

	sessionJSON, err := marshalStringSlice(s.SessionIDs)
	if err != nil {
		return fmt.Errorf("store: marshal session_ids: %w", err)
	}
	decisionJSON, err := marshalStringSlice(s.DecisionIDs)
	if err != nil {
		return fmt.Errorf("store: marshal decision_ids: %w", err)
	}
	wasteJSON, err := marshalStringSlice(s.WasteClasses)
	if err != nil {
		return fmt.Errorf("store: marshal waste_classes: %w", err)
	}
	metaJSON, err := marshalAny(s.WasteMeta)
	if err != nil {
		return fmt.Errorf("store: marshal waste_meta: %w", err)
	}

	const q = `
INSERT INTO work_spans
    (commit_sha, pr_number, exploration_id, bucket, project_path, git_branch,
     session_ids_json, decision_ids_json, waste_classes_json, waste_meta_json,
     tokens_fresh, tokens_cache_read, tokens_cache_write, tokens_out, msg_count,
     opened_at, closed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	res, err := db.Write().ExecContext(ctx, q,
		s.CommitSHA, s.PRNumber, s.ExplorationID, s.Bucket, s.ProjectPath, s.GitBranch,
		sessionJSON, decisionJSON, wasteJSON, metaJSON,
		s.TokensFresh, s.TokensCacheRead, s.TokensCacheWrite, s.TokensOut, s.MsgCount,
		s.OpenedAt, s.ClosedAt,
	)
	if err != nil {
		return fmt.Errorf("store: insert work_span: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: work_span last insert id: %w", err)
	}
	s.ID = id
	return nil
}

// GetWorkSpan fetches one row by its integer id.
// Returns sql.ErrNoRows (wrapped) when no row matches.
func GetWorkSpan(ctx context.Context, db *DB, id int64) (*WorkSpan, error) {
	const q = `
SELECT id, commit_sha, pr_number, exploration_id, bucket, project_path, git_branch,
       session_ids_json, decision_ids_json, waste_classes_json, waste_meta_json,
       tokens_fresh, tokens_cache_read, tokens_cache_write, tokens_out, msg_count,
       opened_at, closed_at
FROM work_spans WHERE id = ?`

	row := db.Read().QueryRowContext(ctx, q, id)
	s, err := scanWorkSpan(row)
	if err != nil {
		return nil, fmt.Errorf("store: get work_span %d: %w", id, err)
	}
	return s, nil
}

// DeleteWorkSpansSince removes every work_spans row whose opened_at is at or
// after sinceMs. The CLI calls this before each `klyne cost week` re-attribution
// so the table contains exactly one batch per time window — without it, repeat
// invocations append, sums double, and the digest reports inflated triples.
//
// Returns the number of rows deleted.
func DeleteWorkSpansSince(ctx context.Context, db *DB, sinceMs int64) (int64, error) {
	const q = `DELETE FROM work_spans WHERE opened_at >= ?`
	res, err := db.Write().ExecContext(ctx, q, sinceMs)
	if err != nil {
		return 0, fmt.Errorf("store: delete work_spans since %d: %w", sinceMs, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: delete work_spans rows-affected: %w", err)
	}
	return n, nil
}

// ListWorkSpans returns work spans ordered by opened_at DESC. Limit defaults
// to 100 when zero. Filter fields are ANDed.
func ListWorkSpans(ctx context.Context, db *DB, f WorkSpanFilter) ([]WorkSpan, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}

	q := `SELECT id, commit_sha, pr_number, exploration_id, bucket, project_path, git_branch,
       session_ids_json, decision_ids_json, waste_classes_json, waste_meta_json,
       tokens_fresh, tokens_cache_read, tokens_cache_write, tokens_out, msg_count,
       opened_at, closed_at
  FROM work_spans WHERE 1=1`
	args := make([]any, 0, 4)

	if f.ProjectPath != "" {
		q += " AND project_path = ?"
		args = append(args, f.ProjectPath)
	}
	if f.Since > 0 {
		q += " AND opened_at >= ?"
		args = append(args, f.Since)
	}
	if f.Until > 0 {
		q += " AND opened_at <= ?"
		args = append(args, f.Until)
	}
	q += " ORDER BY opened_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.Read().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list work_spans: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := make([]WorkSpan, 0)
	for rows.Next() {
		s, err := scanWorkSpanRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan work_span: %w", err)
		}
		out = append(out, *s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: work_spans rows: %w", err)
	}
	return out, nil
}

// ---- scanner helpers -------------------------------------------------------

func scanWorkSpan(r rowScanner) (*WorkSpan, error) {
	return scanWorkSpanRow(r)
}

func scanWorkSpanRow(r rowScanner) (*WorkSpan, error) {
	var s WorkSpan
	var sessionJSON, decisionJSON, wasteJSON, metaJSON string
	err := r.Scan(
		&s.ID, &s.CommitSHA, &s.PRNumber, &s.ExplorationID, &s.Bucket,
		&s.ProjectPath, &s.GitBranch,
		&sessionJSON, &decisionJSON, &wasteJSON, &metaJSON,
		&s.TokensFresh, &s.TokensCacheRead, &s.TokensCacheWrite, &s.TokensOut, &s.MsgCount,
		&s.OpenedAt, &s.ClosedAt,
	)
	if err != nil {
		return nil, err
	}

	s.SessionIDs = unmarshalStringSlice(sessionJSON)
	s.DecisionIDs = unmarshalStringSlice(decisionJSON)
	s.WasteClasses = unmarshalStringSlice(wasteJSON)

	if metaJSON != "" && metaJSON != "{}" && metaJSON != "null" {
		var m any
		if err := json.Unmarshal([]byte(metaJSON), &m); err == nil {
			s.WasteMeta = m
		}
	}
	return &s, nil
}

// ---- JSON helpers ----------------------------------------------------------

func marshalStringSlice(ss []string) (string, error) {
	if len(ss) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(ss)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func unmarshalStringSlice(raw string) []string {
	if raw == "" || raw == "[]" || raw == "null" {
		return nil
	}
	var ss []string
	if err := json.Unmarshal([]byte(raw), &ss); err != nil {
		return nil
	}
	return ss
}

func marshalAny(v any) (string, error) {
	if v == nil {
		return "{}", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
