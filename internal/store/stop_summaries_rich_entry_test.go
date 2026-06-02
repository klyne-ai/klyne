package store_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/store"
)

// TestListPendingWorklogEntries_FiltersAndOrders covers the worker's
// queue-drain contract: rows with verdict '' OR 'pending' AND
// attempts < maxAttempts surface in ts ASC order. Terminal verdicts
// (admitted-*, skipped-*, failed-permanent) are EXCLUDED.
func TestListPendingWorklogEntries_FiltersAndOrders(t *testing.T) {
	db := openStopSummariesDB(t)
	ctx := context.Background()
	// Seed mixed-state rows.
	rows := []struct {
		id       string
		ts       int64
		verdict  string
		attempts int
	}{
		{"a", 3000, "", 0},                  // pending (never processed)
		{"b", 1000, "pending", 1},           // explicit pending, eligible
		{"c", 5000, "pending", 3},           // attempts >= MAX, excluded
		{"d", 2000, "admitted-heuristic", 0}, // terminal, excluded
		{"e", 4000, "skipped-validator", 1}, // terminal, excluded
		{"f", 6000, "failed-permanent", 5},  // terminal, excluded
	}
	for _, r := range rows {
		if err := store.UpsertStopSummaryWithEntry(ctx, db,
			store.StopSummary{SessionID: r.id, Ts: r.ts, Summary: "x"},
			store.WorklogEntryJSON{SchemaVersion: 1}, r.verdict, r.attempts); err != nil {
			t.Fatal(err)
		}
	}
	// MAX_WORKLOG_ATTEMPTS = 3 → 'c' (attempts=3) excluded.
	got, err := store.ListPendingWorklogEntries(ctx, db, 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 pending rows; got %d: %+v", len(got), got)
	}
	// Order check: 'b' (ts=1000) before 'a' (ts=3000).
	if got[0].SessionID != "b" || got[1].SessionID != "a" {
		t.Errorf("order: got [%s, %s] want [b, a]", got[0].SessionID, got[1].SessionID)
	}
	// Attempts counter must propagate so the worker can compute the
	// next attempts value when persisting.
	if got[0].WorklogAttempts != 1 {
		t.Errorf("attempts for 'b': got %d want 1", got[0].WorklogAttempts)
	}
	if got[1].WorklogAttempts != 0 {
		t.Errorf("attempts for 'a': got %d want 0", got[1].WorklogAttempts)
	}
}

func TestListPendingWorklogEntries_RespectsLimit(t *testing.T) {
	db := openStopSummariesDB(t)
	ctx := context.Background()
	for i, id := range []string{"x", "y", "z"} {
		_ = store.UpsertStopSummaryWithEntry(ctx, db,
			store.StopSummary{SessionID: id, Ts: int64(100 * (i + 1)), Summary: "x"},
			store.WorklogEntryJSON{SchemaVersion: 1}, "", 0)
	}
	got, _ := store.ListPendingWorklogEntries(ctx, db, 2, 3)
	if len(got) != 2 {
		t.Errorf("limit ignored: got %d rows", len(got))
	}
}

// TestIncrementWorklogAttempts_UpdatesAttemptsAndVerdictOnly — for
// the worker's LLM-transient-error retry path: bump attempts + set
// verdict=pending, but leave the entry_json (and the base body)
// untouched so a partial earlier write or the deterministic body
// stays as-is.
func TestIncrementWorklogAttempts_UpdatesAttemptsAndVerdictOnly(t *testing.T) {
	db := openStopSummariesDB(t)
	ctx := context.Background()
	if err := store.InsertStopSummary(ctx, db, &store.StopSummary{
		SessionID: "incr-test", Ts: 1, ProjectPath: "/r", Summary: "DETERMINISTIC",
	}); err != nil {
		t.Fatal(err)
	}
	// Seed entry with attempts=2, verdict=admitted-llm.
	entry := store.WorklogEntryJSON{
		SchemaVersion: 1,
		Categories:    map[string][]store.WorklogItem{"shipped": {{Summary: "existing"}}},
	}
	if err := store.UpsertStopSummaryWithEntry(ctx, db,
		store.StopSummary{SessionID: "incr-test", Ts: 1, Summary: "ignored"},
		entry, "admitted-llm", 2); err != nil {
		t.Fatal(err)
	}
	// Now increment (LLM error scenario): attempts 2→3, verdict→pending,
	// but entry_json + base body stay.
	if err := store.IncrementWorklogAttempts(ctx, db, "incr-test", 1, "pending"); err != nil {
		t.Fatal(err)
	}
	var verdict, body, entryJSON string
	var attempts int
	if err := db.Read().QueryRowContext(ctx,
		`SELECT worklog_gate_verdict, worklog_attempts, summary, worklog_entry_json
		   FROM stop_summaries WHERE session_id=? AND ts=?`, "incr-test", int64(1),
	).Scan(&verdict, &attempts, &body, &entryJSON); err != nil {
		t.Fatal(err)
	}
	if verdict != "pending" {
		t.Errorf("verdict: got %q", verdict)
	}
	if attempts != 3 {
		t.Errorf("attempts: got %d want 3", attempts)
	}
	if body != "DETERMINISTIC" {
		t.Errorf("base body was clobbered: got %q", body)
	}
	if !strings.Contains(entryJSON, "existing") {
		t.Errorf("entry_json was clobbered: got %q", entryJSON)
	}
}

// TestReadWorklogEntry_RoundTrip writes a rich entry then reads it
// back through the typed helper. Asserts the helper returns the
// schema_version, the populated category, AND the gate verdict — all
// the fields downstream consumers (Phase 5 worker, Phase 6 reflection)
// will read.
func TestReadWorklogEntry_RoundTrip(t *testing.T) {
	db := openStopSummariesDB(t)
	ctx := context.Background()
	entry := store.WorklogEntryJSON{SchemaVersion: 1, Categories: map[string][]store.WorklogItem{
		"shipped": {{Summary: "merged PR #400", Refs: []string{"#400"}}},
	}}
	if err := store.UpsertStopSummaryWithEntry(ctx, db,
		store.StopSummary{SessionID: "s-read", Ts: 1, Summary: "body"},
		entry, "admitted-heuristic", 0); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, verdict, err := store.ReadWorklogEntry(ctx, db, "s-read", 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if verdict != "admitted-heuristic" {
		t.Errorf("verdict: got %q", verdict)
	}
	if got.SchemaVersion != 1 {
		t.Errorf("schema_version: got %d", got.SchemaVersion)
	}
	shipped := got.Categories["shipped"]
	if len(shipped) != 1 || shipped[0].Refs[0] != "#400" {
		t.Errorf("shipped lost: %+v", shipped)
	}
}

// TestReadWorklogEntry_MissingRow returns the zero entry + empty
// verdict + nil error — Phase 5's worker uses this to distinguish
// "queue is empty for this id" from a real DB error.
func TestReadWorklogEntry_MissingRow(t *testing.T) {
	db := openStopSummariesDB(t)
	got, verdict, err := store.ReadWorklogEntry(context.Background(), db, "nope", 42)
	if err != nil {
		t.Fatalf("err on missing row: %v", err)
	}
	if verdict != "" {
		t.Errorf("verdict on missing: %q", verdict)
	}
	if got.SchemaVersion != 0 || got.Categories != nil {
		t.Errorf("entry on missing: %+v", got)
	}
}

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
				Repo:    "ops-app",
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
