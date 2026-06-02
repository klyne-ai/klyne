package handlers

import (
	"context"
	"testing"

	"github.com/klyne-ai/klyne/internal/productivity"
)

// TestAppendNonGitSummaryServices_SurfacesCodexScratchWork covers the
// root cause behind "I can't see my Codex work": DiscoverRepos drops any
// project_path that is not inside a git work tree, so work done in a
// non-git scratch dir (e.g. ~/Documents/Codex/<date>/...) is captured as
// stop_summaries but never assigned a Service, so no "what was done" card
// renders. appendNonGitSummaryServices surfaces such projects when they
// have an admissible L1 floor for the day.
func TestAppendNonGitSummaryServices_SurfacesCodexScratchWork(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/codex-scratch" // deliberately NOT a git work tree
	ts := int64(1_716_700_000_000)
	day := dayForTs(ts)
	seedStopSummary(t, db, proj, "s1", ts, "CLI-2001 implemented the coupon engine via Codex")

	got := appendNonGitSummaryServices(context.Background(), db, nil, day)

	var found *productivity.Service
	for i := range got {
		if got[i].ProjectPath == proj {
			found = &got[i]
		}
	}
	if found == nil {
		t.Fatalf("non-git project %q not surfaced as a service; services=%+v", proj, got)
	}
	if found.Repo != "codex-scratch" {
		t.Errorf("Repo = %q, want codex-scratch", found.Repo)
	}

	// The surfaced service must hydrate into a real floor-derived card.
	rep := productivity.Report{Services: got}
	hydrateWhatWasDone(context.Background(), db, &rep, day)
	if rep.Services[0].WhatWasDone == nil {
		t.Fatal("expected a floor-derived WhatWasDone card for the surfaced non-git service")
	}
}

// TestAppendNonGitSummaryServices_SkipsAlreadyPresent guards against
// double-counting a project that DiscoverRepos already surfaced as a git
// service.
func TestAppendNonGitSummaryServices_SkipsAlreadyPresent(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/already"
	ts := int64(1_716_700_000_000)
	day := dayForTs(ts)
	seedStopSummary(t, db, proj, "s1", ts, "CLI-3 work")

	existing := []productivity.Service{{ProjectPath: proj, Repo: "already"}}
	got := appendNonGitSummaryServices(context.Background(), db, existing, day)

	count := 0
	for _, s := range got {
		if s.ProjectPath == proj {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("project surfaced %d times, want exactly 1 (no duplicate of an existing service)", count)
	}
}

// TestAppendNonGitSummaryServices_SkipsProjectsWithoutFloor ensures the
// floor-admissible gate keeps parent-dir / observer noise out: a project
// whose only turn is a klyne tool-run (excluded by the floor) is NOT
// surfaced.
func TestAppendNonGitSummaryServices_SkipsProjectsWithoutFloor(t *testing.T) {
	t.Parallel()
	db := newReflectTestStore(t)
	const proj = "/proj/noise"
	ts := int64(1_716_700_000_000)
	day := dayForTs(ts)
	// A klyne tool-run turn is excluded by floorTurnAdmitted, so the
	// floor is empty and the project must not be surfaced.
	seedStopSummaryAs(t, db, proj, "s1", ts, "Ran /klyne:productivity-sync", "/klyne:productivity-sync project_path=/proj/noise day="+day)

	got := appendNonGitSummaryServices(context.Background(), db, nil, day)
	for _, s := range got {
		if s.ProjectPath == proj {
			t.Fatalf("project with no admissible floor was surfaced: %+v", s)
		}
	}
}
