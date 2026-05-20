package productivity

import (
	"sort"
	"time"
)

// idleCapMinutes is the §6.4 idle-gap cap: an intra-session gap longer
// than this closes the current active interval. 30 min matches the
// substrate's documented attribution contract; callers pass it through
// AttributeMinutes so it stays configurable, but this is the canonical
// default and the single named source of the value.
const idleCapMinutes = 30

// SessionActivity is the time-attribution input for one klyne session:
// the in-window message timestamps (active-time is computed from these,
// not from naive commit deltas — D1) and the repo(s) the session
// touched. ProjectPath is the canonical project the session ran in;
// Repos, when it spans more than one repo in-window, drives the
// proportional multi-repo split (§6.4). CLI is the producing connector
// ("claude" | "codex") so attribution can be broken down per CLI.
type SessionActivity struct {
	SessionID    string
	ProjectPath  string
	CLI          string
	MessageTimes []time.Time
	Repos        []string // empty → attributed wholly to ProjectPath
}

// RepoTime is the deterministic per-repo time verdict (D1/D4).
// ManualOnly is true when the repo had in-window commits but ZERO AI
// session active-time — manual time is reported separately and never
// folded into AI time (D4). Split flags an approximate figure produced
// by the multi-repo proportional split (§6.4: "≈, split A/B").
//
// AIMinutes is the UNION of the repo's active wall-clock intervals
// across ALL its sessions (overlapping/parallel sessions are merged, not
// summed — see mergeIntervals). ByCLI breaks the same union down per CLI
// ("claude" → merged minutes for claude within the repo). Per-CLI values
// are themselves union totals, so claude+codex may sum to slightly more
// than AIMinutes when both CLIs ran at once — that is correct.
type RepoTime struct {
	AIMinutes  int
	ByCLI      map[string]int
	ManualOnly bool
	Split      bool
}

// interval is a half-open-ish active wall-clock span [start, end].
// Touching intervals (one's end == the next's start) are treated as
// continuous and merged.
type interval struct {
	start time.Time
	end   time.Time
}

// mergeIntervals returns the minimal set of non-overlapping intervals
// covering the same wall-clock time as the input: it sorts by start then
// folds, extending the running interval whenever the next one overlaps
// or touches it. This is the core of the union fix — summing per-session
// active-time double-counts wall-clock minutes when sessions run in
// parallel; merging then summing the merged lengths does not. The input
// slice is not mutated (a copy is sorted).
func mergeIntervals(in []interval) []interval {
	if len(in) == 0 {
		return nil
	}
	sorted := make([]interval, len(in))
	copy(sorted, in)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].start.Before(sorted[j].start)
	})

	merged := []interval{sorted[0]}
	for _, iv := range sorted[1:] {
		last := &merged[len(merged)-1]
		// Overlap OR touch: next starts at or before the running end.
		if !iv.start.After(last.end) {
			if iv.end.After(last.end) {
				last.end = iv.end
			}
			continue
		}
		merged = append(merged, iv)
	}
	return merged
}

// totalMinutes sums the lengths of a set of (assumed already merged)
// intervals, in whole minutes.
func totalMinutes(ivs []interval) int {
	var total time.Duration
	for _, iv := range ivs {
		if d := iv.end.Sub(iv.start); d > 0 {
			total += d
		}
	}
	return int(total.Minutes())
}

