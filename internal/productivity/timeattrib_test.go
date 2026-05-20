package productivity

import (
	"testing"
	"time"
)

func ts(base time.Time, mins ...int) []time.Time {
	out := make([]time.Time, len(mins))
	for i, m := range mins {
		out[i] = base.Add(time.Duration(m) * time.Minute)
	}
	return out
}

func TestAttributeMinutes_IdleCapExcludesGaps(t *testing.T) {
	base := time.Date(2026, 5, 19, 9, 0, 0, 0, time.UTC)
	// Messages at 0,5,10 min, then a 90-min gap, then 100,105.
	// With a 30-min idle cap: active = (10-0) + (105-100) = 15 min.
	sess := []SessionActivity{{
		SessionID:    "s1",
		ProjectPath:  "/repo/a",
		MessageTimes: ts(base, 0, 5, 10, 100, 105),
	}}
	res := AttributeMinutes(sess, map[string]int{"/repo/a": 3}, 30)
	if got := res["/repo/a"].AIMinutes; got != 15 {
		t.Errorf("AIMinutes = %d; want 15 (gap > cap excluded)", got)
	}
	if res["/repo/a"].ManualOnly {
		t.Errorf("ManualOnly = true; want false (session present)")
	}
}

func TestAttributeMinutes_MultiRepoSplitByCommitCount(t *testing.T) {
	base := time.Date(2026, 5, 19, 14, 0, 0, 0, time.UTC)
	// One session, 60 contiguous active minutes (msgs every 10m, under cap).
	// Window has commits in 2 repos: 3 in /a, 1 in /b → split 45 / 15.
	sess := []SessionActivity{{
		SessionID:    "s2",
		ProjectPath:  "/multi",
		MessageTimes: ts(base, 0, 10, 20, 30, 40, 50, 60),
		Repos:        []string{"/a", "/b"},
	}}
	res := AttributeMinutes(sess, map[string]int{"/a": 3, "/b": 1}, 30)
	if res["/a"].AIMinutes != 45 {
		t.Errorf("/a AIMinutes = %d; want 45 (3/4 of 60)", res["/a"].AIMinutes)
	}
	if res["/b"].AIMinutes != 15 {
		t.Errorf("/b AIMinutes = %d; want 15 (1/4 of 60)", res["/b"].AIMinutes)
	}
	if !res["/a"].Split || !res["/b"].Split {
		t.Errorf("Split flag not set on multi-repo attribution")
	}
}

func TestAttributeMinutes_ManualOnlyWhenNoSession(t *testing.T) {
	// Repo /manual has in-window commits but NO session active-time (D4:
	// manual time is never folded into AI time).
	res := AttributeMinutes(nil, map[string]int{"/manual": 2}, 30)
	r := res["/manual"]
	if r.AIMinutes != 0 {
		t.Errorf("AIMinutes = %d; want 0 (no session)", r.AIMinutes)
	}
	if !r.ManualOnly {
		t.Errorf("ManualOnly = false; want true (commits but no AI session)")
	}
}

// span returns an interval whose endpoints are base+startMin / base+endMin.
func span(base time.Time, startMin, endMin int) interval {
	return interval{
		start: base.Add(time.Duration(startMin) * time.Minute),
		end:   base.Add(time.Duration(endMin) * time.Minute),
	}
}

func TestMergeIntervals(t *testing.T) {
	base := time.Date(2026, 5, 19, 9, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		in   []interval
		want []interval
	}{
		{
			name: "empty",
			in:   nil,
			want: nil,
		},
		{
			name: "disjoint stays separate",
			in:   []interval{span(base, 0, 60), span(base, 120, 180)},
			want: []interval{span(base, 0, 60), span(base, 120, 180)},
		},
		{
			name: "overlapping merges",
			in:   []interval{span(base, 0, 120), span(base, 60, 180)},
			want: []interval{span(base, 0, 180)},
		},
		{
			name: "touching merges (end == next start)",
			in:   []interval{span(base, 0, 60), span(base, 60, 120)},
			want: []interval{span(base, 0, 120)},
		},
		{
			name: "fully contained absorbed",
			in:   []interval{span(base, 0, 240), span(base, 60, 120)},
			want: []interval{span(base, 0, 240)},
		},
		{
			name: "identical collapse to one",
			in:   []interval{span(base, 30, 90), span(base, 30, 90)},
			want: []interval{span(base, 30, 90)},
		},
		{
			name: "unsorted input is sorted first",
			in:   []interval{span(base, 120, 180), span(base, 0, 60), span(base, 30, 90)},
			want: []interval{span(base, 0, 90), span(base, 120, 180)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeIntervals(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("mergeIntervals len = %d, want %d (%+v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if !got[i].start.Equal(tc.want[i].start) || !got[i].end.Equal(tc.want[i].end) {
					t.Errorf("interval[%d] = %v..%v; want %v..%v",
						i, got[i].start, got[i].end, tc.want[i].start, tc.want[i].end)
				}
			}
		})
	}
}

