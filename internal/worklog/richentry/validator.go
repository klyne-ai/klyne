// Package richentry implements the gate + writer + validator for the
// migration-019 rich worklog entry. This file is the validator: a
// structural anti-hallucination guard that walks every `refs[]` in a
// store.WorklogEntryJSON and rejects any citation that wasn't in the
// session's real inputs.
//
// The validator exists because the previous reflection writer
// hallucinated by citing the same session_id under every bullet (the
// "e4696ed8-..." regression). The fix isn't to ban citations — it's
// to ban citations the LLM couldn't have honestly known about.
package richentry

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/store"
)

// Allowlist is the per-turn set of tokens any `refs[]` element is
// permitted to cite. Built from real session inputs by BuildAllowlist;
// the validator rejects anything outside it.
//
// PRCacheLive distinguishes "PR cache returned zero results" (live,
// reject unknown PR refs) from "PR cache was never reachable / fresh
// repo" (skip PR checks entirely so a structurally-correct entry
// against an offline repo isn't rejected for missing data).
type Allowlist struct {
	ShortSHAs   map[string]bool
	PRNumbers   map[string]bool // keys include the "#" prefix
	PRCacheLive bool
	TicketIDs   map[string]bool
	FilePaths   map[string]bool
	Durations   map[string]bool // canonical "~Xh Ym" / "~Nm"
	Clocks      map[string]bool // "HH:MM" from commit times + interval endpoints
	SessionIDs  map[string]bool // UUIDs of prior klyne sessions explicitly cited
}

// CommitRef is the minimal commit shape BuildAllowlist needs from the
// session context — short SHA, the files this commit touched, and
// when it was committed (for the HH:MM clock allowlist). The
// session-writer adapts whatever the daemon hands it into this shape.
type CommitRef struct {
	SHA         string
	Files       []string
	CommittedAt time.Time
}

// Interval is one active-time window from the session — the
// productivity package emits these, and the writer hands them in so
// durations and start/end HH:MM clocks can be cited honestly.
type Interval struct {
	Start time.Time
	End   time.Time
}

// ValidationError describes one bad citation found inside an entry.
// Category + ItemIdx + Ref locate the offender precisely so the
// writer can build a corrected re-prompt.
type ValidationError struct {
	Category string
	ItemIdx  int
	Ref      string
	Reason   string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("category=%s item=%d ref=%q: %s", e.Category, e.ItemIdx, e.Ref, e.Reason)
}

// Regex shapes for the 7 token classes. Matched in priority order:
// UUID > PR > SHA > ticket > duration > clock > path. A token that
// fails its shape's allowlist check is rejected with a specific
// reason (so the writer's re-prompt knows what to fix).
var (
	shaRe      = regexp.MustCompile(`^[0-9a-f]{7,12}$`)
	uuidRe     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	prRefRe    = regexp.MustCompile(`^#\d+$`)
	ticketRe   = regexp.MustCompile(`^[A-Z]{2,}-\d+$`)
	durationRe = regexp.MustCompile(`^~?\d+h(\s\d+m)?$|^~?\d+m$`)
	clockRe    = regexp.MustCompile(`^\d{1,2}:\d{2}$`)
)

// Validate walks every refs[] across every category and returns the
// list of rejected citations. Empty list = entry is allowlist-clean.
//
// Two passes on UUIDs:
//   1. Each UUID must be in allow.SessionIDs (a real prior session).
//   2. Each UUID must appear in at most ONE bullet's refs across the
//      whole entry — the actual regression pattern was one valid
//      session_id pasted under every bullet, so uniqueness across
//      bullets is the structural guard that catches it.
func Validate(entry store.WorklogEntryJSON, allow Allowlist) []ValidationError {
	uuidUses := countUUIDUses(entry)
	var errs []ValidationError
	// Iterate in stable order (category name) so error lists are
	// deterministic across runs — easier to grep, easier to test.
	for _, cat := range sortedKeys(entry.Categories) {
		for i, it := range entry.Categories[cat] {
			for _, ref := range it.Refs {
				if reason := classify(ref, allow, uuidUses); reason != "" {
					errs = append(errs, ValidationError{
						Category: cat,
						ItemIdx:  i,
						Ref:      ref,
						Reason:   reason,
					})
				}
			}
		}
	}
	return errs
}

func classify(ref string, allow Allowlist, uuidUses map[string]int) string {
	r := strings.TrimSpace(ref)
	if r == "" {
		return "empty ref"
	}
	switch {
	case uuidRe.MatchString(r):
		if !allow.SessionIDs[r] {
			return "uuid not in session-id allowlist (regression class)"
		}
		if uuidUses[r] > 1 {
			return "uuid reused across multiple bullets (regression class)"
		}
	case prRefRe.MatchString(r):
		// Stale / missing PR cache → skip the check entirely (the writer
		// logs the skip so we can audit "passing because cache empty").
		if allow.PRCacheLive && !allow.PRNumbers[r] {
			return "PR not in allowlist"
		}
	case shaRe.MatchString(r):
		if !allow.ShortSHAs[r] {
			return "SHA not in commit list"
		}
	case ticketRe.MatchString(r):
		if !allow.TicketIDs[r] {
			return "ticket not on any branch in this session"
		}
	case durationRe.MatchString(r):
		if !allow.Durations[r] {
			return "duration not derivable from session intervals"
		}
	case clockRe.MatchString(r):
		if !allow.Clocks[r] {
			return "clock time not at any commit or interval endpoint"
		}
	default:
		// Anything else is treated as a file-path citation.
		if !allow.FilePaths[r] {
			return "path not in session's touched-files"
		}
	}
	return ""
}

