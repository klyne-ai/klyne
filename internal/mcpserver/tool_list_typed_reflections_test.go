package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/worklog"
)

// TestHandleListTypedReflections_ReturnsTypedRowsOnly seeds two rows
// — one typed, one legacy prose-only — and asserts the tool returns
// only the typed one with its body_json parsed.
func TestHandleListTypedReflections_ReturnsTypedRowsOnly(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	// Typed row via the worklog recorder so body_json + body_md both
	// land authentically.
	typedIn := RecordReflectionInput{
		ProjectPath: "/p",
		Day:         "2026-05-26",
		BodyJSON: &worklog.WWDPayload{
			Service: "klyne",
			Details: []worklog.WWDDetail{
				{Kind: worklog.DetailKindShipped, When: "16:49", Text: "Wrote hooks", Evidence: []string{"be8cc8c8"}, SessionID: "s1"},
			},
		},
	}
	if _, err := handleRecordReflection(context.Background(), db, typedIn); err != nil {
		t.Fatalf("seed typed: %v", err)
	}

	// Legacy prose row.
	proseIn := RecordReflectionInput{
		ProjectPath: "/p",
		Day:         "2026-05-26",
		Insights: []worklog.Insight{
			{Text: "did the auth thing", Evidence: []string{"s1"}},
		},
	}
	if _, err := handleRecordReflection(context.Background(), db, proseIn); err != nil {
		t.Fatalf("seed prose: %v", err)
	}

	out, err := handleListTypedReflections(context.Background(), db, ListTypedReflectionsInput{
		ProjectPath: "/p",
		Day:         "2026-05-26",
	})
	if err != nil {
		t.Fatalf("list typed: %v", err)
	}
	if len(out.Rows) != 1 {
		t.Fatalf("expected 1 typed row, got %d", len(out.Rows))
	}
	got := out.Rows[0]
	if got.BodyJSON.Service != "klyne" {
		t.Errorf("service = %q, want klyne", got.BodyJSON.Service)
	}
	if len(got.BodyJSON.Details) != 1 || got.BodyJSON.Details[0].Kind != "SHIPPED" {
		t.Errorf("details lost on round-trip: %+v", got.BodyJSON.Details)
	}
	if got.ReflectionID == "" {
		t.Errorf("expected reflection_id propagated")
	}
}

// TestHandleListTypedReflections_EmptyOnNoRows is the C1 no-op shape
// — empty Rows (non-nil), no error.
func TestHandleListTypedReflections_EmptyOnNoRows(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	out, err := handleListTypedReflections(context.Background(), db, ListTypedReflectionsInput{
		ProjectPath: "/p-empty",
		Day:         "2026-05-26",
	})
	if err != nil {
		t.Fatalf("list typed: %v", err)
	}
	if out.Rows == nil {
		t.Errorf("Rows must be non-nil even when empty")
	}
	if len(out.Rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(out.Rows))
	}
}

func TestHandleListTypedReflections_RejectsBadDay(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	_, err := handleListTypedReflections(context.Background(), db, ListTypedReflectionsInput{
		ProjectPath: "/p",
		Day:         "May 26",
	})
	if err == nil {
		t.Fatalf("expected error on malformed day")
	}
	if !strings.Contains(err.Error(), "bad day") {
		t.Errorf("expected error mentioning 'bad day', got %v", err)
	}
}

func TestHandleListTypedReflections_RequiresProjectPath(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	_, err := handleListTypedReflections(context.Background(), db, ListTypedReflectionsInput{
		Day: "2026-05-26",
	})
	if err == nil {
		t.Fatalf("expected error on missing project_path")
	}
}

// TestHandleListTypedReflections_FiltersBadJSON: a row with malformed
// body_json (defensive — the writer-side validator prevents this in
// practice) is silently skipped, not returned as an error.
func TestHandleListTypedReflections_FiltersBadJSON(t *testing.T) {
	withFakeHome(t)
	db := withBootstrapDB(t)

	// Insert directly with raw malformed body_json so we bypass the
	// recorder's validator.
	bad := store.Reflection{
		ID:               "bad-1",
		TS:               1_000,
		ProjectPath:      "/p",
		Day:              "2026-05-26",
		Title:            "Daily reflection — 2026-05-26",
		BodyMD:           "stub",
		Tier:             1,
		EvidenceEntryIDs: []string{"e1"},
		BodyJSON:         "{not json",
	}
	if err := store.InsertReflection(context.Background(), db, bad); err != nil {
		t.Fatalf("seed bad json: %v", err)
	}

	out, err := handleListTypedReflections(context.Background(), db, ListTypedReflectionsInput{
		ProjectPath: "/p",
		Day:         "2026-05-26",
	})
	if err != nil {
		t.Fatalf("list typed: %v", err)
	}
	if len(out.Rows) != 0 {
		t.Errorf("expected 0 rows (malformed filtered), got %d", len(out.Rows))
	}
}
