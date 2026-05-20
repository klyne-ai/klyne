package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
// PROTOTYPE STUB (spec §11): live-scan-only — the git_session_snapshots
// capture half of D6 (historical "uncommitted-at-11:30" reconstruction)
// and dashboard_cache memoization are documented follow-ups. This
// endpoint computes everything live each request.
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
	ctx := r.Context()

	// §6.1 / D5: discover the repos to scan from the session
	// project_paths in the window (+ sibling worktrees). Discovery
	// failures degrade gracefully to an empty report rather than 500 —
	// the dashboard must stay stable (D8).
	lister := &sessionPathLister{db: h.db}
	targets, err := productivity.DiscoverRepos(ctx, lister, since, until)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	userEmails := productivity.UserEmails()

	var scans []productivity.ScanResult
	commitCounts := map[string]int{}
	projectPaths := map[string]string{}
	dirtyRepos := map[string]productivity.DirtyState{}

	// Latest session end (last_msg_at) per project_path, used as the
	// done-uncommitted anchor (§6.5).
	sessionEnds, err := h.sessionEnds(ctx, since, until)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	for _, t := range targets {
		sc, err := productivity.ScanRepo(t.Dir, since, until, userEmails)
		if err != nil {
			// A repo that won't scan (e.g. not a git dir / shallow) is
			// skipped, not fatal — keeps the dashboard deterministic and
			// stable across a heterogeneous repo set.
			continue
		}
		scans = append(scans, sc)
		commitCounts[sc.Dir] = len(sc.Commits)
		projectPaths[sc.Dir] = t.ProjectPath

		// PROTOTYPE STUB (spec §11): current dirty state only, no
		// historical snapshot. The D6 session-end snapshot capture is a
		// documented follow-up; here we read the live working tree.
		dirtyCount, dirtyFiles := dirtyStatus(t.Dir)
		dirtyRepos[sc.Dir] = productivity.DirtyState{
			DirtyFileCount: dirtyCount,
			DirtyFiles:     dirtyFiles,
			SessionEnd:     sessionEnds[t.ProjectPath],
		}
	}

	// §6.4 / D1: session-anchored time attribution. Build SessionActivity
	// from per-message timestamps in the window.
	sessions, err := h.sessionActivity(ctx, since, until)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	attribution := productivity.AttributeMinutes(sessions, commitCounts, productivityIdleCapMin)

	in := productivity.ReportInput{
		// B1: Day must reflect the WINDOW's start (the resolved `since`
		// after defaulting), not today. When `since` is absent it
		// defaults to local-midnight-today (defStart), so the
		// default-when-absent behaviour is preserved; when an explicit
		// window is passed Day tracks that window.
		Day:          time.UnixMilli(sinceMs).Local().Format("2006-01-02"),
		Now:          now,
		Scans:        scans,
		Attribution:  attribution,
		ProjectPaths: projectPaths,
		DirtyRepos:   dirtyRepos,
		// Sessions drives the report-level GLOBAL interval union
		// (TotalActiveMinutes / per-CLI MinutesByCLI — Change 1) and the
		// per-session SessionStat proof-of-work list (Change 2).
		Sessions: sessions,
	}

	rep, err := productivity.BuildReport(ctx, in, &reflectionLookup{db: h.db})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Layer-2 GitHub enrichment: attach merged PRs per Service via the
	// TTL-cached `gh pr list` (productivity_github.go). Best-effort —
	// never fails the request; the deterministic report stands alone.
	h.enrichMergedPRs(ctx, &rep, since, until)

	writeJSON(w, http.StatusOK, rep)
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
// ("claude" | "codex") so AI time can be broken down per CLI. Repos is
// left empty: the prototype attributes a session wholly to its
// ProjectPath (the multi-repo split is exercised by the substrate's own
// tests; a cwd-switch heuristic from message events is a documented
// follow-up).
//
// The query selects exactly the columns the §6.4 attribution + the
// Change 2 SessionStat evidence list need — session id, project_path,
// cli, and one row per in-window message timestamp. MessageCount is the
// count of those rows per session (the proof-of-work message count).
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
		// One row per in-window message → the count is the proof-of-work
		// message count surfaced in SessionStat (Change 2).
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
// It runs the distinct-project_path-in-window query the substrate's
// DiscoverRepos (D5/§6.1) expects.
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

// reflectionLookup adapts *store.DB to productivity.ReflectionLookup. A
// reflection "exists for the day" when a worklog_reflections row for the
// project falls on that local calendar date. Per D8 the existence flag
// only toggles the L3 nudge — it never changes the deterministic
// numbers; the returned body_md is purely additive narrative enrichment
// surfaced as Service.ReflectionMarkdown (Change 3).
type reflectionLookup struct {
	db *store.DB
}

func (l *reflectionLookup) HasReflection(
	ctx context.Context, projectPath string, day time.Time,
) (bool, string, error) {
	// worklog_reflections.ts is epoch-ms (migration 016). Match on the
	// local calendar date of the reflection's timestamp and return the
	// most recent reflection's markdown body for that project+day so the
	// dashboard can surface the worklog's own account of the work.
	dayStr := day.Format("2006-01-02")
	const q = `
SELECT body_md
FROM worklog_reflections
WHERE project_path = ?
  AND date(ts / 1000, 'unixepoch', 'localtime') = ?
ORDER BY ts DESC
LIMIT 1`

	var bodyMD string
	err := l.db.Read().QueryRowContext(ctx, q, projectPath, dayStr).Scan(&bodyMD)
	if err != nil {
		// sql.ErrNoRows ⇒ no reflection ⇒ not an error condition.
		if errors.Is(err, sql.ErrNoRows) {
			return false, "", nil
		}
		return false, "", fmt.Errorf("productivity: reflection lookup: %w", err)
	}
	return true, bodyMD, nil
}

// dirtyFileCap bounds how many uncommitted file paths a done-uncommitted
// risk carries — the count is always exact; only the displayed list is
// capped.
const dirtyFileCap = 25

// dirtyStatus returns the count AND the (capped) list of uncommitted /
// untracked paths for the working tree at dir — porcelain short status,
// so the done-uncommitted risk can show WHICH files are dirty. Errors
// map to (0, nil): a repo we can't stat is treated as clean rather than
// failing the whole dashboard.
//
// PROTOTYPE STUB (spec §11): current dirty state only, no historical
// snapshot (the D6 session-end snapshot half is a documented follow-up).
func dirtyStatus(dir string) (int, []string) {
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return 0, nil
	}
	// Trim only TRAILING newlines — leading whitespace must be preserved
	// because porcelain v1 starts modified-not-staged lines with " M ..."
	// (X=' ', Y='M'), and stripping it shifts every path by one char.
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
		// Porcelain v1 lines are "XY <path>" — XY at 0..1, space at 2,
		// path from index 3 onward. Skip lines too short to carry a path.
		if len(ln) > 3 {
			files = append(files, ln[3:])
		}
	}
	return len(lines), files
}
