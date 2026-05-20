package richentry_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/worklog/richentry"
)

// --- helpers --------------------------------------------------------------

func seedAdmittedEntry(t *testing.T, db *store.DB, projectPath, sessionID string, ts int64, entry store.WorklogEntryJSON) {
	t.Helper()
	ctx := context.Background()
	if err := store.UpsertStopSummaryWithEntry(ctx, db,
		store.StopSummary{
			SessionID: sessionID, Ts: ts, ProjectPath: projectPath, Summary: "x",
		}, entry, "admitted-heuristic", 1); err != nil {
		t.Fatal(err)
	}
}

func entryWithCategory(category string, items ...store.WorklogItem) store.WorklogEntryJSON {
	return store.WorklogEntryJSON{
		SchemaVersion: 1,
		Categories:    map[string][]store.WorklogItem{category: items},
	}
}

// --- DB-side: LoadDayEntries --------------------------------------------

func TestListWorklogEntriesForDay_FiltersByProject(t *testing.T) {
	db := openDB(t)
	day := time.Date(2026, 5, 19, 12, 0, 0, 0, time.Local)
	ts := day.UnixMilli()
	seedAdmittedEntry(t, db, "/repoA", "s1", ts, entryWithCategory(
		"shipped", store.WorklogItem{Summary: "A's PR"},
	))
	seedAdmittedEntry(t, db, "/repoB", "s2", ts+10, entryWithCategory(
		"shipped", store.WorklogItem{Summary: "B's PR"},
	))
	got, err := store.ListWorklogEntriesForDay(context.Background(), db, "/repoA", day)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SessionID != "s1" {
		t.Errorf("filter: got %+v", got)
	}
}

func TestListWorklogEntriesForDay_ExcludesNonAdmitted(t *testing.T) {
	db := openDB(t)
	day := time.Date(2026, 5, 19, 12, 0, 0, 0, time.Local)
	ts := day.UnixMilli()
	ctx := context.Background()
	// admitted-heuristic — keep
	seedAdmittedEntry(t, db, "/r", "keep", ts, entryWithCategory(
		"shipped", store.WorklogItem{Summary: "yes"},
	))
	// skipped-heuristic — drop
	_ = store.UpsertStopSummaryWithEntry(ctx, db,
		store.StopSummary{SessionID: "drop1", Ts: ts + 1, ProjectPath: "/r", Summary: "x"},
		entryWithCategory("shipped", store.WorklogItem{Summary: "no"}),
		"skipped-heuristic", 1)
	// pending — drop
	_ = store.UpsertStopSummaryWithEntry(ctx, db,
		store.StopSummary{SessionID: "drop2", Ts: ts + 2, ProjectPath: "/r", Summary: "x"},
		store.WorklogEntryJSON{SchemaVersion: 1}, "pending", 1)

	got, _ := store.ListWorklogEntriesForDay(ctx, db, "/r", day)
	if len(got) != 1 || got[0].SessionID != "keep" {
		t.Errorf("got %+v", got)
	}
}

func TestListWorklogEntriesForDay_OrdersChronologically(t *testing.T) {
	db := openDB(t)
	day := time.Date(2026, 5, 19, 12, 0, 0, 0, time.Local)
	seedAdmittedEntry(t, db, "/r", "later", day.Add(2*time.Hour).UnixMilli(), entryWithCategory("shipped", store.WorklogItem{Summary: "b"}))
	seedAdmittedEntry(t, db, "/r", "earlier", day.UnixMilli(), entryWithCategory("shipped", store.WorklogItem{Summary: "a"}))
	got, _ := store.ListWorklogEntriesForDay(context.Background(), db, "/r", day)
	if len(got) != 2 || got[0].SessionID != "earlier" || got[1].SessionID != "later" {
		t.Errorf("order: %+v", got)
	}
}

// --- Renderer: MergeMarkdown --------------------------------------------

