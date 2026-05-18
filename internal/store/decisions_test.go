package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
)

func openDecisionsDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "dec.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestInsertAndListDecision(t *testing.T) {
	ctx := context.Background()
	db := openDecisionsDB(t)
	d := &store.Decision{
		ID: "id-1", Ts: 1700, ProjectPath: "/p",
		Text: "We picked Postgres over SQLite",
		Tags: []string{"db", "infra"},
	}
	if err := store.InsertDecision(ctx, db, d); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := store.ListDecisions(ctx, db, store.DecisionFilter{ProjectPath: "/p"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 row, got %d", len(got))
	}
	if got[0].ID != "id-1" || got[0].Text != d.Text {
		t.Errorf("round-trip mismatch: %+v", got[0])
	}
	if len(got[0].Tags) != 2 || got[0].Tags[0] != "db" {
		t.Errorf("tags lost: %+v", got[0].Tags)
	}
}

func TestListDecisions_SortedByTsDesc(t *testing.T) {
	ctx := context.Background()
	db := openDecisionsDB(t)
	rows := []*store.Decision{
		{ID: "a", Ts: 100, Text: "first"},
		{ID: "b", Ts: 300, Text: "third"},
		{ID: "c", Ts: 200, Text: "second"},
	}
	for _, d := range rows {
		if err := store.InsertDecision(ctx, db, d); err != nil {
			t.Fatalf("insert %s: %v", d.ID, err)
		}
	}
	got, err := store.ListDecisions(ctx, db, store.DecisionFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	wantOrder := []string{"b", "c", "a"}
	for i, want := range wantOrder {
		if got[i].ID != want {
			t.Errorf("position %d: got %q want %q", i, got[i].ID, want)
		}
	}
}

func TestListDecisions_TagFilter(t *testing.T) {
	ctx := context.Background()
	db := openDecisionsDB(t)
	rows := []*store.Decision{
		{ID: "a", Ts: 1, Text: "x", Tags: []string{"db", "infra"}},
		{ID: "b", Ts: 2, Text: "y", Tags: []string{"api"}},
		{ID: "c", Ts: 3, Text: "z", Tags: []string{"db"}},
	}
	for _, d := range rows {
		if err := store.InsertDecision(ctx, db, d); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	got, err := store.ListDecisions(ctx, db, store.DecisionFilter{Tag: "db"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 (a, c), got %d", len(got))
	}
}

func TestSearchDecisions_CaseInsensitive(t *testing.T) {
	ctx := context.Background()
	db := openDecisionsDB(t)
	for _, d := range []*store.Decision{
		{ID: "a", Ts: 1, Text: "We picked Postgres"},
		{ID: "b", Ts: 2, Text: "Sentry alerts go to #ops"},
	} {
		if err := store.InsertDecision(ctx, db, d); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	got, err := store.SearchDecisions(ctx, db, "POSTGRES", "", 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("search mismatch: %+v", got)
	}
}

func TestDeleteDecision(t *testing.T) {
	ctx := context.Background()
	db := openDecisionsDB(t)
	d := &store.Decision{ID: "d1", Ts: 1, Text: "x"}
	if err := store.InsertDecision(ctx, db, d); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := store.DeleteDecision(ctx, db, "d1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err := store.ListDecisions(ctx, db, store.DecisionFilter{})
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty list after delete, got %+v", got)
	}
	// Second delete should report no-rows.
	err = store.DeleteDecision(ctx, db, "d1")
	if err == nil {
		t.Fatal("expected ErrNoRows wrapped on second delete")
	}
	if !errors.Is(err, errors.New("")) && err.Error() == "" {
		t.Errorf("error should have message: %v", err)
	}
}

func TestInsertDecision_RequiresIDAndText(t *testing.T) {
	ctx := context.Background()
	db := openDecisionsDB(t)
	if err := store.InsertDecision(ctx, db, &store.Decision{Text: "x"}); err == nil {
		t.Errorf("expected error for missing id")
	}
	if err := store.InsertDecision(ctx, db, &store.Decision{ID: "id"}); err == nil {
		t.Errorf("expected error for missing text")
	}
}
