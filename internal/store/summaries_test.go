package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// openSummaryTestDB opens a fresh test database and seeds a session row for
// use in summary tests.
func openSummaryTestDB(t *testing.T) (*store.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "summary_test.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sessID := "sess-summary-001"
	ctx := context.Background()
	_, err = db.Write().ExecContext(ctx, `
		INSERT INTO sessions (id, cli, project_path, started_at, last_msg_at, raw_path)
		VALUES (?, 'claude', '/tmp/proj', ?, ?, '/tmp/proj/sess.jsonl')`,
		sessID, time.Now().UnixMilli(), time.Now().UnixMilli(),
	)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return db, sessID
}

// makeSummary builds a Summary for testing.
func makeSummary(sessID, text, model string) *store.Summary {
	return &store.Summary{
		SessionID: sessID,
		Text:      text,
		Model:     model,
		TS:        time.Now().UnixMilli(),
	}
}

// TestInsertSummary_AutoVersions verifies that five sequential inserts produce
// versions 1 through 5 with no gaps or duplicates.
func TestInsertSummary_AutoVersions(t *testing.T) {
	db, sessID := openSummaryTestDB(t)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		s := makeSummary(sessID, fmt.Sprintf("summary text %d", i), "gemini-2.5-flash-lite")
		if err := store.InsertSummary(ctx, db, s); err != nil {
			t.Fatalf("InsertSummary #%d: %v", i, err)
		}
		if s.Version != i {
			t.Errorf("insert #%d: Version = %d; want %d", i, s.Version, i)
		}
	}
}

// TestInsertSummary_ConcurrentNoDuplicates verifies that 10 goroutines
// inserting summaries for the same session simultaneously never produce
// duplicate version numbers and that all versions are unique.
func TestInsertSummary_ConcurrentNoDuplicates(t *testing.T) {
	db, sessID := openSummaryTestDB(t)
	ctx := context.Background()

	const n = 10
	versions := make([]int, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s := makeSummary(sessID, fmt.Sprintf("concurrent summary %d", idx), "gemini-2.5-flash-lite")
			errs[idx] = store.InsertSummary(ctx, db, s)
			versions[idx] = s.Version
		}(i)
	}
	wg.Wait()

	// Check no errors.
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: InsertSummary error: %v", i, err)
		}
	}

	// Verify uniqueness.
	seen := make(map[int]bool)
	for i, v := range versions {
		if v == 0 {
			continue // skip errored slots
		}
		if seen[v] {
			t.Errorf("duplicate version %d (assigned to goroutine %d and at least one other)", v, i)
		}
		seen[v] = true
	}

	// Verify versions form a contiguous range 1..n.
	vList := make([]int, 0, n)
	for v := range seen {
		vList = append(vList, v)
	}
	sort.Ints(vList)

	if len(vList) != n {
		t.Errorf("expected %d unique versions, got %d: %v", n, len(vList), vList)
	}
	for i, v := range vList {
		if v != i+1 {
			t.Errorf("version gap: expected %d at position %d, got %d (versions=%v)", i+1, i, v, vList)
			break
		}
	}
}

