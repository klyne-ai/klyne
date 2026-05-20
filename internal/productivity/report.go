package productivity

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ReflectionLookup is the §7 L2/L3 dependency: "is there a klyne worklog
// reflection for this project on this day, and what does it say?".
// Defined as an interface so this package stays free of a direct
// *store.DB dependency and testable with a stub (the API handler adapts
// the real worklog_reflections store).
//
// HasReflection returns (found, bodyMD, err): found drives the L3
// status/nudge; bodyMD is the reflection's markdown narrative surfaced
// as Service.ReflectionMarkdown (Change 3) — it is "" when found is
// false. bodyMD is purely additive enrichment and never gates the
// deterministic numbers (D8).
type ReflectionLookup interface {
	HasReflection(ctx context.Context, projectPath string, day time.Time) (found bool, bodyMD string, err error)
}

// DirtyState is the current working-tree state for a repo plus the end
// of its most recent substantive session — the inputs to the
// done-uncommitted risk (D4/§6.5).
type DirtyState struct {
	DirtyFileCount int
	// DirtyFiles is the uncommitted/untracked paths (porcelain
	// short-status) so the "done-uncommitted" risk can show WHICH files
	// are dirty, not just a count. May be empty even when
	// DirtyFileCount > 0 if the list could not be read.
	DirtyFiles []string
	SessionEnd time.Time // zero ⇒ no recent session ⇒ no done-uncommitted signal
}

// ReportInput is the fully-deterministic, pre-computed substrate fed to
// BuildReport. The API handler (Task 6) populates it from DiscoverRepos
// + ScanRepo + AttributeMinutes; tests populate it with fixtures. No
// field here is LLM-derived (spec §7.1 determinism boundary).
type ReportInput struct {
	Day          string                // local date string, e.g. "2026-05-19"
	Now          time.Time             // render clock (risk-age anchor)
	Scans        []ScanResult          // one per discovered repo+worktree
	Attribution  map[string]RepoTime   // keyed by repo Dir
	ProjectPaths map[string]string     // repo Dir → canonical project_path
	DirtyRepos   map[string]DirtyState // repo Dir → current dirty/session state
	// Sessions is the raw per-session activity in the window. BuildReport
	// uses it to compute the report-level GLOBAL interval union
	// (TotalActiveMinutes / per-CLI MinutesByCLI — Change 1) and the
	// per-session SessionStat evidence list (Change 2). Empty is valid:
	// the report's headline figures are then 0 and Sessions is [].
	Sessions []SessionActivity
}

