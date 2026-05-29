package handlers

import (
	"context"
	"fmt"
	"sync"
	"time"

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

// CompileJobRegistry holds one *CompileJob per day behind a mutex and
// drives each job's detached goroutine. In-memory only (Option A): a
// daemon restart loses in-flight jobs and the user re-clicks Compile.
type CompileJobRegistry struct {
	mu     sync.Mutex
	jobs   map[string]*CompileJob // keyed by day
	db     *store.DB              // for persistKlyneUsage; nil in registry-only tests
	runCmd compileRunFn
}

// NewCompileJobRegistry constructs an empty registry. db may be nil in
// unit tests that only exercise state transitions (persistKlyneUsage
// tolerates a nil db by skipping the write — see Start's goroutine).
func NewCompileJobRegistry(db *store.DB, runCmd compileRunFn) *CompileJobRegistry {
	return &CompileJobRegistry{jobs: map[string]*CompileJob{}, db: db, runCmd: runCmd}
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
// client reload cannot cancel it. Each service gets the existing
// 10-minute per-subprocess timeout. Per-service state and the overall
// job status are mutated under the registry lock.
func (r *CompileJobRegistry) execute(job *CompileJob) {
	okCount, failCount := 0, 0
	for i := range job.Services {
		path := job.Services[i].ProjectPath
		r.setService(job, i, func(s *CompileServiceState) {
			s.Status = "running"
			s.StartedAt = time.Now().UnixMilli()
		})

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		start := time.Now()
		res, runErr := r.runCmd(ctx, path, job.Day, job.Model)
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
	job.Status = final
	job.FinishedAt = time.Now().UnixMilli()
	r.mu.Unlock()
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
