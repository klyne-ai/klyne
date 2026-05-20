// Layer-2 reflection enrichment (spec §7, §7.1, §7.2): turns the
// deterministic productivity substrate (internal/productivity) into the
// git-grounded sections that every daily worklog reflection now carries.
//
// Per the spec D8 / §7.1 determinism boundary, EVERYTHING in this file is
// computed with fmt/strings only — no LLM. The git-derived facts (ship
// state, ahead counts, risk ages, short SHAs, commit timestamps) are
// FIXED inputs. The seven worklog improvements (§7.2) split as:
//
//   - 1 Open Loops / Unpushed block       — deterministic (this file)
//   - 2 Salience gate                     — deterministic ranking (this file)
//   - 3 Commit-timestamp dating           — deterministic (this file)
//   - 4 Suppress unverified artifact IDs  — deterministic guard + prompt
//   - 5 Shipped ledger line               — deterministic (this file)
//   - 6 Author/identity filter            — productivity already filters
//   - 7 Cross-project initiative thread   — deterministic (this file)
//
// Graceful degradation (D8): when a project has no git repo / no
// substrate, GitSubstrateSections returns nil and the existing
// reflection flow is unchanged.
package worklog

import (
	"fmt"
	"sort"
	"strings"

	"github.com/klyne-ai/klyne/internal/productivity"
)

// GitSubstrateSections renders the deterministic git-grounded markdown
// sections for one project's daily reflection from a productivity.Report
// (improvements 1, 3, 5 of spec §7.2). Each returned string is a
// self-contained markdown block; the recorder appends them to body_md
// after the AI-authored insight bullets.
//
// projectPath selects the matching Service in the report. When no
// Service matches (the project is not a git repo, or had no in-window
// activity) the result is nil — the caller keeps the existing
// reflection body unchanged (graceful degradation, D8).
func GitSubstrateSections(rep productivity.Report, projectPath string) []string {
	svc := findService(rep, projectPath)
	if svc == nil {
		return nil
	}
	var secs []string
	if s := shippedLedger(*svc); s != "" {
		secs = append(secs, s)
	}
	if s := openLoopsBlock(*svc); s != "" {
		secs = append(secs, s)
	}
	return secs
}

// findService returns the Service whose ProjectPath matches projectPath.
// Match is exact on ProjectPath (the canonical repo root) so a worktree
// reflection — already canonicalized by RecordReflection — lines up.
func findService(rep productivity.Report, projectPath string) *productivity.Service {
	for i := range rep.Services {
		if rep.Services[i].ProjectPath == projectPath {
			return &rep.Services[i]
		}
	}
	return nil
}

// shippedLedger renders improvement 5: a terse per-repo line of what
// merged/pushed/landed-locally, one line per branch, ordered by the
// salience rule (highest-impact branch first). Ship state is quoted
// verbatim from the deterministic state machine; every line cites the
// latest short SHA on the branch and the commit's day (improvement 3 —
// commit-timestamp dating, not session start).
func shippedLedger(svc productivity.Service) string {
	branches := rankBranches(svc.Branches)
	if len(branches) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("**Shipped (" + svc.Repo + ")**\n")
	for _, b := range branches {
		latest := latestCommit(b.Commits)
		if latest == nil {
			continue
		}
		ticket := ""
		if b.TicketID != "" {
			ticket = " [" + b.TicketID + "]"
		}
		fmt.Fprintf(&sb, "- `%s`%s — %s, %d commit(s), latest %s on %s\n",
			b.Name, ticket, b.Ship, len(b.Commits),
			latest.SHA, latest.CommittedAt.Format("2006-01-02"))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// openLoopsBlock renders improvement 1: the Open Loops / Unpushed block.
// It lists every unpushed branch (commits-ahead + age) and any
// done-uncommitted state, sourced purely from productivity risk
// signals. Returns "" when the repo has no risks (degrade gracefully —
// a clean repo gets no block at all).
func openLoopsBlock(svc productivity.Service) string {
	if len(svc.Risks) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("**Open Loops (" + svc.Repo + ")**\n")
	for _, r := range svc.Risks {
		switch r.Kind {
		case "unpushed":
			branch := firstUnpushedBranch(svc)
			fmt.Fprintf(&sb, "- unpushed: `%s` — %s (%s)\n",
				branch, r.Detail, humanizeAge(r.AgeMinutes))
		case "done-uncommitted":
			fmt.Fprintf(&sb, "- done-uncommitted: %s (%s)\n",
				r.Detail, humanizeAge(r.AgeMinutes))
		default:
			fmt.Fprintf(&sb, "- %s: %s (%s)\n", r.Kind, r.Detail, humanizeAge(r.AgeMinutes))
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// firstUnpushedBranch returns the name of the first committed-local-only
// branch with commits ahead — the branch the unpushed risk refers to.
// Falls back to the highest-salience branch name when none is local.
func firstUnpushedBranch(svc productivity.Service) string {
	for _, b := range svc.Branches {
		if b.Ship == productivity.ShipLocal && b.Ahead > 0 {
			return b.Name
		}
	}
	ranked := rankBranches(svc.Branches)
	if len(ranked) > 0 {
		return ranked[0].Name
	}
	return svc.Repo
}

// rankBranches applies the §7.1 rule-5 salience ordering: highest commit
// count first, then most net-new lines. A copy is returned so callers
// never mutate the report.
func rankBranches(in []productivity.Branch) []productivity.Branch {
	out := make([]productivity.Branch, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Commits) != len(out[j].Commits) {
			return len(out[i].Commits) > len(out[j].Commits)
		}
		return netLines(out[i].Commits) > netLines(out[j].Commits)
	})
	return out
}

// netLines sums net-new (insertions − deletions) across commits — the
// secondary salience key.
func netLines(cs []productivity.Commit) int {
	n := 0
	for _, c := range cs {
		n += c.Insertions - c.Deletions
	}
	return n
}

// latestCommit returns the commit with the newest CommittedAt — the one
// the shipped ledger cites and dates by (improvement 3).
func latestCommit(cs []productivity.Commit) *productivity.Commit {
	var latest *productivity.Commit
	for i := range cs {
		if latest == nil || cs[i].CommittedAt.After(latest.CommittedAt) {
			latest = &cs[i]
		}
	}
	return latest
}

// humanizeAge renders an age in minutes as a terse "Nh"/"Nm" string for
// the open-loops block.
func humanizeAge(minutes int) string {
	if minutes <= 0 {
		return "just now"
	}
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	h := minutes / 60
	m := minutes % 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}
