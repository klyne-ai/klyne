package richentry_test

// Worker tests live in package richentry_test so they go through the
// exported surface only — the worker should be usable from any
// package without poking at internals.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/ai"
	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/worklog/richentry"
)

// --- helpers --------------------------------------------------------------

func openDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), dir+"/test.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seedPending inserts a stop_summaries row in the pending queue
// (verdict='', attempts=0) so the worker picks it up.
func seedPending(t *testing.T, db *store.DB, id string, ts int64, body string) {
	t.Helper()
	ctx := context.Background()
	if err := store.InsertStopSummary(ctx, db, &store.StopSummary{
		SessionID: id, Ts: ts, ProjectPath: "/r", Summary: body,
	}); err != nil {
		t.Fatal(err)
	}
}

// readState reads the worker-relevant columns back for assertions.
func readState(t *testing.T, db *store.DB, id string, ts int64) (verdict string, attempts int, entryHasShipped bool) {
	t.Helper()
	var raw string
	if err := db.Read().QueryRowContext(context.Background(),
		`SELECT worklog_gate_verdict, worklog_attempts, worklog_entry_json
		   FROM stop_summaries WHERE session_id=? AND ts=?`, id, ts,
	).Scan(&verdict, &attempts, &raw); err != nil {
		t.Fatal(err)
	}
	// Crude "did the entry land?" probe — just look for the "shipped"
	// category having any content.
	entryHasShipped = len(raw) > 50 && (containsAll(raw, "shipped", "summary"))
	return
}

func containsAll(s string, needles ...string) bool {
	for _, n := range needles {
		if !contains(s, n) {
			return false
		}
	}
	return true
}

func contains(s, n string) bool {
	for i := 0; i+len(n) <= len(s); i++ {
		if s[i:i+len(n)] == n {
			return true
		}
	}
	return false
}

// stubInputs returns canned BundleInputs + SessionMetrics so the
// worker can be exercised without real git/PR/messages adapters.
type stubInputs struct {
	bundle  richentry.BundleInputs
	metrics richentry.SessionMetrics
	err     error
}

func (s *stubInputs) Build(_ context.Context, _ store.PendingWorklogEntry) (richentry.BundleInputs, richentry.SessionMetrics, error) {
	return s.bundle, s.metrics, s.err
}

// scriptedAI returns scripted Chat replies in order.
type scriptedAI struct {
	replies []string
	errs    []error
	calls   int
}

func (s *scriptedAI) Chat(_ context.Context, _ ai.ChatRequest) (*ai.ChatResponse, error) {
	i := s.calls
	s.calls++
	if i < len(s.errs) && s.errs[i] != nil {
		return nil, s.errs[i]
	}
	if i >= len(s.replies) {
		return nil, errors.New("scripted AI exhausted")
	}
	return &ai.ChatResponse{Text: s.replies[i]}, nil
}

// admitMetrics produces metrics that fall into the heuristic-admit
// path (>=1 commit + lines_changed >= 10) so the worker doesn't
// touch the gate's LLM tiebreak in those tests.
func admitMetrics() richentry.SessionMetrics {
	return richentry.SessionMetrics{UserCommits: 1, LinesChanged: 50, NonDocFilesTouched: 1}
}

// denyMetrics — short turn, heuristic-deny path.
func denyMetrics() richentry.SessionMetrics {
	return richentry.SessionMetrics{ActiveDuration: 1 * time.Minute}
}

// minimalBundleInputs — a small but valid BundleInputs that admits
// the validReply below.
func minimalBundleInputs() richentry.BundleInputs {
	return richentry.BundleInputs{
		SessionID:       "w-test",
		Ts:              time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
		ProjectPath:     "/r",
		RepoName:        "repo",
		BranchName:      "feature/CLI-1325-x",
		BranchTicket:    "CLI-1325",
		StopSummaryBody: "body",
		Commits: []richentry.CommitRef{
			{SHA: "abc1234"},
		},
		MergedPRs: []richentry.MergedPRRef{
			{Number: 1, MergeSHA: "fedc789"},
		},
	}
}

const validWriterReply = `{
  "schema_version": 1,
  "categories": {
    "features_worked_on": [],
    "shipped": [{"summary":"PR merged","refs":["#1","fedc789"]}],
    "features_picked": [], "bugs_found": [], "bugs_fixed": [],
    "investigations": [], "decisions": [], "config_changes": [],
    "blockers": [], "blocked_on": [], "pending": [],
    "followups_for_others": [], "must_remember": [],
    "mistakes_or_dead_ends": [], "reviews_given": []
  }
}`

// --- tests ---------------------------------------------------------------

func TestWorker_HappyPath_HeuristicAdmit_PersistsEntry(t *testing.T) {
	db := openDB(t)
	seedPending(t, db, "row-a", 1000, "body")

	w := richentry.NewWorker(richentry.WorkerDeps{
		DB:     db,
		LLM:    &scriptedAI{replies: []string{validWriterReply}},
		Inputs: &stubInputs{bundle: minimalBundleInputs(), metrics: admitMetrics()},
	})
	n, err := w.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("processed: got %d want 1", n)
	}
	verdict, attempts, hasShipped := readState(t, db, "row-a", 1000)
	if verdict != "admitted-heuristic" {
		t.Errorf("verdict: got %q", verdict)
	}
	if attempts != 1 {
		t.Errorf("attempts: got %d want 1", attempts)
	}
	if !hasShipped {
		t.Errorf("entry lost the shipped item")
	}
}

