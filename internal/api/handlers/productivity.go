package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/productivity"
	"github.com/klyne-ai/klyne/internal/store"
)

// ProductivityHandler serves GET /productivity — the deterministic-first
// AI productivity dashboard (spec
// docs/superpowers/specs/2026-05-19-ai-productivity-dashboard-design.md).
//
// It fuses live git activity with klyne session telemetry into a
// Service→Branch→Topic record. Per the spec's hard determinism boundary
// (§7.1) NOTHING here calls an LLM: every number, ship state, and the
// Layer-1 narrative is computed from git + the sessions/messages tables.
// The reflection layer only enriches (L2) — its absence surfaces a nudge
// (L3), it never gates or destabilizes the deterministic data (D8).
//
// Snapshot-backed determinism (migration 021): past-day data is read
// from daily_productivity_snapshot rows so a reload at 4:01pm shows
// the same numbers as the same reload at 4:00pm. The reflection writer
// is the authoritative source ("reflection" rows); the handler lazily
// backfills "live" rows for past days the user has not yet reflected.
// Today is always live-computed — today is not "past" until it ends.
type ProductivityHandler struct {
	db *store.DB
}

// ProductivityDatesResponse is the small index the UI uses to bound the
// explicit calendar picker. Days are local calendar dates with at least one
// session row ending on that day.
type ProductivityDatesResponse struct {
	Days   []string `json:"days"`
	MinDay string   `json:"min_day,omitempty"`
	MaxDay string   `json:"max_day,omitempty"`
}

// NewProductivityHandler constructs a ProductivityHandler.
func NewProductivityHandler(db *store.DB) *ProductivityHandler {
	return &ProductivityHandler{db: db}
}

