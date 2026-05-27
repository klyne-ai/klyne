package store_test

import (
	"context"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
)

// seedAskRow inserts one stop_summaries row with the fields LoadAskContext
// projects. Uses UpsertStopSummaryWithWorklog so the row survives the same
// gating any production write would go through.
func seedAskRow(t *testing.T, db *store.DB, sessionID string, ts int64, project, aiSummary, worklogJSON string) {
	t.Helper()
	row := store.StopSummary{
		SessionID:   sessionID,
		Ts:          ts,
		ProjectPath: project,
		CLI:         "claude",
	}
	cols := store.WorklogColumns{
		AIDraftedSummary: aiSummary,
	}
	if err := store.UpsertStopSummaryWithWorklog(context.Background(), db, row, cols); err != nil {
		t.Fatalf("seed %s: %v", sessionID, err)
	}
	// UpsertStopSummaryWithWorklog does not write worklog_entry_json — patch
	// it in directly so we can exercise LoadAskContext's projection of that
	// column.
	if worklogJSON != "" {
		if _, err := db.Write().ExecContext(context.Background(),
			`UPDATE stop_summaries SET worklog_entry_json = ? WHERE session_id = ? AND ts = ?`,
			worklogJSON, sessionID, ts,
		); err != nil {
			t.Fatalf("patch worklog %s: %v", sessionID, err)
		}
	}
}

func TestLoadAskContext_FiltersByRange(t *testing.T) {
	t.Parallel()
	db := openStopSummariesDB(t)
	ctx := context.Background()

	seedAskRow(t, db, "s-before", 100, "/p/a", "before", `{"bugs_fixed":[]}`)
	seedAskRow(t, db, "s-in", 200, "/p/a", "in", `{"bugs_fixed":[{"summary":"x"}]}`)
	seedAskRow(t, db, "s-after", 300, "/p/a", "after", `{"bugs_fixed":[]}`)

	rows, err := store.LoadAskContext(ctx, db, nil, 150, 250)
	if err != nil {
		t.Fatalf("LoadAskContext: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].SessionID != "s-in" {
		t.Errorf("SessionID = %q, want s-in", rows[0].SessionID)
	}
	if rows[0].AIDraftedSummary != "in" {
		t.Errorf("AIDraftedSummary = %q, want in", rows[0].AIDraftedSummary)
	}
	if rows[0].WorklogEntryJSON == "" {
		t.Errorf("WorklogEntryJSON empty; expected JSON")
	}
}

func TestLoadAskContext_FiltersByProjects(t *testing.T) {
	t.Parallel()
	db := openStopSummariesDB(t)
	ctx := context.Background()

	seedAskRow(t, db, "s1", 200, "/p/a", "a", "")
	seedAskRow(t, db, "s2", 200, "/p/b", "b", "")
	seedAskRow(t, db, "s3", 200, "/p/c", "c", "")

	rows, err := store.LoadAskContext(ctx, db, []string{"/p/a", "/p/c"}, 100, 300)
	if err != nil {
		t.Fatalf("LoadAskContext: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2 (got %+v)", len(rows), rows)
	}
	paths := map[string]bool{rows[0].ProjectPath: true, rows[1].ProjectPath: true}
	if !paths["/p/a"] || !paths["/p/c"] {
		t.Errorf("paths = %v, want /p/a + /p/c", paths)
	}
}

func TestLoadAskContext_EmptyResult(t *testing.T) {
	t.Parallel()
	db := openStopSummariesDB(t)
	ctx := context.Background()

	rows, err := store.LoadAskContext(ctx, db, nil, 0, 1000)
	if err != nil {
		t.Fatalf("LoadAskContext: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("len(rows) = %d, want 0", len(rows))
	}
}

func TestLoadAskContext_OrderedAscending(t *testing.T) {
	t.Parallel()
	db := openStopSummariesDB(t)
	ctx := context.Background()

	seedAskRow(t, db, "third", 300, "/p", "", "")
	seedAskRow(t, db, "first", 100, "/p", "", "")
	seedAskRow(t, db, "second", 200, "/p", "", "")

	rows, err := store.LoadAskContext(ctx, db, nil, 0, 1000)
	if err != nil {
		t.Fatalf("LoadAskContext: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3", len(rows))
	}
	got := []string{rows[0].SessionID, rows[1].SessionID, rows[2].SessionID}
	want := []string{"first", "second", "third"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("rows[%d].SessionID = %q, want %q", i, got[i], want[i])
		}
	}
}