// countUUIDUses tallies how many distinct bullets reference each UUID.
// The hallucination pattern was one UUID cited under every bullet —
// uniqueness across bullets is the structural guard against it.
// Duplicate UUIDs INSIDE one bullet's refs[] count once for that
// bullet (still a single semantic citation).
func countUUIDUses(entry store.WorklogEntryJSON) map[string]int {
	out := map[string]int{}
	for _, items := range entry.Categories {
		for _, it := range items {
			seenInThisBullet := map[string]bool{}
			for _, ref := range it.Refs {
				r := strings.TrimSpace(ref)
				if uuidRe.MatchString(r) && !seenInThisBullet[r] {
					out[r]++
					seenInThisBullet[r] = true
				}
			}
		}
	}
	return out
}

// sortedKeys returns m's keys sorted lexicographically for stable
// iteration order. Inline to avoid pulling in a slices/sort dep.
func sortedKeys(m map[string][]store.WorklogItem) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Tiny insertion sort — Categories is at most 15 entries.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}

// BuildAllowlist derives the per-turn citation allowlist from the real
// session inputs the writer will see. Inputs are exactly what the
// session-end pipeline (Phase 4) has on hand:
//   - cmts:            in-window commits attributed to the user
//   - prs:             merged PR numbers from the gh cache (nil = no
//                      cache lookup happened; []int{} = cache live, zero PRs)
//   - branches:        branch names (for CLI-NNNN ticket extraction)
//   - files:           session-level touched files from stop_summaries.files_json
//   - intervals:       gap-capped active intervals around this turn
//   - priorSessionIDs: UUIDs of prior klyne sessions explicitly cited
//                      in the writer's input bundle (rare — most calls pass nil)
//
// PRCacheLive is true exactly when prs is non-nil (even if empty) —
// callers must pass nil to signal "no cache lookup happened" so the
// validator skips PR-number checks entirely.
func BuildAllowlist(
	cmts []CommitRef, prs []int, branches []string, files []string,
	intervals []Interval, priorSessionIDs []string,
) Allowlist {
	al := Allowlist{
		ShortSHAs:   map[string]bool{},
		PRNumbers:   map[string]bool{},
		PRCacheLive: prs != nil,
		TicketIDs:   map[string]bool{},
		FilePaths:   map[string]bool{},
		Durations:   map[string]bool{},
		Clocks:      map[string]bool{},
		SessionIDs:  map[string]bool{},
	}

	// 3a + 3b: shas + per-commit file paths
	for _, c := range cmts {
		if c.SHA != "" {
			al.ShortSHAs[c.SHA] = true
		}
		for _, f := range c.Files {
			al.FilePaths[f] = true
		}
		// 3f (clocks from commit time) handled in the intervals loop below
		// so all clock canonicalization shares one path
	}

	// 3b cont.: session-level touched files
	for _, f := range files {
		al.FilePaths[f] = true
	}

	// 3c: PR numbers
	for _, n := range prs {
		al.PRNumbers[fmt.Sprintf("#%d", n)] = true
	}

	// 3d: ticket ids from branch names (case-insensitive match, store upper)
	branchTicketRe := regexp.MustCompile(`(?i)\b([a-z]{2,}-\d+)\b`)
	for _, b := range branches {
		for _, m := range branchTicketRe.FindAllStringSubmatch(b, -1) {
			al.TicketIDs[strings.ToUpper(m[1])] = true
		}
	}

	// 3e + 3f: durations (canonical "~Xh Ym" / "~Nm") and clocks
	// (HH:MM from interval endpoints + commit times) — both derived
	// from the interval list so the same canonicalization path handles
	// the canonical-format contract the validator enforces.
	for _, iv := range intervals {
		if iv.End.After(iv.Start) {
			al.Durations[canonicalDuration(iv.End.Sub(iv.Start))] = true
		}
		al.Clocks[iv.Start.Format("15:04")] = true
		al.Clocks[iv.End.Format("15:04")] = true
	}
	for _, c := range cmts {
		if !c.CommittedAt.IsZero() {
			al.Clocks[c.CommittedAt.Format("15:04")] = true
		}
	}

	// 3g: prior session ids — UUID-validated; the validator's per-UUID
	// uniqueness check still applies, so even a known id can't be
	// reused across bullets.
	for _, sid := range priorSessionIDs {
		s := strings.TrimSpace(sid)
		if uuidRe.MatchString(s) {
			al.SessionIDs[s] = true
		}
	}
	return al
}

// canonicalDuration matches the dashboard's productivity.hm() helper
// convention: "~Xh Ym" when d >= 1h, "~Nm" otherwise. Anything below
// a minute rounds to "~0m" (rare; intervals are gap-capped at 10min
// in the productivity package and bottom out at ~1min in practice).
func canonicalDuration(d time.Duration) string {
	mins := int(d.Minutes())
	if mins >= 60 {
		return fmt.Sprintf("~%dh %dm", mins/60, mins%60)
	}
	return fmt.Sprintf("~%dm", mins)
}
