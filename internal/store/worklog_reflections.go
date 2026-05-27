package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Reflection is one row of worklog_reflections — the synthesis tier of
// the cross-AI worklog feature. Citation invariant: at least one
// EvidenceEntryID or EvidenceReflectionID must be non-empty. The
// underlying CHECK constraint enforces this at write time too.
//
// StopSummaryCursorTS is the iterative-reflection cursor
// (docs/features/iterative-reflection.md): this row covers stop_summaries
// with ts <= cursor for its (project_path, day). The next /klyne:reflect
// run for the same day reads MAX(stop_summary_cursor_ts) and asks for
// summaries strictly after that point. Zero means "not set" (legacy
// rows written before the iterative workflow); callers treat that as
// "covers everything up through the row's own ts".
type Reflection struct {
	ID                    string   `json:"id"`
	ProjectPath           string   `json:"project_path"`
	Title                 string   `json:"title"`
	BodyMD                string   `json:"body_md"`
	SummarySource         string   `json:"summary_source"` // "ai" | "user" | "hybrid"
	State                 string   `json:"state"`          // "proposed" | "accepted" | "dismissed"
	Tier                  int      `json:"tier"`           // 1=daily, 2=weekly, 3=quarterly
	Importance            int      `json:"importance"`
	TS                    int64    `json:"ts"`
	StateChangedAt        int64    `json:"state_changed_at"`
	EvidenceEntryIDs      []string `json:"evidence_entry_ids"`
	EvidenceReflectionIDs []string `json:"evidence_reflection_ids"`
	StopSummaryCursorTS   int64    `json:"stop_summary_cursor_ts,omitempty"`
	// Day is the local-zone YYYY-MM-DD this reflection COVERS — distinct
	// from TS (the row's write moment). The dashboard's per-day lookup
	// filters on this column so a catch-up reflection written today for
	// a past day surfaces under the day it covers, not the day it was
	// written. Empty for legacy rows means "use the row's local-ts day"
	// — migration 022 backfills it from the title's trailing date.
	Day string `json:"day,omitempty"`
	// BodyJSON is the typed What-was-done payload (migration 023). Carries
	// the §1.1 schema of docs/plan/2026-05-26-wwd-typed-cards.md — one
	// service per row, one or more details with kind/when/text/evidence/
	// session_id. Empty (NULL in the DB) for legacy / prose-only rows
	// written before the typed-cards workflow; the productivity composer
	// (internal/productivity.ComposeWWD) skips rows where BodyJSON is
	// empty.
	BodyJSON string `json:"body_json,omitempty"`
}

// WWDPayload is the parsed shape of Reflection.BodyJSON — the typed
// "What was done" payload for the productivity dashboard.
//
// Two generations of schema coexist on disk:
//
//   - v1 (legacy, docs/plan/2026-05-26-wwd-typed-cards.md): Details[]
//     with kind/when:HH:MM/text≤200/evidence — what /klyne:productivity-
//     sync wrote before the narrative-card redesign. Old rows still
//     parse and render via the legacy path.
//
//   - v2 (2026-05-27 narrative redesign): ServiceSummary + Cards[] with
//     ticket-grouped title/body (markdown) + typed refs. Drives the
//     stat-tile + per-ticket-card dashboard layout that matches what the
//     user actually wants to read at standup. New writes use Cards;
//     Details stays nil.
//
// ParseBodyJSON returns this shape unchanged for both generations; the
// productivity composer (internal/productivity.ComposeWWD) prefers
// Cards when present and falls back to Details otherwise.
type WWDPayload struct {
	Service        string      `json:"service"`
	ServiceSummary string      `json:"service_summary,omitempty"`
	Stats          *WWDStats   `json:"stats,omitempty"`
	Cards          []WWDCard   `json:"cards,omitempty"`
	Followup       string      `json:"followup,omitempty"`
	// Legacy v1 — kept for backward-compat reads. New writes leave this
	// nil and populate Cards instead.
	Details []WWDDetail `json:"details,omitempty"`
}

// WWDStats are the derived per-kind counts shown as stat tiles on the
// dashboard. Computed mechanically from Cards by the composer, but the
// writer may populate them defensively for legacy reads.
type WWDStats struct {
	Shipped      int `json:"shipped"`
	Fixed        int `json:"fixed"`
	Decisions    int `json:"decisions"`
	Investigated int `json:"investigated"`
	InProgress   int `json:"in_progress,omitempty"`
}