// Dates handles GET /api/productivity/dates.
func (h *ProductivityHandler) Dates(w http.ResponseWriter, r *http.Request) {
	const q = `
SELECT date(last_msg_at / 1000, 'unixepoch', 'localtime') AS day
FROM sessions
WHERE last_msg_at > 0
GROUP BY day
ORDER BY day ASC`

	rows, err := h.db.Read().QueryContext(r.Context(), q)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close() //nolint:errcheck

	resp := ProductivityDatesResponse{}
	for rows.Next() {
		var day string
		if err := rows.Scan(&day); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if day == "" {
			continue
		}
		resp.Days = append(resp.Days, day)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(resp.Days) > 0 {
		resp.MinDay = resp.Days[0]
		resp.MaxDay = resp.Days[len(resp.Days)-1]
	}

	writeJSON(w, http.StatusOK, resp)
}

// productivityIdleCapMin is the §6.4 idle-gap cap: when the gap between
// two consecutive messages exceeds this, the whole gap is nullified and
// counts as zero productive time. 10 min — longer gaps reliably mean
// the user stepped away; a 30-min cap inflated the day's total.
const productivityIdleCapMin = 10

// Get handles GET /productivity.
//
// Query params:
//   - since int64  epoch-ms lower bound (default = today local 00:00)
//   - until int64  epoch-ms upper bound (default = now)
//   - refresh "1"  force live recompute + overwrite snapshot rows
//
// Behaviour: the window is split into local-day buckets. Past days
// prefer the per-project snapshot rows in daily_productivity_snapshot
// (written by the reflection recorder, or lazy-backfilled on first
// read). Today is always live. The composite response is the
// sweep-line union of every per-day Report (productivity.AggregateReports).
func (h *ProductivityHandler) Get(w http.ResponseWriter, r *http.Request) {
	now := time.Now()

	defStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	sinceMs, err := queryInt64(r, "since", defStart.UnixMilli())
	if err != nil || sinceMs < 0 {
		http.Error(w, "invalid since: must be a non-negative epoch-ms integer", http.StatusBadRequest)
		return
	}
	untilMs, err := queryInt64(r, "until", now.UnixMilli())
	if err != nil || untilMs < 0 {
		http.Error(w, "invalid until: must be a non-negative epoch-ms integer", http.StatusBadRequest)
		return
	}

	since := time.UnixMilli(sinceMs)
	until := time.UnixMilli(untilMs)
	force := r.URL.Query().Get("refresh") == "1"
	ctx := r.Context()

	days := localDaysInRange(since, until)
	todayStr := now.Local().Format("2006-01-02")

	perDay := make([]productivity.Report, 0, len(days))
	for _, dayStart := range days {
		dayStr := dayStart.Format("2006-01-02")
		dayEnd := dayStart.Add(24 * time.Hour).Add(-time.Millisecond)
		// Clamp the per-day window to the caller's requested bounds so a
		// request like "today since 10am" doesn't fold midnight→10am
		// minutes the user didn't ask for. Today's tail is clamped to
		// now so the live scan doesn't query the future.
		winStart := dayStart
		if winStart.Before(since) {
			winStart = since
		}
		winEnd := dayEnd
		if winEnd.After(until) {
			winEnd = until
		}
		if dayStr == todayStr && winEnd.After(now) {
			winEnd = now
		}
		if !winStart.Before(winEnd) {
			// Empty intersection (e.g. dayStart > until) — skip.
			continue
		}

		isToday := dayStr == todayStr

		// Past days: snapshot-preferred. Today: always live.
		if !isToday && !force {
			if rep, ok, err := h.tryReadSnapshotDay(ctx, dayStr); err == nil && ok {
				perDay = append(perDay, rep)
				continue
			} else if err != nil {
				log.Printf("productivity: snapshot read for %s failed: %v", dayStr, err)
				// fall through to live compute
			}
		}

		rep, err := h.computeLiveReport(ctx, winStart, winEnd, force)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Force the per-day Report's Day field to this day's local date —
		// computeLiveReport uses the WINDOW's start which is the same
		// here, but pinning it makes the snapshot we write below
		// unambiguously a "this day's snapshot."
		rep.Day = dayStr

		// Lazy-compose the typed What-was-done cards from the day's
		// worklog_reflections rows (§1.2). Same composer as the
		// snapshot-read path so today's live render and tomorrow's
		// snapshot read both produce identical cards for the same input.
		hydrateWhatWasDone(ctx, h.db, &rep, dayStr)

		// Persist a per-project snapshot for every past day. Today is
		// never persisted — it's not done yet, and persisting now would
		// let a same-day reload read a stale row instead of recomputing.
		if !isToday {
			h.persistSnapshotsForDay(ctx, rep, dayStr, "live")
		}

		perDay = append(perDay, rep)
	}

	// Compose the window. Single-day request → return the day's report
	// directly so the response shape is identical to the legacy single-
	// day path (Day = the local date string). Multi-day → aggregate.
	var composite productivity.Report
	if len(perDay) == 1 {
		composite = perDay[0]
	} else {
		composite = productivity.AggregateReports(perDay, since.Local().Format("2006-01-02"))
	}

	// Layer-2 enrichments — applied ONCE per request against the union
	// window (not per-day, the previous code path). Merged-PR data is
	// TTL-cached via github_pr_cache so a hot reload is a no-op; the
	// per-day loop previously hit 7 cache keys for a 7-day window and
	// triggered 7 cold `gh pr list` invocations on a miss. GitFetchedAt
	// is the current local FETCH_HEAD mtime — a "live state" fact
	// that should never have been persisted in per-day snapshots.
	h.enrichMergedPRs(ctx, &composite, since, until, force)
	for i := range composite.Services {
		composite.Services[i].GitFetchedAt = gitFetchedAt(composite.Services[i].ProjectPath)
	}

	// Pending-entries count: stop_summaries written across every service
	// in the response whose ts is past that project's latest reflection
	// cursor AND falls inside the visible window. Drives the dashboard's
	// "Sync productivity dashboard" highlight + "N new" badge.
	composite.PendingEntries = h.countPendingEntries(ctx, composite.Services, sinceMs, untilMs)

	// Pending-compile count: services that have at least one typed
	// reflection but whose what_was_done card is NOT llm_compiled.
	// Drives the dashboard's "Generate productivity" button.
	composite.PendingCompile = countPendingCompile(composite.Services)

	writeJSON(w, http.StatusOK, composite)
}

// countPendingCompile returns the number of services in svcs that have
// at least one typed reflection (signalled by a non-nil what_was_done
// derived from typed body_json rows) but whose what_was_done.LLMCompiled
// is false — i.e. /klyne:productivity-sync hasn't run for them yet.
//
// Services with WhatWasDone == nil are skipped: nothing to compile.
func countPendingCompile(svcs []productivity.Service) int {
	n := 0
	for _, s := range svcs {
		if s.WhatWasDone == nil {
			continue
		}
		if !s.WhatWasDone.LLMCompiled {
			n++
		}
	}
	return n
}

// countPendingEntries returns the number of visible stop_summaries
// rows across all services in svcs whose ts is greater than that
// project's latest reflection cursor (MaxReflectionCursor) AND whose
// ts falls inside [sinceMs, untilMs). Failures degrade to 0 so the UI
// never blocks on a slow count.
func (h *ProductivityHandler) countPendingEntries(
	ctx context.Context, svcs []productivity.Service, sinceMs, untilMs int64,
) int {
	if len(svcs) == 0 {
		return 0
	}
	seen := make(map[string]struct{}, len(svcs))
	total := 0
	for _, s := range svcs {
		if s.ProjectPath == "" {
			continue
		}
		if _, dup := seen[s.ProjectPath]; dup {
			continue
		}
		seen[s.ProjectPath] = struct{}{}
		cursor, err := store.MaxReflectionCursor(ctx, h.db, s.ProjectPath)
		if err != nil {
			continue
		}
		var n int
		err = h.db.Read().QueryRowContext(ctx, `
            SELECT COUNT(*) FROM stop_summaries
            WHERE project_path = ?
              AND recap_visible = 1
              AND ts > ?
              AND ts >= ? AND ts < ?`,
			s.ProjectPath, cursor, sinceMs, untilMs,
		).Scan(&n)
		if err != nil {
			continue
		}
		total += n
	}
	return total
}

// computeLiveReport runs the full live-scan pipeline for one (since,
// until) window — repo discovery, git scan, session activity, time
// attribution, BuildReport, and the Layer-2 enrichments
// (merged-PR + git_fetched_at). Used for today (always) and for past
// days that have no snapshot yet (lazy backfill).
func (h *ProductivityHandler) computeLiveReport(
	ctx context.Context, since, until time.Time, force bool,
) (productivity.Report, error) {
	now := time.Now()

	// §6.1 / D5: discover the repos to scan from the session
	// project_paths in the window (+ sibling worktrees). Discovery
	// failures degrade gracefully to an empty report rather than 500.
	lister := &sessionPathLister{db: h.db}
	targets, err := productivity.DiscoverRepos(ctx, lister, since, until)
	if err != nil {
		return productivity.Report{}, err
	}

	userEmails := productivity.UserEmails()

	// Refresh stale origin/* refs BEFORE scanning so ahead/behind and
	// ship-state read current GitHub state, not a stale mirror.
	dirs := make([]string, 0, len(targets))
	for _, t := range targets {
		dirs = append(dirs, t.Dir)
	}
	refreshStaleRemotes(ctx, dirs, prCacheTTL(), force)

	var scans []productivity.ScanResult
	commitCounts := map[string]int{}
	projectPaths := map[string]string{}
	dirtyRepos := map[string]productivity.DirtyState{}

	sessionEnds, err := h.sessionEnds(ctx, since, until)
	if err != nil {
		return productivity.Report{}, err
	}

	for _, t := range targets {
		sc, err := productivity.ScanRepo(t.Dir, since, until, userEmails)
		if err != nil {
			continue
		}
		scans = append(scans, sc)
		commitCounts[sc.Dir] = len(sc.Commits)
		projectPaths[sc.Dir] = t.ProjectPath

		dirtyCount, dirtyFiles := dirtyStatus(t.Dir)
		dirtyRepos[sc.Dir] = productivity.DirtyState{
			DirtyFileCount: dirtyCount,
			DirtyFiles:     dirtyFiles,
			SessionEnd:     sessionEnds[t.ProjectPath],
		}
	}

	sessions, err := h.sessionActivity(ctx, since, until)
	if err != nil {
		return productivity.Report{}, err
	}
	attribution := productivity.AttributeMinutes(sessions, commitCounts, productivityIdleCapMin)

	in := productivity.ReportInput{
		Day:          since.Local().Format("2006-01-02"),
		Now:          now,
		Scans:        scans,
		Attribution:  attribution,
		ProjectPaths: projectPaths,
		DirtyRepos:   dirtyRepos,
		Sessions:     sessions,
	}

	rep, err := productivity.BuildReport(ctx, in, &reflectionLookup{db: h.db})
	if err != nil {
		return productivity.Report{}, err
	}

	// NOTE: Layer-2 GitHub enrichment (merged_prs) and the current
	// FETCH_HEAD mtime (git_fetched_at) are NOT applied here. They are
	// derived once at the end of Get() against the union window so a
	// 7-day "Last Week" cold backfill triggers ONE `gh` call per slug
	// instead of 7. Keeping them out of computeLiveReport also keeps
	// snapshots free of external/time-derived data — a snapshot stores
	// only what is locally deterministic for the day.
	return rep, nil
}

// tryReadSnapshotDay loads every per-project snapshot row for one
// local day and aggregates them into one multi-project Report
// (productivity.AggregateReports with a single-day input). Returns
// ok=false when no rows exist for the day — the caller falls through
// to live compute + backfill.
//
// Reflection groups are deliberately NOT trusted from the persisted
// payload — they are re-hydrated from worklog_reflections on every
// read. The snapshot writer in worklog.recordReflection serialises the
// substrate Report BEFORE the new reflection row is inserted, so a
// freshly-written snapshot for a catch-up day has reflection_status
// "missing" / empty ReflectionGroups even though the row exists. And
// even when the snapshot was fresh, a LATER reflection for the same
// day would not rewrite it. Treating worklog_reflections as the source
// of truth at read time keeps the dashboard correct without a
// rewrite-on-every-reflection invariant.
func (h *ProductivityHandler) tryReadSnapshotDay(
	ctx context.Context, dayStr string,
) (productivity.Report, bool, error) {
	rows, err := store.ListDailyProductivitySnapshotsForDay(ctx, h.db, dayStr)
	if err != nil {
		return productivity.Report{}, false, err
	}
	if len(rows) == 0 {
		return productivity.Report{}, false, nil
	}
	perProject := make([]productivity.Report, 0, len(rows))
	for _, row := range rows {
		var rep productivity.Report
		if err := json.Unmarshal([]byte(row.PayloadJSON), &rep); err != nil {
			log.Printf("productivity: skip corrupt snapshot %s/%s: %v", row.ProjectPath, row.Day, err)
			continue
		}
		if err := h.hydrateSnapshotReflections(ctx, &rep, dayStr); err != nil {
			log.Printf("productivity: hydrate reflections for %s/%s: %v", row.ProjectPath, dayStr, err)
		}
		perProject = append(perProject, rep)
	}
	if len(perProject) == 0 {
		return productivity.Report{}, false, nil
	}
	// Aggregating per-project single-day Reports yields one multi-project
	// single-day Report — same shape as the live path would produce.
	day := productivity.AggregateReports(perProject, dayStr)
	day.Day = dayStr
	return day, true, nil
}

// hydrateSnapshotReflections refreshes the per-service ReflectionGroups,
// ReflectionMarkdown, and the report-level ReflectionStatus on a
// deserialised single-project snapshot Report by looking up the current
// worklog_reflections rows for (project_path, day). Mirrors what
// productivity.BuildReport does on the live path — exact same lookup
// adapter and same "any group → current" semantics — so a snapshot
// read is indistinguishable from a live recompute for the reflection
// layer.
//
// Also lazy-composes the typed What-was-done cards (§1.2 of
// docs/plan/2026-05-26-wwd-typed-cards.md) by running
// productivity.ComposeWWD over the same per-day reflection rows. A
// persisted snapshot may already carry a Service.WhatWasDone pointer
// (the reflection-recorder writes it on commit) — re-composing on read
// keeps the panel honest if any reflection row was deleted or edited
// (catch-up reflects).
//
// dayStr is parsed in the local zone to match the recorder's day
// labelling (reflection_recorder.go uses day.Local() since the fix in
// migration 022).
func (h *ProductivityHandler) hydrateSnapshotReflections(
	ctx context.Context, rep *productivity.Report, dayStr string,
) error {
	day, err := time.ParseInLocation("2006-01-02", dayStr, time.Local)
	if err != nil {
		return fmt.Errorf("parse day %q: %w", dayStr, err)
	}
	lookup := &reflectionLookup{db: h.db}
	anyReflection := false
	for i := range rep.Services {
		svc := &rep.Services[i]
		groups, err := lookup.LoadReflections(ctx, svc.ProjectPath, day)
		if err != nil {
			return fmt.Errorf("load reflections for %s: %w", svc.ProjectPath, err)
		}
		if len(groups) > 0 {
			anyReflection = true
			svc.ReflectionGroups = groups
			svc.ReflectionMarkdown = productivity.ConcatReflectionBodies(groups)
		} else {
			// Defensive: clear any stale groups carried in the persisted
			// payload so a deleted/orphaned reflection doesn't linger.
			svc.ReflectionGroups = nil
			svc.ReflectionMarkdown = ""
		}
	}
	if anyReflection {
		rep.ReflectionStatus = "current"
		rep.Nudge = ""
	} else {
		rep.ReflectionStatus = "missing"
		// Leave Nudge to AggregateReports — it composes the multi-day
		// message that depends on the union of per-day statuses.
	}

	// Lazy-compose the typed What-was-done cards from the day's
	// worklog_reflections rows. ComposeWWD skips legacy prose-only
	// rows (BodyJSON empty), so this is a no-op for services whose
	// reflections never opted into the typed payload — exactly the
	// behaviour the UI's "fall back to legacy bullets" path expects.
	hydrateWhatWasDone(ctx, h.db, rep, dayStr)
	return nil
}

// hydrateWhatWasDone attaches the typed §1.2 What-was-done cards onto
// each Service in rep by running ComposeWWD over the day's reflection
// rows per-project. Always emits — services with no typed reflection
// land with WhatWasDone == nil so the UI can fall back to the legacy
// bullets path without ambiguity.
//
// Errors are logged-but-not-fatal: the typed panel is a UX enrichment;
// the rest of the dashboard MUST render even if the per-project
// reflection read fails.
func hydrateWhatWasDone(
	ctx context.Context, db *store.DB, rep *productivity.Report, dayStr string,
) {
	for i := range rep.Services {
		svc := &rep.Services[i]

		// LLM-compiled override: if the day's productivity_snapshots row
		// contains a card for this service with llm_compiled=true (written
		// by PersistLLMCompiledCard during /klyne:productivity-sync), prefer
		// it. Today's path bypasses tryReadSnapshotDay so without this we'd
		// always re-derive via ComposeWWD and the Sonnet-written tldr +
		// llm_compiled badge would never surface on today's view.
		if snap, ok, err := store.GetDailyProductivitySnapshot(ctx, db, svc.ProjectPath, dayStr); err == nil && ok && strings.TrimSpace(snap.PayloadJSON) != "" {
			var saved productivity.Report
			if err := json.Unmarshal([]byte(snap.PayloadJSON), &saved); err == nil {
				key := serviceKeyForLookup(*svc)
				base := basePath(svc.ProjectPath)
				for j := range saved.Services {
					ssvc := &saved.Services[j]
					if ssvc.WhatWasDone == nil || !ssvc.WhatWasDone.LLMCompiled {
						continue
					}
					sk := ssvc.Repo
					if sk == "" {
						sk = basePath(ssvc.ProjectPath)
					}
					if sk == key || sk == base {
						cardCopy := *ssvc.WhatWasDone
						svc.WhatWasDone = &cardCopy
						break
					}
				}
				if svc.WhatWasDone != nil && svc.WhatWasDone.LLMCompiled {
					continue
				}
			}
		}

		rows, err := store.ListReflectionsForProjectDay(ctx, db, svc.ProjectPath, dayStr)
		if err != nil {
			log.Printf("productivity: wwd hydrate %s/%s: %v", svc.ProjectPath, dayStr, err)
			svc.WhatWasDone = nil
			continue
		}
		cards := productivity.ComposeWWD(rows)
		// At most one card per service per (project, day) — the payload
		// schema scopes one row to one service, and ComposeWWD groups
		// by service. Find the matching card by Service basename; nil
		// when no typed reflection landed.
		var match *productivity.WhatWasDoneCard
		key := serviceKeyForLookup(*svc)
		for j := range cards {
			if cards[j].Service == key {
				match = &cards[j]
				break
			}
		}
		// Defensive fallback: when the service-key disagrees (e.g. the
		// writer keyed by basename and the Repo string is e.g.
		// "klyne-ai/klyne"), match by basename of svc.ProjectPath.
		if match == nil {
			base := basePath(svc.ProjectPath)
			for j := range cards {
				if cards[j].Service == base {
					match = &cards[j]
					break
				}
			}
		}
		// Last resort: if there is exactly one card and the service
		// list only contains one service, attach it — covers the
		// single-project recompose-stub case where Repo is empty.
		if match == nil && len(cards) == 1 && len(rep.Services) == 1 {
			match = &cards[0]
		}
		if match != nil {
			cardCopy := *match
			svc.WhatWasDone = &cardCopy
		} else {
			svc.WhatWasDone = nil
		}
	}
}

// serviceKeyForLookup mirrors persistRecomposedCards.serviceKey but
// scoped to this file's lookup direction.
func serviceKeyForLookup(svc productivity.Service) string {
	if r := strings.TrimSpace(svc.Repo); r != "" {
		return r
	}
	return basePath(svc.ProjectPath)
}

// basePath returns the last path segment of p, or p itself when it has
// no separator. Empty for empty input.
func basePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if i := strings.LastIndex(p, "/"); i >= 0 && i < len(p)-1 {
		return p[i+1:]
	}
	return p
}

