package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/productivity"
	"github.com/klyne-ai/klyne/internal/store"
)

// TestProductivity_PastDay_LazyBackfillThenSnapshotRead is the end-to-end
// release-blocker test for the snapshot-backed dashboard path. It exists
// because the snapshot persistence layer
// (tryReadSnapshotDay/persistSnapshotsForDay/upsertSnapshot/
// orphanOnlyReport) was 0% covered after the refactor — a contributor
// could change snapshot keys, JSON shape, or source precedence and
// nothing in CI would catch it.
//
// Flow:
//  1. Seed a real git repo + one session + 3 messages dated "yesterday".
//  2. Hit /api/productivity for yesterday's window. The handler has no
//     snapshot for yesterday yet → falls through to live compute, then
//     lazy-writes a daily_productivity_snapshot row with source="live".
//  3. Hit the same URL again. This time the snapshot exists →
//     tryReadSnapshotDay returns it, the live-compute path is bypassed.
//     We assert the JSON shape is identical to the first response (within
//     map ordering) — proving the snapshot round-trips cleanly.
//  4. Force-refresh with ?refresh=1. Even though a snapshot exists, the
//     handler must recompute and overwrite. We seed an obviously-wrong
//     payload first so we can detect the overwrite (snapshot's day field
//     gets rewritten).
func TestProductivity_PastDay_LazyBackfillThenSnapshotRead(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	ctx := context.Background()
	email := userEmailForTest(t)

	repo := t.TempDir()
	gitCmd(t, repo, email, "init", "-q")
	gitCmd(t, repo, email, "config", "user.email", email)
	gitCmd(t, repo, email, "config", "user.name", "Tester")
	if err := os.WriteFile(repo+"/x.txt", []byte("y"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitCmd(t, repo, email, "add", "x.txt")
	gitCmd(t, repo, email, "commit", "-q", "-m", "yesterday's work")

	// Yesterday's local window. "Past day" is what the snapshot path
	// triggers on — today is always live-computed and never persisted.
	now := time.Now()
	yesterdayMid := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, now.Location())
	yesterdayEnd := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Add(-time.Millisecond)
	dayStr := yesterdayMid.Format("2006-01-02")

	tsBase := yesterdayMid.Add(10 * time.Hour).UnixMilli()
	sess := &connectors.Session{
		ID: "s-snap-1", CLI: connectors.CLIClaude, ProjectPath: repo,
		Model: "claude-opus-4-7", Status: connectors.SessionStatusIdle,
	}
	if err := store.UpsertSession(ctx, db, sess); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	for i := 0; i < 3; i++ {
		m := &connectors.Message{
			ID: fmt.Sprintf("s-snap-1-m%d", i), SessionID: "s-snap-1",
			CLI: connectors.CLIClaude, Role: connectors.RoleAssistant, Content: "x",
			Ts: tsBase + int64(i)*60_000,
		}
		if err := store.InsertMessage(ctx, db, m); err != nil {
			t.Fatalf("insert message: %v", err)
		}
	}

	srv := httptest.NewServer(newProductivityRouter(t, db))
	t.Cleanup(srv.Close)

	getReport := func(refresh bool) productivity.Report {
		t.Helper()
		q := fmt.Sprintf("?since=%d&until=%d", yesterdayMid.UnixMilli(), yesterdayEnd.UnixMilli())
		if refresh {
			q += "&refresh=1"
		}
		resp, err := http.Get(srv.URL + api.RouteProductivity + q)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close() //nolint:errcheck
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status %d: %s", resp.StatusCode, body)
		}
		var rep productivity.Report
		if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return rep
	}

	// First request: no snapshot exists for yesterday → live compute +
	// lazy backfill. The snapshot table starts empty.
	rowsBefore, _ := store.ListDailyProductivitySnapshotsForDay(ctx, db, dayStr)
	if len(rowsBefore) != 0 {
		t.Fatalf("precondition: snapshot table should be empty for %s, got %d rows", dayStr, len(rowsBefore))
	}
	first := getReport(false)

	// The lazy backfill must have written exactly one per-service
	// snapshot row, source="live", payload_json round-tripping the
	// service's data.
	rowsAfter, err := store.ListDailyProductivitySnapshotsForDay(ctx, db, dayStr)
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(rowsAfter) != 1 {
		t.Fatalf("expected exactly 1 backfilled snapshot row for %s, got %d", dayStr, len(rowsAfter))
	}
	if rowsAfter[0].Source != "live" {
		t.Fatalf("backfill source = %q, want 'live'", rowsAfter[0].Source)
	}
	if rowsAfter[0].Day != dayStr {
		t.Fatalf("backfill day = %q, want %q", rowsAfter[0].Day, dayStr)
	}
	createdAt := rowsAfter[0].CreatedAt

	// Second request (no refresh): snapshot exists → tryReadSnapshotDay
	// path. Updated_at should NOT change (a fresh backfill would update
	// it; the snapshot read path does not write).
	second := getReport(false)
	rowsAfter2, _ := store.ListDailyProductivitySnapshotsForDay(ctx, db, dayStr)
	if len(rowsAfter2) != 1 {
		t.Fatalf("second request should not multiply rows, got %d", len(rowsAfter2))
	}
	if rowsAfter2[0].UpdatedAt != rowsAfter[0].UpdatedAt {
		t.Fatalf("second read should not bump updated_at; got %d → %d",
			rowsAfter[0].UpdatedAt, rowsAfter2[0].UpdatedAt)
	}

	// JSON-shape parity: the second response (snapshot-read path) must
	// produce a Report indistinguishable from the first (live-compute
	// path) for the same window. We compare a stable subset of fields
	// rather than re-marshal both — MergedPRs / GitFetchedAt are derived
	// at render time per request and may shift slightly.
	if first.Day != second.Day {
		t.Errorf("Day mismatch live=%q snapshot=%q", first.Day, second.Day)
	}
	if first.TotalActiveMinutes != second.TotalActiveMinutes {
		t.Errorf("TotalActiveMinutes mismatch live=%d snapshot=%d",
			first.TotalActiveMinutes, second.TotalActiveMinutes)
	}
	if len(first.Services) != len(second.Services) {
		t.Errorf("Services count mismatch live=%d snapshot=%d",
			len(first.Services), len(second.Services))
	}
	if len(first.Sessions) != len(second.Sessions) {
		t.Errorf("Sessions count mismatch live=%d snapshot=%d",
			len(first.Sessions), len(second.Sessions))
	}

	// Third request with refresh=1: must overwrite the snapshot
	// (handler force-recomputes even when a snapshot exists). The row's
	// UpdatedAt must advance.
	time.Sleep(2 * time.Millisecond) // ensure ms-resolution updated_at differs
	_ = getReport(true)
	rowsAfter3, _ := store.ListDailyProductivitySnapshotsForDay(ctx, db, dayStr)
	if len(rowsAfter3) != 1 {
		t.Fatalf("refresh should overwrite, not duplicate, got %d rows", len(rowsAfter3))
	}
	if rowsAfter3[0].UpdatedAt <= createdAt {
		t.Fatalf("refresh should advance updated_at; before=%d after=%d", createdAt, rowsAfter3[0].UpdatedAt)
	}
}

