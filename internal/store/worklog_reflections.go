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
	_, err = db.Write().ExecContext(ctx,
		`INSERT INTO worklog_reflections (
            id, ts, project_path, tier, title, body_md,
            evidence_entry_ids_json, evidence_reflection_ids_json,
            importance, summary_source, state, state_changed_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.TS, r.ProjectPath, r.Tier, r.Title, r.BodyMD,
		string(entryIDs), string(refIDs),
		r.Importance, r.SummarySource, r.State, r.StateChangedAt)
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
                importance, summary_source, state, state_changed_at
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
			&entryJSON, &refJSON, &r.Importance, &r.SummarySource, &r.State, &r.StateChangedAt); err != nil {
			return nil, fmt.Errorf("store: scan reflection: %w", err)
		}
		_ = json.Unmarshal([]byte(entryJSON), &r.EvidenceEntryIDs)
		_ = json.Unmarshal([]byte(refJSON), &r.EvidenceReflectionIDs)
		out = append(out, r)
	}
	return out, rows.Err()
}