// AttributeMinutes implements D1/§6.4 deterministically (NO LLM):
//
//   - Per session, active sub-intervals are derived from MessageTimes:
//     consecutive timestamps within idleCap extend the current interval;
//     a larger gap closes it and starts a new one (activeIntervals).
//   - Per repo, ALL active intervals from ALL that repo's sessions are
//     collected, merged (mergeIntervals), and the merged lengths summed.
//     That merged total is the repo's true wall-clock active minutes —
//     overlapping/parallel sessions are unioned, never double-counted,
//     so AIMinutes can never exceed 24h/day.
//   - Per-CLI breakdown: the same merge is run per CLI group, so ByCLI
//     holds each CLI's own union total within the repo.
//   - Multi-repo split: a session touching >1 repo in-window splits its
//     merged active-duration across those repos proportionally by
//     in-window commit count (commitCounts), marked Split (approximate).
//     Split minutes are added on top of the merged single-repo union.
//   - Manual work: a repo with in-window commits but no session
//     active-time gets ManualOnly=true and 0 AI minutes (D4).
//   - Gap rule (D4): the session→commit/push gap is never added — only
//     intra-session active intervals are counted.
func AttributeMinutes(sessions []SessionActivity, commitCounts map[string]int, idleCapMin int) map[string]RepoTime {
	cap := time.Duration(idleCapMin) * time.Minute

	// Per-repo collected active intervals from whole single-repo
	// attribution, plus per-CLI interval sets for the per-CLI union.
	repoIntervals := map[string][]interval{}
	repoCLIIntervals := map[string]map[string][]interval{}
	// Per-repo extra minutes contributed by the multi-repo split, and the
	// per-CLI split minutes. These are summed on top of the merged union
	// (a split share has no usable wall-clock sub-interval, so it cannot
	// itself participate in the merge).
	splitMinutes := map[string]int{}
	splitCLIMinutes := map[string]map[string]int{}
	splitFlag := map[string]bool{}

	addCLI := func(m map[string]map[string][]interval, repo, cli string, ivs []interval) {
		if cli == "" {
			cli = "unknown"
		}
		if m[repo] == nil {
			m[repo] = map[string][]interval{}
		}
		m[repo][cli] = append(m[repo][cli], ivs...)
	}
	addSplitCLI := func(repo, cli string, mins int) {
		if cli == "" {
			cli = "unknown"
		}
		if splitCLIMinutes[repo] == nil {
			splitCLIMinutes[repo] = map[string]int{}
		}
		splitCLIMinutes[repo][cli] += mins
	}

	for _, s := range sessions {
		ivs := activeIntervals(s.MessageTimes, cap)
		if len(ivs) == 0 {
			continue
		}

		repos := s.Repos
		if len(repos) == 0 {
			repos = []string{s.ProjectPath}
		}

		if len(repos) == 1 {
			repoIntervals[repos[0]] = append(repoIntervals[repos[0]], ivs...)
			addCLI(repoCLIIntervals, repos[0], s.CLI, ivs)
			continue
		}

		// Multi-repo: split this session's merged active-duration
		// proportionally by in-window commit count. The session's own
		// intervals are merged first so the split base is itself a union.
		sessionMinutes := totalMinutes(mergeIntervals(ivs))
		total := 0
		for _, rp := range repos {
			n := commitCounts[rp]
			if n <= 0 {
				n = 1 // never zero-weight a touched repo
			}
			total += n
		}
		for _, rp := range repos {
			n := commitCounts[rp]
			if n <= 0 {
				n = 1
			}
			share := sessionMinutes * n / total
			splitMinutes[rp] += share
			addSplitCLI(rp, s.CLI, share)
			splitFlag[rp] = true
		}
	}

	out := map[string]RepoTime{}

	// Every repo that saw any session activity (whole or split).
	repoSet := map[string]bool{}
	for r := range repoIntervals {
		repoSet[r] = true
	}
	for r := range splitMinutes {
		repoSet[r] = true
	}

	for repo := range repoSet {
		merged := mergeIntervals(repoIntervals[repo])
		aiMinutes := totalMinutes(merged) + splitMinutes[repo]

		byCLI := map[string]int{}
		for cli, ivs := range repoCLIIntervals[repo] {
			byCLI[cli] += totalMinutes(mergeIntervals(ivs))
		}
		for cli, mins := range splitCLIMinutes[repo] {
			byCLI[cli] += mins
		}
		if len(byCLI) == 0 {
			byCLI = nil
		}

		out[repo] = RepoTime{
			AIMinutes: aiMinutes,
			ByCLI:     byCLI,
			Split:     splitFlag[repo],
		}
	}

	// Repos with in-window commits but no AI session → ManualOnly (D4).
	for repo, n := range commitCounts {
		if n <= 0 {
			continue
		}
		r, ok := out[repo]
		if !ok || r.AIMinutes == 0 {
			r.ManualOnly = true
			out[repo] = r
		}
	}
	return out
}

// activeIntervals walks consecutive (sorted) message timestamps and
// builds the session's list of active sub-intervals: a gap ≤ cap extends
// the current interval, a larger gap closes it and starts a new one. A
// session with fewer than two timestamps yields no measurable interval
// (there is no span between a single message and itself).
func activeIntervals(times []time.Time, cap time.Duration) []interval {
	if len(times) < 2 {
		return nil
	}
	sorted := make([]time.Time, len(times))
	copy(sorted, times)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Before(sorted[j]) })

	var out []interval
	start := sorted[0]
	prev := sorted[0]
	for _, t := range sorted[1:] {
		gap := t.Sub(prev)
		if gap < 0 {
			gap = 0
		}
		if gap > cap {
			// Close the current interval; only emit one with real span.
			if prev.After(start) {
				out = append(out, interval{start: start, end: prev})
			}
			start = t
		}
		prev = t
	}
	if prev.After(start) {
		out = append(out, interval{start: start, end: prev})
	}
	return out
}
