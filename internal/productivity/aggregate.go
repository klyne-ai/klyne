package productivity

import (
	"sort"
	"time"
)

// AggregateReports composes one multi-day Report from N single-day
// reports — the read-path counterpart to the per-day snapshots written
// by the reflection recorder + handler backfill. Inputs are typically
// the JSON payloads unmarshalled out of daily_productivity_snapshot
// rows for the requested window.
//
// Time semantics (§6.4 union, NOT sum):
//   - TotalActiveMinutes is the sweep-line union of every Session's
//     ActiveIntervals across ALL days. A naive sum of per-day totals
//     would over-count the (rare) case where a single session spans
//     midnight, AND would conflate the headline with the per-day
//     concurrency the dashboard's timeline draws. The union preserves
//     the §6.4 "real wall-clock" semantics across the window.
//   - MinutesByCLI is the per-CLI sweep-line union, same logic.
//
// Service merging:
//   - Keyed by ProjectPath (canonical repo root). Branches are unioned
//     by Name (later wins for ship facts since later-day snapshots
//     reflect the latest ship state).
//   - Risks are unioned by (Kind, Branch, WorktreePath) so the same
//     "done-uncommitted on branch X" doesn't appear N times.
//   - MergedPRs are unioned by Number.
//   - MinutesByCLI per service is summed across days (per-service is a
//     daily union already; cross-day sum is correct because no single
//     repo's daily union double-counts another day's union).
//   - GitFetchedAt: latest of any contributing day.
//   - ReflectionMarkdown / ReflectionGroups: concatenated by day, oldest
//     day first, with a YYYY-MM-DD header before each non-empty body so
//     the user can see what each day's reflection said.
//
// Reflection status across the window:
//   - "current"  — every day has at least one reflection group.
//   - "missing"  — no day has any reflection group.
//   - "stale"    — some days have one, others don't.
//
// `day` is the label written into the composite Report.Day field — the
// handler passes the requested range's start day so existing UI code
// that reads `rep.day` still gets a meaningful local-date string.
func AggregateReports(reports []Report, day string) Report {
	if len(reports) == 0 {
		return Report{
			Day:          day,
			Services:     []Service{},
			MinutesByCLI: map[string]int{},
			Sessions:     []SessionStat{},
			// An empty window means we have nothing to report — treat as
			// missing so the UI shows the no-reflection nudge rather than
			// claiming a current snapshot exists.
			ReflectionStatus: "missing",
			Nudge:            "Run /klyne:reflect to populate this window.",
		}
	}

	out := Report{
		Day:          day,
		Services:     []Service{},
		MinutesByCLI: map[string]int{},
		Sessions:     []SessionStat{},
	}

	// ---- Sessions: union per SessionID across days ----------------------
	// A session that spans midnight appears in multiple per-day Reports,
	// each carrying only that day's slice of intervals. The previous
	// last-wins dedupe silently dropped the earlier day's intervals, so
	// the headline TotalActiveMinutes under-counted by that morning. The
	// fix is to merge: concat every day's intervals + extend
	// StartedAt/EndedAt to the outer envelope + sum MessageCount + sum
	// ActiveMinutes (then trust the sweep-line union below to recompute
	// the global headline correctly).
	sessionByID := map[string]*SessionStat{}
	sessionOrder := []string{}
	for _, r := range reports {
		for _, s := range r.Sessions {
			existing, ok := sessionByID[s.SessionID]
			if !ok {
				dup := s
				dup.ActiveIntervals = append([]ActiveInterval(nil), s.ActiveIntervals...)
				sessionByID[s.SessionID] = &dup
				sessionOrder = append(sessionOrder, s.SessionID)
				continue
			}
			existing.ActiveIntervals = append(existing.ActiveIntervals, s.ActiveIntervals...)
			existing.MessageCount += s.MessageCount
			if s.StartedAt.Before(existing.StartedAt) || existing.StartedAt.IsZero() {
				existing.StartedAt = s.StartedAt
			}
			if s.EndedAt.After(existing.EndedAt) {
				existing.EndedAt = s.EndedAt
			}
		}
	}
	for _, id := range sessionOrder {
		s := sessionByID[id]
		// Per-session ActiveMinutes is the union of its (now merged)
		// intervals — keeps the invariant that interval lengths sum to
		// SessionActiveMinutes, even after a cross-day merge.
		s.ActiveMinutes = UnionMinutes(s.ActiveIntervals)
		out.Sessions = append(out.Sessions, *s)
	}
	sort.Slice(out.Sessions, func(i, j int) bool {
		return out.Sessions[i].StartedAt.Before(out.Sessions[j].StartedAt)
	})

	// ---- Total/per-CLI minutes: sweep-line union of every session's
	// ActiveIntervals across the window. Implementation: flatten every
	// (start, end) pair from every session into one interval list, sort,
	// merge overlaps, sum. For per-CLI, partition by CLI before merge.
	allIntervals := make([]ActiveInterval, 0)
	cliIntervals := map[string][]ActiveInterval{}
	for _, s := range out.Sessions {
		for _, iv := range s.ActiveIntervals {
			if !iv.End.After(iv.Start) {
				continue
			}
			allIntervals = append(allIntervals, iv)
			cliIntervals[s.CLI] = append(cliIntervals[s.CLI], iv)
		}
	}
	out.TotalActiveMinutes = UnionMinutes(allIntervals)
	for cli, ivs := range cliIntervals {
		m := UnionMinutes(ivs)
		if m > 0 {
			out.MinutesByCLI[cli] = m
		}
	}

	// ---- Services: merge per-ProjectPath ----------------------------------
	type accum struct {
		svc         Service
		branchOrder []string
		branches    map[string]Branch
		riskKeys    map[string]struct{}
		prByNumber  map[int]MergedPR
	}
	order := []string{}
	bag := map[string]*accum{}
	for _, r := range reports {
		for _, s := range r.Services {
			key := s.ProjectPath
			a, ok := bag[key]
			if !ok {
				a = &accum{
					svc: Service{
						Repo:         s.Repo,
						ProjectPath:  s.ProjectPath,
						MinutesByCLI: map[string]int{},
						Branches:     []Branch{},
						Risks:        []RiskSignal{},
						MergedPRs:    []MergedPR{},
						PullRequests: []PullRequest{},
						OpenItems:    []OpenWorkItem{},
					},
					branches:   map[string]Branch{},
					riskKeys:   map[string]struct{}{},
					prByNumber: map[int]MergedPR{},
				}
				bag[key] = a
				order = append(order, key)
			}
			// ManualOnly: a single day with AI activity flips the whole
			// window to non-manual-only.
			if !s.ManualOnly {
				a.svc.ManualOnly = false
			}
			// Per-service per-CLI minutes — daily values are themselves
			// unions; summing across distinct days is correct.
			for cli, m := range s.MinutesByCLI {
				a.svc.MinutesByCLI[cli] += m
			}
			// Branches — keep the LAST observed copy so ship-state /
			// ahead-behind reflect the most recent day in the window.
			for _, b := range s.Branches {
				if _, seen := a.branches[b.Name]; !seen {
					a.branchOrder = append(a.branchOrder, b.Name)
				}
				a.branches[b.Name] = b
			}
			// Risks — dedupe by (Kind, Branch, WorktreePath).
			for _, rk := range s.Risks {
				k := rk.Kind + "\x00" + rk.Branch + "\x00" + rk.WorktreePath
				if _, dup := a.riskKeys[k]; dup {
					continue
				}
				a.riskKeys[k] = struct{}{}
				a.svc.Risks = append(a.svc.Risks, rk)
			}
			// MergedPRs — dedupe by Number, keep the row with the latest
			// MergedAt (defensive — should be identical across days).
			for _, pr := range s.MergedPRs {
				if existing, ok := a.prByNumber[pr.Number]; !ok || pr.MergedAt.After(existing.MergedAt) {
					a.prByNumber[pr.Number] = pr
				}
			}
			// MergedPRsAsOf / GitFetchedAt: latest wins.
			if s.MergedPRsAsOf.After(a.svc.MergedPRsAsOf) {
				a.svc.MergedPRsAsOf = s.MergedPRsAsOf
			}
			if s.GitFetchedAt.After(a.svc.GitFetchedAt) {
				a.svc.GitFetchedAt = s.GitFetchedAt
			}
			// Preserve every day's reconciled outcomes. Picking only the
			// richest card dropped valid work from multi-day windows.
			a.svc.WhatWasDone = MergeWhatWasDoneCards(a.svc.WhatWasDone, s.WhatWasDone)
		}
	}

	// Concatenate per-day reflection markdown per service, oldest day
	// first. Build a stable [dayString → service-body-on-that-day] map so
	// we can render `## YYYY-MM-DD\n<body>` headers.
	perServiceReflections := map[string][]dayBody{}
	perServiceGroups := map[string][]ReflectionGroup{}
	for _, r := range reports {
		for _, s := range r.Services {
			if body := s.ReflectionMarkdown; body != "" {
				perServiceReflections[s.ProjectPath] = append(perServiceReflections[s.ProjectPath], dayBody{day: r.Day, body: body})
			}
			perServiceGroups[s.ProjectPath] = append(perServiceGroups[s.ProjectPath], s.ReflectionGroups...)
		}
	}

	for _, key := range order {
		a := bag[key]
		for _, name := range a.branchOrder {
			a.svc.Branches = append(a.svc.Branches, a.branches[name])
		}
		for _, pr := range a.prByNumber {
			a.svc.MergedPRs = append(a.svc.MergedPRs, pr)
		}
		sort.Slice(a.svc.MergedPRs, func(i, j int) bool {
			return a.svc.MergedPRs[i].MergedAt.After(a.svc.MergedPRs[j].MergedAt)
		})
		// Reflection markdown — concat with day headers when window > 1 day.
		if bodies := perServiceReflections[key]; len(bodies) > 0 {
			a.svc.ReflectionMarkdown = concatDayBodies(bodies)
		}
		if groups := perServiceGroups[key]; len(groups) > 0 {
			sort.Slice(groups, func(i, j int) bool { return groups[i].TS < groups[j].TS })
			a.svc.ReflectionGroups = groups
		}
		out.Services = append(out.Services, a.svc)
	}

	// ---- Report-level reflection status across the window ---------------
	withRefl := 0
	for _, r := range reports {
		if hasAnyReflection(r) {
			withRefl++
		}
	}
	switch {
	case withRefl == len(reports):
		out.ReflectionStatus = "current"
	case withRefl == 0:
		out.ReflectionStatus = "missing"
		out.Nudge = "Run /klyne:reflect to populate this window."
	default:
		out.ReflectionStatus = "stale"
		out.Nudge = "Some days in this window have no reflection yet — run /klyne:reflect to fill the gaps."
	}

	// Report-level ReflectionMarkdown / ReflectionGroups: union across
	// services. The dashboard's report-level fields are optional fallbacks
	// (per-service ones carry the bulk); keep ReflectionGroups concatenated
	// so the global timeline shows every group chronologically.
	var allGroups []ReflectionGroup
	for _, s := range out.Services {
		allGroups = append(allGroups, s.ReflectionGroups...)
	}
	if len(allGroups) > 0 {
		sort.Slice(allGroups, func(i, j int) bool { return allGroups[i].TS < allGroups[j].TS })
		out.ReflectionGroups = allGroups
	}

	return out
}