// BuildReport assembles the deterministic Service→Branch→Topic Report
// (§6.6) with the ship-state machine (D2/§6.5), current-state risk
// signals (§6.5), the Layer-1 templated narrative (§7 L1), and the
// reflection status/nudge (§7 L3). NO LLM is invoked anywhere.
func BuildReport(ctx context.Context, in ReportInput, refl ReflectionLookup) (Report, error) {
	// Services starts as a non-nil empty slice so an empty-window report
	// marshals "services":[] not null — the {}/[] wire contract.
	rep := Report{Day: in.Day, Services: []Service{}}

	day := dayTime(in.Day, in.Now)
	anyReflection := false

	// §6.6 / D5: group scans by canonical project path so every worktree
	// of a repo (the main tree + sibling per-ticket / claude / codex
	// checkouts) collapses into ONE Service. The attribution map is keyed
	// by ScanResult.Dir, so per-branch (per-worktree) attribution and
	// per-worktree ship facts are preserved by building branches per
	// scan and concatenating them under the canonical Service.
	type group struct {
		repo        string
		projectPath string
		scans       []ScanResult
	}
	var order []string
	groups := map[string]*group{}
	for _, sc := range in.Scans {
		pp := in.projectPath(sc.Dir)
		g, ok := groups[pp]
		if !ok {
			g = &group{repo: RepoName(pp), projectPath: pp}
			groups[pp] = g
			order = append(order, pp)
		}
		g.scans = append(g.scans, sc)
	}

	for _, pp := range order {
		g := groups[pp]
		svc := Service{Repo: g.repo, ProjectPath: g.projectPath}

		manualOnly := true
		anyUserCommits := false
		// Per-CLI minutes for the Service: aggregated across every
		// worktree/scan of the canonical repo (time is repo-scoped).
		minutesByCLI := map[string]int{}
		for _, sc := range g.scans {
			// §6.3 identity filter: only the user's commits count toward
			// the productivity figures. Co-actors/bots (Jenkins,
			// Ravi-Ranjan) are dropped here so they never enter a Branch.
			var userCommits []Commit
			for _, c := range sc.Commits {
				if c.IsUser {
					userCommits = append(userCommits, c)
				}
			}

			rt := in.Attribution[sc.Dir]
			if len(userCommits) > 0 {
				anyUserCommits = true
				if !(rt.ManualOnly && rt.AIMinutes == 0) {
					manualOnly = false
				}
			}
			for cli, mins := range rt.ByCLI {
				minutesByCLI[cli] += mins
			}

			svc.Branches = append(svc.Branches, buildBranches(sc, userCommits, rt)...)

			// Risk signals (§6.5): repo-scoped, aggregated across every
			// worktree of the canonical repo.
			svc.Risks = append(svc.Risks, riskSignals(sc, userCommits, in.DirtyRepos[sc.Dir], in.Now)...)
		}
		svc.ManualOnly = manualOnly && anyUserCommits
		svc.MinutesByCLI = minutesByCLI

		// Always emit JSON objects/arrays, never null, so UI consumers
		// can rely on the {}/[] contract (a service with no
		// branches/risks/CLI time would otherwise marshal nil to null).
		// MergedPRs starts empty here; the API-layer GitHub enrichment
		// (productivity_github.go) fills it after BuildReport.
		if svc.Branches == nil {
			svc.Branches = []Branch{}
		}
		if svc.Risks == nil {
			svc.Risks = []RiskSignal{}
		}
		if svc.MinutesByCLI == nil {
			svc.MinutesByCLI = map[string]int{}
		}
		svc.MergedPRs = []MergedPR{}

		// Salience ordering (§7.1 rule 5) applied across the merged
		// branch set so the highest-impact worktree leads.
		sortBranches(svc.Branches)

		// Reflection status (§7 L3) + narrative body (Change 3): per
		// canonical project/day. The body_md is purely additive
		// enrichment — it never gates the deterministic numbers (D8).
		if refl != nil {
			has, body, err := refl.HasReflection(ctx, svc.ProjectPath, day)
			if err != nil {
				return Report{}, fmt.Errorf("productivity: reflection lookup for %s: %w", svc.ProjectPath, err)
			}
			if has {
				anyReflection = true
				svc.ReflectionMarkdown = body
				// Surface the most recent per-project reflection body as the
				// report-wide overall narrative when none is set yet. With a
				// single active project this is the natural overall summary;
				// with several it is the first project's — honest, not
				// fabricated (per-Service bodies carry the rest).
				if rep.ReflectionMarkdown == "" {
					rep.ReflectionMarkdown = body
				}
			}
		}

		rep.Services = append(rep.Services, svc)
	}

	// Report-level GLOBAL interval union (Change 1): the headline AI time
	// is the union of every session's active wall-clock intervals across
	// ALL repos — true elapsed wall-clock, not the sum of per-Service
	// unions (which double-counts parallel cross-repo agents). Per-CLI
	// MinutesByCLI is likewise the per-CLI global union.
	rep.TotalActiveMinutes, rep.MinutesByCLI = GlobalActiveMinutes(in.Sessions, idleCapMinutes)
	if rep.MinutesByCLI == nil {
		rep.MinutesByCLI = map[string]int{}
	}

	// Per-session proof-of-work evidence list (Change 2), sorted by start.
	rep.Sessions = buildSessionStats(in.Sessions, in.ProjectPaths)

	if anyReflection {
		rep.ReflectionStatus = "current"
		rep.Nudge = ""
	} else {
		// PROTOTYPE STUB (spec §11): "stale" detection (reflection
		// exists but predates the latest substrate change) is a Layer-2
		// follow-up; the prototype distinguishes only missing vs current.
		rep.ReflectionStatus = "missing"
		rep.Nudge = "No reflection yet for this window — run a klyne worklog reflection to enrich these entries. Showing the deterministic factual summary."
	}
	return rep, nil
}

