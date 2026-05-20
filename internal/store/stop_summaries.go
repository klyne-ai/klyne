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

// WorklogColumns groups the migration-015 columns that extend a stop_summaries
// row with worklog-feature metadata (memory layer). It is paired with
// StopSummary by UpsertStopSummaryWithWorklog so both detectors (Claude Stop
// hook and Codex episode-boundary detector) write through one call.
type WorklogColumns struct {
	RecapVisible     int
	RecapTopic       string
	AIDraftedSummary string
	DraftState       string
	Signature        string
	Importance       int
	LastAccessedAt   int64
}

// UpsertStopSummaryWithWorklog writes a stop_summaries row together with
// its worklog-feature columns. Idempotent on (session_id, ts) — re-running
// at the same logical instant updates the worklog columns but leaves the
// base fields stable (the ON CONFLICT clause intentionally only touches the
// migration-015 columns so a later AI prose pass does not clobber the
// deterministic body written by the Stop hook).
//
// row.Summary may be empty (the NOT NULL constraint allows empty string);
// the worklog memory layer stores its prose in WorklogColumns.AIDraftedSummary.
func UpsertStopSummaryWithWorklog(ctx context.Context, db *DB, row StopSummary, w WorklogColumns) error {
	if strings.TrimSpace(row.SessionID) == "" {
		return errors.New("store: upsert worklog: session_id required")
	}
	if row.Ts == 0 {
		row.Ts = time.Now().UnixMilli()
	}
	if row.CLI == "" {
		row.CLI = "claude"
	}
	files := row.Files
	if files == nil {
		files = []string{}
	}
	filesJSON, err := json.Marshal(files)
	if err != nil {
		return fmt.Errorf("store: marshal files: %w", err)
	}
	const q = `
INSERT INTO stop_summaries (
    session_id, ts, project_path, cli, summary, last_user, last_bash, files_json,
    recap_visible, recap_topic, ai_drafted_summary, draft_state, signature, importance, last_accessed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id, ts) DO UPDATE SET
    recap_visible      = excluded.recap_visible,
    recap_topic        = excluded.recap_topic,
    ai_drafted_summary = excluded.ai_drafted_summary,
    draft_state        = excluded.draft_state,
    signature          = excluded.signature,
    importance         = excluded.importance,
    last_accessed_at   = excluded.last_accessed_at`
	_, err = db.Write().ExecContext(ctx, q,
		row.SessionID, row.Ts, row.ProjectPath, row.CLI, row.Summary,
		row.LastUser, row.LastBash, string(filesJSON),
		w.RecapVisible, w.RecapTopic, w.AIDraftedSummary, w.DraftState,
		w.Signature, w.Importance, w.LastAccessedAt)
	if err != nil {
		return fmt.Errorf("store: upsert stop summary with worklog: %w", err)
	}
	return nil
}

// WorklogItem is one entry inside a WorklogEntryJSON category. Uniform
// shape across all 15 categories so the reflection consumer can iterate
// without special-casing. Every Refs[] element MUST survive the
// internal/worklog/richentry allowlist validator (short SHA from this
// turn's commits, PR # from the gh cache, ticket id from the branch,
// file path from this turn's touched-files, canonical duration / clock,
// or a UUID that's in the session-id allowlist AND unique across bullets).
type WorklogItem struct {
	Summary string   `json:"summary"`
	Repo    string   `json:"repo,omitempty"`
	Refs    []string `json:"refs,omitempty"`
	Ticket  string   `json:"ticket,omitempty"`
}

