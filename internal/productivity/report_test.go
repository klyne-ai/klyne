package productivity

import (
	"context"
	"strings"
	"testing"
	"time"
)

type fakeReflections struct {
	has  bool
	body string
}

func (f fakeReflections) HasReflection(_ context.Context, _ string, _ time.Time) (bool, string, error) {
	return f.has, f.body, nil
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

// TestBuildReport_MinutesByCLI checks Change 2 surfacing: RepoTime.ByCLI
// must reach the API as Service.MinutesByCLI (Service-level: time is
// repo-scoped, not branch-scoped) and roll up into Report.MinutesByCLI.
// TestBuildBranches_ShipSpan checks Change 3: a branch's ShipSpanMinutes
// is the minutes between its earliest and latest commit CommittedAt, and
// FirstCommitAt/LastCommitAt carry those anchors.
func TestBuildBranches_ShipSpan(t *testing.T) {
	base := time.Date(2026, 5, 19, 9, 0, 0, 0, time.UTC)
	sc := ScanResult{Repo: "svc", Dir: "/svc", Branch: "feat/x", Ship: ShipLocal}
	commits := []Commit{
		{SHA: "11111111", Subject: "first", CommittedAt: base, IsUser: true},
		{SHA: "22222222", Subject: "mid", CommittedAt: base.Add(45 * time.Minute), IsUser: true},
		{SHA: "33333333", Subject: "last", CommittedAt: base.Add(150 * time.Minute), IsUser: true},
	}
	branches := buildBranches(sc, commits, RepoTime{AIMinutes: 60})
	if len(branches) != 1 {
		t.Fatalf("len(branches) = %d; want 1", len(branches))
	}
	b := branches[0]
	if !b.FirstCommitAt.Equal(base) {
		t.Errorf("FirstCommitAt = %v; want %v", b.FirstCommitAt, base)
	}
	if !b.LastCommitAt.Equal(base.Add(150 * time.Minute)) {
		t.Errorf("LastCommitAt = %v; want %v", b.LastCommitAt, base.Add(150*time.Minute))
	}
	if b.ShipSpanMinutes != 150 {
		t.Errorf("ShipSpanMinutes = %d; want 150 (earliest→latest commit)", b.ShipSpanMinutes)
	}
}

// TestBuildBranches_ShipSpanSingleCommitIsZero checks Change 3 edge: a
// branch with fewer than 2 commits has ShipSpanMinutes 0 (no span).
func TestBuildBranches_ShipSpanSingleCommitIsZero(t *testing.T) {
	base := time.Date(2026, 5, 19, 9, 0, 0, 0, time.UTC)
	sc := ScanResult{Repo: "svc", Dir: "/svc", Branch: "feat/x", Ship: ShipLocal}
	commits := []Commit{{SHA: "11111111", Subject: "only", CommittedAt: base, IsUser: true}}
	b := buildBranches(sc, commits, RepoTime{})[0]
	if b.ShipSpanMinutes != 0 {
		t.Errorf("ShipSpanMinutes = %d; want 0 (<2 commits)", b.ShipSpanMinutes)
	}
	if !b.FirstCommitAt.Equal(base) || !b.LastCommitAt.Equal(base) {
		t.Errorf("single-commit anchors = %v/%v; want both %v", b.FirstCommitAt, b.LastCommitAt, base)
	}
}

// TestBuildReport_MinutesByCLI checks Change 2 surfacing: RepoTime.ByCLI
// must reach the API as Service.MinutesByCLI (Service-level: time is
// repo-scoped, not branch-scoped). Per-Service MinutesByCLI stays the
// per-repo union; Report.MinutesByCLI is the per-CLI GLOBAL union
// (Change 1) computed from ReportInput.Sessions, NOT the sum of
// per-Service values.
func TestBuildReport_MinutesByCLI(t *testing.T) {
	day := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	now := day.Add(16 * time.Hour)
	mk := func(startH, spanMin int) []time.Time {
		var out []time.Time
		for m := 0; m <= spanMin; m += 10 {
			out = append(out, day.Add(time.Duration(startH)*time.Hour+time.Duration(m)*time.Minute))
		}
		return out
	}
	// /r/a: claude session 09:00-10:00 (60m), codex session 09:00-09:30
	// (30m). /r/b: claude session 14:00-14:30 (30m).
	sessions := []SessionActivity{
		{SessionID: "a-claude", ProjectPath: "/r/a", CLI: "claude", MessageTimes: mk(9, 60)},
		{SessionID: "a-codex", ProjectPath: "/r/a", CLI: "codex", MessageTimes: mk(9, 30)},
		{SessionID: "b-claude", ProjectPath: "/r/b", CLI: "claude", MessageTimes: mk(14, 30)},
	}
	attrib := AttributeMinutes(sessions, map[string]int{"/r/a": 1, "/r/b": 1}, 30)
	in := ReportInput{
		Day: "2026-05-19",
		Now: now,
		Scans: []ScanResult{
			{
				Repo: "svc-a", Dir: "/r/a", Branch: "main", Ship: ShipLocal, Ahead: 1,
				Commits: []Commit{{SHA: "aaaaaaaa", Subject: "a work", CommittedAt: now, IsUser: true}},
			},
			{
				Repo: "svc-b", Dir: "/r/b", Branch: "main", Ship: ShipLocal, Ahead: 1,
				Commits: []Commit{{SHA: "bbbbbbbb", Subject: "b work", CommittedAt: now, IsUser: true}},
			},
		},
		Attribution:  attrib,
		ProjectPaths: map[string]string{"/r/a": "/r/a", "/r/b": "/r/b"},
		Sessions:     sessions,
	}
	rep, err := BuildReport(context.Background(), in, fakeReflections{has: false})
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	byPath := map[string]Service{}
	for _, s := range rep.Services {
		byPath[s.ProjectPath] = s
	}
	// /r/a per-repo union: claude 60m, codex 30m (each its own lane).
	a := byPath["/r/a"]
	if a.MinutesByCLI["claude"] != 60 || a.MinutesByCLI["codex"] != 30 {
		t.Errorf("/r/a MinutesByCLI = %v; want claude:60 codex:30", a.MinutesByCLI)
	}
	b := byPath["/r/b"]
	if b.MinutesByCLI["claude"] != 30 {
		t.Errorf("/r/b MinutesByCLI = %v; want claude:30", b.MinutesByCLI)
	}
	// Report-level per-CLI GLOBAL union: claude ran /r/a 09:00-10:00 +
	// /r/b 14:00-14:30 (disjoint) = 90m. codex ran /r/a 09:00-09:30 = 30m.
	if rep.MinutesByCLI["claude"] != 90 {
		t.Errorf("Report.MinutesByCLI[claude] = %d; want 90 (global union)", rep.MinutesByCLI["claude"])
	}
	if rep.MinutesByCLI["codex"] != 30 {
		t.Errorf("Report.MinutesByCLI[codex] = %d; want 30 (global union)", rep.MinutesByCLI["codex"])
	}
}

// TestBuildReport_TotalActiveMinutesGlobalUnion is the Change 1
// report-level regression guard: 3 sessions across 2 different repos
// with overlapping wall-clock must yield a report TotalActiveMinutes
// equal to the global union span, STRICTLY LESS than the sum of the
// per-repo (per-Service) unions. Report.MinutesByCLI must also be the
// per-CLI GLOBAL union, not the sum of per-Service MinutesByCLI.
func TestBuildReport_TotalActiveMinutesGlobalUnion(t *testing.T) {
	day := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	now := day.Add(13 * time.Hour)
	mk := func(startH, startMin, spanMin int) []time.Time {
		var out []time.Time
		base := day.Add(time.Duration(startH)*time.Hour + time.Duration(startMin)*time.Minute)
		for m := 0; m <= spanMin; m += 10 {
			out = append(out, base.Add(time.Duration(m)*time.Minute))
		}
		return out
	}
	// repo /a: one session 09:00-11:00 (120m, claude).
	// repo /b: two sessions, 10:00-12:00 (120m, codex) + 11:30-12:30 (60m, claude).
	// Per-repo unions: /a = 120, /b = 150 → sum-of-services = 270.
	// Global union = 09:00..12:30 = 210m, strictly < 270.
	sessions := []SessionActivity{
		{SessionID: "a1", ProjectPath: "/a", CLI: "claude", MessageTimes: mk(9, 0, 120)},
		{SessionID: "b1", ProjectPath: "/b", CLI: "codex", MessageTimes: mk(10, 0, 120)},
		{SessionID: "b2", ProjectPath: "/b", CLI: "claude", MessageTimes: mk(11, 30, 60)},
	}
	commitCounts := map[string]int{"/a": 1, "/b": 1}
	attrib := AttributeMinutes(sessions, commitCounts, 30)

	in := ReportInput{
		Day: "2026-05-19",
		Now: now,
		Scans: []ScanResult{
			{Repo: "a", Dir: "/a", Branch: "main", Ship: ShipLocal, Ahead: 1,
				Commits: []Commit{{SHA: "aaaaaaaa", Subject: "a work", CommittedAt: now, IsUser: true}}},
			{Repo: "b", Dir: "/b", Branch: "main", Ship: ShipLocal, Ahead: 1,
				Commits: []Commit{{SHA: "bbbbbbbb", Subject: "b work", CommittedAt: now, IsUser: true}}},
		},
		Attribution:  attrib,
		ProjectPaths: map[string]string{"/a": "/a", "/b": "/b"},
		Sessions:     sessions,
	}
	rep, err := BuildReport(context.Background(), in, fakeReflections{has: false})
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}

	if rep.TotalActiveMinutes != 210 {
		t.Errorf("Report.TotalActiveMinutes = %d; want 210 (global union 09:00..12:30)", rep.TotalActiveMinutes)
	}

	// Sum of per-Service unions must be STRICTLY GREATER than the headline.
	sumServices := 0
	for _, svc := range rep.Services {
		for _, b := range svc.Branches {
			sumServices += b.AttributedMinutes
		}
	}
	if sumServices <= rep.TotalActiveMinutes {
		t.Errorf("sum of per-Service AttributedMinutes = %d; want STRICTLY > headline %d",
			sumServices, rep.TotalActiveMinutes)
	}
	if sumServices != 270 {
		t.Errorf("sum of per-Service AttributedMinutes = %d; want 270 (/a 120 + /b 150)", sumServices)
	}

	// Report.MinutesByCLI is the per-CLI GLOBAL union, not the sum of
	// per-Service MinutesByCLI. claude ran a1 (09:00-11:00) + b2
	// (11:30-12:30) disjoint = 180m. codex ran b1 (10:00-12:00) = 120m.
	if rep.MinutesByCLI["claude"] != 180 {
		t.Errorf("Report.MinutesByCLI[claude] = %d; want 180 (global union)", rep.MinutesByCLI["claude"])
	}
	if rep.MinutesByCLI["codex"] != 120 {
		t.Errorf("Report.MinutesByCLI[codex] = %d; want 120 (global union)", rep.MinutesByCLI["codex"])
	}

	// Per-Service MinutesByCLI keeps the per-repo union (lanes that may
	// overlap) — /b's claude session b2 is its own 60m union.
	byPath := map[string]Service{}
	for _, s := range rep.Services {
		byPath[s.ProjectPath] = s
	}
	if byPath["/b"].MinutesByCLI["claude"] != 60 {
		t.Errorf("/b Service.MinutesByCLI[claude] = %d; want 60 (per-repo union preserved)",
			byPath["/b"].MinutesByCLI["claude"])
	}
}