// buildSessionStats builds the per-session proof-of-work evidence list
// (Change 2): one SessionStat per contributing session, sorted by
// StartedAt. Each carries its own gap-capped active total AND the
// gap-capped active sub-intervals behind it (ActiveIntervals) — the
// deterministic minutes/intervals that, unioned, produce the headline
// number. The dashboard timeline draws the SAME intervals so the
// concurrency breakdown sums to TotalActiveMinutes.
//
// Sessions with no measurable activity (fewer than two in-window
// timestamps → no span) are still listed: they are honest evidence that
// a session ran even if it contributes 0 active minutes. The repo is the
// canonical repo name the session is attributed to, derived from its
// project_path via the same canonicalization the Services use.
func buildSessionStats(sessions []SessionActivity, projectPaths map[string]string) []SessionStat {
	out := make([]SessionStat, 0, len(sessions))
	for _, s := range sessions {
		started, ended := messageSpan(s.MessageTimes)
		count := s.MessageCount
		if count == 0 {
			count = len(s.MessageTimes)
		}
		// Canonicalize the session's project_path to the repo it rolls
		// into, matching Service.Repo (worktrees collapse to one repo).
		pp := s.ProjectPath
		if canon, ok := projectPaths[pp]; ok && canon != "" {
			pp = canon
		}
		out = append(out, SessionStat{
			SessionID:       s.SessionID,
			CLI:             s.CLI,
			Repo:            RepoName(pp),
			StartedAt:       started,
			EndedAt:         ended,
			ActiveMinutes:   SessionActiveMinutes(s.MessageTimes, idleCapMinutes),
			ActiveIntervals: SessionActiveIntervals(s.MessageTimes, idleCapMinutes),
			MessageCount:    count,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartedAt.Before(out[j].StartedAt)
	})
	return out
}

// messageSpan returns the earliest and latest timestamp in times. Both
// are zero when times is empty.
func messageSpan(times []time.Time) (first, last time.Time) {
	for _, t := range times {
		if first.IsZero() || t.Before(first) {
			first = t
		}
		if last.IsZero() || t.After(last) {
			last = t
		}
	}
	return first, last
}

// buildBranches groups the user's commits by branch (§6.6). When commits
// carry no BranchName (the normal single-branch ScanRepo path) they all
// roll up under ScanResult.Branch. Branches are ordered by the salience
// rule (§7.1 rule 5): highest commit count first, then most net-new
// insertions, so the dashboard leads with the highest-impact work — the
// fix for the documented salience-inversion failure.
func buildBranches(sc ScanResult, userCommits []Commit, rt RepoTime) []Branch {
	groups := map[string][]Commit{}
	var order []string
	for _, c := range userCommits {
		bn := c.BranchName
		if bn == "" {
			bn = sc.Branch
		}
		if _, ok := groups[bn]; !ok {
			order = append(order, bn)
		}
		groups[bn] = append(groups[bn], c)
	}
	if len(groups) == 0 {
		return nil
	}

	branches := make([]Branch, 0, len(groups))
	for _, name := range order {
		cs := groups[name]
		first, last := commitSpan(cs)
		b := Branch{
			Name:              name,
			TicketID:          ticketFor(name, sc),
			Ship:              sc.Ship,
			Ahead:             sc.Ahead,
			Behind:            sc.Behind,
			Commits:           cs,
			AttributedMinutes: rt.AIMinutes,
			FirstCommitAt:     first,
			LastCommitAt:      last,
			ShipSpanMinutes:   shipSpanMinutes(cs),
		}
		b.Narrative = layer1Narrative(b, rt)
		branches = append(branches, b)
	}

	sortBranches(branches)
	return branches
}

// sortBranches applies the §7.1 rule-5 salience ordering: highest commit
// count first, then most net-new insertions, so the dashboard leads with
// the highest-impact work (the documented salience-inversion fix). It is
// applied both per-scan and across the merged per-repo branch set.
func sortBranches(branches []Branch) {
	sort.SliceStable(branches, func(i, j int) bool {
		if len(branches[i].Commits) != len(branches[j].Commits) {
			return len(branches[i].Commits) > len(branches[j].Commits)
		}
		return netNew(branches[i].Commits) > netNew(branches[j].Commits)
	})
}

func ticketFor(branch string, sc ScanResult) string {
	if branch == sc.Branch && sc.TicketID != "" {
		return sc.TicketID
	}
	if m := ticketRe.FindString(branch); m != "" {
		return strings.ToUpper(m)
	}
	return ""
}

func netNew(cs []Commit) int {
	n := 0
	for _, c := range cs {
		n += c.Insertions - c.Deletions
	}
	return n
}

// riskSignals derives the two current-state risks (§6.5):
//   - unpushed:        Ahead>0 and not pushed/merged; age = since oldest
//     in-window unpushed commit.
//   - done-uncommitted: a recent substantive session ended with a dirty
//     tree and no commit after it; age = now − session end. Surfaced
//     only; NEVER added to work-time (D4).
func riskSignals(sc ScanResult, userCommits []Commit, dirty DirtyState, now time.Time) []RiskSignal {
	var risks []RiskSignal

	if sc.Ahead > 0 && sc.Ship == ShipLocal {
		age := 0
		oldest := oldestCommitTime(userCommits)
		if !oldest.IsZero() {
			age = minutesBetween(oldest, now)
		}
		commits := sc.AheadCommits
		if commits == nil {
			commits = []RiskCommit{}
		}
		risks = append(risks, RiskSignal{
			Kind:         "unpushed",
			Detail:       fmt.Sprintf("%d commit(s) ahead, not on origin", sc.Ahead),
			AgeMinutes:   age,
			Branch:       sc.Branch,
			WorktreePath: sc.Dir,
			Commits:      commits,
			Files:        []string{},
		})
	}

	if dirty.DirtyFileCount > 0 && !dirty.SessionEnd.IsZero() {
		committedAfter := false
		for _, c := range userCommits {
			if c.CommittedAt.After(dirty.SessionEnd) {
				committedAfter = true
				break
			}
		}
		if !committedAfter {
			files := dirty.DirtyFiles
			if files == nil {
				files = []string{}
			}
			risks = append(risks, RiskSignal{
				Kind:         "done-uncommitted",
				Detail:       fmt.Sprintf("%d uncommitted file(s) since last session ended", dirty.DirtyFileCount),
				AgeMinutes:   minutesBetween(dirty.SessionEnd, now),
				Branch:       sc.Branch,
				WorktreePath: sc.Dir,
				Commits:      []RiskCommit{},
				Files:        files,
			})
		}
	}
	return risks
}

// layer1Narrative builds the deterministic Layer-1 "what I did" string
// (§7 L1) with fmt only — NO LLM. Anti-hallucination contract (§7.1):
// it cites short SHAs, quotes the deterministic minutes/ship-state
// verbatim, leads with the highest-impact commits, and structurally
// cannot emit "PR #" / invented ticket numbers (it only ever interpolates
// the raw branch-derived TicketID and real short SHAs).
func layer1Narrative(b Branch, rt RepoTime) string {
	if len(b.Commits) == 0 {
		return ""
	}
	ranked := make([]Commit, len(b.Commits))
	copy(ranked, b.Commits)
	sort.SliceStable(ranked, func(i, j int) bool {
		return (ranked[i].Insertions - ranked[i].Deletions) > (ranked[j].Insertions - ranked[j].Deletions)
	})

	const maxSubjects = 4
	subjects := make([]string, 0, maxSubjects)
	for i, c := range ranked {
		if i >= maxSubjects {
			break
		}
		subjects = append(subjects, fmt.Sprintf("%s (%s)", c.Subject, c.SHA))
	}
	more := ""
	if len(ranked) > maxSubjects {
		more = fmt.Sprintf(", +%d more", len(ranked)-maxSubjects)
	}

	ticket := ""
	if b.TicketID != "" {
		ticket = " [" + b.TicketID + "]"
	}
	approx := ""
	if rt.Split {
		approx = "≈ "
	}

	return fmt.Sprintf("%d commit(s) on `%s`%s (%s): %s%s — %s%dm AI session time.",
		len(b.Commits),
		b.Name,
		ticket,
		b.Ship,
		strings.Join(subjects, "; "),
		more,
		approx,
		b.AttributedMinutes,
	)
}

func (in ReportInput) projectPath(dir string) string {
	if p, ok := in.ProjectPaths[dir]; ok && p != "" {
		return p
	}
	return dir
}

// commitSpan returns the earliest and latest commit CommittedAt across
// cs (zero-valued times are ignored). Both are zero when cs has no
// timestamped commits.
func commitSpan(cs []Commit) (first, last time.Time) {
	for _, c := range cs {
		if c.CommittedAt.IsZero() {
			continue
		}
		if first.IsZero() || c.CommittedAt.Before(first) {
			first = c.CommittedAt
		}
		if last.IsZero() || c.CommittedAt.After(last) {
			last = c.CommittedAt
		}
	}
	return first, last
}

// shipSpanMinutes is the honest "work span / time to ship" proxy
// (Change 3): minutes between the earliest and latest commit on the
// branch. It is 0 when the branch has fewer than 2 timestamped commits
// — a single commit has no span.
func shipSpanMinutes(cs []Commit) int {
	timestamped := 0
	for _, c := range cs {
		if !c.CommittedAt.IsZero() {
			timestamped++
		}
	}
	if timestamped < 2 {
		return 0
	}
	first, last := commitSpan(cs)
	return minutesBetween(first, last)
}

func oldestCommitTime(cs []Commit) time.Time {
	var oldest time.Time
	for _, c := range cs {
		if c.CommittedAt.IsZero() {
			continue
		}
		if oldest.IsZero() || c.CommittedAt.Before(oldest) {
			oldest = c.CommittedAt
		}
	}
	return oldest
}

func minutesBetween(from, to time.Time) int {
	d := to.Sub(from)
	if d < 0 {
		return 0
	}
	return int(d.Minutes())
}

// dayTime parses the YYYY-MM-DD day string; falls back to now's date so
// the reflection lookup always gets a sane anchor.
func dayTime(day string, now time.Time) time.Time {
	if t, err := time.Parse("2006-01-02", day); err == nil {
		return t
	}
	return now
}
