package handlers

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/klyne-ai/klyne/internal/productivity"
	"github.com/klyne-ai/klyne/internal/store"
)

// discoverPendingCompileServices returns the pending set for a day: one
// entry per worklog-rollup project for which projectNeedsCompile is true.
// The pending rule matches what the dashboard's per-service pending_compile
// count reflects (spec D3), so the ✨ Compile pill and the start route
// agree on what needs compiling.
func discoverPendingCompileServices(ctx context.Context, db *store.DB, dayStr string) ([]CompileServiceState, error) {
	rollup, err := store.ListWorklogRollup(ctx, db)
	if err != nil {
		return nil, err
	}
	var pending []CompileServiceState
	seen := map[string]bool{}
	for _, p := range rollup {
		if p.ProjectPath == "" || seen[p.ProjectPath] {
			continue
		}
		need, nerr := projectNeedsCompile(ctx, db, p.ProjectPath, dayStr)
		if nerr != nil {
			log.Printf("compile: pending check for %s/%s: %v", p.ProjectPath, dayStr, nerr)
			continue
		}
		if !need {
			continue
		}
		seen[p.ProjectPath] = true
		name := strings.TrimSpace(p.Name)
		if name == "" {
			name = basePath(p.ProjectPath)
		}
		pending = append(pending, CompileServiceState{
			Service:     name,
			ProjectPath: p.ProjectPath,
			Status:      "queued",
		})
	}
	return pending, nil
}

// projectNeedsCompile reports whether (project, day) has outstanding
// compile work, using the SAME freshness + coverage rule the dashboard's
// per-service pending_compile count reflects (spec D3). Pending when the L1
// floor is non-empty AND any of:
//   - no llm_compiled card exists yet;
//   - a newer floor-admitted summary landed since the card was compiled
//     (stale by time — new work, even on an already-cited ticket);
//   - the floor contains a ticket the compiled cards don't yet cover —
//     e.g. a SECOND service under the same project_path that was never
//     compiled, or a ticket the LLM under-cited. This coverage check is
//     what keeps discovery from skipping a whole project_path just because
//     ONE of its services already has a fresh card (the dead-Compile-button
//     bug).
//
// A transient floor/freshness read error degrades to "fresh" (not pending)
// so a flaky read never triggers a spurious recompile.
func projectNeedsCompile(ctx context.Context, db *store.DB, projectPath, dayStr string) (bool, error) {
	floor, err := productivity.BuildFloorFromStopSummaries(ctx, db.Read(), projectPath, dayStr)
	if err != nil {
		return false, err
	}
	if len(floor) == 0 {
		return false, nil
	}
	compiled, compiledAt, covered := compiledFloorCoverage(ctx, db, projectPath, dayStr)
	if !compiled {
		return true, nil
	}
	if latest, lerr := productivity.LatestFloorTurnTs(ctx, db.Read(), projectPath, dayStr); lerr == nil && latest > compiledAt {
		return true, nil
	}
	for _, f := range floor {
		if f.TicketID != "" && !covered[f.TicketID+"\x1f"+f.Kind] {
			return true, nil
		}
	}
	return false, nil
}

// compiledFloorCoverage reads the (project, day) snapshot and returns
// whether any llm_compiled card exists, the time it was compiled
// (snapshot.UpdatedAt), and the set of ticket IDs those compiled cards
// already cover. Mirrors hydrateWhatWasDone's snapshot read.
func compiledFloorCoverage(ctx context.Context, db *store.DB, projectPath, dayStr string) (compiled bool, compiledAt int64, covered map[string]bool) {
	covered = map[string]bool{}
	snap, ok, err := store.GetDailyProductivitySnapshot(ctx, db, projectPath, dayStr)
	if err != nil || !ok || strings.TrimSpace(snap.PayloadJSON) == "" {
		return false, 0, covered
	}
	var saved productivity.Report
	if err := json.Unmarshal([]byte(snap.PayloadJSON), &saved); err != nil {
		return false, 0, covered
	}
	for i := range saved.Services {
		c := saved.Services[i].WhatWasDone
		if c == nil || !c.LLMCompiled {
			continue
		}
		compiled = true
		compiledAt = snap.UpdatedAt
		for key := range productivity.CardOutcomeKeys(c) {
			covered[key] = true
		}
	}
	return compiled, compiledAt, covered
}
