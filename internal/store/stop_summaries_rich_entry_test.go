package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
)

// TestUpsertStopSummaryWithEntry_RoundTrip writes a row with a rich
// worklog entry + gate verdict and reads back every column we care
// about — entry JSON, verdict, attempts. Asserts the JSON round-trips
// through the column intact (no schema drift between the Go struct
// and the stored bytes).
func TestUpsertStopSummaryWithEntry_RoundTrip(t *testing.T) {
	db := openStopSummariesDB(t)
	ctx := context.Background()

	base := store.StopSummary{
		SessionID:   "test-sess",
		Ts:          1000,
		ProjectPath: "/repo",
		Summary:     "base body",
	}
	entry := store.WorklogEntryJSON{
		SchemaVersion: 1,
		Categories: map[string][]store.WorklogItem{
			"features_worked_on": {{
				Summary: "phone/lab props",
				Repo:    "operations-app",
				Refs:    []string{"c5c97a86"},
			}},
		},
	}
	if err := store.UpsertStopSummaryWithEntry(ctx, db, base, entry, "admitted-heuristic", 0); err != nil {
		t.Fatalf("UpsertStopSummaryWithEntry: %v", err)
	}

	var gotJSON, gotVerdict string
	var gotAttempts int
	if err := db.Read().QueryRowContext(ctx,
		`SELECT worklog_entry_json, worklog_gate_verdict, worklog_attempts
		   FROM stop_summaries WHERE session_id=? AND ts=?`,
		"test-sess", int64(1000)).Scan(&gotJSON, &gotVerdict, &gotAttempts); err != nil {
		t.Fatalf("select: %v", err)
	}
	if gotVerdict != "admitted-heuristic" {
		t.Errorf("gate verdict: got %q want %q", gotVerdict, "admitted-heuristic")
	}
	if gotAttempts != 0 {
		t.Errorf("attempts: got %d want 0", gotAttempts)
	}
	var roundTrip store.WorklogEntryJSON
	if err := json.Unmarshal([]byte(gotJSON), &roundTrip); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := roundTrip.Categories["features_worked_on"]
	if len(got) != 1 || got[0].Refs[0] != "c5c97a86" {
		t.Errorf("round-trip lost data: %+v", got)
	}
}

// TestUpsertStopSummaryWithEntry_DoesNotClobberBase enforces the 015
// precedent the plan calls out: a later AI-prose pass must NOT
// overwrite the deterministic body written by the Stop hook. The
// migration-019 ON CONFLICT clause must only update the three new
// columns; summary / last_user / last_bash / files_json stay intact.
func TestUpsertStopSummaryWithEntry_DoesNotClobberBase(t *testing.T) {
	db := openStopSummariesDB(t)
	ctx := context.Background()

	// First write the base row via the existing InsertStopSummary —
	// this is the deterministic Stop-hook surface.
	if err := store.InsertStopSummary(ctx, db, &store.StopSummary{
		SessionID:   "s2",
		Ts:          2000,
		ProjectPath: "/r",
		Summary:     "DETERMINISTIC BODY",
		LastUser:    "the original last_user",
	}); err != nil {
		t.Fatalf("insert base: %v", err)
	}

	// Now the rich-entry pass runs (later, async). Even though we pass
	// a different Summary/LastUser here, ON CONFLICT must only update
	// the migration-019 columns.
	if err := store.UpsertStopSummaryWithEntry(ctx, db,
		store.StopSummary{
			SessionID: "s2",
			Ts:        2000,
			Summary:   "WRONG IF WRITTEN",
			LastUser:  "WRONG IF WRITTEN",
		},
		store.WorklogEntryJSON{SchemaVersion: 1}, "admitted-llm", 1); err != nil {
		t.Fatalf("upsert entry: %v", err)
	}

	var gotSummary, gotLastUser string
	if err := db.Read().QueryRowContext(ctx,
		`SELECT summary, last_user FROM stop_summaries WHERE session_id=? AND ts=?`,
		"s2", int64(2000)).Scan(&gotSummary, &gotLastUser); err != nil {
		t.Fatalf("select base: %v", err)
	}
	if gotSummary != "DETERMINISTIC BODY" {
		t.Errorf("base summary was clobbered: got %q", gotSummary)
	}
	if gotLastUser != "the original last_user" {
		t.Errorf("base last_user was clobbered: got %q", gotLastUser)
	}
}
