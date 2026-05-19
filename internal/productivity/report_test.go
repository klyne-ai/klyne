package productivity

import (
	"context"
	"strings"
	"testing"
	"time"
)

type fakeReflections struct{ has bool }

func (f fakeReflections) HasReflection(_ context.Context, _ string, _ time.Time) (bool, error) {
	return f.has, nil
}

func TestBuildReport_RisksNarrativeAndReflectionStatus(t *testing.T) {
	now := time.Date(2026, 5, 19, 16, 0, 0, 0, time.UTC)
	day := "2026-05-19"

	// consultation-service: 3 commits, committed-local-only, unpushed
	// (the exact CLI-1396 blind spot the old worklog missed).
	scan := []ScanResult{{
		Repo:     "consultation-service",
		Dir:      "/repos/consultation-service",
		Branch:   "feat/CLI-1396-pipeline",
		TicketID: "CLI-1396",
		Ship:     ShipLocal,
		Ahead:    3,
		Behind:   0,
		Commits: []Commit{
			{SHA: "aaaaaaaa", Subject: "extractReportIdFromLink", CommittedAt: now.Add(-4 * time.Hour), IsUser: true, Files: 2, Insertions: 40},
			{SHA: "bbbbbbbb", Subject: "processOneLabStackReport", CommittedAt: now.Add(-3 * time.Hour), IsUser: true, Files: 1, Insertions: 12},
			{SHA: "cccccccc", Subject: "wire pipeline entrypoint", CommittedAt: now.Add(-2 * time.Hour), IsUser: true, Files: 3, Insertions: 60},
			// co-actor commit must be excluded from the user's figures.
			{SHA: "deadbeef", Subject: "Jenkins: bump build", CommittedAt: now.Add(-90 * time.Minute), IsUser: false},
		},
	}}

	attrib := map[string]RepoTime{
		"/repos/consultation-service": {AIMinutes: 130},
	}

	in := ReportInput{
		Day:         day,
		Now:         now,
		Scans:       scan,
		Attribution: attrib,
		ProjectPaths: map[string]string{
			"/repos/consultation-service": "/repos/consultation-service",
		},
		// repo had a recent session ending 20m ago with a dirty tree and
		// no commit after → done-uncommitted (D4).
		DirtyRepos: map[string]DirtyState{
			"/repos/consultation-service": {DirtyFileCount: 2, SessionEnd: now.Add(-20 * time.Minute)},
		},
	}

	rep, err := BuildReport(context.Background(), in, fakeReflections{has: false})
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if rep.Day != day {
		t.Errorf("Day = %q; want %q", rep.Day, day)
	}
	if len(rep.Services) != 1 {
		t.Fatalf("len(Services) = %d; want 1", len(rep.Services))
	}
	svc := rep.Services[0]
	if len(svc.Branches) != 1 {
		t.Fatalf("len(Branches) = %d; want 1", len(svc.Branches))
	}
	b := svc.Branches[0]

	if b.Ship != ShipLocal {
		t.Errorf("Ship = %q; want %q", b.Ship, ShipLocal)
	}
	// Co-actor commit excluded from the user's commit list.
	if len(b.Commits) != 3 {
		t.Errorf("user commits = %d; want 3 (Jenkins excluded)", len(b.Commits))
	}
	if b.AttributedMinutes != 130 {
		t.Errorf("AttributedMinutes = %d; want 130", b.AttributedMinutes)
	}

	// Unpushed risk present with an age.
	var unpushed, doneUncommitted bool
	for _, rs := range svc.Risks {
		if rs.Kind == "unpushed" {
			unpushed = true
			if rs.AgeMinutes <= 0 {
				t.Errorf("unpushed AgeMinutes = %d; want > 0", rs.AgeMinutes)
			}
		}
		if rs.Kind == "done-uncommitted" {
			doneUncommitted = true
		}
	}
	if !unpushed {
		t.Errorf("missing unpushed risk signal; risks=%+v", svc.Risks)
	}
	if !doneUncommitted {
		t.Errorf("missing done-uncommitted risk signal; risks=%+v", svc.Risks)
	}

	// L1 narrative: non-empty, has short SHA, has the minutes, NO "PR #".
	if strings.TrimSpace(b.Narrative) == "" {
		t.Fatalf("Narrative is empty")
	}
	if !strings.Contains(b.Narrative, "aaaaaaaa") {
		t.Errorf("Narrative missing short sha; got %q", b.Narrative)
	}
	if !strings.Contains(b.Narrative, "130") {
		t.Errorf("Narrative missing attributed minutes; got %q", b.Narrative)
	}
	if strings.Contains(b.Narrative, "PR #") {
		t.Errorf("Narrative contains forbidden 'PR #' (hallucination guard); got %q", b.Narrative)
	}

	// No reflection row → status missing + nudge.
	if rep.ReflectionStatus != "missing" {
		t.Errorf("ReflectionStatus = %q; want missing", rep.ReflectionStatus)
	}
	if strings.TrimSpace(rep.Nudge) == "" {
		t.Errorf("Nudge empty when no reflection present")
	}
}

func TestBuildReport_ReflectionPresentIsCurrent(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	in := ReportInput{
		Day: "2026-05-19",
		Now: now,
		Scans: []ScanResult{{
			Repo: "oms-service", Dir: "/r/oms", Branch: "main", Ship: ShipMerged,
			Commits: []Commit{{SHA: "12345678", Subject: "merge work", CommittedAt: now, IsUser: true}},
		}},
		Attribution:  map[string]RepoTime{"/r/oms": {AIMinutes: 30}},
		ProjectPaths: map[string]string{"/r/oms": "/r/oms"},
	}
	rep, err := BuildReport(context.Background(), in, fakeReflections{has: true})
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if rep.ReflectionStatus != "current" {
		t.Errorf("ReflectionStatus = %q; want current", rep.ReflectionStatus)
	}
}