// TestBuildReport_SessionsProofOfWork checks Change 2: every
// contributing session appears in Report.Sessions, sorted by StartedAt,
// each with its own gap-capped ActiveMinutes, message count, repo, and
// start/end anchors — the deterministic evidence behind the headline.
func TestBuildReport_SessionsProofOfWork(t *testing.T) {
	day := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	now := day.Add(15 * time.Hour)
	mk := func(startH, spanMin int) []time.Time {
		var out []time.Time
		for m := 0; m <= spanMin; m += 10 {
			out = append(out, day.Add(time.Duration(startH)*time.Hour+time.Duration(m)*time.Minute))
		}
		return out
	}
	// Deliberately out of start order so the sort is exercised.
	sessions := []SessionActivity{
		{SessionID: "later", ProjectPath: "/r/a", CLI: "codex", MessageTimes: mk(14, 30), MessageCount: 9},
		{SessionID: "earlier", ProjectPath: "/r/a", CLI: "claude", MessageTimes: mk(9, 60), MessageCount: 7},
	}
	in := ReportInput{
		Day: "2026-05-19",
		Now: now,
		Scans: []ScanResult{{
			Repo: "svc-a", Dir: "/r/a", Branch: "main", Ship: ShipLocal, Ahead: 1,
			Commits: []Commit{{SHA: "aaaaaaaa", Subject: "a work", CommittedAt: now, IsUser: true}},
		}},
		Attribution:  AttributeMinutes(sessions, map[string]int{"/r/a": 1}, 30),
		ProjectPaths: map[string]string{"/r/a": "/r/a"},
		Sessions:     sessions,
	}
	rep, err := BuildReport(context.Background(), in, fakeReflections{has: false})
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if len(rep.Sessions) != 2 {
		t.Fatalf("len(Sessions) = %d; want 2", len(rep.Sessions))
	}
	// Sorted by StartedAt: "earlier" (09:00) first.
	if rep.Sessions[0].SessionID != "earlier" {
		t.Errorf("Sessions[0] = %q; want earlier (sorted by started_at)", rep.Sessions[0].SessionID)
	}
	e := rep.Sessions[0]
	if e.ActiveMinutes != 60 {
		t.Errorf("earlier ActiveMinutes = %d; want 60", e.ActiveMinutes)
	}
	if e.MessageCount != 7 {
		t.Errorf("earlier MessageCount = %d; want 7", e.MessageCount)
	}
	if e.CLI != "claude" {
		t.Errorf("earlier CLI = %q; want claude", e.CLI)
	}
	if e.Repo == "" {
		t.Errorf("earlier Repo is empty; want a repo name derived from project_path")
	}
	if !e.StartedAt.Equal(day.Add(9*time.Hour)) {
		t.Errorf("earlier StartedAt = %v; want 09:00", e.StartedAt)
	}
	if !e.EndedAt.Equal(day.Add(10*time.Hour)) {
		t.Errorf("earlier EndedAt = %v; want 10:00", e.EndedAt)
	}
	l := rep.Sessions[1]
	if l.SessionID != "later" || l.ActiveMinutes != 30 {
		t.Errorf("later = %+v; want id=later ActiveMinutes=30", l)
	}
	// Headline TotalActiveMinutes = global union 09:00-10:00 + 14:00-14:30
	// (disjoint) = 90m, and the SessionStat active totals corroborate it.
	if rep.TotalActiveMinutes != 90 {
		t.Errorf("TotalActiveMinutes = %d; want 90", rep.TotalActiveMinutes)
	}
	// Each session's ActiveIntervals lengths must sum to its scalar
	// ActiveMinutes — the consistency contract the timeline relies on.
	for _, st := range rep.Sessions {
		if st.ActiveIntervals == nil {
			t.Errorf("%s ActiveIntervals is nil; want [] (never null)", st.SessionID)
		}
		var sum time.Duration
		for _, iv := range st.ActiveIntervals {
			sum += iv.End.Sub(iv.Start)
		}
		if int(sum.Minutes()) != st.ActiveMinutes {
			t.Errorf("%s ActiveIntervals sum = %v; want == ActiveMinutes %d",
				st.SessionID, sum.Minutes(), st.ActiveMinutes)
		}
	}
}