// persistSnapshotsForDay writes one daily_productivity_snapshot row per
// Service in the multi-project Report. Source is the caller's tag
// ("live" for handler backfill; reflection-recorder writes its own
// rows with source="reflection" via worklog.writeProductivitySnapshot).
//
// Orphan-session attribution: sessions whose Repo doesn't match any
// Service (ran outside any discovered git repo, or in a repo whose
// scan failed) would otherwise be dropped from every per-project
// slice — the re-aggregation invariant promised by singleProjectReport
// would silently under-count. We attach all such orphans to the FIRST
// service's snapshot (alphabetical ProjectPath order is stable across
// runs), so re-aggregating N per-project payloads still recovers
// every session's minutes. When the report has zero services but
// non-empty Sessions, we write one sentinel row keyed by
// "__orphan_sessions__" to preserve the orphan minutes.
//
// Errors per-row are logged-but-not-fatal: a snapshot is a perf/UX
// optimisation, not load-bearing.
func (h *ProductivityHandler) persistSnapshotsForDay(
	ctx context.Context, rep productivity.Report, dayStr, source string,
) {
	now := time.Now().UnixMilli()
	knownRepos := map[string]struct{}{}
	for _, svc := range rep.Services {
		knownRepos[svc.Repo] = struct{}{}
	}
	var orphans []productivity.SessionStat
	for _, s := range rep.Sessions {
		if _, ok := knownRepos[s.Repo]; !ok {
			orphans = append(orphans, s)
		}
	}

	if len(rep.Services) == 0 && len(orphans) > 0 {
		// No services to carry the orphans — write one sentinel row so
		// the day's minutes are preserved across reloads.
		h.upsertSnapshot(ctx, "__orphan_sessions__", dayStr, source,
			orphanOnlyReport(rep, orphans, dayStr), now)
		return
	}
	for i, svc := range rep.Services {
		var attach []productivity.SessionStat
		if i == 0 {
			attach = orphans
		}
		single := singleProjectReport(rep, svc, dayStr, attach)
		h.upsertSnapshot(ctx, svc.ProjectPath, dayStr, source, single, now)
	}
}

