package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
)

func openReflectionsDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "reflections.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestInsertReflection_RoundTrip(t *testing.T) {
	db := openReflectionsDB(t)
	r := store.Reflection{
		ID: "ref-1", TS: 1, ProjectPath: "/p", Tier: 2,
		Title: "Weekly", BodyMD: "- did stuff (evidence: e1)\n",
		EvidenceEntryIDs: []string{"e1"},
		Importance:       7, SummarySource: "ai", State: "proposed",
		StateChangedAt: 1,
	}
	if err := store.InsertReflection(context.Background(), db, r); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := store.ListReflectionsForProject(context.Background(), db, "/p", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "ref-1" {
		t.Fatalf("expected 1 row id=ref-1, got %v", rows)
	}
	if len(rows[0].EvidenceEntryIDs) != 1 || rows[0].EvidenceEntryIDs[0] != "e1" {
		t.Errorf("evidence round-trip failed: %v", rows[0].EvidenceEntryIDs)
	}
}

func TestInsertReflection_CitationInvariant(t *testing.T) {
	db := openReflectionsDB(t)
	r := store.Reflection{
		ID: "ref-empty", TS: 1, ProjectPath: "/p", Tier: 2,
		Title: "Empty", BodyMD: "",
		Importance: 5, SummarySource: "ai", State: "proposed",
		StateChangedAt: 1,
	}
	err := store.InsertReflection(context.Background(), db, r)
	if err == nil {
		t.Errorf("expected error for empty evidence, got nil")
	}
}
