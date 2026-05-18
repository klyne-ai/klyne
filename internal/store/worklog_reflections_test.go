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

func TestListReflectionsForProject_MultiDayBatchOrderedByTitleSecondary(t *testing.T) {
	db := openReflectionsDB(t)
	ctx := context.Background()
	// Simulate three RecordReflection calls in a single /klyne:reflect run.
	// All three rows get the SAME ts (the bug repro: multi-day catch-up
	// where host roundtrip is faster than ms resolution OR host issues
	// MCP calls in date order other than oldest-first).
	const ts = int64(1_700_000_000_000)
	rows := []store.Reflection{
		{ID: "r-15", TS: ts, ProjectPath: "/p", Tier: 1, Title: "Daily reflection — 2026-05-15", BodyMD: "x", EvidenceEntryIDs: []string{"e15"}, SummarySource: "ai", State: "proposed", Importance: 7, StateChangedAt: ts},
		{ID: "r-17", TS: ts, ProjectPath: "/p", Tier: 1, Title: "Daily reflection — 2026-05-17", BodyMD: "x", EvidenceEntryIDs: []string{"e17"}, SummarySource: "ai", State: "proposed", Importance: 7, StateChangedAt: ts},
		{ID: "r-16", TS: ts, ProjectPath: "/p", Tier: 1, Title: "Daily reflection — 2026-05-16", BodyMD: "x", EvidenceEntryIDs: []string{"e16"}, SummarySource: "ai", State: "proposed", Importance: 7, StateChangedAt: ts},
	}
	for _, r := range rows {
		if err := store.InsertReflection(ctx, db, r); err != nil {
			t.Fatalf("insert %s: %v", r.ID, err)
		}
	}

	got, err := store.ListReflectionsForProject(ctx, db, "/p", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3", len(got))
	}
	// Newest day first. Daily titles end in YYYY-MM-DD so title DESC
	// breaks the ts tie correctly: 05-17, 05-16, 05-15.
	wantOrder := []string{"r-17", "r-16", "r-15"}
	for i, want := range wantOrder {
		if got[i].ID != want {
			t.Errorf("position %d: got %s, want %s (full order: %v)", i, got[i].ID, want, idsOf(got))
		}
	}
}

func idsOf(rs []store.Reflection) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}