func TestWorker_HeuristicDeny_PersistsSkippedHeuristic(t *testing.T) {
	db := openDB(t)
	seedPending(t, db, "row-b", 1000, "tiny")

	w := richentry.NewWorker(richentry.WorkerDeps{
		DB:     db,
		LLM:    &scriptedAI{}, // never called
		Inputs: &stubInputs{bundle: minimalBundleInputs(), metrics: denyMetrics()},
	})
	_, err := w.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	verdict, attempts, _ := readState(t, db, "row-b", 1000)
	if verdict != "skipped-heuristic" {
		t.Errorf("verdict: got %q", verdict)
	}
	if attempts != 1 {
		t.Errorf("attempts: got %d want 1", attempts)
	}
}

func TestWorker_LLMErrorInWriter_IncrementsAttemptsPending(t *testing.T) {
	db := openDB(t)
	seedPending(t, db, "row-c", 1000, "body")

	w := richentry.NewWorker(richentry.WorkerDeps{
		DB:     db,
		LLM:    &scriptedAI{errs: []error{errors.New("api down")}},
		Inputs: &stubInputs{bundle: minimalBundleInputs(), metrics: admitMetrics()},
	})
	_, _ = w.RunOnce(context.Background())
	verdict, attempts, _ := readState(t, db, "row-c", 1000)
	if verdict != "pending" {
		t.Errorf("verdict: got %q want pending", verdict)
	}
	if attempts != 1 {
		t.Errorf("attempts: got %d want 1", attempts)
	}
}

func TestWorker_AttemptsAtMax_ExcludedFromQueue(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	// Seed a row already at attempts=MAX with verdict='pending'. The
	// queue filter (attempts < MAX) must exclude it.
	_ = store.UpsertStopSummaryWithEntry(ctx, db,
		store.StopSummary{SessionID: "exhausted", Ts: 1, Summary: "x"},
		store.WorklogEntryJSON{SchemaVersion: 1}, "pending", 3)
	// And a normal pending row to confirm the worker did its tick.
	seedPending(t, db, "fresh", 2, "body")

	w := richentry.NewWorker(richentry.WorkerDeps{
		DB:          db,
		LLM:         &scriptedAI{replies: []string{validWriterReply}},
		Inputs:      &stubInputs{bundle: minimalBundleInputs(), metrics: admitMetrics()},
		MaxAttempts: 3,
	})
	n, _ := w.RunOnce(context.Background())
	if n != 1 {
		t.Errorf("processed: got %d want 1 (exhausted row must be skipped)", n)
	}
	// exhausted row stays at attempts=3 verdict=pending.
	v, a, _ := readState(t, db, "exhausted", 1)
	if v != "pending" || a != 3 {
		t.Errorf("exhausted row mutated: verdict=%q attempts=%d", v, a)
	}
}

func TestWorker_InputBuilderError_TransientRetry(t *testing.T) {
	db := openDB(t)
	seedPending(t, db, "row-d", 1000, "body")

	w := richentry.NewWorker(richentry.WorkerDeps{
		DB:     db,
		LLM:    &scriptedAI{}, // never reached
		Inputs: &stubInputs{err: errors.New("git scan failed")},
	})
	_, _ = w.RunOnce(context.Background())
	verdict, attempts, _ := readState(t, db, "row-d", 1000)
	if verdict != "pending" {
		t.Errorf("verdict: got %q want pending (transient)", verdict)
	}
	if attempts != 1 {
		t.Errorf("attempts: got %d want 1", attempts)
	}
}

func TestWorker_EmptyQueue_NoOp(t *testing.T) {
	db := openDB(t)
	w := richentry.NewWorker(richentry.WorkerDeps{
		DB:     db,
		LLM:    &scriptedAI{},
		Inputs: &stubInputs{},
	})
	n, err := w.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("processed: got %d want 0", n)
	}
}

func TestWorker_BatchProcessesMultipleRows(t *testing.T) {
	db := openDB(t)
	for i, id := range []string{"x", "y", "z"} {
		seedPending(t, db, id, int64((i+1)*1000), "body")
	}
	w := richentry.NewWorker(richentry.WorkerDeps{
		DB:     db,
		LLM:    &scriptedAI{replies: []string{validWriterReply, validWriterReply, validWriterReply}},
		Inputs: &stubInputs{bundle: minimalBundleInputs(), metrics: admitMetrics()},
	})
	n, _ := w.RunOnce(context.Background())
	if n != 3 {
		t.Errorf("processed: got %d want 3", n)
	}
}

func TestWorker_Run_ExitsOnContextCancel(t *testing.T) {
	db := openDB(t)
	w := richentry.NewWorker(richentry.WorkerDeps{
		DB:     db,
		LLM:    &scriptedAI{},
		Inputs: &stubInputs{},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := w.Run(ctx, 10*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded; got %v", err)
	}
}
