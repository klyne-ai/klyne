package handlers

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// waitForStatus polls the registry until the day's job reaches a terminal
// status or the deadline passes. Returns the final snapshot.
func waitForStatus(t *testing.T, reg *CompileJobRegistry, day string) CompileJob {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := reg.Status(day)
		if ok && job.Status != "running" {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job did not reach terminal status within 2s")
	return CompileJob{}
}

func TestCompileRegistry_AllSucceed(t *testing.T) {
	t.Parallel()
	reg := NewCompileJobRegistry(nil, func(ctx context.Context, projectPath, day, modelKey string) (claudeRunResult, error) {
		return claudeRunResult{Output: "ok", Model: "stub"}, nil
	})
	pending := []CompileServiceState{
		{Service: "a", ProjectPath: "/p/a", Status: "queued"},
		{Service: "b", ProjectPath: "/p/b", Status: "queued"},
	}
	job := reg.Start("2026-05-26", "sonnet", pending)
	if job.Status != "running" {
		t.Fatalf("initial Status = %q, want running", job.Status)
	}
	final := waitForStatus(t, reg, "2026-05-26")
	if final.Status != "done" {
		t.Errorf("final Status = %q, want done", final.Status)
	}
	for _, s := range final.Services {
		if s.Status != "done" {
			t.Errorf("service %s Status = %q, want done", s.Service, s.Status)
		}
	}
}

func TestCompileRegistry_PartialOnOneFailure(t *testing.T) {
	t.Parallel()
	reg := NewCompileJobRegistry(nil, func(ctx context.Context, projectPath, day, modelKey string) (claudeRunResult, error) {
		if projectPath == "/p/b" {
			return claudeRunResult{}, errors.New("boom")
		}
		return claudeRunResult{Output: "ok", Model: "stub"}, nil
	})
	pending := []CompileServiceState{
		{Service: "a", ProjectPath: "/p/a", Status: "queued"},
		{Service: "b", ProjectPath: "/p/b", Status: "queued"},
	}
	reg.Start("2026-05-26", "sonnet", pending)
	final := waitForStatus(t, reg, "2026-05-26")
	if final.Status != "partial" {
		t.Errorf("final Status = %q, want partial", final.Status)
	}
	var aState, bState string
	for _, s := range final.Services {
		switch s.ProjectPath {
		case "/p/a":
			aState = s.Status
		case "/p/b":
			bState = s.Status
		}
	}
	if aState != "done" || bState != "failed" {
		t.Errorf("states a=%q b=%q, want done/failed", aState, bState)
	}
}

func TestCompileRegistry_DoubleStartIsIdempotent(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	var calls int32
	reg := NewCompileJobRegistry(nil, func(ctx context.Context, projectPath, day, modelKey string) (claudeRunResult, error) {
		atomic.AddInt32(&calls, 1)
		<-release // block so the first job stays "running" across the second Start
		return claudeRunResult{Output: "ok", Model: "stub"}, nil
	})
	pending := []CompileServiceState{{Service: "a", ProjectPath: "/p/a", Status: "queued"}}
	first := reg.Start("2026-05-26", "sonnet", pending)
	second := reg.Start("2026-05-26", "opus", pending) // different model, same day
	if first.ID != second.ID {
		t.Errorf("second Start returned a new job (%q != %q); want the same running job", second.ID, first.ID)
	}
	if second.Model != "sonnet" {
		t.Errorf("running job Model = %q, want sonnet (second Start must not re-spawn)", second.Model)
	}
	close(release)
	waitForStatus(t, reg, "2026-05-26")
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("runCmd invoked %d times, want 1 (double-start must not spawn a second run)", got)
	}
}

func TestCompileRegistry_StatusNoneForUnknownDay(t *testing.T) {
	t.Parallel()
	reg := NewCompileJobRegistry(nil, nil)
	if _, ok := reg.Status("2026-01-01"); ok {
		t.Error("Status ok = true for a day with no job, want false")
	}
}

func TestDiscoverPending_FloorWithoutCompiledCard(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/disc"
	ts := int64(1_716_700_000_000)
	day := dayForTs(ts)
	// seedStopSummary registers the project in the worklog rollup AND
	// produces a non-empty floor for `day`.
	seedStopSummary(t, db, proj, "s1", ts, "CLI-1473 implemented discovery")

	pending, err := discoverPendingCompileServices(context.Background(), db, day)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(pending) != 1 || pending[0].ProjectPath != proj {
		t.Fatalf("pending = %+v, want one entry for %s", pending, proj)
	}
	if pending[0].Status != "queued" {
		t.Errorf("Status = %q, want queued", pending[0].Status)
	}
}

func TestDiscoverPending_EmptyWhenNoFloor(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/none"
	seedAllowlist(t, db, proj) // rollup row, but its summary lands on 1970, not `day`
	pending, err := discoverPendingCompileServices(context.Background(), db, "2026-05-26")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %+v, want empty (no floor on that day)", pending)
	}
}

func TestDiscoverPending_SkipsFreshCompiledDay(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/fresh"
	ts := int64(1_716_700_000_000)
	day := dayForTs(ts)
	seedStopSummary(t, db, proj, "s1", ts, "CLI-1452 shipped")
	seedCompiledSnapshot(t, db, proj, "fresh", day, ts+1000, "CLI-1452 shipped") // compiled after the summary

	pending, err := discoverPendingCompileServices(context.Background(), db, day)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %+v, want empty (fresh compiled day must not be re-offered)", pending)
	}
}

func TestDiscoverPending_MultiServicePartialCompile(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/multi"
	ts := int64(1_716_700_000_000)
	day := dayForTs(ts)
	// Floor spans two tickets (two services' worth of work under one path).
	seedStopSummary(t, db, proj, "s1", ts, "CLI-1001 service A work")
	seedStopSummary(t, db, proj, "s2", ts+100, "CLI-2002 service B work")
	// A fresh compiled card covers only CLI-1001; CLI-2002 is uncompiled.
	seedCompiledSnapshot(t, db, proj, "multi", day, ts+1000, "CLI-1001 service A work")

	pending, err := discoverPendingCompileServices(context.Background(), db, day)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(pending) != 1 || pending[0].ProjectPath != proj {
		t.Fatalf("pending = %+v, want one entry for %s (CLI-2002 uncovered → re-offer; the old any-card skip was the dead-button bug)", pending, proj)
	}
}

func TestDiscoverPending_ReoffersStaleCompiledDay(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/stale"
	ts := int64(1_716_700_000_000)
	day := dayForTs(ts)
	seedStopSummary(t, db, proj, "s1", ts, "CLI-1452 shipped")
	seedCompiledSnapshot(t, db, proj, "stale", day, ts+1000, "CLI-1452 shipped")
	seedStopSummary(t, db, proj, "s2", ts+5000, "CLI-1452 follow-up") // newer than compile → stale

	pending, err := discoverPendingCompileServices(context.Background(), db, day)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(pending) != 1 || pending[0].ProjectPath != proj {
		t.Fatalf("pending = %+v, want one entry for %s (stale day re-offered)", pending, proj)
	}
}
