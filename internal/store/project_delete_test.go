package store_test

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
)

func openProjectDeleteDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "pd.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seedProjectScopedRows writes rows into every project-scoped table for both
// projectA and projectB so isolation can be verified after a project-A delete.
//
// Counts written for projectA (test asserts exact counts):
//   - 2 stop_summaries
//   - 1 worklog_reflections
//   - 2 decisions
//   - 1 runbook_dismissals
//   - 1 work_spans
//   - 1 git_session_snapshots
func seedProjectScopedRows(t *testing.T, db *store.DB, projectA, projectB string) {
	t.Helper()
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Write().ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("seed exec: %v\n%s", err, q)
		}
	}

	// stop_summaries (NOT NULL with no default: session_id, ts, summary).
	for i, ts := range []int64{1000, 2000} {
		exec(`INSERT INTO stop_summaries (session_id, ts, project_path, summary)
		      VALUES (?, ?, ?, 'stop-a')`, "sA-"+strconv.Itoa(i+1), ts, projectA)
	}
	exec(`INSERT INTO stop_summaries (session_id, ts, project_path, summary)
	      VALUES ('sB-1', 9000, ?, 'stop-b')`, projectB)

	// worklog_reflections: id/ts/tier/title/body_md/state_changed_at required;
	// CHECK requires evidence_entry_ids_json length > 2.
	exec(`INSERT INTO worklog_reflections
	      (id, ts, project_path, tier, title, body_md,
	       evidence_entry_ids_json, state_changed_at)
	      VALUES ('refl-a-1', 5000, ?, 1, 'reflection A',
	              'body', '["e1"]', 5000)`, projectA)
	exec(`INSERT INTO worklog_reflections
	      (id, ts, project_path, tier, title, body_md,
	       evidence_entry_ids_json, state_changed_at)
	      VALUES ('refl-b-1', 5100, ?, 1, 'reflection B',
	              'body', '["e1"]', 5100)`, projectB)

	// decisions: id/ts/text required.
	for _, id := range []string{"dec-a-1", "dec-a-2"} {
		exec(`INSERT INTO decisions (id, ts, project_path, text)
		      VALUES (?, 1, ?, 'dec a')`, id, projectA)
	}
	exec(`INSERT INTO decisions (id, ts, project_path, text)
	      VALUES ('dec-b-1', 1, ?, 'dec b')`, projectB)

	// runbook_dismissals: PK (signature, project_path).
	exec(`INSERT INTO runbook_dismissals (signature, project_path, ts, reason)
	      VALUES ('rb-a', ?, 1, '')`, projectA)
	exec(`INSERT INTO runbook_dismissals (signature, project_path, ts, reason)
	      VALUES ('rb-b', ?, 1, '')`, projectB)

	// work_spans: bucket/opened_at/closed_at required.
	exec(`INSERT INTO work_spans (bucket, project_path, opened_at, closed_at)
	      VALUES ('exploration', ?, 100, 200)`, projectA)
	exec(`INSERT INTO work_spans (bucket, project_path, opened_at, closed_at)
	      VALUES ('exploration', ?, 100, 200)`, projectB)

	// git_session_snapshots: project_path/repo_name/captured_at required.
	exec(`INSERT INTO git_session_snapshots
	      (project_path, repo_name, branch, head_sha, captured_at)
	      VALUES (?, 'repoA', 'main', 'deadbeef', 100)`, projectA)
	exec(`INSERT INTO git_session_snapshots
	      (project_path, repo_name, branch, head_sha, captured_at)
	      VALUES (?, 'repoB', 'main', 'cafebabe', 100)`, projectB)
}

func TestDeleteProjectScopedRows_DryRunCountsOnly(t *testing.T) {
	ctx := context.Background()
	db := openProjectDeleteDB(t)
	const projectA, projectB = "/proj/a", "/proj/b"
	seedProjectScopedRows(t, db, projectA, projectB)

	got, err := store.DeleteProjectScopedRows(ctx, db, projectA, true /*dryRun*/)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	want := store.ProjectDeleteCounts{
		StopSummaries:       2,
		WorklogReflections:  1,
		Decisions:           2,
		RunbookDismissals:   1,
		WorkSpans:           1,
		GitSessionSnapshots: 1,
		Total:               8,
	}
	if got != want {
		t.Errorf("dry-run counts: got %+v, want %+v", got, want)
	}

	// Dry-run is read-only — re-run, expect identical numbers.
	again, err := store.DeleteProjectScopedRows(ctx, db, projectA, true)
	if err != nil {
		t.Fatalf("dry-run #2: %v", err)
	}
	if again != want {
		t.Errorf("dry-run is not read-only: first %+v, second %+v", want, again)
	}
}

func TestDeleteProjectScopedRows_RealDeleteRemovesOnlyTargetProject(t *testing.T) {
	ctx := context.Background()
	db := openProjectDeleteDB(t)
	const projectA, projectB = "/proj/a", "/proj/b"
	seedProjectScopedRows(t, db, projectA, projectB)

	counts, err := store.DeleteProjectScopedRows(ctx, db, projectA, false)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if counts.Total != 8 {
		t.Errorf("delete total: got %d, want 8", counts.Total)
	}

	// Re-run on the same project — everything is gone, all counts zero.
	zero, err := store.DeleteProjectScopedRows(ctx, db, projectA, false)
	if err != nil {
		t.Fatalf("delete idempotent: %v", err)
	}
	if zero.Total != 0 {
		t.Errorf("second delete should report 0, got %+v", zero)
	}

	// Project B should be completely untouched.
	preview, err := store.DeleteProjectScopedRows(ctx, db, projectB, true)
	if err != nil {
		t.Fatalf("dry-run B: %v", err)
	}
	wantB := store.ProjectDeleteCounts{
		StopSummaries:       1,
		WorklogReflections:  1,
		Decisions:           1,
		RunbookDismissals:   1,
		WorkSpans:           1,
		GitSessionSnapshots: 1,
		Total:               6,
	}
	if preview != wantB {
		t.Errorf("project B disturbed: got %+v, want %+v", preview, wantB)
	}
}

func TestDeleteProjectScopedRows_EmptyPathErrors(t *testing.T) {
	db := openProjectDeleteDB(t)
	if _, err := store.DeleteProjectScopedRows(context.Background(), db, "", false); err == nil {
		t.Error("expected error for empty project path, got nil")
	}
}

func TestDeleteProjectScopedRows_UnknownPathReturnsZeros(t *testing.T) {
	ctx := context.Background()
	db := openProjectDeleteDB(t)
	const projectA, projectB = "/proj/a", "/proj/b"
	seedProjectScopedRows(t, db, projectA, projectB)

	counts, err := store.DeleteProjectScopedRows(ctx, db, "/proj/ghost", false)
	if err != nil {
		t.Fatalf("delete unknown: %v", err)
	}
	if counts.Total != 0 {
		t.Errorf("expected zero counts for unknown path, got %+v", counts)
	}

	// A and B still intact.
	a, _ := store.DeleteProjectScopedRows(ctx, db, projectA, true)
	b, _ := store.DeleteProjectScopedRows(ctx, db, projectB, true)
	if a.Total == 0 || b.Total == 0 {
		t.Errorf("unknown-path delete touched real data: A=%+v B=%+v", a, b)
	}
}