// upsertSnapshot is the marshal-and-log wrapper for one snapshot row.
// Extracted so persistSnapshotsForDay's orphan and per-service paths
// share the same error handling.
func (h *ProductivityHandler) upsertSnapshot(
	ctx context.Context, projectPath, dayStr, source string,
	rep productivity.Report, now int64,
) {
	payload, err := json.Marshal(rep)
	if err != nil {
		log.Printf("productivity: marshal snapshot %s/%s: %v", projectPath, dayStr, err)
		return
	}
	if err := store.UpsertDailyProductivitySnapshot(ctx, h.db, store.DailyProductivitySnapshot{
		ProjectPath:        projectPath,
		Day:                dayStr,
		PayloadJSON:        string(payload),
		TotalActiveMinutes: rep.TotalActiveMinutes,
		Source:             source,
		CreatedAt:          now,
		UpdatedAt:          now,
	}); err != nil {
		log.Printf("productivity: upsert snapshot %s/%s: %v", projectPath, dayStr, err)
	}
}

// orphanOnlyReport builds a sentinel-project snapshot payload carrying
// only the orphan sessions for a day. Services is empty by definition
// (these sessions didn't attribute to any repo). TotalActiveMinutes is
// the union of the orphans' active intervals so re-aggregation lines up.
func orphanOnlyReport(rep productivity.Report, orphans []productivity.SessionStat, dayStr string) productivity.Report {
	out := productivity.Report{
		Day:              dayStr,
		Services:         []productivity.Service{},
		MinutesByCLI:     map[string]int{},
		Sessions:         append([]productivity.SessionStat(nil), orphans...),
		ReflectionStatus: rep.ReflectionStatus,
	}
	allIntervals := make([]productivity.ActiveInterval, 0)
	cliIntervals := map[string][]productivity.ActiveInterval{}
	for _, s := range orphans {
		for _, iv := range s.ActiveIntervals {
			allIntervals = append(allIntervals, iv)
			cliIntervals[s.CLI] = append(cliIntervals[s.CLI], iv)
		}
	}
	out.TotalActiveMinutes = productivity.UnionMinutes(allIntervals)
	for cli, ivs := range cliIntervals {
		if m := productivity.UnionMinutes(ivs); m > 0 {
			out.MinutesByCLI[cli] = m
		}
	}
	return out
}

