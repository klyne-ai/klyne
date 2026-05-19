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

// productivityIdleCapMin is the §6.4 idle-gap cap: an intra-session gap
// longer than this is excluded from active-time. 30 min matches the
// substrate's documented attribution contract.
const productivityIdleCapMin = 30

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
		dirtyRepos[sc.Dir] = productivity.DirtyState{
			DirtyFileCount: dirtyFileCount(t.Dir),
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
		Day:          defStart.Format("2006-01-02"),
		Now:          now,
		Scans:        scans,
		Attribution:  attribution,
		ProjectPaths: projectPaths,
		DirtyRepos:   dirtyRepos,
	}

	rep, err := productivity.BuildReport(ctx, in, &reflectionLookup{db: h.db})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

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
// the substrate can compute the active-span (§6.4). Repos is left empty:
// the prototype attributes a session wholly to its ProjectPath (the
// multi-repo split is exercised by the substrate's own tests; a
// cwd-switch heuristic from message events is a documented follow-up).
func (h *ProductivityHandler) sessionActivity(
	ctx context.Context, since, until time.Time,
) ([]productivity.SessionActivity, error) {
	const q = `
SELECT s.id, s.project_path, m.ts
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
		var id, path string
		var ts int64
		if err := rows.Scan(&id, &path, &ts); err != nil {
			return nil, fmt.Errorf("productivity: session-activity scan: %w", err)
		}
		sa, ok := bySession[id]
		if !ok {
			sa = &productivity.SessionActivity{SessionID: id, ProjectPath: path}
			bySession[id] = sa
			order = append(order, id)
		}
		sa.MessageTimes = append(sa.MessageTimes, time.UnixMilli(ts))
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
// project falls on that local calendar date. Per D8 this only toggles
// the L3 nudge — it never changes the deterministic numbers.
type reflectionLookup struct {
	db *store.DB
}

func (l *reflectionLookup) HasReflection(
	ctx context.Context, projectPath string, day time.Time,
) (bool, error) {
	// worklog_reflections.ts is epoch-ms (migration 016). Match on the
	// local calendar date of the reflection's timestamp.
	dayStr := day.Format("2006-01-02")
	const q = `
SELECT 1
FROM worklog_reflections
WHERE project_path = ?
  AND date(ts / 1000, 'unixepoch', 'localtime') = ?
LIMIT 1`

	var one int
	err := l.db.Read().QueryRowContext(ctx, q, projectPath, dayStr).Scan(&one)
	if err != nil {
		// sql.ErrNoRows ⇒ no reflection ⇒ not an error condition.
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("productivity: reflection lookup: %w", err)
	}
	return true, nil
}

// dirtyFileCount counts porcelain status lines (uncommitted + untracked)
// for the working tree at dir. Errors map to 0 — a repo we can't stat is
// treated as clean rather than failing the whole dashboard.
//
// PROTOTYPE STUB (spec §11): current dirty state only, no historical
// snapshot (the D6 session-end snapshot half is a documented follow-up).
func dirtyFileCount(dir string) int {
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0
	}
	return len(strings.Split(s, "\n"))
}
