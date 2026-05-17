package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// StopSummary is one row of the stop_summaries table — a
// deterministic, no-AI summary written by the Claude Code Stop
// hook (`klyne session-end`) when a session ends.
//
// The summary captures: the last user prompt, the last bash
// command run, the files touched in the final tool-call window,
// and a markdown body suitable for verbatim display to a future
// session via /klyne:bootstrap or recall.
type StopSummary struct {
	SessionID   string   `json:"session_id"`
	Ts          int64    `json:"ts"`
	ProjectPath string   `json:"project_path,omitempty"`
	CLI         string   `json:"cli,omitempty"`
	Summary     string   `json:"summary"`
	LastUser    string   `json:"last_user,omitempty"`
	LastBash    string   `json:"last_bash,omitempty"`
	Files       []string `json:"files,omitempty"`
}

// InsertStopSummary writes a stop-hook summary row. ts is set to
// time.Now() when zero. Idempotent on (session_id, ts) — repeated
// inserts at the same logical instant overwrite.
func InsertStopSummary(ctx context.Context, db *DB, s *StopSummary) error {
	if strings.TrimSpace(s.SessionID) == "" {
		return errors.New("store: stop summary session_id required")
	}
	if strings.TrimSpace(s.Summary) == "" {
		return errors.New("store: stop summary text required")
	}
	if s.Ts == 0 {
		s.Ts = time.Now().UnixMilli()
	}
	if s.CLI == "" {
		s.CLI = "claude"
	}
	files := s.Files
	if files == nil {
		files = []string{}
	}
	filesJSON, err := json.Marshal(files)
	if err != nil {
		return fmt.Errorf("store: marshal files: %w", err)
	}
	const q = `
INSERT INTO stop_summaries
    (session_id, ts, project_path, cli, summary, last_user, last_bash, files_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id, ts) DO UPDATE SET
    project_path = excluded.project_path,
    cli          = excluded.cli,
    summary      = excluded.summary,
    last_user    = excluded.last_user,
    last_bash    = excluded.last_bash,
    files_json   = excluded.files_json`
	_, err = db.Write().ExecContext(ctx, q,
		s.SessionID, s.Ts, s.ProjectPath, s.CLI, s.Summary,
		s.LastUser, s.LastBash, string(filesJSON))
	if err != nil {
		return fmt.Errorf("store: insert stop summary: %w", err)
	}
	return nil
}

// LatestStopSummaryForProject returns the most recent stop-hook
// summary scoped to projectPath, or nil if there is none. Empty
// projectPath returns the most recent summary across all projects.
func LatestStopSummaryForProject(ctx context.Context, db *DB, projectPath string) (*StopSummary, error) {
	q := `SELECT session_id, ts, project_path, cli, summary, last_user, last_bash, files_json
	        FROM stop_summaries`
	args := []any{}
	if projectPath != "" {
		q += " WHERE project_path = ?"
		args = append(args, projectPath)
	}
	q += " ORDER BY ts DESC LIMIT 1"
	row := db.Read().QueryRowContext(ctx, q, args...)
	var s StopSummary
	var filesJSON string
	if err := row.Scan(&s.SessionID, &s.Ts, &s.ProjectPath, &s.CLI, &s.Summary,
		&s.LastUser, &s.LastBash, &filesJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: latest stop summary: %w", err)
	}
	if filesJSON != "" {
		_ = json.Unmarshal([]byte(filesJSON), &s.Files)
	}
	return &s, nil
}

// ListStopSummariesForProject returns recent stop-hook summaries
// for projectPath, newest first. limit caps the result (default 10).
func ListStopSummariesForProject(ctx context.Context, db *DB, projectPath string, limit int) ([]StopSummary, error) {
	if limit <= 0 {
		limit = 10
	}
	q := `SELECT session_id, ts, project_path, cli, summary, last_user, last_bash, files_json
	        FROM stop_summaries`
	args := []any{}
	if projectPath != "" {
		q += " WHERE project_path = ?"
		args = append(args, projectPath)
	}
	q += " ORDER BY ts DESC LIMIT ?"
	args = append(args, limit)
	rows, err := db.Read().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list stop summaries: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := make([]StopSummary, 0)
	for rows.Next() {
		var s StopSummary
		var filesJSON string
		if err := rows.Scan(&s.SessionID, &s.Ts, &s.ProjectPath, &s.CLI, &s.Summary,
			&s.LastUser, &s.LastBash, &filesJSON); err != nil {
			return nil, fmt.Errorf("store: scan stop summary: %w", err)
		}
		if filesJSON != "" {
			_ = json.Unmarshal([]byte(filesJSON), &s.Files)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: stop summary rows: %w", err)
	}
	return out, nil
}

// ListStopSummariesForSession returns stop-hook summaries for one
// session_id, newest first. limit caps the result (default 10).
// Returns an empty slice when no rows exist.
func ListStopSummariesForSession(ctx context.Context, db *DB, sessionID string, limit int) ([]StopSummary, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("store: stop summary session_id required")
	}
	if limit <= 0 {
		limit = 10
	}
	const q = `SELECT session_id, ts, project_path, cli, summary, last_user, last_bash, files_json
	             FROM stop_summaries
	            WHERE session_id = ?
	            ORDER BY ts DESC
	            LIMIT ?`
	rows, err := db.Read().QueryContext(ctx, q, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list stop summaries by session: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := make([]StopSummary, 0)
	for rows.Next() {
		var s StopSummary
		var filesJSON string
		if err := rows.Scan(&s.SessionID, &s.Ts, &s.ProjectPath, &s.CLI, &s.Summary,
			&s.LastUser, &s.LastBash, &filesJSON); err != nil {
			return nil, fmt.Errorf("store: scan stop summary: %w", err)
		}
		if filesJSON != "" {
			_ = json.Unmarshal([]byte(filesJSON), &s.Files)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: stop summary rows: %w", err)
	}
	return out, nil
}