func TestMergeMarkdown_Empty_ReturnsEmpty(t *testing.T) {
	if got := richentry.MergeMarkdown(nil); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestMergeMarkdown_SingleEntry_RendersSection(t *testing.T) {
	rows := []store.DayEntry{
		{SessionID: "s", Ts: 1, Entry: entryWithCategory("shipped",
			store.WorklogItem{Summary: "PR #400 merged", Ticket: "CLI-1325", Refs: []string{"#400", "e9a1b2a"}},
		)},
	}
	got := richentry.MergeMarkdown(rows)
	for _, want := range []string{
		"## Shipped",
		"- PR #400 merged [CLI-1325]",
		"(`#400, e9a1b2a`)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// Regression — bullets must surface the source repo so the reader can
// tell which codebase each line came from. A pre-fix renderItem
// dropped it.Repo, making cross-service days look like one
// undifferentiated stream of work.
func TestMergeMarkdown_BulletIncludesRepoPrefix(t *testing.T) {
	rows := []store.DayEntry{
		{SessionID: "s1", Entry: entryWithCategory("shipped", store.WorklogItem{
			Summary: "merged labstack flow",
			Repo:    "consultation-service",
			Refs:    []string{"#146"},
		})},
		{SessionID: "s2", Entry: entryWithCategory("shipped", store.WorklogItem{
			Summary: "ran cli-1412 discount-engine flag",
			Repo:    "oms-service",
			Refs:    []string{"4848e18a"},
		})},
		{SessionID: "s3", Entry: entryWithCategory("features_worked_on", store.WorklogItem{
			Summary: "rich-entry pipeline + CLI subprocess providers",
			Repo:    "klyne",
		})},
	}
	got := richentry.MergeMarkdown(rows)
	for _, want := range []string{
		"- **consultation-service** — merged labstack flow",
		"- **oms-service** — ran cli-1412 discount-engine flag",
		"- **klyne** — rich-entry pipeline + CLI subprocess providers",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing bullet %q in:\n%s", want, got)
		}
	}
}

func TestMergeMarkdown_AggregatesAcrossRows(t *testing.T) {
	rows := []store.DayEntry{
		{SessionID: "s1", Entry: entryWithCategory("shipped", store.WorklogItem{Summary: "a"})},
		{SessionID: "s2", Entry: entryWithCategory("shipped", store.WorklogItem{Summary: "b"})},
		{SessionID: "s3", Entry: entryWithCategory("bugs_fixed", store.WorklogItem{Summary: "c"})},
	}
	got := richentry.MergeMarkdown(rows)
	if !strings.Contains(got, "- a\n- b\n") {
		t.Errorf("multi-row shipped not merged in order:\n%s", got)
	}
	// Final section won't have a trailing newline (TrimRight strips it).
	if !strings.Contains(got, "## Bugs fixed\n\n- c") {
		t.Errorf("missing bugs_fixed section:\n%s", got)
	}
}

func TestMergeMarkdown_SkipsEmptyCategories(t *testing.T) {
	rows := []store.DayEntry{
		{Entry: store.WorklogEntryJSON{
			SchemaVersion: 1,
			Categories: map[string][]store.WorklogItem{
				"shipped":            {{Summary: "yes"}},
				"features_worked_on": {}, // empty — must not produce a section
				"bugs_found":         nil,
			},
		}},
	}
	got := richentry.MergeMarkdown(rows)
	if strings.Contains(got, "## Features worked on") {
		t.Errorf("empty category leaked a header:\n%s", got)
	}
	if !strings.Contains(got, "## Shipped") {
		t.Errorf("non-empty category dropped:\n%s", got)
	}
}

func TestMergeMarkdown_DisplayOrderRespected(t *testing.T) {
	// Even when input has decisions first, output must lead with Shipped.
	rows := []store.DayEntry{
		{Entry: store.WorklogEntryJSON{
			SchemaVersion: 1,
			Categories: map[string][]store.WorklogItem{
				"decisions": {{Summary: "decided"}},
				"shipped":   {{Summary: "merged"}},
			},
		}},
	}
	got := richentry.MergeMarkdown(rows)
	shipIdx := strings.Index(got, "## Shipped")
	decIdx := strings.Index(got, "## Decisions")
	if shipIdx == -1 || decIdx == -1 || shipIdx > decIdx {
		t.Errorf("display order wrong; Shipped@%d Decisions@%d in:\n%s", shipIdx, decIdx, got)
	}
}

func TestMergeMarkdown_UnknownCategory_RenderedLast(t *testing.T) {
	// A future writer might emit a non-canonical category. Don't drop
	// the content — render it under its own section, after the
	// canonical ones.
	rows := []store.DayEntry{
		{Entry: store.WorklogEntryJSON{
			SchemaVersion: 1,
			Categories: map[string][]store.WorklogItem{
				"shipped":     {{Summary: "a"}},
				"surprise":    {{Summary: "x"}},
				"another_new": {{Summary: "y"}},
			},
		}},
	}
	got := richentry.MergeMarkdown(rows)
	if !strings.Contains(got, "## another_new") || !strings.Contains(got, "## surprise") {
		t.Errorf("unknown categories not surfaced:\n%s", got)
	}
	// Both must come after Shipped.
	shipIdx := strings.Index(got, "## Shipped")
	surpriseIdx := strings.Index(got, "## surprise")
	if shipIdx > surpriseIdx {
		t.Errorf("unknown category came before canonical Shipped")
	}
}

// --- MergeDay (DB + render) end-to-end -----------------------------------

func TestMergeDay_EmptyDay_ReturnsEmpty(t *testing.T) {
	db := openDB(t)
	got, err := richentry.MergeDay(context.Background(), db, "/r", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("expected empty signal; got %q", got)
	}
}

func TestMergeDay_AdmittedEntries_RenderToMarkdown(t *testing.T) {
	db := openDB(t)
	day := time.Date(2026, 5, 19, 12, 0, 0, 0, time.Local)
	seedAdmittedEntry(t, db, "/r", "s1", day.UnixMilli(), entryWithCategory(
		"shipped", store.WorklogItem{Summary: "PR #1", Refs: []string{"#1"}},
	))
	got, err := richentry.MergeDay(context.Background(), db, "/r", day)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "## Shipped") || !strings.Contains(got, "PR #1") {
		t.Errorf("end-to-end render missing content:\n%s", got)
	}
}