// WorklogEntryJSON is the 15-category structured worklog entry the
// session-end pipeline (gate + writer + validator) attaches to a
// stop_summaries row via UpsertStopSummaryWithEntry. Empty categories
// are []; the JSON column never holds null. SchemaVersion lets future
// schema changes coexist with older rows.
//
// The 15 categories (carried in Categories[name]):
//   - features_worked_on, features_picked, shipped
//   - bugs_found, bugs_fixed
//   - investigations, decisions
//   - config_changes, reviews_given
//   - blockers, blocked_on, pending, followups_for_others
//   - must_remember, mistakes_or_dead_ends
type WorklogEntryJSON struct {
	SchemaVersion int                      `json:"schema_version"`
	Categories    map[string][]WorklogItem `json:"categories"`
}

// UpsertStopSummaryWithEntry writes a stop_summaries row plus the
// migration-019 rich entry, gate verdict, and worker-attempts counter.
// Following the 015 precedent: ON CONFLICT ONLY updates the
// migration-019 columns, preserving the base body
// (summary/last_user/last_bash/files_json) and the 015 worklog
// metadata so this pass can run after the Stop hook without
// clobbering anything.
//
// attempts is the worker's try-count. The gate calls this with 0 on
// first success; the worker increments on retry. When attempts
// >= MAX_WORKLOG_ATTEMPTS the caller is expected to pass
// gateVerdict='failed-permanent' so the worker queue scan drops the row.
func UpsertStopSummaryWithEntry(
	ctx context.Context, db *DB, row StopSummary, entry WorklogEntryJSON,
	gateVerdict string, attempts int,
) error {
	if strings.TrimSpace(row.SessionID) == "" {
		return errors.New("store: upsert entry: session_id required")
	}
	if row.Ts == 0 {
		row.Ts = time.Now().UnixMilli()
	}
	if row.CLI == "" {
		row.CLI = "claude"
	}
	files := row.Files
	if files == nil {
		files = []string{}
	}
	filesJSON, err := json.Marshal(files)
	if err != nil {
		return fmt.Errorf("store: marshal files: %w", err)
	}
	entryBytes, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("store: marshal worklog entry: %w", err)
	}
	const q = `
INSERT INTO stop_summaries (
    session_id, ts, project_path, cli, summary, last_user, last_bash, files_json,
    worklog_entry_json, worklog_gate_verdict, worklog_attempts
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id, ts) DO UPDATE SET
    worklog_entry_json   = excluded.worklog_entry_json,
    worklog_gate_verdict = excluded.worklog_gate_verdict,
    worklog_attempts     = excluded.worklog_attempts`
	_, err = db.Write().ExecContext(ctx, q,
		row.SessionID, row.Ts, row.ProjectPath, row.CLI, row.Summary,
		row.LastUser, row.LastBash, string(filesJSON),
		string(entryBytes), gateVerdict, attempts)
	if err != nil {
		return fmt.Errorf("store: upsert stop summary with entry: %w", err)
	}
	return nil
}

// ReadWorklogEntry returns the migration-019 rich entry + gate verdict
// for one (session_id, ts). Missing rows are not an error: returns the
// zero WorklogEntryJSON + empty verdict + nil so Phase 5's worker can
// distinguish "queue is empty / row not processed yet" from a real DB
// failure.
func ReadWorklogEntry(ctx context.Context, db *DB, sessionID string, ts int64) (WorklogEntryJSON, string, error) {
	const q = `SELECT worklog_entry_json, worklog_gate_verdict
	             FROM stop_summaries WHERE session_id = ? AND ts = ?`
	var raw, verdict string
	if err := db.Read().QueryRowContext(ctx, q, sessionID, ts).Scan(&raw, &verdict); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorklogEntryJSON{}, "", nil
		}
		return WorklogEntryJSON{}, "", fmt.Errorf("store: read worklog entry: %w", err)
	}
	var entry WorklogEntryJSON
	if raw != "" {
		// Unmarshal best-effort: a malformed JSON column shouldn't take
		// down the whole reflection-consumer scan. Caller sees the
		// empty entry + verdict and can log the row id.
		_ = json.Unmarshal([]byte(raw), &entry)
	}
	return entry, verdict, nil
}