// WWDCard is one narrative card on a service's dashboard panel. ONE
// card per (ticket, kind) pair — a single ticket can produce multiple
// cards under different sections (e.g. CLI-1452 with a SHIPPED card AND
// a FIXED card for a dead-code revert).
//
// Body is markdown prose (2-4 sentences), not a verb-led one-liner.
// Refs are typed so the UI can style file paths / branches / PRs /
// commits differently — file paths get monospace inline, PR/branch
// names get a distinct chip.
type WWDCard struct {
	Kind     string   `json:"kind"`               // SHIPPED|FIXED|DECISION|INVESTIGATED|MAJOR|IN_PROGRESS
	TicketID string   `json:"ticket_id,omitempty"` // e.g. CLI-1473; omit for ticket-less work
	Title    string   `json:"title"`              // ≤120 chars, outcome-led (not verb-led)
	Body     string   `json:"body"`               // ≤800 chars, markdown prose narrative
	Refs     []WWDRef `json:"refs,omitempty"`
}

// WWDRef is one typed reference token shown in the card's footer.
// Type lets the UI pick styling (file path → monospace inline,
// PR/commit/branch/ticket → chip with subtle background).
type WWDRef struct {
	Type string `json:"type"` // file|branch|pr|commit|ticket|test|session
	Text string `json:"text"`
}

// WWDDetail is the legacy v1 detail shape. Retained for back-compat
// reads of pre-2026-05-27 rows. New writers use Cards.
type WWDDetail struct {
	Kind      string   `json:"kind"`
	When      string   `json:"when"`
	Text      string   `json:"text"`
	Evidence  []string `json:"evidence"`
	SessionID string   `json:"session_id,omitempty"`
}

// ParseBodyJSON parses the typed What-was-done payload stored in
// Reflection.BodyJSON. Returns an empty WWDPayload with no error when
// BodyJSON is empty / whitespace — the typed-cards composer treats
// legacy prose-only rows as "no typed contribution" and skips them
// without surfacing as an error.
func ParseBodyJSON(r Reflection) (WWDPayload, error) {
	body := strings.TrimSpace(r.BodyJSON)
	if body == "" {
		return WWDPayload{}, nil
	}
	var p WWDPayload
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		return WWDPayload{}, fmt.Errorf("store: parse worklog_reflection body_json: %w", err)
	}
	return p, nil
}

