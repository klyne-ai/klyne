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
// stop_summaries floor for the day AND (b) has no llm_compiled snapshot
// card yet. This is the SAME rule the dashboard's pending_compile count
// uses after D1 (what_was_done is floor-derived and non-llm_compiled),
// so server and dashboard agree on what needs compiling (spec D3).
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
		if hasLLMCompiledCard(ctx, db, p.ProjectPath, dayStr) {
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

// hasLLMCompiledCard reports whether the (project, day) snapshot already
// carries any service card with llm_compiled=true — the "done" signal a
// prior compile leaves behind. Mirrors hydrateWhatWasDone's snapshot read.
func hasLLMCompiledCard(ctx context.Context, db *store.DB, projectPath, dayStr string) bool {
	snap, ok, err := store.GetDailyProductivitySnapshot(ctx, db, projectPath, dayStr)
	if err != nil || !ok || strings.TrimSpace(snap.PayloadJSON) == "" {
		return false
	}
	var saved productivity.Report
	if err := json.Unmarshal([]byte(snap.PayloadJSON), &saved); err != nil {
		return false
	}
	for i := range saved.Services {
		if saved.Services[i].WhatWasDone != nil && saved.Services[i].WhatWasDone.LLMCompiled {
			return true
		}
	}
	return false
}