// TestAttributeMinutes_OverlappingSessionsUnionNotSum is the core
// regression guard for Change 1: two sessions in the SAME project whose
// wall-clock active intervals overlap (09:00-11:00 and 10:00-12:00) must
// yield the UNION length (3h = 180m), NOT the sum (4h = 240m). Summing
// double-counts the 10:00-11:00 overlap and produced the implausible
// "Total AI time 20h 6m" on a single day.
func TestAttributeMinutes_OverlappingSessionsUnionNotSum(t *testing.T) {
	day := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	at := func(h, m int) time.Time {
		return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
	}
	// Session A: messages every 10m from 09:00 to 11:00 → one 120m interval.
	var aTimes []time.Time
	for m := 0; m <= 120; m += 10 {
		aTimes = append(aTimes, at(9, 0).Add(time.Duration(m)*time.Minute))
	}
	// Session B: messages every 10m from 10:00 to 12:00 → one 120m interval.
	var bTimes []time.Time
	for m := 0; m <= 120; m += 10 {
		bTimes = append(bTimes, at(10, 0).Add(time.Duration(m)*time.Minute))
	}
	sessions := []SessionActivity{
		{SessionID: "a", ProjectPath: "/repo/x", MessageTimes: aTimes},
		{SessionID: "b", ProjectPath: "/repo/x", MessageTimes: bTimes},
	}
	res := AttributeMinutes(sessions, map[string]int{"/repo/x": 2}, 30)
	if got := res["/repo/x"].AIMinutes; got != 180 {
		t.Errorf("AIMinutes = %d; want 180 (union of 09:00-11:00 and 10:00-12:00, NOT 240 sum)", got)
	}
}

// TestAttributeMinutes_ByCLISplit checks Change 2: a repo worked by both
// claude and codex breaks down per CLI. The two CLIs ran in overlapping
// wall-clock windows (claude 09:00-11:00, codex 10:00-12:00), so:
//   - AIMinutes is the all-CLI UNION → 180m.
//   - ByCLI["claude"] and ByCLI["codex"] are each their own union → 120m.
//   - claude + codex (240m) intentionally exceeds AIMinutes (180m) when
//     both CLIs ran at once — that is correct and expected.
func TestAttributeMinutes_ByCLISplit(t *testing.T) {
	day := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	mk := func(startH, spanMin int) []time.Time {
		var out []time.Time
		for m := 0; m <= spanMin; m += 10 {
			out = append(out, day.Add(time.Duration(startH)*time.Hour+time.Duration(m)*time.Minute))
		}
		return out
	}
	sessions := []SessionActivity{
		{SessionID: "a", ProjectPath: "/repo/x", CLI: "claude", MessageTimes: mk(9, 120)},
		{SessionID: "b", ProjectPath: "/repo/x", CLI: "codex", MessageTimes: mk(10, 120)},
	}
	res := AttributeMinutes(sessions, map[string]int{"/repo/x": 2}, 30)
	rt := res["/repo/x"]
	if rt.AIMinutes != 180 {
		t.Errorf("AIMinutes = %d; want 180 (all-CLI union)", rt.AIMinutes)
	}
	if rt.ByCLI["claude"] != 120 {
		t.Errorf("ByCLI[claude] = %d; want 120", rt.ByCLI["claude"])
	}
	if rt.ByCLI["codex"] != 120 {
		t.Errorf("ByCLI[codex] = %d; want 120", rt.ByCLI["codex"])
	}
}