func TestBuildReport_WorktreesCollapseToOneServicePerCanonicalRepo(t *testing.T) {
	// Two scans for the SAME canonical repo (main tree + a sibling
	// worktree on a feature branch). The report must contain exactly ONE
	// Service for that repo, with BOTH branches under it, and per-branch
	// attribution preserved (keyed by ScanResult.Dir).
	now := time.Date(2026, 5, 19, 16, 0, 0, 0, time.UTC)
	scans := []ScanResult{
		{
			Repo: "klyne", Dir: "/repos/klyne", Branch: "init", Ship: ShipLocal, Ahead: 1,
			Commits: []Commit{
				{SHA: "aaaaaaaa", Subject: "main tree work", CommittedAt: now.Add(-2 * time.Hour), IsUser: true, Insertions: 10},
			},
		},
		{
			Repo: "productivity-dashboard", Dir: "/repos/klyne/.worktrees/productivity-dashboard",
			Branch: "feat/productivity-dashboard", TicketID: "", Ship: ShipLocal, Ahead: 5,
			Commits: []Commit{
				{SHA: "bbbbbbbb", Subject: "dashboard substrate", CommittedAt: now.Add(-1 * time.Hour), IsUser: true, Insertions: 200},
				{SHA: "cccccccc", Subject: "dashboard handler", CommittedAt: now.Add(-30 * time.Minute), IsUser: true, Insertions: 80},
			},
		},
	}
	in := ReportInput{
		Day:   "2026-05-19",
		Now:   now,
		Scans: scans,
		Attribution: map[string]RepoTime{
			"/repos/klyne":                                  {AIMinutes: 15},
			"/repos/klyne/.worktrees/productivity-dashboard": {AIMinutes: 240},
		},
		// Both scans canonicalize to the SAME project path → one Service.
		ProjectPaths: map[string]string{
			"/repos/klyne":                                  "/repos/klyne",
			"/repos/klyne/.worktrees/productivity-dashboard": "/repos/klyne",
		},
	}
	rep, err := BuildReport(context.Background(), in, fakeReflections{has: false})
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if len(rep.Services) != 1 {
		t.Fatalf("len(Services) = %d; want 1 (worktrees collapse to one canonical repo)", len(rep.Services))
	}
	svc := rep.Services[0]
	if svc.ProjectPath != "/repos/klyne" {
		t.Errorf("Service.ProjectPath = %q; want /repos/klyne", svc.ProjectPath)
	}
	if len(svc.Branches) != 2 {
		t.Fatalf("len(Branches) = %d; want 2 (init + feat/productivity-dashboard)", len(svc.Branches))
	}
	byName := map[string]Branch{}
	for _, b := range svc.Branches {
		byName[b.Name] = b
	}
	mt, ok := byName["init"]
	if !ok {
		t.Fatalf("missing 'init' branch; got %v", byName)
	}
	if mt.AttributedMinutes != 15 {
		t.Errorf("init AttributedMinutes = %d; want 15 (per-Dir attribution preserved)", mt.AttributedMinutes)
	}
	pd, ok := byName["feat/productivity-dashboard"]
	if !ok {
		t.Fatalf("missing 'feat/productivity-dashboard' branch; got %v", byName)
	}
	if pd.AttributedMinutes != 240 {
		t.Errorf("feat branch AttributedMinutes = %d; want 240 (per-Dir attribution preserved)", pd.AttributedMinutes)
	}
	if pd.Ahead != 5 {
		t.Errorf("feat branch Ahead = %d; want 5 (per-worktree ship facts preserved)", pd.Ahead)
	}
}

func TestBuildReport_SalienceLeadsWithHighestCommitBranch(t *testing.T) {
	now := time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC)
	in := ReportInput{
		Day: "2026-05-19",
		Now: now,
		Scans: []ScanResult{{
			Repo: "svc", Dir: "/svc", Branch: "main", Ship: ShipLocal, Ahead: 4,
			Commits: []Commit{
				{SHA: "11111111", Subject: "tiny docs tweak", BranchName: "docs/cleanup", CommittedAt: now, IsUser: true},
				{SHA: "22222222", Subject: "core pipeline a", BranchName: "feat/CLI-1", CommittedAt: now, IsUser: true},
				{SHA: "33333333", Subject: "core pipeline b", BranchName: "feat/CLI-1", CommittedAt: now, IsUser: true},
				{SHA: "44444444", Subject: "core pipeline c", BranchName: "feat/CLI-1", CommittedAt: now, IsUser: true},
			},
		}},
		Attribution:  map[string]RepoTime{"/svc": {AIMinutes: 90}},
		ProjectPaths: map[string]string{"/svc": "/svc"},
	}
	rep, err := BuildReport(context.Background(), in, fakeReflections{has: false})
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if len(rep.Services[0].Branches) < 2 {
		t.Fatalf("expected per-commit branch grouping; got %+v", rep.Services[0].Branches)
	}
	// Salience rule (§7.1 rule 5): the 3-commit feature branch must lead
	// the 1-commit docs branch.
	if rep.Services[0].Branches[0].Name != "feat/CLI-1" {
		t.Errorf("salience: leading branch = %q; want feat/CLI-1 (highest commit count)", rep.Services[0].Branches[0].Name)
	}
}
