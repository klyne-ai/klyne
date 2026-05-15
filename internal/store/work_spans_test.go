package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
)

func openWorkSpansDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "work_spans.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestInsertAndGetWorkSpan(t *testing.T) {
	ctx := context.Background()
	db := openWorkSpansDB(t)

	s := &store.WorkSpan{
		CommitSHA:       "abc1234def567890",
		Bucket:          "commit",
		ProjectPath:     "/home/user/myproject",
		GitBranch:       "feat/auth",
		SessionIDs:      []string{"sess-1", "sess-2"},
		DecisionIDs:     []string{"dec-a"},
		WasteClasses:    []string{"WASTE_LOOP"},
		WasteMeta:       map[string]any{"hash": "deadbeef", "count": 8},
		TokensFresh:     120000,
		TokensCacheRead: 50000,
		TokensOut:       8000,
		MsgCount:        42,
		OpenedAt:        1000000,
		ClosedAt:        2000000,
	}
	if err := store.InsertWorkSpan(ctx, db, s); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if s.ID == 0 {
		t.Fatal("expected non-zero ID after insert")
	}

	got, err := store.GetWorkSpan(ctx, db, s.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.CommitSHA != s.CommitSHA {
		t.Errorf("commit_sha mismatch: got %q, want %q", got.CommitSHA, s.CommitSHA)
	}
	if got.Bucket != "commit" {
		t.Errorf("bucket mismatch: got %q", got.Bucket)
	}
	if len(got.SessionIDs) != 2 {
		t.Errorf("session_ids len mismatch: got %d", len(got.SessionIDs))
	}
	if len(got.WasteClasses) != 1 || got.WasteClasses[0] != "WASTE_LOOP" {
		t.Errorf("waste_classes mismatch: got %v", got.WasteClasses)
	}
	if got.TokensFresh != 120000 {
		t.Errorf("tokens_fresh mismatch: got %d", got.TokensFresh)
	}
	if got.MsgCount != 42 {
		t.Errorf("msg_count mismatch: got %d", got.MsgCount)
	}
}

func TestGetWorkSpan_NotFound(t *testing.T) {
	ctx := context.Background()
	db := openWorkSpansDB(t)

	_, err := store.GetWorkSpan(ctx, db, 9999)
	if err == nil {
		t.Fatal("expected error for missing row")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestListWorkSpans_OrderedByOpenedAtDesc(t *testing.T) {
	ctx := context.Background()
	db := openWorkSpansDB(t)

	for _, s := range []store.WorkSpan{
		{Bucket: "commit", CommitSHA: "aaa", OpenedAt: 1000, ClosedAt: 1500},
		{Bucket: "commit", CommitSHA: "bbb", OpenedAt: 3000, ClosedAt: 3500},
		{Bucket: "exploration", ExplorationID: "exp-1", OpenedAt: 2000, ClosedAt: 2500},
	} {
		cp := s
		if err := store.InsertWorkSpan(ctx, db, &cp); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	got, err := store.ListWorkSpans(ctx, db, store.WorkSpanFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 rows, got %d", len(got))
	}
	// Should be ordered by opened_at DESC.
	if got[0].OpenedAt != 3000 {
		t.Errorf("first row opened_at = %d, want 3000", got[0].OpenedAt)
	}
	if got[2].OpenedAt != 1000 {
		t.Errorf("last row opened_at = %d, want 1000", got[2].OpenedAt)
	}
}

func TestListWorkSpans_FilterByProject(t *testing.T) {
	ctx := context.Background()
	db := openWorkSpansDB(t)

	for _, s := range []store.WorkSpan{
		{Bucket: "commit", CommitSHA: "aaa", ProjectPath: "/proj/A", OpenedAt: 1000, ClosedAt: 1100},
		{Bucket: "commit", CommitSHA: "bbb", ProjectPath: "/proj/B", OpenedAt: 2000, ClosedAt: 2100},
		{Bucket: "commit", CommitSHA: "ccc", ProjectPath: "/proj/A", OpenedAt: 3000, ClosedAt: 3100},
	} {
		cp := s
		if err := store.InsertWorkSpan(ctx, db, &cp); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.ListWorkSpans(ctx, db, store.WorkSpanFilter{ProjectPath: "/proj/A"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows for /proj/A, got %d", len(got))
	}
}

func TestListWorkSpans_FilterBySince(t *testing.T) {
	ctx := context.Background()
	db := openWorkSpansDB(t)

	for _, s := range []store.WorkSpan{
		{Bucket: "commit", CommitSHA: "old1", OpenedAt: 100, ClosedAt: 200},
		{Bucket: "commit", CommitSHA: "new1", OpenedAt: 5000, ClosedAt: 5100},
		{Bucket: "commit", CommitSHA: "new2", OpenedAt: 6000, ClosedAt: 6100},
	} {
		cp := s
		if err := store.InsertWorkSpan(ctx, db, &cp); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.ListWorkSpans(ctx, db, store.WorkSpanFilter{Since: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows since=1000, got %d", len(got))
	}
}

func TestInsertWorkSpan_RequiresBucket(t *testing.T) {
	ctx := context.Background()
	db := openWorkSpansDB(t)

	err := store.InsertWorkSpan(ctx, db, &store.WorkSpan{CommitSHA: "abc", OpenedAt: 1000, ClosedAt: 1100})
	if err == nil {
		t.Fatal("expected error for missing bucket")
	}
}

func TestListWorkSpans_EmptyIsGraceful(t *testing.T) {
	ctx := context.Background()
	db := openWorkSpansDB(t)

	got, err := store.ListWorkSpans(ctx, db, store.WorkSpanFilter{})
	if err != nil {
		t.Fatalf("list on empty table: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 rows, got %d", len(got))
	}
}

func TestInsertWorkSpan_ExplorationBucket(t *testing.T) {
	ctx := context.Background()
	db := openWorkSpansDB(t)

	s := &store.WorkSpan{
		Bucket:        "exploration",
		ExplorationID: "exp-abc-001",
		ProjectPath:   "/proj/test",
		TokensFresh:   50000,
		MsgCount:      10,
		OpenedAt:      1000,
		ClosedAt:      2000,
	}
	if err := store.InsertWorkSpan(ctx, db, s); err != nil {
		t.Fatalf("insert exploration span: %v", err)
	}
	got, err := store.GetWorkSpan(ctx, db, s.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ExplorationID != "exp-abc-001" {
		t.Errorf("exploration_id mismatch: got %q", got.ExplorationID)
	}
	if got.CommitSHA != "" {
		t.Errorf("expected empty commit_sha for exploration bucket, got %q", got.CommitSHA)
	}
}
