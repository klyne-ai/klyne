package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ProjectDeleteCounts is the per-table row count touched by a
// project-wide delete. Same shape is returned from a dry-run (counts
// that would be deleted) and from the real delete (counts actually
// removed). Total is the sum of all per-table fields.
type ProjectDeleteCounts struct {
	StopSummaries       int `json:"stop_summaries"`
	WorklogReflections  int `json:"worklog_reflections"`
	Decisions           int `json:"decisions"`
	RunbookDismissals   int `json:"runbook_dismissals"`
	WorkSpans           int `json:"work_spans"`
	GitSessionSnapshots int `json:"git_session_snapshots"`
	Total               int `json:"total"`
}

// projectScopedTables lists every table the project-wide delete touches.
// Sessions, threads, messages, thread_sessions, messages_fts, and
// deleted_sessions are intentionally excluded — they are the transcript
// layer or audit/index data that must survive a project wipe.
var projectScopedTables = []string{
	"stop_summaries",
	"worklog_reflections",
	"decisions",
	"runbook_dismissals",
	"work_spans",
	"git_session_snapshots",
}

// DeleteProjectScopedRows clears every row keyed to projectPath across
// the project-scoped tables. When dryRun is true the function only
// counts (no mutation); when false it deletes inside one BEGIN
// IMMEDIATE transaction so the wipe is atomic.
func DeleteProjectScopedRows(ctx context.Context, db *DB, projectPath string, dryRun bool) (ProjectDeleteCounts, error) {
	if strings.TrimSpace(projectPath) == "" {
		return ProjectDeleteCounts{}, errors.New("store: project path required")
	}

	if dryRun {
		return countProjectScopedRows(ctx, db, projectPath)
	}

	tx, err := db.Write().BeginTx(ctx, nil)
	if err != nil {
		return ProjectDeleteCounts{}, fmt.Errorf("store: delete project rows begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	counts, err := deleteProjectScopedRowsTx(ctx, tx, projectPath)
	if err != nil {
		return ProjectDeleteCounts{}, err
	}

	if err := tx.Commit(); err != nil {
		return ProjectDeleteCounts{}, fmt.Errorf("store: delete project rows commit: %w", err)
	}
	return counts, nil
}

func countProjectScopedRows(ctx context.Context, db *DB, projectPath string) (ProjectDeleteCounts, error) {
	var counts ProjectDeleteCounts
	targets := []struct {
		table string
		dst   *int
	}{
		{"stop_summaries", &counts.StopSummaries},
		{"worklog_reflections", &counts.WorklogReflections},
		{"decisions", &counts.Decisions},
		{"runbook_dismissals", &counts.RunbookDismissals},
		{"work_spans", &counts.WorkSpans},
		{"git_session_snapshots", &counts.GitSessionSnapshots},
	}
	for _, t := range targets {
		// Table names are not user-supplied — they come from projectScopedTables.
		q := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE project_path = ?`, t.table)
		if err := db.Read().QueryRowContext(ctx, q, projectPath).Scan(t.dst); err != nil {
			return ProjectDeleteCounts{}, fmt.Errorf("store: count %s: %w", t.table, err)
		}
		counts.Total += *t.dst
	}
	return counts, nil
}

func deleteProjectScopedRowsTx(ctx context.Context, tx *sql.Tx, projectPath string) (ProjectDeleteCounts, error) {
	var counts ProjectDeleteCounts
	targets := []struct {
		table string
		dst   *int
	}{
		{"stop_summaries", &counts.StopSummaries},
		{"worklog_reflections", &counts.WorklogReflections},
		{"decisions", &counts.Decisions},
		{"runbook_dismissals", &counts.RunbookDismissals},
		{"work_spans", &counts.WorkSpans},
		{"git_session_snapshots", &counts.GitSessionSnapshots},
	}
	for _, t := range targets {
		q := fmt.Sprintf(`DELETE FROM %s WHERE project_path = ?`, t.table)
		res, err := tx.ExecContext(ctx, q, projectPath)
		if err != nil {
			return ProjectDeleteCounts{}, fmt.Errorf("store: delete %s: %w", t.table, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return ProjectDeleteCounts{}, fmt.Errorf("store: delete %s rows-affected: %w", t.table, err)
		}
		*t.dst = int(n)
		counts.Total += int(n)
	}
	return counts, nil
}