// PendingWorklogEntry is one row the Phase 5 worker queue surfaces —
// the base stop_summaries data plus the worklog_attempts counter the
// worker needs to compute the next attempts value when persisting.
// Embeds StopSummary so the worker code reads naturally (row.SessionID,
// row.Ts, row.Summary).
type PendingWorklogEntry struct {
	StopSummary
	WorklogAttempts int
}

// ListPendingWorklogEntries returns rows the worker queue should
// drain: verdict IN ('','pending') AND attempts < maxAttempts,
// oldest first. limit caps the batch size; the worker uses small
// batches so a slow LLM doesn't starve later turns.
//
// The "verdict='' OR verdict='pending'" filter is the persistent
// queue signal (Phase 5 design): '' is "never processed" (Stop hook
// wrote the row but worker hasn't seen it yet) and 'pending' is
// "worker has attempted at least once and is retrying". Both should
// be picked up; terminal states (admitted-*, skipped-*, failed-permanent)
// are excluded so the worker doesn't reprocess them.
func ListPendingWorklogEntries(ctx context.Context, db *DB, limit, maxAttempts int) ([]PendingWorklogEntry, error) {
	if limit <= 0 {
		limit = 25
	}
	const q = `
SELECT session_id, ts, project_path, cli, summary, last_user, last_bash, files_json,
       worklog_attempts
  FROM stop_summaries
 WHERE worklog_gate_verdict IN ('', 'pending')
   AND worklog_attempts < ?
 ORDER BY ts ASC
 LIMIT ?`
	rows, err := db.Read().QueryContext(ctx, q, maxAttempts, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list pending worklog entries: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := make([]PendingWorklogEntry, 0)
	for rows.Next() {
		var e PendingWorklogEntry
		var filesJSON string
		if err := rows.Scan(&e.SessionID, &e.Ts, &e.ProjectPath, &e.CLI, &e.Summary,
			&e.LastUser, &e.LastBash, &filesJSON, &e.WorklogAttempts); err != nil {
			return nil, fmt.Errorf("store: scan pending: %w", err)
		}
		if filesJSON != "" {
			_ = json.Unmarshal([]byte(filesJSON), &e.Files)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DayEntry pairs one stop_summaries row's rich worklog entry with
// its session+timestamp so the reflection consumer can stitch
// chronologically across a day's turns.
type DayEntry struct {
	SessionID string
	Ts        int64
	Entry     WorklogEntryJSON
}

// ListWorklogEntriesForDay returns the day's admitted rich entries
// for one project in chronological order. Only rows the worker
// terminally admitted are returned (verdict LIKE 'admitted-%') —
// skipped-* / failed-permanent / pending rows are excluded because
// their entries are empty or unsynthesized.
//
// day is interpreted in local time so the IST-day boundary lines up
// with the user's intuition; the SQL uses sqlite's 'localtime'
// modifier on the epoch-ms ts column.
//
// Returns (nil, nil) when no admitted entries exist — the caller
// (Phase 6 consumer / Phase 7 dashboard) falls back to the legacy
// LLM reflection path on that signal.
func ListWorklogEntriesForDay(ctx context.Context, db *DB, projectPath string, day time.Time) ([]DayEntry, error) {
	const q = `
SELECT session_id, ts, worklog_entry_json
  FROM stop_summaries
 WHERE project_path = ?
   AND date(ts / 1000, 'unixepoch', 'localtime') = ?
   AND worklog_gate_verdict LIKE 'admitted-%'
 ORDER BY ts ASC`
	dayStr := day.Format("2006-01-02")
	rows, err := db.Read().QueryContext(ctx, q, projectPath, dayStr)
	if err != nil {
		return nil, fmt.Errorf("store: list day worklog entries: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var out []DayEntry
	for rows.Next() {
		var d DayEntry
		var raw string
		if err := rows.Scan(&d.SessionID, &d.Ts, &raw); err != nil {
			return nil, fmt.Errorf("store: scan day entry: %w", err)
		}
		// Best-effort unmarshal — a malformed row shouldn't drop the
		// whole day; consumer will see zero categories.
		_ = json.Unmarshal([]byte(raw), &d.Entry)
		out = append(out, d)
	}
	return out, rows.Err()
}

// IncrementWorklogAttempts bumps worklog_attempts by 1 and sets the
// verdict, leaving everything else (the deterministic body, the 015
// worklog metadata, the rich entry_json) untouched. Used by the
// worker on transient LLM failures to schedule a retry on the next
// tick without clobbering any earlier successful entry write.
func IncrementWorklogAttempts(ctx context.Context, db *DB, sessionID string, ts int64, verdict string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("store: increment attempts: session_id required")
	}
	const q = `
UPDATE stop_summaries
   SET worklog_gate_verdict = ?,
       worklog_attempts     = worklog_attempts + 1
 WHERE session_id = ? AND ts = ?`
	res, err := db.Write().ExecContext(ctx, q, verdict, sessionID, ts)
	if err != nil {
		return fmt.Errorf("store: increment worklog attempts: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: increment worklog attempts: no row for (%s, %d)", sessionID, ts)
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

// WorklogEntry is a stop_summaries row including the migration-015
// worklog columns. Returned by ListWorklogEntries for the /worklog
// UI — distinct from StopSummary which is the deterministic core
// the Stop hook writes.
type WorklogEntry struct {
	SessionID    string   `json:"session_id"`
	Ts           int64    `json:"ts"`
	ProjectPath  string   `json:"project_path"`
	CLI          string   `json:"cli"`
	Summary      string   `json:"summary"`
	LastUser     string   `json:"last_user"`
	LastBash     string   `json:"last_bash"`
	Files        []string `json:"files"`
	RecapVisible int      `json:"recap_visible"`
	RecapTopic   string   `json:"recap_topic"`
	Importance   int      `json:"importance"`
	Signature    string   `json:"signature"`
}

// ListWorklogEntriesOpts scopes ListWorklogEntries queries.
type ListWorklogEntriesOpts struct {
	// ProjectPath narrows to one project. Empty returns all projects.
	ProjectPath string
	// Limit caps the result set. Defaults to 500 when zero.
	Limit int
}

// ListWorklogEntries returns stop_summaries rows (with worklog metadata)
// ordered by ts DESC. Includes both visible and suppressed rows.
//
// Uses idx_stop_summaries_project_ts when ProjectPath != "", otherwise
// idx_stop_summaries_ts.
func ListWorklogEntries(ctx context.Context, db *DB, opts ListWorklogEntriesOpts) ([]WorklogEntry, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 500
	}
	q := `
SELECT session_id, ts, project_path, cli, summary, last_user, last_bash,
       files_json, recap_visible, COALESCE(recap_topic,''), importance,
       COALESCE(signature,'')
  FROM stop_summaries
 WHERE 1=1`
	args := make([]any, 0, 2)
	if opts.ProjectPath != "" {
		q += ` AND project_path = ?`
		args = append(args, opts.ProjectPath)
	}
	q += ` ORDER BY ts DESC LIMIT ?`
	args = append(args, limit)

	rows, err := db.Read().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list worklog entries: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := make([]WorklogEntry, 0)
	for rows.Next() {
		var e WorklogEntry
		var filesJSON string
		if err := rows.Scan(
			&e.SessionID, &e.Ts, &e.ProjectPath, &e.CLI, &e.Summary,
			&e.LastUser, &e.LastBash, &filesJSON,
			&e.RecapVisible, &e.RecapTopic, &e.Importance, &e.Signature,
		); err != nil {
			return nil, fmt.Errorf("store: scan worklog entry: %w", err)
		}
		if filesJSON != "" {
			if err := json.Unmarshal([]byte(filesJSON), &e.Files); err != nil {
				// Don't fail the whole query on one bad row — return empty files
				// list. Matches the defensive read pattern above.
				e.Files = nil
			}
		}
		if e.Files == nil {
			e.Files = []string{}
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate worklog entries: %w", err)
	}
	return out, nil
}

// WorklogProjectRollup is one row of the /worklog view — per-project summary
// of reflection coverage and pending-entry pressure.
type WorklogProjectRollup struct {
	ProjectPath      string      `json:"project_path"`
	Name             string      `json:"name"`
	LatestReflection *Reflection `json:"latest_reflection"` // nil if never synthesized
	PendingEntries   int         `json:"pending_entries"`   // visible stop_summaries since latest_reflection.ts (or total visible if never)
	LatestEntryTs    int64       `json:"latest_entry_ts"`   // newest visible entry timestamp (0 if none)
	Stale            bool        `json:"stale"`             // PendingEntries > 0
}

// ListWorklogRollup returns one row per project that has at least one visible
// stop_summaries entry OR at least one worklog_reflections row. Rows are NOT
// sorted by this function — the handler is responsible for the display order.
//
// Cold-start friendly: returns rollups for projects that have entries but no
// reflection yet (PendingEntries set to total visible count, LatestReflection nil).
func ListWorklogRollup(ctx context.Context, db *DB) ([]WorklogProjectRollup, error) {
	// Step 1: collect every project_path that has any visible stop_summaries
	// row OR any worklog_reflections row. UNION avoids the cold-start case
	// where a project has reflections but its entries got cleaned up.
	const projectsQ = `
SELECT DISTINCT project_path FROM stop_summaries WHERE recap_visible = 1 AND project_path <> ''
UNION
SELECT DISTINCT project_path FROM worklog_reflections WHERE project_path <> ''`
	rows, err := db.Read().QueryContext(ctx, projectsQ)
	if err != nil {
		return nil, fmt.Errorf("store: worklog rollup projects: %w", err)
	}
	projects := make([]string, 0)
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close() //nolint:errcheck
			return nil, fmt.Errorf("store: scan project_path: %w", err)
		}
		projects = append(projects, p)
	}
	rows.Close() //nolint:errcheck
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate project_paths: %w", err)
	}

	out := make([]WorklogProjectRollup, 0, len(projects))
	for _, p := range projects {
		row := WorklogProjectRollup{ProjectPath: p, Name: Basename(p)}

		// Latest reflection for this project (may be nil).
		refs, err := ListReflectionsForProject(ctx, db, p, 1)
		if err != nil {
			return nil, fmt.Errorf("store: rollup reflections for %s: %w", p, err)
		}
		var sinceTs int64
		if len(refs) > 0 {
			r := refs[0]
			row.LatestReflection = &r
			sinceTs = r.TS
		}

		// Count visible entries newer than the latest reflection (or all if none).
		// Also fetch the newest entry's ts for display.
		const statsQ = `
SELECT
  COALESCE(SUM(CASE WHEN ts > ? THEN 1 ELSE 0 END), 0) AS pending,
  COALESCE(MAX(ts), 0) AS latest_ts
FROM stop_summaries
WHERE project_path = ? AND recap_visible = 1`
		var pending int
		var latestTs int64
		if err := db.Read().QueryRowContext(ctx, statsQ, sinceTs, p).Scan(&pending, &latestTs); err != nil {
			return nil, fmt.Errorf("store: rollup stats for %s: %w", p, err)
		}
		row.PendingEntries = pending
		row.LatestEntryTs = latestTs
		row.Stale = pending > 0

		out = append(out, row)
	}
	return out, nil
}

// Basename returns the last "/"-separated segment of p. Defined here
// (not in path/filepath) so the worklog code has no implicit dep on the
// OS-aware filepath cleaner — projects use raw absolute Unix-style paths
// inside klyne regardless of host OS.
func Basename(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

