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

// NewProductivityHandler constructs a ProductivityHandler.
func NewProductivityHandler(db *store.DB) *ProductivityHandler {
	return &ProductivityHandler{db: db}
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

	writeJSON(w, http.StatusOK, composite)
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

	// Layer-2 GitHub enrichment + git_fetched_at — runs inside the live
	// compute so a backfilled snapshot row already carries the
	// enrichments (a later read skips this step entirely).
	h.enrichMergedPRs(ctx, &rep, since, until, force)
	for i := range rep.Services {
		rep.Services[i].GitFetchedAt = gitFetchedAt(rep.Services[i].ProjectPath)
	}
	return rep, nil
}

// tryReadSnapshotDay loads every per-project snapshot row for one
// local day and aggregates them into one multi-project Report
// (productivity.AggregateReports with a single-day input). Returns
// ok=false when no rows exist for the day — the caller falls through
// to live compute + backfill.
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

// persistSnapshotsForDay writes one daily_productivity_snapshot row per
// Service in the multi-project Report. Source is the caller's tag
// ("live" for handler backfill; reflection-recorder writes its own
// rows with source="reflection" via worklog.writeProductivitySnapshot).
//
// Errors per-row are logged-but-not-fatal: a snapshot is a perf/UX
// optimisation, not load-bearing.
func (h *ProductivityHandler) persistSnapshotsForDay(
	ctx context.Context, rep productivity.Report, dayStr, source string,
) {
	now := time.Now().UnixMilli()
	for _, svc := range rep.Services {
		single := singleProjectReport(rep, svc, dayStr)
		payload, err := json.Marshal(single)
		if err != nil {
			log.Printf("productivity: marshal snapshot %s/%s: %v", svc.ProjectPath, dayStr, err)
			continue
		}
		if err := store.UpsertDailyProductivitySnapshot(ctx, h.db, store.DailyProductivitySnapshot{
			ProjectPath:        svc.ProjectPath,
			Day:                dayStr,
			PayloadJSON:        string(payload),
			TotalActiveMinutes: single.TotalActiveMinutes,
			Source:             source,
			CreatedAt:          now,
			UpdatedAt:          now,
		}); err != nil {
			log.Printf("productivity: upsert snapshot %s/%s: %v", svc.ProjectPath, dayStr, err)
		}
	}
}

// singleProjectReport carves out the slice of a multi-project Report
// that belongs to one Service so it can be persisted as a per-project
// snapshot. Sessions are filtered by Service.Repo; the report-level
// TotalActiveMinutes / MinutesByCLI are derived from those sessions'
// ActiveIntervals (sweep-line union) so the persisted single-project
// payload is internally consistent — re-aggregating N per-project
// payloads via AggregateReports reproduces the original multi-project
// numbers within rounding.
func singleProjectReport(rep productivity.Report, svc productivity.Service, dayStr string) productivity.Report {
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
	for _, s := range rep.Sessions {
		if s.Repo != svc.Repo {
			continue
		}
		out.Sessions = append(out.Sessions, s)
		for _, iv := range s.ActiveIntervals {
			allIntervals = append(allIntervals, iv)
			cliIntervals[s.CLI] = append(cliIntervals[s.CLI], iv)
		}
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
	for d := startMid; !d.After(endMid); d = d.Add(24 * time.Hour) {
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
