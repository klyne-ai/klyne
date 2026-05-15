// Package resume — per-session relevance scorer (pure, no I/O).
//
// Score formula (v0 — topic embedding dropped, weights redistributed):
//
//	score = 0.40 * recency_decay
//	      + 0.30 * cwd_match
//	      + 0.30 * git_jaccard
//
// Threshold for surfacing: score >= 0.55; cap at top-3.
package resume

import (
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SessionInput carries the fields the scorer needs from the store layer.
// The caller populates this from a sessions row + a list of touched files.
// All I/O is the caller's responsibility.
type SessionInput struct {
	// ID is the session primary key.
	ID string
	// LastMsgAt is the epoch-milliseconds of the most recent message.
	LastMsgAt int64
	// ProjectPath is the canonical project root stored on the session row.
	ProjectPath string
	// TouchedFiles is the set of relative file paths touched during the session.
	// Derived from tool_calls_json (Read/Edit/Write tool inputs) by the caller.
	TouchedFiles []string
	// DecisionCount is the number of decision rows for this session.
	DecisionCount int
}

// Ranked wraps a SessionInput with its computed score.
type Ranked struct {
	Session SessionInput
	Score   float64
}

// Score computes the v0 relevance score for a single session candidate.
//
// Parameters:
//   - session: the candidate session
//   - cwd: the caller's current working directory
//   - gitRecent: files from `git diff --name-only HEAD~5` + uncommitted (may be empty)
//   - now: the reference time (callers pass time.Now(); tests inject a fixed value)
func Score(session SessionInput, cwd string, gitRecent []string, now time.Time) float64 {
	return scoreWeighted(session, cwd, gitRecent, now)
}

// scoreWeighted is the internal implementation separated so tests can call it directly.
func scoreWeighted(s SessionInput, cwd string, gitRecent []string, now time.Time) float64 {
	// Recency decay: half-life ~33 hours via exp(-t/48).
	hoursSince := now.Sub(time.UnixMilli(s.LastMsgAt)).Hours()
	if hoursSince < 0 {
		hoursSince = 0
	}
	recency := math.Exp(-hoursSince / 48.0)

	cwd = filepath.Clean(cwd)
	proj := filepath.Clean(s.ProjectPath)

	cwdScore := cwdMatch(proj, cwd)
	gitScore := gitJaccard(s.TouchedFiles, gitRecent)

	return 0.40*recency + 0.30*cwdScore + 0.30*gitScore
}

// cwdMatch returns:
//   - 1.0 when proj == cwd (exact)
//   - 0.6 when proj is a parent of cwd (session ran in a parent dir)
//   - 0.6 when cwd is a parent of proj (session ran in a child dir)
//   - 0.3 when proj and cwd share the same parent (siblings)
//   - 0.0 otherwise
func cwdMatch(proj, cwd string) float64 {
	if proj == cwd {
		return 1.0
	}

	// parent match: cwd starts with proj (cwd is inside proj)
	if strings.HasPrefix(cwd, proj+"/") {
		return 0.6
	}
	// parent match: proj starts with cwd (proj is inside cwd)
	if strings.HasPrefix(proj, cwd+"/") {
		return 0.6
	}

	// sibling match: same parent directory
	projParent := filepath.Dir(proj)
	cwdParent := filepath.Dir(cwd)
	if projParent == cwdParent && projParent != "." && projParent != "/" {
		return 0.3
	}

	return 0.0
}

// gitJaccard computes the Jaccard similarity between the session's touched
// files and the git-recent file set. Returns 0 when both sets are empty.
func gitJaccard(touched, gitRecent []string) float64 {
	if len(touched) == 0 && len(gitRecent) == 0 {
		return 0.0
	}

	// Build sets using maps for O(n) intersection/union.
	tSet := make(map[string]struct{}, len(touched))
	for _, f := range touched {
		tSet[normalizeFilePath(f)] = struct{}{}
	}
	gSet := make(map[string]struct{}, len(gitRecent))
	for _, f := range gitRecent {
		gSet[normalizeFilePath(f)] = struct{}{}
	}

	intersection := 0
	for f := range tSet {
		if _, ok := gSet[f]; ok {
			intersection++
		}
	}

	// |A ∪ B| = |A| + |B| - |A ∩ B|
	union := len(tSet) + len(gSet) - intersection
	if union == 0 {
		return 0.0
	}
	return float64(intersection) / float64(union)
}

// normalizeFilePath lowercases and cleans a path for case-insensitive
// comparison on file systems that are case-insensitive in practice (macOS).
// Relative paths are returned as-is after cleaning.
func normalizeFilePath(p string) string {
	return strings.ToLower(filepath.Clean(p))
}

// ScoreThreshold is the minimum score for surfacing a session candidate.
const ScoreThreshold = 0.55

// MaxCandidates is the default maximum number of candidates returned by Rank.
const MaxCandidates = 3

// RankOptions tunes Rank's filtering and cap behaviour. The zero value
// reproduces the legacy default (top-3, threshold ≥ 0.55).
type RankOptions struct {
	// Limit caps the number of returned candidates.
	//  - 0  → use MaxCandidates (the default top-3 cap)
	//  - <0 → no cap; return every candidate that passes the threshold
	Limit int
	// IgnoreThreshold disables the ScoreThreshold filter when true, so even
	// stale/unrelated sessions surface. Pair with Limit<0 to dump everything.
	IgnoreThreshold bool
}

// Rank scores all sessions, filters by threshold, and returns the top-N
// sorted by descending score. Equivalent to RankWithOptions with the zero
// RankOptions. Pure — no I/O.
func Rank(sessions []SessionInput, cwd string, gitRecent []string, now time.Time) []Ranked {
	return RankWithOptions(sessions, cwd, gitRecent, now, RankOptions{})
}

// RankWithOptions is Rank with explicit cap and threshold controls.
// See RankOptions for the meaning of Limit and IgnoreThreshold.
func RankWithOptions(sessions []SessionInput, cwd string, gitRecent []string, now time.Time, opts RankOptions) []Ranked {
	out := make([]Ranked, 0, len(sessions))
	for _, s := range sessions {
		sc := Score(s, cwd, gitRecent, now)
		if !opts.IgnoreThreshold && sc < ScoreThreshold {
			continue
		}
		out = append(out, Ranked{Session: s, Score: sc})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Score > out[j].Score
	})

	limit := opts.Limit
	if limit == 0 {
		limit = MaxCandidates
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
