package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	SummarySource         string   `json:"summary_source"`         // "ai" | "user" | "hybrid"
	State                 string   `json:"state"`                  // "proposed" | "accepted" | "dismissed"
	Tier                  int      `json:"tier"`                   // 1=daily, 2=weekly, 3=quarterly
	Importance            int      `json:"importance"`
	TS                    int64    `json:"ts"`
	StateChangedAt        int64    `json:"state_changed_at"`
	EvidenceEntryIDs      []string `json:"evidence_entry_ids"`
	EvidenceReflectionIDs []string `json:"evidence_reflection_ids"`
	StopSummaryCursorTS   int64    `json:"stop_summary_cursor_ts,omitempty"`
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
	_, err = db.Write().ExecContext(ctx,
		`INSERT INTO worklog_reflections (
            id, ts, project_path, tier, title, body_md,
            evidence_entry_ids_json, evidence_reflection_ids_json,
            importance, summary_source, state, state_changed_at,
            stop_summary_cursor_ts
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.TS, r.ProjectPath, r.Tier, r.Title, r.BodyMD,
		string(entryIDs), string(refIDs),
		r.Importance, r.SummarySource, r.State, r.StateChangedAt,
		cursor)
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
                COALESCE(stop_summary_cursor_ts, 0)
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
			&r.StopSummaryCursorTS); err != nil {
			return nil, fmt.Errorf("store: scan reflection: %w", err)
		}
		_ = json.Unmarshal([]byte(entryJSON), &r.EvidenceEntryIDs)
		_ = json.Unmarshal([]byte(refJSON), &r.EvidenceReflectionIDs)
		out = append(out, r)
	}
	return out, rows.Err()
}

// MaxReflectionCursorForDay returns the largest "covered up through ts"
// watermark across rows for (projectPath, day) — the "what work have we
// already summarized" anchor used by /klyne:reflect to fetch only the
// stop_summaries that arrived after the previous run.
//
// dayStr is the local-date string the row was filed under (YYYY-MM-DD,
// matching what the productivity dashboard uses). The window is taken
// against the row's ts converted to local time, mirroring how the
// dashboard's reflection lookup keys by day.
//
// Legacy rows (written before the iterative workflow) have NULL in
// stop_summary_cursor_ts; we fall back to the row's own ts so they
// behave as "covers everything up through when I was written" — that
// matches their pre-change effective semantics.
//
// Returns 0 only when no row exists at all for the day. Callers treat
// that as "no cursor yet — fetch every stop_summary for the day."
func MaxReflectionCursorForDay(ctx context.Context, db *DB, projectPath, dayStr string) (int64, error) {
	const q = `
SELECT COALESCE(MAX(COALESCE(stop_summary_cursor_ts, ts)), 0)
FROM worklog_reflections
WHERE project_path = ?
  AND date(ts / 1000, 'unixepoch', 'localtime') = ?`
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
func ListReflectionsForProjectDay(ctx context.Context, db *DB, projectPath, dayStr string) ([]Reflection, error) {
	rows, err := db.Read().QueryContext(ctx,
		`SELECT id, ts, project_path, tier, title, body_md,
                evidence_entry_ids_json, evidence_reflection_ids_json,
                importance, summary_source, state, state_changed_at,
                COALESCE(stop_summary_cursor_ts, 0)
         FROM worklog_reflections
         WHERE project_path = ?
           AND date(ts / 1000, 'unixepoch', 'localtime') = ?
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
			&r.StopSummaryCursorTS); err != nil {
			return nil, fmt.Errorf("store: scan reflection: %w", err)
		}
		_ = json.Unmarshal([]byte(entryJSON), &r.EvidenceEntryIDs)
		_ = json.Unmarshal([]byte(refJSON), &r.EvidenceReflectionIDs)
		out = append(out, r)
	}
	return out, rows.Err()
}