// singleProjectReport carves out the slice of a multi-project Report
// that belongs to one Service so it can be persisted as a per-project
// snapshot. Sessions are filtered by Service.Repo; extraSessions is
// the orphan-attribution slot (see persistSnapshotsForDay) — sessions
// that didn't attribute to any service get folded into one chosen
// carrier service so re-aggregating N per-project payloads still
// recovers every session's minutes. The report-level
// TotalActiveMinutes / MinutesByCLI are derived from the union of all
// included sessions' ActiveIntervals (sweep-line) so the persisted
// single-project payload is internally consistent.
func singleProjectReport(
	rep productivity.Report, svc productivity.Service, dayStr string,
	extraSessions []productivity.SessionStat,
) productivity.Report {
	out := productivity.Report{
		Day:              dayStr,
		Services:         []productivity.Service{svc},
		ReflectionStatus: rep.ReflectionStatus,
		Nudge:            rep.Nudge,
		MinutesByCLI:     map[string]int{},
		Sessions:         []productivity.SessionStat{},
		ReflectionGroups: svc.ReflectionGroups,
	}
	if rep.ReflectionMarkdown != "" && len(rep.Services) == 1 {
		out.ReflectionMarkdown = rep.ReflectionMarkdown
	}
	allIntervals := make([]productivity.ActiveInterval, 0)
	cliIntervals := map[string][]productivity.ActiveInterval{}
	include := func(s productivity.SessionStat) {
		out.Sessions = append(out.Sessions, s)
		for _, iv := range s.ActiveIntervals {
			allIntervals = append(allIntervals, iv)
			cliIntervals[s.CLI] = append(cliIntervals[s.CLI], iv)
		}
	}
	for _, s := range rep.Sessions {
		if s.Repo == svc.Repo {
			include(s)
		}
	}
	for _, s := range extraSessions {
		include(s)
	}
	out.TotalActiveMinutes = productivity.UnionMinutes(allIntervals)
	for cli, ivs := range cliIntervals {
		m := productivity.UnionMinutes(ivs)
		if m > 0 {
			out.MinutesByCLI[cli] = m
		}
	}
	return out
}