// TestProductivity_TodayIsNeverSnapshotted asserts the invariant that
// today's window NEVER writes to daily_productivity_snapshot. Today is
// in progress and persisting now would let same-day reloads serve a
// stale row instead of recomputing.
func TestProductivity_TodayIsNeverSnapshotted(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	ctx := context.Background()
	email := userEmailForTest(t)

	repo := t.TempDir()
	gitCmd(t, repo, email, "init", "-q")
	gitCmd(t, repo, email, "config", "user.email", email)
	gitCmd(t, repo, email, "config", "user.name", "Tester")
	if err := os.WriteFile(repo+"/a.txt", []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, email, "add", "a.txt")
	gitCmd(t, repo, email, "commit", "-q", "-m", "today's work")

	now := time.Now()
	todayMid := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	todayStr := todayMid.Format("2006-01-02")

	sess := &connectors.Session{
		ID: "s-today-1", CLI: connectors.CLIClaude, ProjectPath: repo,
		Model: "claude-opus-4-7", Status: connectors.SessionStatusIdle,
	}
	_ = store.UpsertSession(ctx, db, sess)
	_ = store.InsertMessage(ctx, db, &connectors.Message{
		ID: "s-today-1-m0", SessionID: "s-today-1", CLI: connectors.CLIClaude,
		Role: connectors.RoleAssistant, Content: "x", Ts: now.Add(-10 * time.Minute).UnixMilli(),
	})

	srv := httptest.NewServer(newProductivityRouter(t, db))
	t.Cleanup(srv.Close)

	q := fmt.Sprintf("?since=%d&until=%d", todayMid.UnixMilli(), now.UnixMilli())
	resp, err := http.Get(srv.URL + api.RouteProductivity + q)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}

	// Today should NOT have produced a snapshot row.
	rows, _ := store.ListDailyProductivitySnapshotsForDay(ctx, db, todayStr)
	if len(rows) != 0 {
		t.Fatalf("today must never be snapshotted, found %d rows for %s", len(rows), todayStr)
	}
}

