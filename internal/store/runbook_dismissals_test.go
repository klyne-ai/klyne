package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
)

func openDismissalsDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "dis.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestUpsertRunbookDismissal_Insert(t *testing.T) {
	ctx := context.Background()
	db := openDismissalsDB(t)
	d := store.RunbookDismissal{
		Signature:   "npm run build ;; npm run deploy",
		ProjectPath: "/proj/a",
		Ts:          12345,
		Reason:      "noisy",
	}
	if err := store.UpsertRunbookDismissal(ctx, db, d); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	out, err := store.ListRunbookDismissals(ctx, db, "/proj/a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 1 || out[0].Signature != d.Signature {
		t.Errorf("round-trip mismatch: %+v", out)
	}
	if out[0].Reason != "noisy" {
		t.Errorf("reason lost: %q", out[0].Reason)
	}
}

func TestUpsertRunbookDismissal_Idempotent(t *testing.T) {
	ctx := context.Background()
	db := openDismissalsDB(t)
	d := store.RunbookDismissal{Signature: "sig", ProjectPath: "/p", Ts: 1}
	if err := store.UpsertRunbookDismissal(ctx, db, d); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	d.Reason = "second take"
	if err := store.UpsertRunbookDismissal(ctx, db, d); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	out, err := store.ListRunbookDismissals(ctx, db, "/p")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 row after idempotent upsert, got %d", len(out))
	}
	if out[0].Reason != "second take" {
		t.Errorf("reason not updated: %q", out[0].Reason)
	}
}

func TestDismissedSignatureSet_IncludesGlobals(t *testing.T) {
	ctx := context.Background()
	db := openDismissalsDB(t)
	_ = store.UpsertRunbookDismissal(ctx, db, store.RunbookDismissal{
		Signature: "scoped", ProjectPath: "/p1", Ts: 1,
	})
	_ = store.UpsertRunbookDismissal(ctx, db, store.RunbookDismissal{
		Signature: "global", ProjectPath: "", Ts: 2,
	})
	set, err := store.DismissedSignatureSet(ctx, db, "/p1")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, ok := set["scoped"]; !ok {
		t.Errorf("expected scoped signature in set")
	}
	if _, ok := set["global"]; !ok {
		t.Errorf("expected global signature in set")
	}
}

func TestDeleteRunbookDismissal_NotFound(t *testing.T) {
	ctx := context.Background()
	db := openDismissalsDB(t)
	err := store.DeleteRunbookDismissal(ctx, db, "nope", "/p")
	if err == nil {
		t.Fatalf("expected not-found error")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected wrapped sql.ErrNoRows, got %v", err)
	}
}

func TestDeleteRunbookDismissal_Removes(t *testing.T) {
	ctx := context.Background()
	db := openDismissalsDB(t)
	d := store.RunbookDismissal{Signature: "x", ProjectPath: "/p", Ts: 1}
	if err := store.UpsertRunbookDismissal(ctx, db, d); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := store.DeleteRunbookDismissal(ctx, db, "x", "/p"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	rows, err := store.ListRunbookDismissals(ctx, db, "/p")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected empty list after delete, got %+v", rows)
	}
}
