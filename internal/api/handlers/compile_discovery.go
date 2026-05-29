package handlers

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/klyne-ai/klyne/internal/productivity"
	"github.com/klyne-ai/klyne/internal/store"
)

// discoverPendingCompileServices returns the floor-based pending set for a
// day: one entry per worklog-rollup project that (a) has a non-empty L1
// stop_summaries floor for the day AND (b) does NOT have a FRESH
// llm_compiled snapshot card. A compiled card is stale (and thus
// re-offered) when newer stop_summaries landed after it was compiled —
// see hasFreshLLMCompiledCard. This is the SAME freshness rule
// hydrateWhatWasDone applies for the dashboard's pending_compile count, so
// server and dashboard agree on what needs compiling (spec D3).
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
		if hasFreshLLMCompiledCard(ctx, db, p.ProjectPath, dayStr) {
			continue
		}
		floor, ferr := productivity.BuildFloorFromStopSummaries(ctx, db.Read(), p.ProjectPath, dayStr)
		if ferr != nil {
			log.Printf("compile: floor discovery for %s/%s: %v", p.ProjectPath, dayStr, ferr)
			continue
		}
		if len(floor) == 0 {
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

// llmCompiledCardAt reports whether the (project, day) snapshot carries
// any service card with llm_compiled=true — the "done" signal a prior
// compile leaves behind — and the time it was compiled
// (snapshot.UpdatedAt). compiledAt is 0 when no compiled card exists.
// Mirrors hydrateWhatWasDone's snapshot read.
func llmCompiledCardAt(ctx context.Context, db *store.DB, projectPath, dayStr string) (compiled bool, compiledAt int64) {
	snap, ok, err := store.GetDailyProductivitySnapshot(ctx, db, projectPath, dayStr)
	if err != nil || !ok || strings.TrimSpace(snap.PayloadJSON) == "" {
		return false, 0
	}
	var saved productivity.Report
	if err := json.Unmarshal([]byte(snap.PayloadJSON), &saved); err != nil {
		return false, 0
	}
	for i := range saved.Services {
		if saved.Services[i].WhatWasDone != nil && saved.Services[i].WhatWasDone.LLMCompiled {
			return true, snap.UpdatedAt
		}
	}
	return false, 0
}

// hasFreshLLMCompiledCard reports whether the (project, day) has a
// compiled card that is still current — i.e. no stop_summary has landed
// since it was compiled. A stale card (newer summaries exist) returns
// false so discovery re-includes the day and the dashboard re-offers
// Compile. On a transient read error it treats the card as fresh, so a
// flaky read never triggers a spurious recompile loop.
func hasFreshLLMCompiledCard(ctx context.Context, db *store.DB, projectPath, dayStr string) bool {
	compiled, compiledAt := llmCompiledCardAt(ctx, db, projectPath, dayStr)
	if !compiled {
		return false
	}
	latest, err := productivity.LatestFloorTurnTs(ctx, db.Read(), projectPath, dayStr)
	if err != nil {
		return true
	}
	return latest <= compiledAt
}