// TestBuildSessionStats_ActiveIntervalsSplitOnGap checks a session with
// an idle gap longer than the cap yields two ActiveIntervals — the gap
// is visible evidence, not silently bridged.
func TestBuildSessionStats_ActiveIntervalsSplitOnGap(t *testing.T) {
	base := time.Date(2026, 5, 19, 9, 0, 0, 0, time.UTC)
	// 0,5,10 then a 90-min gap then 100,105 → two active intervals.
	sessions := []SessionActivity{
		{SessionID: "gappy", ProjectPath: "/r/a", CLI: "claude", MessageTimes: ts(base, 0, 5, 10, 100, 105)},
	}
	stats := buildSessionStats(sessions, map[string]string{"/r/a": "/r/a"})
	if len(stats) != 1 {
		t.Fatalf("len(stats) = %d; want 1", len(stats))
	}
	if got := len(stats[0].ActiveIntervals); got != 2 {
		t.Errorf("ActiveIntervals len = %d; want 2 (gap > cap splits)", got)
	}
	var sum time.Duration
	for _, iv := range stats[0].ActiveIntervals {
		sum += iv.End.Sub(iv.Start)
	}
	if int(sum.Minutes()) != stats[0].ActiveMinutes {
		t.Errorf("ActiveIntervals sum = %v; want == ActiveMinutes %d", sum.Minutes(), stats[0].ActiveMinutes)
	}
}

