package productivity

import (
	"testing"
	"time"
)

// TestUnionMinutes_TableTest covers the sweep-line behaviour the
// aggregator's headline TotalActiveMinutes / MinutesByCLI rely on.
// Each row asserts the union-not-sum semantics (overlapping intervals
// collapse, touching intervals merge, disjoint intervals add).
func TestUnionMinutes_TableTest(t *testing.T) {
	base := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
	mk := func(startMin, endMin int) ActiveInterval {
		return ActiveInterval{
			Start: base.Add(time.Duration(startMin) * time.Minute),
			End:   base.Add(time.Duration(endMin) * time.Minute),
		}
	}
	cases := []struct {
		name string
		ivs  []ActiveInterval
		want int
	}{
		{"empty", nil, 0},
		{"single 30m", []ActiveInterval{mk(0, 30)}, 30},
		{"disjoint sum", []ActiveInterval{mk(0, 10), mk(20, 30)}, 20},
		{"overlapping collapse", []ActiveInterval{mk(0, 30), mk(10, 40)}, 40},
		{"touching merge", []ActiveInterval{mk(0, 30), mk(30, 60)}, 60},
		{"unsorted input", []ActiveInterval{mk(40, 50), mk(0, 10), mk(20, 25)}, 25},
		{"nested interval", []ActiveInterval{mk(0, 60), mk(10, 20)}, 60},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := UnionMinutes(c.ivs)
			if got != c.want {
				t.Fatalf("UnionMinutes(%s) = %d, want %d", c.name, got, c.want)
			}
		})
	}
}

// TestAggregateReports_Empty — empty input must yield a "missing" status
// and a non-nil zero-shaped Report (the wire contract expects [] not null).
func TestAggregateReports_Empty(t *testing.T) {
	got := AggregateReports(nil, "2026-05-24")
	if got.ReflectionStatus != "missing" {
		t.Fatalf("empty input: want status=missing, got %q", got.ReflectionStatus)
	}
	if got.Services == nil || got.Sessions == nil || got.MinutesByCLI == nil {
		t.Fatalf("empty input: nil slices/maps would break JSON []/{} contract: %+v", got)
	}
	if got.Day != "2026-05-24" {
		t.Fatalf("Day not threaded: %q", got.Day)
	}
}

// TestAggregateReports_MidnightSpan guards the release-blocker the
// review caught: a session that spans midnight was being last-wins
// deduped, dropping the earlier day's intervals and under-counting the
// headline. The fix is to merge per-SessionID across days; this test
// asserts the merged session's interval lengths sum back to the cross-
// midnight wall-clock.
func TestAggregateReports_MidnightSpan(t *testing.T) {
	// Session ran 23:30 → 00:30 (1 hour total, split across two days).
	day1 := time.Date(2026, 5, 24, 23, 30, 0, 0, time.UTC)
	day1End := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)
	day2Start := day1End
	day2End := time.Date(2026, 5, 25, 0, 30, 0, 0, time.UTC)

	rep1 := Report{
		Day:          "2026-05-24",
		Services:     []Service{},
		MinutesByCLI: map[string]int{"claude": 30},
		Sessions: []SessionStat{{
			SessionID: "s1", CLI: "claude", Repo: "r",
			StartedAt: day1, EndedAt: day1End,
			ActiveIntervals: []ActiveInterval{{Start: day1, End: day1End}},
			ActiveMinutes:   30, MessageCount: 5,
		}},
		TotalActiveMinutes: 30,
	}
	rep2 := Report{
		Day:          "2026-05-25",
		Services:     []Service{},
		MinutesByCLI: map[string]int{"claude": 30},
		Sessions: []SessionStat{{
			SessionID: "s1", CLI: "claude", Repo: "r",
			StartedAt: day2Start, EndedAt: day2End,
			ActiveIntervals: []ActiveInterval{{Start: day2Start, End: day2End}},
			ActiveMinutes:   30, MessageCount: 3,
		}},
		TotalActiveMinutes: 30,
	}

	got := AggregateReports([]Report{rep1, rep2}, "2026-05-24")

	if len(got.Sessions) != 1 {
		t.Fatalf("midnight-spanning session should merge to one row, got %d", len(got.Sessions))
	}
	merged := got.Sessions[0]
	if merged.ActiveMinutes != 60 {
		t.Fatalf("merged ActiveMinutes = %d, want 60 (full hour across midnight)", merged.ActiveMinutes)
	}
	if merged.MessageCount != 8 {
		t.Fatalf("merged MessageCount = %d, want 8 (5+3)", merged.MessageCount)
	}
	if !merged.StartedAt.Equal(day1) || !merged.EndedAt.Equal(day2End) {
		t.Fatalf("merged envelope wrong: %v..%v", merged.StartedAt, merged.EndedAt)
	}
	if got.TotalActiveMinutes != 60 {
		t.Fatalf("composite TotalActiveMinutes = %d, want 60", got.TotalActiveMinutes)
	}
}