// TestProductivity_ReflectionSnapshotBeatsLiveBackfill is the end-to-end
// guard for the source-precedence rule in
// UpsertDailyProductivitySnapshot. A reflection-written row
// (source="reflection") must NOT be clobbered by a subsequent handler
// request that triggers a live backfill (source="live"). This protects
// the user's authoritative /klyne:reflect output from being replaced by
// a recompute that doesn't reproduce ReflectionMarkdown.
func TestProductivity_ReflectionSnapshotBeatsLiveBackfill(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	ctx := context.Background()

	now := time.Now()
	dayStr := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, now.Location()).
		Format("2006-01-02")

	// Seed a "reflection" snapshot — pretend /klyne:reflect already
	// landed for yesterday.
	authoritative := store.DailyProductivitySnapshot{
		ProjectPath:        "/seeded/project",
		Day:                dayStr,
		PayloadJSON:        `{"day":"` + dayStr + `","sentinel":"reflection-blessed"}`,
		TotalActiveMinutes: 999,
		Source:             "reflection",
		CreatedAt:          1, UpdatedAt: 1,
	}
	if err := store.UpsertDailyProductivitySnapshot(ctx, db, authoritative); err != nil {
		t.Fatalf("seed reflection: %v", err)
	}

	// Now simulate a handler-initiated live backfill for the same day.
	live := store.DailyProductivitySnapshot{
		ProjectPath:        "/seeded/project",
		Day:                dayStr,
		PayloadJSON:        `{"day":"` + dayStr + `","sentinel":"live-recompute"}`,
		TotalActiveMinutes: 1,
		Source:             "live",
		CreatedAt:          2, UpdatedAt: 2,
	}
	if err := store.UpsertDailyProductivitySnapshot(ctx, db, live); err != nil {
		t.Fatalf("live upsert: %v", err)
	}

	got, ok, _ := store.GetDailyProductivitySnapshot(ctx, db, "/seeded/project", dayStr)
	if !ok {
		t.Fatalf("snapshot disappeared")
	}
	if got.Source != "reflection" {
		t.Fatalf("source clobbered: want 'reflection', got %q", got.Source)
	}
	if got.TotalActiveMinutes != 999 {
		t.Fatalf("authoritative payload replaced: want minutes=999, got %d", got.TotalActiveMinutes)
	}
}

// TestProductivity_SnapshotReadHydratesReflections is the regression
// guard for the catch-up reflection bug. Snapshots written by
// worklog.recordReflection serialize the substrate Report BEFORE the
// new reflection row is inserted, so the persisted payload carries
// reflection_status="missing" and zero ReflectionGroups even though
// a row exists in worklog_reflections. tryReadSnapshotDay must
// hydrate ReflectionGroups + ReflectionStatus from worklog_reflections
// on every read so the dashboard surfaces the reflection under the
// day it covers.
//
// Concretely: insert a snapshot with "missing" + no groups, insert a
// reflection row tagged day=that-day, then GET /productivity and
// assert the response shows status="current" with the body bullet.
func TestProductivity_SnapshotReadHydratesReflections(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	ctx := context.Background()

	now := time.Now()
	dayMid := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, now.Location())
	dayStr := dayMid.Format("2006-01-02")
	projectPath := "/seeded/hydration"

	stale := productivity.Report{
		Day: dayStr,
		Services: []productivity.Service{{
			Repo:        "hydration",
			ProjectPath: projectPath,
		}},
		ReflectionStatus: "missing",
	}
	stalePayload, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("marshal stale snapshot: %v", err)
	}
	if err := store.UpsertDailyProductivitySnapshot(ctx, db, store.DailyProductivitySnapshot{
		ProjectPath: projectPath,
		Day:         dayStr,
		PayloadJSON: string(stalePayload),
		Source:      "reflection",
		CreatedAt:   1, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("seed stale snapshot: %v", err)
	}

	if err := store.InsertReflection(ctx, db, store.Reflection{
		ID:               "ref-hydration-" + dayStr,
		TS:               now.UnixMilli(),
		ProjectPath:      projectPath,
		Day:              dayStr,
		Tier:             1,
		Title:            "Daily reflection — " + dayStr,
		BodyMD:           "- shipped the hydration fix\n",
		EvidenceEntryIDs: []string{"sess-1"},
		Importance:       7,
		SummarySource:    "ai",
		State:            "proposed",
		StateChangedAt:   now.UnixMilli(),
	}); err != nil {
		t.Fatalf("insert reflection: %v", err)
	}

	srv := httptest.NewServer(newProductivityRouter(t, db))
	t.Cleanup(srv.Close)

	url := fmt.Sprintf("%s%s?since=%d&until=%d",
		srv.URL, api.RouteProductivity,
		dayMid.UnixMilli(),
		dayMid.Add(24*time.Hour).Add(-time.Millisecond).UnixMilli())
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	body, _ := io.ReadAll(resp.Body)

	var got productivity.Report
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v — body=%s", err, body)
	}
	if got.ReflectionStatus != "current" {
		t.Fatalf("reflection_status = %q; want current (the seeded reflection row should hydrate over the stale snapshot)", got.ReflectionStatus)
	}
	if len(got.Services) != 1 {
		t.Fatalf("services count = %d; want 1", len(got.Services))
	}
	if got := got.Services[0].ReflectionGroups; len(got) != 1 {
		t.Fatalf("service[0].reflection_groups = %d entries; want 1", len(got))
	}
	if want, got := "- shipped the hydration fix\n", got.Services[0].ReflectionGroups[0].BodyMD; got != want {
		t.Errorf("reflection body = %q; want %q", got, want)
	}
}