// localDaysInRange returns one time.Time per local-midnight in
// [since, until] inclusive, capped at 31 days to defensively bound the
// per-day loop (a request for a 5-year range would otherwise fan out
// to ~1800 snapshot lookups). Empty result when until < since.
//
// DST safety: we advance by (year, month, day+1) — NOT +24h. On a
// spring-forward day +24h actually moves to the day-after-tomorrow's
// 01:00, which silently skipped a calendar day in the previous
// implementation. time.Date with a day-arithmetic argument handles the
// 23h or 25h variants correctly because the constructor normalises
// against the location.
func localDaysInRange(since, until time.Time) []time.Time {
	loc := since.Location()
	if loc == nil {
		loc = time.Local
	}
	if until.Before(since) {
		return nil
	}
	startMid := time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, loc)
	endMid := time.Date(until.Year(), until.Month(), until.Day(), 0, 0, 0, 0, loc)
	out := make([]time.Time, 0, 8)
	for d := startMid; !d.After(endMid); d = time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, loc) {
		out = append(out, d)
		if len(out) >= 31 {
			break
		}
	}
	return out
}

// sessionEnds returns the latest last_msg_at (as time.Time) per
// project_path within the window — the done-uncommitted anchor (§6.5).
func (h *ProductivityHandler) sessionEnds(
	ctx context.Context, since, until time.Time,
) (map[string]time.Time, error) {
	const q = `
SELECT project_path, MAX(last_msg_at)
FROM sessions
WHERE last_msg_at >= ? AND last_msg_at <= ?
GROUP BY project_path`

	rows, err := h.db.Read().QueryContext(ctx, q, since.UnixMilli(), until.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("productivity: session-ends query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := map[string]time.Time{}
	for rows.Next() {
		var path string
		var lastMs int64
		if err := rows.Scan(&path, &lastMs); err != nil {
			return nil, fmt.Errorf("productivity: session-ends scan: %w", err)
		}
		if lastMs > 0 {
			out[path] = time.UnixMilli(lastMs)
		}
	}
	return out, rows.Err()
}

// sessionActivity loads one SessionActivity per session with at least one
// message in the window, carrying every in-window message timestamp so
// the substrate can compute the active-span (§6.4) and the session's CLI
// ("claude" | "codex") so AI time can be broken down per CLI.
func (h *ProductivityHandler) sessionActivity(
	ctx context.Context, since, until time.Time,
) ([]productivity.SessionActivity, error) {
	const q = `
SELECT s.id, s.project_path, s.cli, m.ts
FROM messages m
JOIN sessions s ON s.id = m.session_id
WHERE m.ts >= ? AND m.ts <= ?
ORDER BY s.id, m.ts ASC`

	rows, err := h.db.Read().QueryContext(ctx, q, since.UnixMilli(), until.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("productivity: session-activity query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	bySession := map[string]*productivity.SessionActivity{}
	var order []string
	for rows.Next() {
		var id, path, cli string
		var ts int64
		if err := rows.Scan(&id, &path, &cli, &ts); err != nil {
			return nil, fmt.Errorf("productivity: session-activity scan: %w", err)
		}
		sa, ok := bySession[id]
		if !ok {
			sa = &productivity.SessionActivity{SessionID: id, ProjectPath: path, CLI: cli}
			bySession[id] = sa
			order = append(order, id)
		}
		sa.MessageTimes = append(sa.MessageTimes, time.UnixMilli(ts))
		sa.MessageCount++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("productivity: session-activity rows: %w", err)
	}

	out := make([]productivity.SessionActivity, 0, len(order))
	for _, id := range order {
		out = append(out, *bySession[id])
	}
	return out, nil
}

// sessionPathLister adapts *store.DB to productivity.SessionPathLister.
type sessionPathLister struct {
	db *store.DB
}

func (l *sessionPathLister) SessionProjectPaths(
	ctx context.Context, since, until time.Time,
) ([]string, error) {
	const q = `
SELECT DISTINCT project_path
FROM sessions
WHERE last_msg_at >= ? AND last_msg_at <= ?`

	rows, err := l.db.Read().QueryContext(ctx, q, since.UnixMilli(), until.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("productivity: distinct project_path query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("productivity: distinct project_path scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// reflectionLookup adapts *store.DB to productivity.ReflectionLookup
// by reading every worklog_reflections row for (project, day) — the
// iterative-reflection workflow's T1/T2/T3 history.
type reflectionLookup struct {
	db *store.DB
}

func (l *reflectionLookup) LoadReflections(
	ctx context.Context, projectPath string, day time.Time,
) ([]productivity.ReflectionGroup, error) {
	dayStr := day.Format("2006-01-02")
	rows, err := store.ListReflectionsForProjectDay(ctx, l.db, projectPath, dayStr)
	if err != nil {
		return nil, fmt.Errorf("productivity: reflection lookup: %w", err)
	}
	if len(rows) > 0 {
		groups := make([]productivity.ReflectionGroup, 0, len(rows))
		for _, r := range rows {
			if strings.TrimSpace(r.BodyMD) == "" {
				continue
			}
			groups = append(groups, productivity.ReflectionGroup{
				ID:                  r.ID,
				TS:                  r.TS,
				BodyMD:              r.BodyMD,
				EvidenceEntryIDs:    r.EvidenceEntryIDs,
				StopSummaryCursorTS: r.StopSummaryCursorTS,
			})
		}
		if len(groups) > 0 {
			return groups, nil
		}
	}

	// Tier 2: deterministic fallback. When no reflection has been
	// authored yet, synthesize a "what was done" body from the day's
	// stop_summaries rows — but only the IMPORTANT ones (importance ≥ 7).
	body := buildDeterministicWhatWasDone(ctx, l.db, projectPath, day)
	if body == "" {
		return nil, nil
	}
	return []productivity.ReflectionGroup{{
		ID:     "deterministic-" + dayStr,
		TS:     day.UnixMilli(),
		BodyMD: body,
	}}, nil
}

// importanceThreshold is the cutoff that defines "important enough to
// surface in the dashboard's What-was-done block." Base importance is
// 5; +2 for any of commit_landed / migration / revert / security /
// error_resolved (etc.) and +3 for decision_recorded / pr_opened. A
// threshold of 7 admits exactly those rows that carry at least one
// such tag.
const importanceThreshold = 7

// buildDeterministicWhatWasDone renders a bullet list of important
// sessions for projectPath on the local day. Pure SQLite read; NO LM call.
func buildDeterministicWhatWasDone(
	ctx context.Context, db *store.DB, projectPath string, day time.Time,
) string {
	dayStr := day.Format("2006-01-02")
	const q = `
SELECT
  COALESCE(ai_drafted_summary, ''),
  COALESCE(last_user, '')
FROM stop_summaries
WHERE project_path = ?
  AND date(ts / 1000, 'unixepoch', 'localtime') = ?
  AND recap_visible = 1
  AND COALESCE(importance, 0) >= ?
ORDER BY importance DESC, ts ASC
LIMIT 50`

	rows, err := db.Read().QueryContext(ctx, q, projectPath, dayStr, importanceThreshold)
	if err != nil {
		return ""
	}
	defer rows.Close() //nolint:errcheck

	var bullets []string
	for rows.Next() {
		var aiSummary, lastUser string
		if err := rows.Scan(&aiSummary, &lastUser); err != nil {
			continue
		}
		text := strings.TrimSpace(aiSummary)
		if text == "" {
			text = strings.TrimSpace(lastUser)
		}
		if text == "" {
			continue
		}
		bullets = append(bullets, "- "+text)
	}
	if len(bullets) == 0 {
		return ""
	}
	return strings.Join(bullets, "\n")
}

// dirtyFileCap bounds how many uncommitted file paths a done-uncommitted
// risk carries — the count is always exact; only the displayed list is
// capped.
const dirtyFileCap = 25

// dirtyStatus returns the count AND the (capped) list of uncommitted /
// untracked paths for the working tree at dir.
func dirtyStatus(dir string) (int, []string) {
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return 0, nil
	}
	s := strings.TrimRight(string(out), "\r\n")
	if s == "" {
		return 0, nil
	}
	lines := strings.Split(s, "\n")
	files := make([]string, 0, len(lines))
	for i, ln := range lines {
		if i >= dirtyFileCap {
			break
		}
		if len(ln) > 3 {
			files = append(files, ln[3:])
		}
	}
	return len(lines), files
}