// TestInsertSummary_MultipleSessions verifies that version counters are
// independent per session.
func TestInsertSummary_MultipleSessions(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "multisess.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close() //nolint:errcheck

	ctx := context.Background()
	now := time.Now().UnixMilli()

	// Seed two sessions.
	for _, sessID := range []string{"sess-A", "sess-B"} {
		_, err := db.Write().ExecContext(ctx, `
			INSERT INTO sessions (id, cli, project_path, started_at, last_msg_at, raw_path)
			VALUES (?, 'claude', '/tmp/proj', ?, ?, '/tmp/proj/sess.jsonl')`,
			sessID, now, now,
		)
		if err != nil {
			t.Fatalf("insert session %s: %v", sessID, err)
		}
	}

	// Insert 3 summaries for sess-A and 2 for sess-B.
	for i := 0; i < 3; i++ {
		s := makeSummary("sess-A", fmt.Sprintf("A summary %d", i), "model-a")
		if err := store.InsertSummary(ctx, db, s); err != nil {
			t.Fatalf("InsertSummary sess-A #%d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		s := makeSummary("sess-B", fmt.Sprintf("B summary %d", i), "model-b")
		if err := store.InsertSummary(ctx, db, s); err != nil {
			t.Fatalf("InsertSummary sess-B #%d: %v", i, err)
		}
	}

	latestA, err := store.LatestSummary(ctx, db, "sess-A")
	if err != nil {
		t.Fatalf("LatestSummary sess-A: %v", err)
	}
	if latestA.Version != 3 {
		t.Errorf("sess-A latest version = %d; want 3", latestA.Version)
	}

	latestB, err := store.LatestSummary(ctx, db, "sess-B")
	if err != nil {
		t.Fatalf("LatestSummary sess-B: %v", err)
	}
	if latestB.Version != 2 {
		t.Errorf("sess-B latest version = %d; want 2", latestB.Version)
	}
}

// TestLatestSummary_ReturnsHighestVersion inserts v1, v2, v3 and asserts
// LatestSummary returns v3 with the correct text.
func TestLatestSummary_ReturnsHighestVersion(t *testing.T) {
	db, sessID := openSummaryTestDB(t)
	ctx := context.Background()

	for i, text := range []string{"first", "second", "third"} {
		s := makeSummary(sessID, text, "model-x")
		if err := store.InsertSummary(ctx, db, s); err != nil {
			t.Fatalf("InsertSummary #%d: %v", i+1, err)
		}
	}

	latest, err := store.LatestSummary(ctx, db, sessID)
	if err != nil {
		t.Fatalf("LatestSummary: %v", err)
	}
	if latest.Version != 3 {
		t.Errorf("Version = %d; want 3", latest.Version)
	}
	if latest.Text != "third" {
		t.Errorf("Text = %q; want %q", latest.Text, "third")
	}
	if latest.SessionID != sessID {
		t.Errorf("SessionID = %q; want %q", latest.SessionID, sessID)
	}
}

// TestLatestSummary_NotFound verifies that LatestSummary returns sql.ErrNoRows
// when no summary exists for the given session.
func TestLatestSummary_NotFound(t *testing.T) {
	db, _ := openSummaryTestDB(t)
	ctx := context.Background()

	// Query for a session that exists but has no summaries.
	_, err := store.LatestSummary(ctx, db, "sess-summary-001")
	if err == nil {
		t.Fatal("expected error for missing summary, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows, got: %v", err)
	}
}

// TestLatestSummary_NonexistentSession verifies that querying a session that
// does not exist returns sql.ErrNoRows.
func TestLatestSummary_NonexistentSession(t *testing.T) {
	db, _ := openSummaryTestDB(t)
	ctx := context.Background()

	_, err := store.LatestSummary(ctx, db, "nonexistent-session-id")
	if err == nil {
		t.Fatal("expected error for nonexistent session, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows, got: %v", err)
	}
}

// TestListSummaries_OrderDesc verifies that ListSummaries returns summaries
// ordered by version descending.
func TestListSummaries_OrderDesc(t *testing.T) {
	db, sessID := openSummaryTestDB(t)
	ctx := context.Background()

	texts := []string{"alpha", "beta", "gamma", "delta"}
	for i, text := range texts {
		s := makeSummary(sessID, text, "model-y")
		if err := store.InsertSummary(ctx, db, s); err != nil {
			t.Fatalf("InsertSummary #%d: %v", i+1, err)
		}
	}

	list, err := store.ListSummaries(ctx, db, sessID)
	if err != nil {
		t.Fatalf("ListSummaries: %v", err)
	}
	if len(list) != 4 {
		t.Fatalf("expected 4 summaries, got %d", len(list))
	}

	// Should be ordered version DESC: 4, 3, 2, 1.
	for i, s := range list {
		wantVer := 4 - i
		if s.Version != wantVer {
			t.Errorf("list[%d].Version = %d; want %d", i, s.Version, wantVer)
		}
	}
}

// TestListSummaries_EmptySession verifies that ListSummaries returns an empty
// (non-nil) slice when the session has no summaries.
func TestListSummaries_EmptySession(t *testing.T) {
	db, sessID := openSummaryTestDB(t)
	ctx := context.Background()

	list, err := store.ListSummaries(ctx, db, sessID)
	if err != nil {
		t.Fatalf("ListSummaries: %v", err)
	}
	if list == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
	if len(list) != 0 {
		t.Errorf("expected 0 summaries, got %d", len(list))
	}
}

// TestInsertSummary_FieldsRoundTrip verifies that all fields survive a
// round-trip through the database.
func TestInsertSummary_FieldsRoundTrip(t *testing.T) {
	db, sessID := openSummaryTestDB(t)
	ctx := context.Background()

	ts := time.Now().UnixMilli()
	s := &store.Summary{
		SessionID: sessID,
		Text:      "detailed summary text with newlines\nand unicode: 日本語",
		Model:     "gemini-2.5-flash-lite",
		TS:        ts,
	}

	if err := store.InsertSummary(ctx, db, s); err != nil {
		t.Fatalf("InsertSummary: %v", err)
	}

	got, err := store.LatestSummary(ctx, db, sessID)
	if err != nil {
		t.Fatalf("LatestSummary: %v", err)
	}

	if got.Text != s.Text {
		t.Errorf("Text mismatch:\n  got  %q\n  want %q", got.Text, s.Text)
	}
	if got.Model != s.Model {
		t.Errorf("Model = %q; want %q", got.Model, s.Model)
	}
	if got.TS != ts {
		t.Errorf("TS = %d; want %d", got.TS, ts)
	}
	if got.Version != 1 {
		t.Errorf("Version = %d; want 1", got.Version)
	}
}