// InsertReflection writes one synthesized reflection. Enforces the
// citation invariant in code (defensive; the schema CHECK enforces it
// at the DB layer too).
func InsertReflection(ctx context.Context, db *DB, r Reflection) error {
	if strings.TrimSpace(r.ID) == "" {
		return errors.New("store: reflection id required")
	}
	if len(r.EvidenceEntryIDs) == 0 && len(r.EvidenceReflectionIDs) == 0 {
		return errors.New("store: worklog_reflections: citation invariant — at least one evidence ID required")
	}
	entryIDs, err := json.Marshal(r.EvidenceEntryIDs)
	if err != nil {
		return fmt.Errorf("store: marshal entry ids: %w", err)
	}
	// The schema CHECK requires length > 2 on evidence_entry_ids_json; a JSON
	// array like ["x"] satisfies that. Empty arrays would emit "[]" (len 2)
	// and be rejected — the code guard above prevents that case.
	refIDs, err := json.Marshal(r.EvidenceReflectionIDs)
	if err != nil {
		return fmt.Errorf("store: marshal reflection ids: %w", err)
	}
	if len(r.EvidenceEntryIDs) == 0 {
		// Schema CHECK rejects '[]' even though the code allows reflection-only
		// evidence; insert a sentinel single-element array to satisfy the CHECK
		// while preserving the semantic that no per-episode evidence was used.
		entryIDs = []byte(`[""]`)
	}
	// stop_summary_cursor_ts is NULL when the caller didn't set it
	// (legacy path); use a typed nullable to preserve that distinction.
	var cursor any
	if r.StopSummaryCursorTS > 0 {
		cursor = r.StopSummaryCursorTS
	}
	// Defensive default for Day: mirror migration 022's fallback path
	// (local-ts day) so legacy callers that don't set Day write rows
	// that the per-day lookup can still locate. Production writers
	// (recordReflection) always set Day explicitly.
	if strings.TrimSpace(r.Day) == "" && r.TS > 0 {
		r.Day = time.UnixMilli(r.TS).Local().Format("2006-01-02")
	}
	// body_json is the migration-023 typed payload; persist NULL when the
	// caller didn't set it (legacy prose-only path) so the column's
	// "absent vs empty-string" distinction survives a round-trip.
	var bodyJSON any
	if strings.TrimSpace(r.BodyJSON) != "" {
		bodyJSON = r.BodyJSON
	}
	_, err = db.Write().ExecContext(ctx,
		`INSERT INTO worklog_reflections (
            id, ts, project_path, tier, title, body_md,
            evidence_entry_ids_json, evidence_reflection_ids_json,
            importance, summary_source, state, state_changed_at,
            stop_summary_cursor_ts, day, body_json
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.TS, r.ProjectPath, r.Tier, r.Title, r.BodyMD,
		string(entryIDs), string(refIDs),
		r.Importance, r.SummarySource, r.State, r.StateChangedAt,
		cursor, r.Day, bodyJSON)
	if err != nil {
		return fmt.Errorf("store: insert reflection: %w", err)
	}
	return nil
}

// ListReflectionsForProject returns reflections for one project newest-first.
// The secondary `title DESC` sort breaks ts ties when /klyne:reflect writes
// multiple daily reflections in one run (each call shares millisecond-scale
// ts with its peers). Daily titles end in YYYY-MM-DD which sorts lexically
// in chronological order, so the secondary sort puts newest day first.
func ListReflectionsForProject(ctx context.Context, db *DB, projectPath string, limit int) ([]Reflection, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := db.Read().QueryContext(ctx,
		`SELECT id, ts, project_path, tier, title, body_md,
                evidence_entry_ids_json, evidence_reflection_ids_json,
                importance, summary_source, state, state_changed_at,
                COALESCE(stop_summary_cursor_ts, 0), day,
                COALESCE(body_json, '')
         FROM worklog_reflections
         WHERE project_path = ?
         ORDER BY ts DESC, title DESC LIMIT ?`,
		projectPath, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list reflections: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	out := make([]Reflection, 0)
	for rows.Next() {
		var r Reflection
		var entryJSON, refJSON string
		if err := rows.Scan(&r.ID, &r.TS, &r.ProjectPath, &r.Tier, &r.Title, &r.BodyMD,
			&entryJSON, &refJSON, &r.Importance, &r.SummarySource, &r.State, &r.StateChangedAt,
			&r.StopSummaryCursorTS, &r.Day, &r.BodyJSON); err != nil {
			return nil, fmt.Errorf("store: scan reflection: %w", err)
		}
		_ = json.Unmarshal([]byte(entryJSON), &r.EvidenceEntryIDs)
		_ = json.Unmarshal([]byte(refJSON), &r.EvidenceReflectionIDs)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteTypedReflectionsForProjectDayService removes every typed
// worklog_reflections row scoped to (projectPath, day) whose
// body_json.service matches `service`. The typed-cards composer
// (productivity.ComposeWWD) reads ALL rows for a (project, day) and
// merges details by service — without this delete-before-insert, a
// re-reflect of the same day would accumulate stale typed payloads and
// the dashboard would show ghost details from earlier runs. Prose-only
// rows (body_json NULL) are left UNTOUCHED so the legacy prose panel
// retains its insights even when a typed pass overwrites the day.
//
// Returns the count of rows removed for caller telemetry. Errors when
// the DELETE itself fails — the caller should fail the whole reflection
// write rather than risk ghost rows.
func DeleteTypedReflectionsForProjectDayService(
	ctx context.Context, db *DB, projectPath, day, service string,
) (int, error) {
	if strings.TrimSpace(projectPath) == "" || strings.TrimSpace(day) == "" {
		return 0, errors.New("store: delete typed reflections: project_path and day required")
	}
	if strings.TrimSpace(service) == "" {
		return 0, errors.New("store: delete typed reflections: service required")
	}
	// SQLite has no JSON path operator in older builds and we don't want
	// to assume json1 is loaded; scan candidate rows and delete by id.
	const selectQ = `
SELECT id, COALESCE(body_json, '')
  FROM worklog_reflections
 WHERE project_path = ?
   AND day = ?
   AND body_json IS NOT NULL`
	rows, err := db.Read().QueryContext(ctx, selectQ, projectPath, day)
	if err != nil {
		return 0, fmt.Errorf("store: select typed reflections for delete: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	var idsToDelete []string
	for rows.Next() {
		var id, bodyJSON string
		if err := rows.Scan(&id, &bodyJSON); err != nil {
			return 0, fmt.Errorf("store: scan typed reflection: %w", err)
		}
		if strings.TrimSpace(bodyJSON) == "" {
			continue
		}
		var p WWDPayload
		if err := json.Unmarshal([]byte(bodyJSON), &p); err != nil {
			// Malformed row: skip rather than crash the upsert.
			continue
		}
		if strings.EqualFold(strings.TrimSpace(p.Service), strings.TrimSpace(service)) {
			idsToDelete = append(idsToDelete, id)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: iterate typed reflections: %w", err)
	}
	if len(idsToDelete) == 0 {
		return 0, nil
	}
	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: begin delete typed reflections tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	for _, id := range idsToDelete {
		if _, err := tx.ExecContext(ctx, `DELETE FROM worklog_reflections WHERE id = ?`, id); err != nil {
			return 0, fmt.Errorf("store: delete typed reflection %s: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit delete typed reflections: %w", err)
	}
	return len(idsToDelete), nil
}

// MaxReflectionCursor returns the largest "covered up through ts"
// watermark across EVERY reflection row for projectPath, regardless of
// day. This is the "what work has any reflection ever summarized"
// anchor used when /klyne:reflect is run without a specific day — the
// proposer fetches all visible stop_summaries with ts > cursor and the
// AI host buckets them by date to write one reflection per day.
//
// Returns 0 when no reflection row exists at all — callers treat that
// as "no cursor yet — every visible entry is pending."
func MaxReflectionCursor(ctx context.Context, db *DB, projectPath string) (int64, error) {
	const q = `
SELECT COALESCE(MAX(COALESCE(stop_summary_cursor_ts, ts)), 0)
FROM worklog_reflections
WHERE project_path = ?`
	var cursor int64
	if err := db.Read().QueryRowContext(ctx, q, projectPath).Scan(&cursor); err != nil {
		return 0, fmt.Errorf("store: max reflection cursor: %w", err)
	}
	return cursor, nil
}

// MaxReflectionCursorForDay returns the largest "covered up through ts"
// watermark across rows for (projectPath, day) — the "what work have we
// already summarized" anchor used by /klyne:reflect to fetch only the
// stop_summaries that arrived after the previous run.
//
// dayStr is the local-date string the row was filed under (YYYY-MM-DD,
// matching what the productivity dashboard uses). Filters on the
// `day` column (migration 022) — the explicit COVERED day — so a
// catch-up reflection written today for a past day advances that past
// day's cursor, not today's.
//
// Legacy rows (written before the iterative workflow) have NULL in
// stop_summary_cursor_ts; we fall back to the row's own ts so they
// behave as "covers everything up through when I was written" — that
// matches their pre-change effective semantics. Pre-022 rows have been
// backfilled with day = parsed-from-title (or local-ts-day on fallback).
//
// Returns 0 only when no row exists at all for the day. Callers treat
// that as "no cursor yet — fetch every stop_summary for the day."
func MaxReflectionCursorForDay(ctx context.Context, db *DB, projectPath, dayStr string) (int64, error) {
	const q = `
SELECT COALESCE(MAX(COALESCE(stop_summary_cursor_ts, ts)), 0)
FROM worklog_reflections
WHERE project_path = ?
  AND day = ?`
	var cursor int64
	err := db.Read().QueryRowContext(ctx, q, projectPath, dayStr).Scan(&cursor)
	if err != nil {
		return 0, fmt.Errorf("store: max reflection cursor: %w", err)
	}
	return cursor, nil
}

// ListReflectionsForProjectDay returns every reflection row for
// (projectPath, dayStr) ordered ts ASC — chronologically, so the
// productivity dashboard can render T1/T2/T3 groups in the order they
// were written (see docs/features/iterative-reflection.md, A1).
//
// Filters on the `day` column (migration 022) — the explicit COVERED
// day — NOT the row's local write-ts. Catch-up reflections written
// today for past days surface under the day they cover.
func ListReflectionsForProjectDay(ctx context.Context, db *DB, projectPath, dayStr string) ([]Reflection, error) {
	rows, err := db.Read().QueryContext(ctx,
		`SELECT id, ts, project_path, tier, title, body_md,
                evidence_entry_ids_json, evidence_reflection_ids_json,
                importance, summary_source, state, state_changed_at,
                COALESCE(stop_summary_cursor_ts, 0), day,
                COALESCE(body_json, '')
         FROM worklog_reflections
         WHERE project_path = ?
           AND day = ?
         ORDER BY ts ASC`,
		projectPath, dayStr)
	if err != nil {
		return nil, fmt.Errorf("store: list reflections for day: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	out := make([]Reflection, 0)
	for rows.Next() {
		var r Reflection
		var entryJSON, refJSON string
		if err := rows.Scan(&r.ID, &r.TS, &r.ProjectPath, &r.Tier, &r.Title, &r.BodyMD,
			&entryJSON, &refJSON, &r.Importance, &r.SummarySource, &r.State, &r.StateChangedAt,
			&r.StopSummaryCursorTS, &r.Day, &r.BodyJSON); err != nil {
			return nil, fmt.Errorf("store: scan reflection: %w", err)
		}
		_ = json.Unmarshal([]byte(entryJSON), &r.EvidenceEntryIDs)
		_ = json.Unmarshal([]byte(refJSON), &r.EvidenceReflectionIDs)
		out = append(out, r)
	}
	return out, rows.Err()
}