// TestAggregateReports_ReflectionStatusRollup — a window where some
// days have reflections and others don't is "stale," not "current".
func TestAggregateReports_ReflectionStatusRollup(t *testing.T) {
	withRefl := Report{Day: "2026-05-24",
		Services:         []Service{{Repo: "r", ProjectPath: "/r", ReflectionGroups: []ReflectionGroup{{ID: "x", BodyMD: "did stuff"}}}},
		ReflectionStatus: "current",
	}
	noRefl := Report{Day: "2026-05-25", Services: []Service{{Repo: "r", ProjectPath: "/r"}}}
	allRefl := Report{Day: "2026-05-26",
		Services:         []Service{{Repo: "r", ProjectPath: "/r", ReflectionGroups: []ReflectionGroup{{ID: "y", BodyMD: "more"}}}},
		ReflectionStatus: "current",
	}

	if got := AggregateReports([]Report{withRefl, allRefl}, "x"); got.ReflectionStatus != "current" {
		t.Fatalf("all-with-reflection should be current, got %q", got.ReflectionStatus)
	}
	if got := AggregateReports([]Report{withRefl, noRefl}, "x"); got.ReflectionStatus != "stale" {
		t.Fatalf("mixed should be stale, got %q", got.ReflectionStatus)
	}
	if got := AggregateReports([]Report{noRefl}, "x"); got.ReflectionStatus != "missing" {
		t.Fatalf("none should be missing, got %q", got.ReflectionStatus)
	}
}

// TestAggregateReports_ServiceMerge — branches union by name, risks
// dedup by (kind, branch, worktree), MergedPRs dedup by Number.
func TestAggregateReports_ServiceMerge(t *testing.T) {
	day1 := Report{Day: "2026-05-24",
		Services: []Service{{
			Repo: "r", ProjectPath: "/r",
			Branches:  []Branch{{Name: "main", Ahead: 0}, {Name: "feature/x", Ahead: 3}},
			Risks:     []RiskSignal{{Kind: "unpushed", Branch: "feature/x", WorktreePath: "/r"}},
			MergedPRs: []MergedPR{{Number: 1, Title: "First"}},
		}},
	}
	day2 := Report{Day: "2026-05-25",
		Services: []Service{{
			Repo: "r", ProjectPath: "/r",
			Branches: []Branch{{Name: "main", Ahead: 0}, {Name: "feature/y", Ahead: 1}},
			// Same risk key as day1 — should NOT duplicate.
			Risks:     []RiskSignal{{Kind: "unpushed", Branch: "feature/x", WorktreePath: "/r"}},
			MergedPRs: []MergedPR{{Number: 1, Title: "First"}, {Number: 2, Title: "Second"}},
		}},
	}
	got := AggregateReports([]Report{day1, day2}, "x")
	if len(got.Services) != 1 {
		t.Fatalf("same ProjectPath should collapse to one service, got %d", len(got.Services))
	}
	svc := got.Services[0]
	branchNames := map[string]bool{}
	for _, b := range svc.Branches {
		branchNames[b.Name] = true
	}
	if !branchNames["main"] || !branchNames["feature/x"] || !branchNames["feature/y"] {
		t.Fatalf("branch union incomplete: %+v", branchNames)
	}
	if len(svc.Risks) != 1 {
		t.Fatalf("risks should dedupe to 1, got %d", len(svc.Risks))
	}
	prs := map[int]bool{}
	for _, pr := range svc.MergedPRs {
		prs[pr.Number] = true
	}
	if !prs[1] || !prs[2] || len(prs) != 2 {
		t.Fatalf("PR dedupe-by-Number broken: %+v", prs)
	}
}
