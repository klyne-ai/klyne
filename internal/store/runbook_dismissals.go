package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RunbookDismissal is one row of the runbook_dismissals table.
// Recorded when the user rejects a proposed recurring command
// sequence so the proposer never re-surfaces the same shape.
type RunbookDismissal struct {
	Signature   string `json:"signature"`
	ProjectPath string `json:"project_path"`
	Ts          int64  `json:"ts"`
	Reason      string `json:"reason,omitempty"`
}

// UpsertRunbookDismissal records a dismissal. Re-dismissing the
// same (signature, project_path) is a no-op write — we keep the
// original ts and update only the reason when one is given.
func UpsertRunbookDismissal(ctx context.Context, db *DB, d RunbookDismissal) error {
	if strings.TrimSpace(d.Signature) == "" {
		return errors.New("store: dismissal signature required")
	}
	if d.Ts == 0 {
		d.Ts = time.Now().UnixMilli()
	}
	const q = `
INSERT INTO runbook_dismissals (signature, project_path, ts, reason)
VALUES (?, ?, ?, ?)
ON CONFLICT(signature, project_path) DO UPDATE SET
    reason = excluded.reason`
	_, err := db.Write().ExecContext(ctx, q, d.Signature, d.ProjectPath, d.Ts, d.Reason)
	if err != nil {
		return fmt.Errorf("store: upsert runbook dismissal: %w", err)
	}
	return nil
}

// ListRunbookDismissals returns every dismissal for projectPath.
// Pass empty projectPath to scope to the user's "global" dismissals
// (a sequence the user never wants proposed anywhere).
func ListRunbookDismissals(ctx context.Context, db *DB, projectPath string) ([]RunbookDismissal, error) {
	const q = `SELECT signature, project_path, ts, reason
	             FROM runbook_dismissals
	            WHERE project_path = ?
	            ORDER BY ts DESC`
	rows, err := db.Read().QueryContext(ctx, q, projectPath)
	if err != nil {
		return nil, fmt.Errorf("store: list runbook dismissals: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := make([]RunbookDismissal, 0)
	for rows.Next() {
		var d RunbookDismissal
		if err := rows.Scan(&d.Signature, &d.ProjectPath, &d.Ts, &d.Reason); err != nil {
			return nil, fmt.Errorf("store: scan dismissal: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: dismissal rows: %w", err)
	}
	return out, nil
}

// DismissedSignatureSet returns a set of dismissed signatures for
// quick membership checks during proposal generation. Includes both
// project-scoped and global dismissals so a global "never propose
// this" overrides per-project counts.
func DismissedSignatureSet(ctx context.Context, db *DB, projectPath string) (map[string]struct{}, error) {
	set := map[string]struct{}{}
	add := func(rows []RunbookDismissal) {
		for _, r := range rows {
			set[r.Signature] = struct{}{}
		}
	}
	rows, err := ListRunbookDismissals(ctx, db, projectPath)
	if err != nil {
		return nil, err
	}
	add(rows)
	globals, err := ListRunbookDismissals(ctx, db, "")
	if err != nil {
		return nil, err
	}
	add(globals)
	return set, nil
}

// DeleteRunbookDismissal removes a dismissal so the proposer can
// surface the signature again. Returns sql.ErrNoRows (wrapped)
// when no matching row exists — callers can map that to a 404 or
// a friendly "wasn't dismissed" message.
func DeleteRunbookDismissal(ctx context.Context, db *DB, signature, projectPath string) error {
	const q = `DELETE FROM runbook_dismissals WHERE signature = ? AND project_path = ?`
	res, err := db.Write().ExecContext(ctx, q, signature, projectPath)
	if err != nil {
		return fmt.Errorf("store: delete dismissal: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete dismissal rows: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store: dismissal not found: %w", sql.ErrNoRows)
	}
	return nil
}
