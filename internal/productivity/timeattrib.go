package productivity

import (
	"sort"
	"time"
)

// SessionActivity is the time-attribution input for one klyne session:
// the in-window message timestamps (active-time is computed from these,
// not from naive commit deltas — D1) and the repo(s) the session
// touched. ProjectPath is the canonical project the session ran in;
// Repos, when it spans more than one repo in-window, drives the
// proportional multi-repo split (§6.4).
type SessionActivity struct {
	SessionID    string
	ProjectPath  string
	MessageTimes []time.Time
	Repos        []string // empty → attributed wholly to ProjectPath
}

// RepoTime is the deterministic per-repo time verdict (D1/D4).
// ManualOnly is true when the repo had in-window commits but ZERO AI
// session active-time — manual time is reported separately and never
// folded into AI time (D4). Split flags an approximate figure produced
// by the multi-repo proportional split (§6.4: "≈, split A/B").
type RepoTime struct {
	AIMinutes  int
	ManualOnly bool
	Split      bool
}

// AttributeMinutes implements D1/§6.4 deterministically (NO LLM):
//
//   - Per session, active-time = Σ adjacent message-interval durations,
//     excluding any gap longer than idleCapMin (the idle cap — klyne
//     has no reusable contexthealth active-span helper, see final
//     report; this is the documented adaptation).
//   - Multi-repo split: a session touching >1 repo in-window splits its
//     active-time across those repos proportionally by in-window commit
//     count (commitCounts), marked Split (approximate).
//   - Manual work: a repo with in-window commits but no session
//     active-time gets ManualOnly=true and 0 AI minutes (D4).
//   - Gap rule (D4): the session→commit/push gap is never added — only
//     intra-session active intervals are counted.
func AttributeMinutes(sessions []SessionActivity, commitCounts map[string]int, idleCapMin int) map[string]RepoTime {
	out := map[string]RepoTime{}
	cap := time.Duration(idleCapMin) * time.Minute

	for _, s := range sessions {
		active := activeDuration(s.MessageTimes, cap)
		if active <= 0 {
			continue
		}

		repos := s.Repos
		if len(repos) == 0 {
			repos = []string{s.ProjectPath}
		}

		if len(repos) == 1 {
			r := out[repos[0]]
			r.AIMinutes += int(active.Minutes())
			out[repos[0]] = r
			continue
		}

		// Multi-repo: split proportionally by in-window commit count.
		total := 0
		for _, rp := range repos {
			n := commitCounts[rp]
			if n <= 0 {
				n = 1 // never zero-weight a touched repo
			}
			total += n
		}
		mins := active.Minutes()
		for _, rp := range repos {
			n := commitCounts[rp]
			if n <= 0 {
				n = 1
			}
			share := int(mins * float64(n) / float64(total))
			r := out[rp]
			r.AIMinutes += share
			r.Split = true
			out[rp] = r
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

// activeDuration sums adjacent message-interval durations, dropping any
// interval longer than the idle cap. A single-message session has zero
// measurable active span (no interval to measure).
func activeDuration(times []time.Time, cap time.Duration) time.Duration {
	if len(times) < 2 {
		return 0
	}
	sorted := make([]time.Time, len(times))
	copy(sorted, times)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Before(sorted[j]) })

	var total time.Duration
	for i := 1; i < len(sorted); i++ {
		gap := sorted[i].Sub(sorted[i-1])
		if gap <= 0 || gap > cap {
			continue
		}
		total += gap
	}
	return total
}
