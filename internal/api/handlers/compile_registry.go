package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/klyne-ai/klyne/internal/productivity"
	"github.com/klyne-ai/klyne/internal/store"
)

// CompileServiceState is the live state of one service inside a compile
// job. Mirrors the JSON the dashboard's per-service detail panel renders.
type CompileServiceState struct {
	Service     string `json:"service"`
	ProjectPath string `json:"project_path"`
	Status      string `json:"status"` // queued | running | done | failed
	StartedAt   int64  `json:"started_at"`
	FinishedAt  int64  `json:"finished_at"`
	Error       string `json:"error,omitempty"`
}

// CompileJob is one detached background compile run for a single day.
// Exactly one active job per day lives in the registry.
type CompileJob struct {
	ID         string                `json:"id"`
	Day        string                `json:"day"`
	Model      string                `json:"model"`  // sonnet | opus
	Status     string                `json:"status"` // running | done | partial | failed
	StartedAt  int64                 `json:"started_at"`
	FinishedAt int64                 `json:"finished_at"`
	Services   []CompileServiceState `json:"services"`
}

// compileRunFn is the per-service subprocess factory — the same signature
// as spawnClaudeProductivitySync, injectable for tests.
type compileRunFn func(ctx context.Context, projectPath, day, modelKey string) (claudeRunResult, error)

// perServiceCompileTimeout is the per-subprocess budget for one service's
// productivity-sync run.
const perServiceCompileTimeout = 10 * time.Minute

// compileJobSlack is extra wall-clock head-room on top of the sum of the
// per-service budgets, covering scheduling, MCP startup, and the subprocess
// WaitDelay grace on each kill. The job-level watchdog uses
// len(services)*perServiceCompileTimeout + compileJobSlack as its overall
// deadline so a misbehaving runCmd can never pin the day's slot as "running"
// forever (combined with cmd.WaitDelay this fully closes the wedge).
const compileJobSlack = 2 * time.Minute

// CompileJobRegistry holds one *CompileJob per day behind a mutex and
// drives each job's detached goroutine. In-memory only (Option A): a
// daemon restart loses in-flight jobs and the user re-clicks Compile.
type CompileJobRegistry struct {
	mu     sync.Mutex
	jobs   map[string]*CompileJob // keyed by day
	db     *store.DB              // for persistKlyneUsage; nil in registry-only tests
	runCmd compileRunFn

	// perServiceTimeout / jobSlack are the per-subprocess budget and the
	// overall-job head-room. They default to perServiceCompileTimeout /
	// compileJobSlack; tests override them to tiny values to exercise the
	// watchdog without waiting minutes.
	perServiceTimeout time.Duration
	jobSlack          time.Duration
}

// NewCompileJobRegistry constructs an empty registry. db may be nil in
// unit tests that only exercise state transitions (persistKlyneUsage
// tolerates a nil db by skipping the write — see Start's goroutine).
func NewCompileJobRegistry(db *store.DB, runCmd compileRunFn) *CompileJobRegistry {
	return &CompileJobRegistry{
		jobs:              map[string]*CompileJob{},
		db:                db,
		runCmd:            runCmd,
		perServiceTimeout: perServiceCompileTimeout,
		jobSlack:          compileJobSlack,
	}
}

// Start creates and launches a job for day, or returns the existing one
// unchanged when a job for that day is already running (idempotent —
// no second spawn). pending is the floor-based service set the caller
// already computed. Returns a snapshot copy safe to serialize.
func (r *CompileJobRegistry) Start(day, modelKey string, pending []CompileServiceState) CompileJob {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.jobs[day]; ok && existing.Status == "running" {
		return cloneJob(existing)
	}

	now := time.Now().UnixMilli()
	job := &CompileJob{
		ID:        fmt.Sprintf("compile-%s-%d", day, now),
		Day:       day,
		Model:     modelKey,
		Status:    "running",
		StartedAt: now,
		Services:  append([]CompileServiceState(nil), pending...),
	}
	for i := range job.Services {
		job.Services[i].Status = "queued"
	}
	r.jobs[day] = job
	go r.execute(job)
	return cloneJob(job)
}

// Status returns a snapshot of the day's job, or ok=false if none.
func (r *CompileJobRegistry) Status(day string) (CompileJob, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[day]
	if !ok {
		return CompileJob{}, false
	}
	return cloneJob(job), true
}

