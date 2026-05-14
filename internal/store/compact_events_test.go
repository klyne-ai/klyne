package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// openCompactTestDB opens a fresh test database and seeds a session row
// so compact_events rows have a valid foreign key target.
func openCompactTestDB(t *testing.T) (*store.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "compact_test.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sessID := "sess-compact-001"
	ctx := context.Background()
	now := time.Now().UnixMilli()
	_, err = db.Write().ExecContext(ctx, `
		INSERT INTO sessions (id, cli, project_path, started_at, last_msg_at, raw_path)
		VALUES (?, 'claude', '/tmp/proj', ?, ?, '/tmp/proj/sess.jsonl')`,
		sessID, now, now,
	)
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return db, sessID
}

// countCompactEvents returns the number of rows in compact_events for a session.
func countCompactEvents(t *testing.T, db *store.DB, sessionID string) int {
	t.Helper()
	var n int
	err := db.Read().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM compact_events WHERE session_id = ?`, sessionID).Scan(&n)
	if err != nil {
		t.Fatalf("count compact_events: %v", err)
	}
	return n
}

// TestInsertCompactEvent_InsertsRow verifies a fresh insert lands in the
// table with the supplied before/after token counts.
func TestInsertCompactEvent_InsertsRow(t *testing.T) {
	db, sessID := openCompactTestDB(t)
	ctx := context.Background()

	inserted, err := store.InsertCompactEvent(ctx, db, sessID, 1700, 95_000, 6_000)
	if err != nil {
		t.Fatalf("InsertCompactEvent: %v", err)
	}
	if !inserted {
		t.Error("expected inserted=true on first insert")
	}

	var before, after int64
	err = db.Read().QueryRowContext(ctx,
		`SELECT before_token_count, after_token_count
		 FROM compact_events WHERE session_id = ? AND ts = ?`,
		sessID, 1700).Scan(&before, &after)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if before != 95_000 || after != 6_000 {
		t.Errorf("got (before=%d, after=%d); want (95000, 6000)", before, after)
	}
}

// TestInsertCompactEvent_IdempotentOnReplay verifies that re-inserting the
// same (session_id, ts) is silently absorbed — re-replays of the source
// JSONL on daemon restart must not double-count compactions.
func TestInsertCompactEvent_IdempotentOnReplay(t *testing.T) {
	db, sessID := openCompactTestDB(t)
	ctx := context.Background()

	if _, err := store.InsertCompactEvent(ctx, db, sessID, 1700, 95_000, 6_000); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	inserted, err := store.InsertCompactEvent(ctx, db, sessID, 1700, 95_000, 6_000)
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if inserted {
		t.Error("expected inserted=false on duplicate (session_id, ts)")
	}
	if n := countCompactEvents(t, db, sessID); n != 1 {
		t.Errorf("got %d compact rows after duplicate insert; want 1", n)
	}
}

// TestInsertCompactEvent_DistinctTsInsertsAdditionalRow verifies that the
// same session compacted twice (different ts) gets two distinct rows —
// the dashboard counts each compaction.
func TestInsertCompactEvent_DistinctTsInsertsAdditionalRow(t *testing.T) {
	db, sessID := openCompactTestDB(t)
	ctx := context.Background()

	for _, ts := range []int64{1700, 2400} {
		if _, err := store.InsertCompactEvent(ctx, db, sessID, ts, 95_000, 6_000); err != nil {
			t.Fatalf("insert at ts=%d: %v", ts, err)
		}
	}
	if n := countCompactEvents(t, db, sessID); n != 2 {
		t.Errorf("got %d compact rows; want 2", n)
	}
}
