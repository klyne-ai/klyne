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

func TestUpsertStopSummaryWithWorklog(t *testing.T) {
	ctx := context.Background()
	db := openStopSummariesDB(t)

	row := store.StopSummary{
		SessionID:   "sess-w",
		Ts:          7000,
		ProjectPath: "/proj/w",
		CLI:         "claude",
		Summary:     "", // memory-layer rows leave summary empty
		LastUser:    "ship it",
		LastBash:    "git commit -m x",
		Files:       []string{"src/auth.go", "src/db.go"},
	}
	cols := store.WorklogColumns{
		RecapVisible:     1,
		RecapTopic:       "auth",
		AIDraftedSummary: "drafted body",
		DraftState:       "proposed",
		Signature:        "sig-abc",
		Importance:       8,
		LastAccessedAt:   1234567890,
	}

	t.Run("round_trip", func(t *testing.T) {
		if err := store.UpsertStopSummaryWithWorklog(ctx, db, row, cols); err != nil {
			t.Fatalf("upsert: %v", err)
		}

		const q = `SELECT recap_visible, recap_topic, ai_drafted_summary, draft_state,
			signature, importance, last_accessed_at, files_json
			FROM stop_summaries WHERE session_id = ? AND ts = ?`
		var (
			recapVisible    int
			recapTopic      string
			aiDraftedSum    string
			draftState      string
			signature       string
			importance      int
			lastAccessedAt  int64
			filesJSON       string
		)
		if err := db.Read().QueryRowContext(ctx, q, row.SessionID, row.Ts).Scan(
			&recapVisible, &recapTopic, &aiDraftedSum, &draftState,
			&signature, &importance, &lastAccessedAt, &filesJSON,
		); err != nil {
			t.Fatalf("query: %v", err)
		}
		if recapVisible != 1 {
			t.Errorf("recap_visible = %d, want 1", recapVisible)
		}
		if recapTopic != "auth" {
			t.Errorf("recap_topic = %q, want auth", recapTopic)
		}
		if aiDraftedSum != "drafted body" {
			t.Errorf("ai_drafted_summary = %q, want drafted body", aiDraftedSum)
		}
		if draftState != "proposed" {
			t.Errorf("draft_state = %q, want proposed", draftState)
		}
		if signature != "sig-abc" {
			t.Errorf("signature = %q, want sig-abc", signature)
		}
		if importance != 8 {
			t.Errorf("importance = %d, want 8", importance)
		}
		if lastAccessedAt != 1234567890 {
			t.Errorf("last_accessed_at = %d, want 1234567890", lastAccessedAt)
		}
		if filesJSON != `["src/auth.go","src/db.go"]` {
			t.Errorf("files_json = %q, want JSON-encoded list", filesJSON)
		}
	})

	t.Run("on_conflict_updates_worklog_columns", func(t *testing.T) {
		updated := store.WorklogColumns{
			RecapVisible:     0,
			RecapTopic:       "auth-revised",
			AIDraftedSummary: "second pass",
			DraftState:       "accepted",
			Signature:        "sig-xyz",
			Importance:       9,
			LastAccessedAt:   9999999999,
		}
		if err := store.UpsertStopSummaryWithWorklog(ctx, db, row, updated); err != nil {
			t.Fatalf("re-upsert: %v", err)
		}

		const q = `SELECT recap_visible, recap_topic, ai_drafted_summary, draft_state,
			signature, importance, last_accessed_at, COUNT(*) OVER ()
			FROM stop_summaries WHERE session_id = ? AND ts = ?`
		var (
			recapVisible    int
			recapTopic      string
			aiDraftedSum    string
			draftState      string
			signature       string
			importance      int
			lastAccessedAt  int64
			rowCount        int
		)
		if err := db.Read().QueryRowContext(ctx, q, row.SessionID, row.Ts).Scan(
			&recapVisible, &recapTopic, &aiDraftedSum, &draftState,
			&signature, &importance, &lastAccessedAt, &rowCount,
		); err != nil {
			t.Fatalf("query: %v", err)
		}
		if rowCount != 1 {
			t.Errorf("rowCount = %d, want 1 (ON CONFLICT should not insert a 2nd row)", rowCount)
		}
		if recapVisible != 0 || recapTopic != "auth-revised" || aiDraftedSum != "second pass" ||
			draftState != "accepted" || signature != "sig-xyz" || importance != 9 ||
			lastAccessedAt != 9999999999 {
			t.Errorf("ON CONFLICT did not update worklog columns: visible=%d topic=%q ai=%q draft=%q sig=%q imp=%d last=%d",
				recapVisible, recapTopic, aiDraftedSum, draftState, signature, importance, lastAccessedAt)
		}
	})
}