// TestGlobalActiveMinutes_UnionAcrossRepos is the core regression guard
// for Change 1: three sessions across TWO different repos whose
// wall-clock active intervals overlap must yield a global union span
// STRICTLY LESS than the sum of the per-repo unions. The user ran
// parallel AI agents across repos, so the repo lanes legitimately
// overlap — the headline number must be the all-repo wall-clock union.
func TestGlobalActiveMinutes_UnionAcrossRepos(t *testing.T) {
	day := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	mk := func(startH, startMin, spanMin int) []time.Time {
		var out []time.Time
		base := day.Add(time.Duration(startH)*time.Hour + time.Duration(startMin)*time.Minute)
		for m := 0; m <= spanMin; m += 10 {
			out = append(out, base.Add(time.Duration(m)*time.Minute))
		}
		return out
	}
	// repo /a: session worked 09:00-11:00 (120m).
	// repo /b: two sessions, 10:00-12:00 (120m) and 11:30-12:30 (60m).
	// Per-repo unions: /a = 120m, /b = union(10:00-12:00, 11:30-12:30) = 150m.
	// Sum of per-repo unions = 270m.
	// Global union = 09:00 .. 12:30 = 210m, strictly < 270m.
	sessions := []SessionActivity{
		{SessionID: "a1", ProjectPath: "/a", CLI: "claude", MessageTimes: mk(9, 0, 120)},
		{SessionID: "b1", ProjectPath: "/b", CLI: "codex", MessageTimes: mk(10, 0, 120)},
		{SessionID: "b2", ProjectPath: "/b", CLI: "claude", MessageTimes: mk(11, 30, 60)},
	}

	total, byCLI := GlobalActiveMinutes(sessions, 30)
	if total != 210 {
		t.Errorf("GlobalActiveMinutes total = %d; want 210 (09:00..12:30 union)", total)
	}

	// Sum of per-repo unions must be strictly greater than the global union.
	repo := AttributeMinutes(sessions, map[string]int{"/a": 1, "/b": 2}, 30)
	sumOfRepos := repo["/a"].AIMinutes + repo["/b"].AIMinutes
	if sumOfRepos <= total {
		t.Errorf("sum-of-per-repo-unions = %d; want STRICTLY > global union %d", sumOfRepos, total)
	}
	if sumOfRepos != 270 {
		t.Errorf("sum-of-per-repo-unions = %d; want 270 (/a 120 + /b 150)", sumOfRepos)
	}

	// Per-CLI global union: claude ran a1 (09:00-11:00) and b2
	// (11:30-12:30) → union = 120 + 60 = 180m (disjoint). codex ran b1
	// (10:00-12:00) → 120m.
	if byCLI["claude"] != 180 {
		t.Errorf("GlobalActiveMinutes byCLI[claude] = %d; want 180", byCLI["claude"])
	}
	if byCLI["codex"] != 120 {
		t.Errorf("GlobalActiveMinutes byCLI[codex] = %d; want 120", byCLI["codex"])
	}
}

// TestSessionActiveMinutes checks the per-session gap-capped active total
// used for the SessionStat proof-of-work breakdown (Change 2).
func TestSessionActiveMinutes(t *testing.T) {
	base := time.Date(2026, 5, 19, 9, 0, 0, 0, time.UTC)
	// 0,5,10 then 90-min gap then 100,105 → active = 10 + 5 = 15m.
	got := SessionActiveMinutes(ts(base, 0, 5, 10, 100, 105), 30)
	if got != 15 {
		t.Errorf("SessionActiveMinutes = %d; want 15 (gap > cap excluded)", got)
	}
	if SessionActiveMinutes(ts(base, 0), 30) != 0 {
		t.Errorf("SessionActiveMinutes single message; want 0")
	}
}

// TestAttributeMinutes_DisjointSessionsSumNormally confirms the union
// fix does not under-count: two non-overlapping sessions still total the
// sum of their individual spans (60m + 60m = 120m).
func TestAttributeMinutes_DisjointSessionsSumNormally(t *testing.T) {
	day := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	mk := func(startH int) []time.Time {
		var out []time.Time
		for m := 0; m <= 60; m += 10 {
			out = append(out, day.Add(time.Duration(startH)*time.Hour+time.Duration(m)*time.Minute))
		}
		return out
	}
	sessions := []SessionActivity{
		{SessionID: "a", ProjectPath: "/repo/y", MessageTimes: mk(9)},
		{SessionID: "b", ProjectPath: "/repo/y", MessageTimes: mk(14)},
	}
	res := AttributeMinutes(sessions, map[string]int{"/repo/y": 2}, 30)
	if got := res["/repo/y"].AIMinutes; got != 120 {
		t.Errorf("AIMinutes = %d; want 120 (disjoint 60m + 60m)", got)
	}
}
