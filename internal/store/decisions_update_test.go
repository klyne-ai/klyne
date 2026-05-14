package store_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
)

func TestUpdateDecision_TextOnly(t *testing.T) {
	ctx := context.Background()
	db := openDecisionsDB(t)
	d := &store.Decision{
		ID: "u-1", Ts: 1, ProjectPath: "/p",
		Text: "original text",
		Tags: []string{"db", "infra"},
	}
	if err := store.InsertDecision(ctx, db, d); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := store.UpdateDecision(ctx, db, "u-1", "new text", nil, true, false); err != nil {
		t.Fatalf("update text: %v", err)
	}
	got, err := store.ListDecisions(ctx, db, store.DecisionFilter{ProjectPath: "/p"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 row, got %d", len(got))
	}
	if got[0].Text != "new text" {
		t.Errorf("text not updated: got %q", got[0].Text)
	}
	if len(got[0].Tags) != 2 || got[0].Tags[0] != "db" {
		t.Errorf("tags should be unchanged: %+v", got[0].Tags)
	}
}

func TestUpdateDecision_TagsOnly(t *testing.T) {
	ctx := context.Background()
	db := openDecisionsDB(t)
	d := &store.Decision{
		ID: "u-2", Ts: 1, ProjectPath: "/p",
		Text: "keeper",
		Tags: []string{"db"},
	}
	if err := store.InsertDecision(ctx, db, d); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := store.UpdateDecision(ctx, db, "u-2", "", []string{"api", "ops"}, false, true); err != nil {
		t.Fatalf("update tags: %v", err)
	}
	got, err := store.ListDecisions(ctx, db, store.DecisionFilter{ProjectPath: "/p"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got[0].Text != "keeper" {
		t.Errorf("text should be unchanged: %q", got[0].Text)
	}
	if len(got[0].Tags) != 2 || got[0].Tags[0] != "api" || got[0].Tags[1] != "ops" {
		t.Errorf("tags not updated: %+v", got[0].Tags)
	}
}

func TestUpdateDecision_TagsClearWithEmptySlice(t *testing.T) {
	ctx := context.Background()
	db := openDecisionsDB(t)
	d := &store.Decision{
		ID: "u-2b", Ts: 1, ProjectPath: "/p",
		Text: "keeper", Tags: []string{"x"},
	}
	if err := store.InsertDecision(ctx, db, d); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := store.UpdateDecision(ctx, db, "u-2b", "", []string{}, false, true); err != nil {
		t.Fatalf("clear tags: %v", err)
	}
	got, err := store.ListDecisions(ctx, db, store.DecisionFilter{ProjectPath: "/p"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got[0].Tags) != 0 {
		t.Errorf("tags should be cleared: %+v", got[0].Tags)
	}
}

func TestUpdateDecision_Both(t *testing.T) {
	ctx := context.Background()
	db := openDecisionsDB(t)
	d := &store.Decision{
		ID: "u-3", Ts: 1, ProjectPath: "/p",
		Text: "old", Tags: []string{"a"},
	}
	if err := store.InsertDecision(ctx, db, d); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := store.UpdateDecision(ctx, db, "u-3", "new", []string{"b", "c"}, true, true); err != nil {
		t.Fatalf("update both: %v", err)
	}
	got, err := store.ListDecisions(ctx, db, store.DecisionFilter{ProjectPath: "/p"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got[0].Text != "new" {
		t.Errorf("text not updated: %q", got[0].Text)
	}
	if len(got[0].Tags) != 2 || got[0].Tags[0] != "b" || got[0].Tags[1] != "c" {
		t.Errorf("tags not updated: %+v", got[0].Tags)
	}
}

func TestUpdateDecision_NoFlagsIsError(t *testing.T) {
	ctx := context.Background()
	db := openDecisionsDB(t)
	d := &store.Decision{ID: "u-4", Ts: 1, Text: "x"}
	if err := store.InsertDecision(ctx, db, d); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := store.UpdateDecision(ctx, db, "u-4", "y", []string{"z"}, false, false); err == nil {
		t.Fatal("expected error when neither flag is set")
	}
}

func TestUpdateDecision_EmptyTextIsError(t *testing.T) {
	ctx := context.Background()
	db := openDecisionsDB(t)
	d := &store.Decision{ID: "u-5", Ts: 1, Text: "x"}
	if err := store.InsertDecision(ctx, db, d); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := store.UpdateDecision(ctx, db, "u-5", "   ", nil, true, false); err == nil {
		t.Fatal("expected error when text is whitespace-only")
	}
}

func TestUpdateDecision_UnknownIDReturnsNoRows(t *testing.T) {
	ctx := context.Background()
	db := openDecisionsDB(t)
	err := store.UpdateDecision(ctx, db, "nope", "new", nil, true, false)
	if err == nil {
		t.Fatal("expected sql.ErrNoRows wrap for unknown id")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected errors.Is sql.ErrNoRows, got %v", err)
	}
}
