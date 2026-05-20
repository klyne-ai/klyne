package richentry

import (
	"context"
	"fmt"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// InputBuilder is the daemon-side glue: given a stop_summaries row,
// it produces the BundleInputs the writer needs PLUS the
// SessionMetrics the gate needs. Implementation reads messages, runs
// git scans, queries the gh PR cache, computes active intervals.
// Defined as an interface so the worker is testable with a stub.
type InputBuilder interface {
	Build(ctx context.Context, row store.PendingWorklogEntry) (BundleInputs, SessionMetrics, error)
}

// WorkerDeps groups the runtime dependencies the worker needs.
// MaxAttempts and BatchSize have sensible defaults applied in
// NewWorker so callers don't need to fill them in.
type WorkerDeps struct {
	DB          *store.DB
	LLM         AIClient
	Inputs      InputBuilder
	MaxAttempts int // default 3 — terminal state 'failed-permanent' once reached
	BatchSize   int // default 25 — max rows processed per RunOnce tick
}

// Worker drains the rich-entry queue (rows where verdict IN
// ('','pending') AND attempts < MaxAttempts). For each row it
// builds the per-turn inputs, runs the gate, runs the writer if
// admitted, and persists the outcome. Single-threaded; the daemon
// runs ONE Worker so attempt counters are race-free.
type Worker struct {
	deps WorkerDeps
}

// NewWorker constructs a Worker, applying defaults to MaxAttempts +
// BatchSize when zero so callers can construct with only the deps
// they explicitly care about.
func NewWorker(deps WorkerDeps) *Worker {
	if deps.MaxAttempts == 0 {
		deps.MaxAttempts = 3
	}
	if deps.BatchSize == 0 {
		deps.BatchSize = 25
	}
	return &Worker{deps: deps}
}

// RunOnce drains one batch of pending rows. Returns the number of
// rows processed in this tick. Per-row failures don't abort the
// batch — the worker is best-effort; failing rows either bump
// attempts (transient) or land at a terminal verdict (validator
// rejected twice / heuristic denied).
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	rows, err := store.ListPendingWorklogEntries(ctx, w.deps.DB, w.deps.BatchSize, w.deps.MaxAttempts)
	if err != nil {
		return 0, fmt.Errorf("worker: list pending: %w", err)
	}

	processed := 0
	for _, row := range rows {
		// Per-row errors are tolerated — the row's verdict + attempts
		// already capture state; skipping just means we move on.
		_ = w.processRow(ctx, row)
		processed++
	}
	return processed, nil
}

// Run loops every tick until ctx is cancelled, calling RunOnce. The
// loop swallows per-tick errors so transient DB hiccups don't tear
// down the worker; callers get ctx.Err() on cancel.
func (w *Worker) Run(ctx context.Context, tick time.Duration) error {
	if tick <= 0 {
		tick = 30 * time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			_, _ = w.RunOnce(ctx)
		}
	}
}

// processRow handles one row end-to-end:
//  1. Build inputs (transient retry on failure: bump attempts + pending)
//  2. Gate — admit / deny / borderline → admit-or-deny via Decide
//  3. If deny — persist gate verdict + bump attempts (terminal-for-this-row)
//  4. If admit — call Write; on LLM error bump+pending, on validator-skip
//     persist skipped-validator + empty entry (terminal), on success
//     persist the entry + gate verdict (terminal-for-this-row).
//
// Every path advances worklog_attempts so a stuck row eventually
// crosses MaxAttempts and drops out of the queue.
func (w *Worker) processRow(ctx context.Context, row store.PendingWorklogEntry) error {
	inputs, metrics, err := w.deps.Inputs.Build(ctx, row)
	if err != nil {
		// Input build failure — likely a missing repo / corrupted state
		// the worker can't fix. Bump attempts; row drops out at MAX.
		return store.IncrementWorklogAttempts(ctx, w.deps.DB, row.SessionID, row.Ts, "pending")
	}

	gateVerdict, admit := Decide(ctx, w.deps.LLM, metrics, row.Summary)
	if !admit {
		// Heuristic / LLM denied — persist a clean empty entry with the
		// terminal skipped-* verdict so the reflection consumer can
		// distinguish denied turns from un-processed ones.
		return store.UpsertStopSummaryWithEntry(ctx, w.deps.DB,
			row.StopSummary, emptyEntry(), gateVerdict, row.WorklogAttempts+1)
	}

	outcome, err := Write(ctx, w.deps.LLM, inputs, gateVerdict)
	if err != nil {
		// LLM transport error in the writer — bump attempts, leave as
		// pending for the next tick.
		return store.IncrementWorklogAttempts(ctx, w.deps.DB, row.SessionID, row.Ts, "pending")
	}
	// outcome.Verdict is either the gate verdict (admitted-*) on
	// successful validation, or "skipped-validator" if both LLM
	// attempts failed validation. Either is terminal — persist + bump.
	return store.UpsertStopSummaryWithEntry(ctx, w.deps.DB,
		row.StopSummary, outcome.Entry, outcome.Verdict, row.WorklogAttempts+1)
}