// hasAnyReflection — true when the day's report carries at least one
// reflection group on any service or at the report level. Drives the
// per-window "current / stale / missing" verdict.
func hasAnyReflection(r Report) bool {
	if len(r.ReflectionGroups) > 0 {
		return true
	}
	for _, s := range r.Services {
		if len(s.ReflectionGroups) > 0 || s.ReflectionMarkdown != "" {
			return true
		}
	}
	return false
}

// UnionMinutes sums the union (not the sum) of an interval set in
// minutes, using a standard sweep-line: sort by start, merge any
// interval whose start is ≤ the current end, otherwise close the
// running interval and open a new one. Result rounded to the nearest
// minute — matches the per-session ActiveMinutes rounding so the
// composite headline lines up with the timeline.
func UnionMinutes(ivs []ActiveInterval) int {
	if len(ivs) == 0 {
		return 0
	}
	sorted := make([]ActiveInterval, len(ivs))
	copy(sorted, ivs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start.Before(sorted[j].Start) })
	total := time.Duration(0)
	cur := sorted[0]
	for i := 1; i < len(sorted); i++ {
		next := sorted[i]
		if !next.Start.After(cur.End) {
			if next.End.After(cur.End) {
				cur.End = next.End
			}
			continue
		}
		total += cur.End.Sub(cur.Start)
		cur = next
	}
	total += cur.End.Sub(cur.Start)
	return int((total + 30*time.Second) / time.Minute)
}

// concatDayBodies renders one body per day, oldest first, with a small
// `### YYYY-MM-DD` header so the user can see which day each section
// belongs to. The window's a single day → return the bare body (no
// redundant header).
func concatDayBodies(bodies []dayBody) string {
	if len(bodies) == 1 {
		return bodies[0].body
	}
	sort.Slice(bodies, func(i, j int) bool { return bodies[i].day < bodies[j].day })
	out := ""
	for i, b := range bodies {
		if i > 0 {
			out += "\n\n"
		}
		out += "### " + b.day + "\n\n" + b.body
	}
	return out
}

// dayBody is a (day, body) pair used during reflection concatenation in
// AggregateReports. Declared as a package-private type so the slice
// helpers above stay strongly typed.
type dayBody struct {
	day  string
	body string
}
