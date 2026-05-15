package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
)

func openSafetySnapshotsDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "safety.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestInsertAndGetSafetySnapshot(t *testing.T) {
	ctx := context.Background()
	db := openSafetySnapshotsDB(t)

	s := &store.SafetySnapshot{
		SessionID: "sess-abc",
		CWD:       "/home/user/project",
		Command:   "git reset --hard HEAD~1",
		PatternID: "git-reset-hard",
		Severity:  "high",
		StashSHA:  "abc123",
		FileCount: 5,
	}
	if err := store.InsertSafetySnapshot(ctx, db, s); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if s.ID == 0 {
		t.Fatal("expected non-zero ID after insert")
	}

	got, err := store.GetSafetySnapshot(ctx, db, s.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Command != s.Command {
		t.Errorf("command mismatch: got %q, want %q", got.Command, s.Command)
	}
	if got.StashSHA != "abc123" {
		t.Errorf("stash_sha mismatch: got %q", got.StashSHA)
	}
	if got.FileCount != 5 {
		t.Errorf("file_count mismatch: got %d", got.FileCount)
	}
}

func TestGetSafetySnapshot_NotFound(t *testing.T) {
	ctx := context.Background()
	db := openSafetySnapshotsDB(t)

	_, err := store.GetSafetySnapshot(ctx, db, 9999)
	if err == nil {
		t.Fatal("expected error for missing row")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestListSafetySnapshots_OrderedByTsDesc(t *testing.T) {
	ctx := context.Background()
	db := openSafetySnapshotsDB(t)

	rows := []store.SafetySnapshot{
		{Command: "git reset --hard HEAD", PatternID: "git-reset-hard", Severity: "high", Ts: 1000},
		{Command: "git clean -fd .", PatternID: "git-clean-fd", Severity: "high", Ts: 3000},
		{Command: "DROP TABLE users", PatternID: "schema-drop", Severity: "high", Ts: 2000},
	}
	for i := range rows {
		if err := store.InsertSafetySnapshot(ctx, db, &rows[i]); err != nil {
			t.Fatalf("insert row %d: %v", i, err)
		}
	}

	got, err := store.ListSafetySnapshots(ctx, db, store.SafetySnapshotFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 rows, got %d", len(got))
	}
	if got[0].Ts != 3000 {
		t.Errorf("first row ts = %d, want 3000 (ts DESC)", got[0].Ts)
	}
	if got[2].Ts != 1000 {
		t.Errorf("last row ts = %d, want 1000", got[2].Ts)
	}
}

func TestListSafetySnapshots_FilterBySession(t *testing.T) {
	ctx := context.Background()
	db := openSafetySnapshotsDB(t)

	for _, s := range []store.SafetySnapshot{
		{SessionID: "sess-A", Command: "git reset --hard", PatternID: "git-reset-hard", Severity: "high"},
		{SessionID: "sess-B", Command: "git clean -fd", PatternID: "git-clean-fd", Severity: "high"},
		{SessionID: "sess-A", Command: "DROP TABLE x", PatternID: "schema-drop", Severity: "high"},
	} {
		cp := s
		if err := store.InsertSafetySnapshot(ctx, db, &cp); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.ListSafetySnapshots(ctx, db, store.SafetySnapshotFilter{SessionID: "sess-A"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows for sess-A, got %d", len(got))
	}
}

func TestInsertSafetySnapshot_RequiresCommand(t *testing.T) {
	ctx := context.Background()
	db := openSafetySnapshotsDB(t)

	err := store.InsertSafetySnapshot(ctx, db, &store.SafetySnapshot{PatternID: "x", Severity: "high"})
	if err == nil {
		t.Fatal("expected error for missing command")
	}
}

func TestInsertSafetySnapshot_RequiresPatternID(t *testing.T) {
	ctx := context.Background()
	db := openSafetySnapshotsDB(t)

	err := store.InsertSafetySnapshot(ctx, db, &store.SafetySnapshot{Command: "git reset --hard HEAD", Severity: "high"})
	if err == nil {
		t.Fatal("expected error for missing pattern_id")
	}
}

func TestListSafetySnapshots_EmptyIsGraceful(t *testing.T) {
	ctx := context.Background()
	db := openSafetySnapshotsDB(t)

	got, err := store.ListSafetySnapshots(ctx, db, store.SafetySnapshotFilter{})
	if err != nil {
		t.Fatalf("list on empty table: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 rows, got %d", len(got))
	}
}
