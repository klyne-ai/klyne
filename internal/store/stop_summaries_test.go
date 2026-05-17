package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
)

func openStopSummariesDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "stop.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestInsertStopSummary_RoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openStopSummariesDB(t)
	s := &store.StopSummary{
		SessionID:   "sess-1",
		Ts:          1000,
		ProjectPath: "/proj",
		CLI:         "claude",
		Summary:     "# klyne session-end\n\nDid a thing.",
		LastUser:    "deploy please",
		LastBash:    "npm run deploy",
		Files:       []string{"a.go", "b.go"},
	}
	if err := store.InsertStopSummary(ctx, db, s); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := store.LatestStopSummaryForProject(ctx, db, "/proj")
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if got == nil {
		t.Fatal("expected a row")
	}
	if got.SessionID != "sess-1" || got.LastBash != "npm run deploy" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if len(got.Files) != 2 || got.Files[0] != "a.go" {
		t.Errorf("files lost: %+v", got.Files)
	}
}

func TestInsertStopSummary_UpsertOnSameTs(t *testing.T) {
	ctx := context.Background()
	db := openStopSummariesDB(t)
	s := &store.StopSummary{
		SessionID: "sess-1", Ts: 500,
		ProjectPath: "/p", Summary: "first",
	}
	if err := store.InsertStopSummary(ctx, db, s); err != nil {
		t.Fatalf("first: %v", err)
	}
	s.Summary = "second"
	s.LastUser = "redo"
	if err := store.InsertStopSummary(ctx, db, s); err != nil {
		t.Fatalf("second: %v", err)
	}
	rows, err := store.ListStopSummariesForProject(ctx, db, "/p", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after upsert, got %d", len(rows))
	}
	if rows[0].Summary != "second" || rows[0].LastUser != "redo" {
		t.Errorf("upsert did not overwrite: %+v", rows[0])
	}
}

func TestListStopSummaries_SortedDesc(t *testing.T) {
	ctx := context.Background()
	db := openStopSummariesDB(t)
	for _, ts := range []int64{100, 300, 200} {
		_ = store.InsertStopSummary(ctx, db, &store.StopSummary{
			SessionID: "s", Ts: ts, ProjectPath: "/p", Summary: "x",
		})
	}
	rows, err := store.ListStopSummariesForProject(ctx, db, "/p", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 3 || rows[0].Ts != 300 || rows[2].Ts != 100 {
		t.Errorf("unexpected order: %+v", rows)
	}
}

func TestLatestStopSummaryForProject_None(t *testing.T) {
	ctx := context.Background()
	db := openStopSummariesDB(t)
	got, err := store.LatestStopSummaryForProject(ctx, db, "/empty")
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for empty project, got %+v", got)
	}
}

func TestListStopSummariesForSession(t *testing.T) {
	db := openStopSummariesDB(t)
	ctx := context.Background()

	// Two summaries for session A (different ts), one for session B.
	for _, s := range []*store.StopSummary{
		{SessionID: "sess-A", Ts: 1000, ProjectPath: "/p/a", Summary: "a-first"},
		{SessionID: "sess-A", Ts: 2000, ProjectPath: "/p/a", Summary: "a-second"},
		{SessionID: "sess-B", Ts: 1500, ProjectPath: "/p/b", Summary: "b-only"},
	} {
		if err := store.InsertStopSummary(ctx, db, s); err != nil {
			t.Fatalf("insert %s/%d: %v", s.SessionID, s.Ts, err)
		}
	}

	got, err := store.ListStopSummariesForSession(ctx, db, "sess-A", 10)
	if err != nil {
		t.Fatalf("ListStopSummariesForSession: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	// Newest first.
	if got[0].Ts != 2000 || got[1].Ts != 1000 {
		t.Errorf("ts order = %d,%d, want 2000,1000", got[0].Ts, got[1].Ts)
	}
	if got[0].Summary != "a-second" {
		t.Errorf("got[0].Summary = %q, want a-second", got[0].Summary)
	}

	// Empty session id → empty result, not all rows.
	none, err := store.ListStopSummariesForSession(ctx, db, "sess-missing", 10)
	if err != nil {
		t.Fatalf("missing session: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("missing session returned %d rows, want 0", len(none))
	}
}

func TestListStopSummariesForSession_EmptyIDRejected(t *testing.T) {
	db := openStopSummariesDB(t)
	ctx := context.Background()
	_, err := store.ListStopSummariesForSession(ctx, db, "", 10)
	if err == nil {
		t.Fatal("expected error for empty session_id, got nil")
	}
}