// TestBuildReport_ReflectionMarkdownSurfaced checks Change 3: when a
// reflection exists, its body_md narrative is surfaced on the matching
// Service.ReflectionMarkdown and on Report.ReflectionMarkdown.
func TestBuildReport_ReflectionMarkdownSurfaced(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	body := "## What I did\n- shipped the lab pipeline\n"
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
	rep, err := BuildReport(context.Background(), in, fakeReflections{has: true, body: body})
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if rep.Services[0].ReflectionMarkdown != body {
		t.Errorf("Service.ReflectionMarkdown = %q; want %q", rep.Services[0].ReflectionMarkdown, body)
	}
	if rep.ReflectionMarkdown != body {
		t.Errorf("Report.ReflectionMarkdown = %q; want %q", rep.ReflectionMarkdown, body)
	}
	if rep.ReflectionStatus != "current" {
		t.Errorf("ReflectionStatus = %q; want current", rep.ReflectionStatus)
	}
}

// TestBuildReport_NoReflectionEmptyMarkdown checks Change 3 edge: with
// no reflection, ReflectionMarkdown is empty on both Service and Report.
func TestBuildReport_NoReflectionEmptyMarkdown(t *testing.T) {
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
	rep, err := BuildReport(context.Background(), in, fakeReflections{has: false})
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if rep.Services[0].ReflectionMarkdown != "" {
		t.Errorf("Service.ReflectionMarkdown = %q; want empty", rep.Services[0].ReflectionMarkdown)
	}
	if rep.ReflectionMarkdown != "" {
		t.Errorf("Report.ReflectionMarkdown = %q; want empty", rep.ReflectionMarkdown)
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
