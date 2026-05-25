package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
)

func openSnapshotDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "snapshots.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestUpsertDailyProductivitySnapshot_RoundTrip(t *testing.T) {
	db := openSnapshotDB(t)
	ctx := context.Background()
	s := store.DailyProductivitySnapshot{
		ProjectPath: "/p", Day: "2026-05-24",
		PayloadJSON:        `{"day":"2026-05-24"}`,
		TotalActiveMinutes: 42, Source: "live",
		CreatedAt: 1000, UpdatedAt: 1000,
	}
	if err := store.UpsertDailyProductivitySnapshot(ctx, db, s); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, ok, err := store.GetDailyProductivitySnapshot(ctx, db, "/p", "2026-05-24")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.TotalActiveMinutes != 42 || got.Source != "live" || got.PayloadJSON != s.PayloadJSON {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

// TestUpsertDailyProductivitySnapshot_ReflectionWins guards the
// release-blocker the review caught: a "live" upsert MUST NOT overwrite
// an existing "reflection" row, otherwise a handler request landing
// after /klyne:reflect would silently replace the user's authoritative
// snapshot with the handler's recomputation (which drops the
// ReflectionMarkdown the recompute doesn't reproduce).
func TestUpsertDailyProductivitySnapshot_ReflectionWins(t *testing.T) {
	db := openSnapshotDB(t)
	ctx := context.Background()
	ref := store.DailyProductivitySnapshot{
		ProjectPath: "/p", Day: "2026-05-24",
		PayloadJSON: `{"src":"ref"}`, TotalActiveMinutes: 100,
		Source: "reflection", CreatedAt: 1, UpdatedAt: 1,
	}
	if err := store.UpsertDailyProductivitySnapshot(ctx, db, ref); err != nil {
		t.Fatalf("seed reflection: %v", err)
	}
	live := store.DailyProductivitySnapshot{
		ProjectPath: "/p", Day: "2026-05-24",
		PayloadJSON: `{"src":"live"}`, TotalActiveMinutes: 1,
		Source: "live", CreatedAt: 2, UpdatedAt: 2,
	}
	if err := store.UpsertDailyProductivitySnapshot(ctx, db, live); err != nil {
		t.Fatalf("live upsert: %v", err)
	}
	got, _, _ := store.GetDailyProductivitySnapshot(ctx, db, "/p", "2026-05-24")
	if got.Source != "reflection" || got.PayloadJSON != `{"src":"ref"}` || got.TotalActiveMinutes != 100 {
		t.Fatalf("reflection row was clobbered by live upsert: %+v", got)
	}
}

// A fresh "reflection" upsert MUST overwrite an existing row (re-running
// /klyne:reflect must produce a fresh authoritative snapshot).
func TestUpsertDailyProductivitySnapshot_ReflectionOverwrites(t *testing.T) {
	db := openSnapshotDB(t)
	ctx := context.Background()
	for _, s := range []store.DailyProductivitySnapshot{
		{ProjectPath: "/p", Day: "2026-05-24", PayloadJSON: `{"v":1}`, Source: "live", UpdatedAt: 1},
		{ProjectPath: "/p", Day: "2026-05-24", PayloadJSON: `{"v":2}`, Source: "reflection", UpdatedAt: 2},
		{ProjectPath: "/p", Day: "2026-05-24", PayloadJSON: `{"v":3}`, Source: "reflection", UpdatedAt: 3},
	} {
		if err := store.UpsertDailyProductivitySnapshot(ctx, db, s); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	got, _, _ := store.GetDailyProductivitySnapshot(ctx, db, "/p", "2026-05-24")
	if got.PayloadJSON != `{"v":3}` || got.Source != "reflection" {
		t.Fatalf("latest reflection should win: %+v", got)
	}
}

func TestListDailyProductivitySnapshotsForDay(t *testing.T) {
	db := openSnapshotDB(t)
	ctx := context.Background()
	for _, s := range []store.DailyProductivitySnapshot{
		{ProjectPath: "/a", Day: "2026-05-24", PayloadJSON: `{}`, Source: "live", UpdatedAt: 1},
		{ProjectPath: "/b", Day: "2026-05-24", PayloadJSON: `{}`, Source: "live", UpdatedAt: 1},
		{ProjectPath: "/a", Day: "2026-05-25", PayloadJSON: `{}`, Source: "live", UpdatedAt: 1},
	} {
		_ = store.UpsertDailyProductivitySnapshot(ctx, db, s)
	}
	rows, err := store.ListDailyProductivitySnapshotsForDay(ctx, db, "2026-05-24")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows for day, got %d", len(rows))
	}
	if rows[0].ProjectPath != "/a" || rows[1].ProjectPath != "/b" {
		t.Fatalf("expected project_path ASC ordering, got %v / %v", rows[0].ProjectPath, rows[1].ProjectPath)
	}
}

func TestGetDailyProductivitySnapshot_Missing(t *testing.T) {
	db := openSnapshotDB(t)
	_, ok, err := store.GetDailyProductivitySnapshot(context.Background(), db, "/nope", "1970-01-01")
	if err != nil || ok {
		t.Fatalf("want ok=false err=nil, got ok=%v err=%v", ok, err)
	}
}
