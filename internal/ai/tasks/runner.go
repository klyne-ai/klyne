package tasks

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/mohitpatell/agentdeck/internal/ai"
	"github.com/mohitpatell/agentdeck/internal/api"
	"github.com/mohitpatell/agentdeck/internal/store"
)

// defaultWindow is the number of messages that triggers a summarise run when
// no explicit Window is set.
const defaultWindow = 50

// compactDebounce is the minimum interval between two CompactDetected-driven
// summarise calls for the same session. Multi-emits within this window produce
// only one summary (spec acceptance criterion).
const compactDebounce = 500 * time.Millisecond

// RunnerDeps holds all external dependencies the Runner needs.
type RunnerDeps struct {
	DB       *store.DB
	Hub      *api.Hub
	Provider ai.Provider
	// Model is the model identifier to pass to Summarize (e.g. result of
	// ai.Pick().Model).
	Model  string
	Logger *slog.Logger
	// Window is the message count that triggers a summarise run.
	// Defaults to 50 when 0.
	Window int
}

// Runner subscribes to hub events and triggers background summarise tasks.
//
// It maintains a per-session message counter. Every Window messages OR on a
// CompactDetected event it enqueues a Summarize call. Summarize NEVER runs
// inline with the event-delivery goroutine — it runs in a dedicated worker
// goroutine (mitigates risk R7 from W0-INTEGRATION-NOTES).
type Runner struct {
	deps   RunnerDeps
	window int

	// counters maps sessionID → messages-since-last-summary.
	countersMu sync.Mutex
	counters   map[string]int

	// compactSeen maps sessionID → time of last compact-triggered summarize
	// (for debouncing).
	compactMu   sync.Mutex
	compactSeen map[string]time.Time

	// work queue for summarize tasks.
	workCh chan summarizeJob

	// wg tracks in-flight summarize goroutines so Stop can wait.
	wg sync.WaitGroup

	// cancel and done for Start lifecycle.
	cancel context.CancelFunc
	done   chan struct{}

	// stopOnce guards Stop so it is idempotent.
	stopOnce sync.Once
}

// summarizeJob carries the parameters for one background summarize call.
type summarizeJob struct {
	sessionID string
}

// NewRunner creates a Runner from the provided dependencies.
func NewRunner(deps RunnerDeps) *Runner {
	window := deps.Window
	if window <= 0 {
		window = defaultWindow
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	deps.Logger = logger

	return &Runner{
		deps:        deps,
		window:      window,
		counters:    make(map[string]int),
		compactSeen: make(map[string]time.Time),
		workCh:      make(chan summarizeJob, 64),
		done:        make(chan struct{}),
	}
}

// Start subscribes to MsgNew and CompactDetected events. It returns when ctx
// is cancelled. Workers are launched on the same ctx so they respect
// cancellation too.
//
// Callers should either call Stop() or cancel ctx to shut down cleanly.
func (r *Runner) Start(ctx context.Context) error {
	ctx, r.cancel = context.WithCancel(ctx)

	// Start the worker pool (one goroutine is enough for v1; the channel
	// provides back-pressure and NEVER blocks the hub's Publish caller).
	r.wg.Add(1)
	go r.worker(ctx)

	// Subscribe to hub events.
	ch, unsub := r.deps.Hub.Subscribe()
	defer unsub()

	for {
		select {
		case <-ctx.Done():
			close(r.workCh)
			r.wg.Wait()
			close(r.done)
			return ctx.Err()
		case ev, ok := <-ch:
			if !ok {
				// Hub was closed.
				close(r.workCh)
				r.wg.Wait()
				close(r.done)
				return nil
			}
			r.handleEvent(ev)
		}
	}
}

// Stop unsubscribes from the hub and waits for all in-flight summarise tasks
// to complete. Idempotent.
func (r *Runner) Stop() error {
	r.stopOnce.Do(func() {
		if r.cancel != nil {
			r.cancel()
		}
	})
	// Wait until Start's goroutine has closed done.
	<-r.done
	return nil
}

// handleEvent processes a single hub event inline. It ONLY updates counters
// and enqueues work — it never calls Summarize directly.
func (r *Runner) handleEvent(ev api.Event) {
	switch ev.EventName() {
	case api.EventMsgNew:
		payload, ok := ev.Payload().(api.MsgNew)
		if !ok {
			return
		}
		r.countersMu.Lock()
		r.counters[payload.SessionID]++
		count := r.counters[payload.SessionID]
		if count >= r.window {
			r.counters[payload.SessionID] = 0
			r.countersMu.Unlock()
			r.enqueue(payload.SessionID)
		} else {
			r.countersMu.Unlock()
		}

	case api.EventCompactDetected:
		payload, ok := ev.Payload().(api.CompactDetected)
		if !ok {
			return
		}
		// Debounce: ignore if we ran a compact-triggered summarize within
		// the debounce window for this session.
		r.compactMu.Lock()
		last, seen := r.compactSeen[payload.SessionID]
		if seen && time.Since(last) < compactDebounce {
			r.compactMu.Unlock()
			return
		}
		r.compactSeen[payload.SessionID] = time.Now()
		r.compactMu.Unlock()

		// Reset the message counter so the next batch starts fresh.
		r.countersMu.Lock()
		r.counters[payload.SessionID] = 0
		r.countersMu.Unlock()

		r.enqueue(payload.SessionID)
	}
}

// enqueue submits a summarize job to the work channel without blocking.
// If the channel is full the job is dropped and a warning is logged.
func (r *Runner) enqueue(sessionID string) {
	select {
	case r.workCh <- summarizeJob{sessionID: sessionID}:
	default:
		r.deps.Logger.Warn("tasks/runner: work queue full, dropping summarize",
			slog.String("session_id", sessionID))
	}
}

// worker drains workCh and calls Summarize for each job.
// It uses context.Background() for individual tasks so that a context
// cancellation (from Stop/shutdown) does not abort in-flight summarizations —
// Stop() instead waits for the worker to drain (mitigates risk R7).
func (r *Runner) worker(_ context.Context) {
	defer r.wg.Done()
	for job := range r.workCh {
		r.runSummarize(context.Background(), job.sessionID)
	}
}

// runSummarize calls Summarize and publishes a SummaryReady event on success.
func (r *Runner) runSummarize(ctx context.Context, sessionID string) {
	summary, err := Summarize(ctx, r.deps.DB, r.deps.Provider, r.deps.Model, sessionID, r.window)
	if err != nil {
		r.deps.Logger.Error("tasks/runner: summarize failed",
			slog.String("session_id", sessionID),
			slog.Any("error", err))
		return
	}

	r.deps.Hub.Publish(api.SummaryReadyEvent(api.SummaryReady{
		SessionID: summary.SessionID,
		Version:   summary.Version,
		Ts:        summary.TS,
		Model:     summary.Model,
	}))

	r.deps.Logger.Info("tasks/runner: summary published",
		slog.String("session_id", sessionID),
		slog.Int("version", summary.Version))
}