// execute runs each service sequentially on a detached context so a
// client reload cannot cancel it. Each service gets the per-subprocess
// timeout. Per-service state and the overall job status are mutated under
// the registry lock.
//
// A job-level watchdog guarantees the job ALWAYS reaches a terminal status
// and frees the day's "running" slot, even if a runCmd ignores its context
// and blocks forever: the per-service loop runs in a child goroutine driven
// by jobCtx (an overall budget of len(services)*perService + slack); if that
// deadline fires before the loop returns, execute records a terminal failed
// status and returns without waiting on the wedged goroutine. (Combined with
// cmd.WaitDelay on the real spawn, a genuine subprocess kill cannot wedge
// either path.)
func (r *CompileJobRegistry) execute(job *CompileJob) {
	overall := time.Duration(len(job.Services))*r.perServiceTimeout + r.jobSlack
	jobCtx, jobCancel := context.WithTimeout(context.Background(), overall)
	defer jobCancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.runServices(jobCtx, job)
	}()

	select {
	case <-done:
		// Worker loop finished and already set the terminal status.
		return
	case <-jobCtx.Done():
		// Watchdog fired: the worker loop is wedged (a runCmd ignored its
		// context). Force the job to a terminal status so the day's slot is
		// freed; mark any not-yet-terminal services as failed. The leaked
		// worker goroutine (if any) will set its own per-service fields later
		// under the lock, but the job is already terminal and re-Compile is
		// unblocked.
		r.mu.Lock()
		if job.FinishedAt == 0 {
			for i := range job.Services {
				switch job.Services[i].Status {
				case "queued", "running":
					job.Services[i].Status = "failed"
					job.Services[i].FinishedAt = time.Now().UnixMilli()
					if job.Services[i].Error == "" {
						job.Services[i].Error = "compile job exceeded overall budget"
					}
				}
			}
			job.Status = "failed"
			job.FinishedAt = time.Now().UnixMilli()
		}
		r.mu.Unlock()
	}
}

// runServices runs every service sequentially under jobCtx. It is the body
// of the watchdog'd worker goroutine; on normal completion it sets the job's
// terminal status. Per-service ctx is the smaller of the per-service budget
// and jobCtx's remaining deadline so a slow early service can't starve later
// ones past the overall budget.
func (r *CompileJobRegistry) runServices(jobCtx context.Context, job *CompileJob) {
	okCount, failCount := 0, 0
	for i := range job.Services {
		path := job.Services[i].ProjectPath
		r.setService(job, i, func(s *CompileServiceState) {
			s.Status = "running"
			s.StartedAt = time.Now().UnixMilli()
		})

		ctx, cancel := context.WithTimeout(jobCtx, r.perServiceTimeout)
		start := time.Now()
		res, runErr := r.runCmd(ctx, path, job.Day, job.Model)
		if runErr == nil && r.db != nil {
			verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := verifyCompiledCardPersisted(verifyCtx, r.db, path, job.Day, start.UnixMilli()); err != nil {
				runErr = err
			}
			verifyCancel()
		}
		dur := time.Since(start).Milliseconds()
		timedOut := ctx.Err() == context.DeadlineExceeded
		cancel()

		status := "ok"
		switch {
		case runErr == nil:
			status = "ok"
		case timedOut:
			status = "timeout"
		default:
			status = "error"
		}
		r.setService(job, i, func(s *CompileServiceState) {
			s.FinishedAt = time.Now().UnixMilli()
			if runErr == nil {
				s.Status = "done"
			} else {
				s.Status = "failed"
				if timedOut {
					s.Error = "subprocess exceeded 10m budget"
				} else {
					s.Error = runErr.Error()
				}
			}
		})
		if runErr == nil {
			okCount++
		} else {
			failCount++
		}

		// Record token usage exactly as the legacy blocking handler did —
		// per service, regardless of success. Skipped when db is nil
		// (registry-only unit tests).
		if r.db != nil {
			persistKlyneUsage(context.Background(), r.db, path, job.Day,
				"productivity_sync", res, status, dur)
		}
	}

	final := "done"
	switch {
	case failCount == 0:
		final = "done"
	case okCount == 0:
		final = "failed"
	default:
		final = "partial"
	}
	r.mu.Lock()
	// The watchdog may have already forced a terminal status if it fired in a
	// tight race; don't clobber it.
	if job.FinishedAt == 0 {
		job.Status = final
		job.FinishedAt = time.Now().UnixMilli()
	}
	r.mu.Unlock()
}

// verifyCompiledCardPersisted enforces the compile job's real postcondition:
// a successful agent process must have written a fresh llm_compiled card.
func verifyCompiledCardPersisted(
	ctx context.Context, db *store.DB, projectPath, day string, startedAt int64,
) error {
	snap, ok, err := store.GetDailyProductivitySnapshot(ctx, db, projectPath, day)
	if err != nil {
		return fmt.Errorf("compile postcondition: read snapshot: %w", err)
	}
	if !ok || strings.TrimSpace(snap.PayloadJSON) == "" {
		return fmt.Errorf("compile postcondition: agent exited without persisting a productivity card")
	}
	if snap.UpdatedAt < startedAt {
		return fmt.Errorf("compile postcondition: productivity card was not refreshed by this run")
	}
	var rep productivity.Report
	if err := json.Unmarshal([]byte(snap.PayloadJSON), &rep); err != nil {
		return fmt.Errorf("compile postcondition: decode snapshot: %w", err)
	}
	for _, svc := range rep.Services {
		if svc.WhatWasDone != nil && svc.WhatWasDone.LLMCompiled {
			return nil
		}
	}
	return fmt.Errorf("compile postcondition: persisted snapshot contains no llm_compiled card")
}

// setService mutates one service state under the lock.
func (r *CompileJobRegistry) setService(job *CompileJob, i int, fn func(*CompileServiceState)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(&job.Services[i])
}

// cloneJob returns a deep copy so callers serialize a stable snapshot
// without racing the goroutine. Caller must hold r.mu.
func cloneJob(j *CompileJob) CompileJob {
	out := *j
	out.Services = append([]CompileServiceState(nil), j.Services...)
	return out
}
