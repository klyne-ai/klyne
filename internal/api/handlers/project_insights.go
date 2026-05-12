package handlers

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// ProjectInsightsHandler serves GET /insights/projects — the per-project
// rollup that backs the Insights dashboard.
//
// Design note: this endpoint reports tokens, activity, and a small set
// of pain/efficiency signals (cache hit %, tokens/msg, /compact count,
// trend vs prior window). It deliberately omits dollar costs — klyne
// users are on flat subscription plans, so inferred $ figures don't
// match the bill they actually pay. Share-of-total and the prior-window
// trend carry the "weight" of each project instead.
type ProjectInsightsHandler struct {
	db *store.DB
}

// NewProjectInsightsHandler constructs a ProjectInsightsHandler.
func NewProjectInsightsHandler(db *store.DB) *ProjectInsightsHandler {
	return &ProjectInsightsHandler{db: db}
}

// defaultTopSessions / maxTopSessions cap the per-row drill-down.
// 3 is enough to answer "which session ate the tokens" without
// returning a long tail the UI would just clip anyway.
const (
	defaultTopSessions = 3
	maxTopSessions     = 10
)

// Get handles GET /insights/projects.
//
// Query params:
//   - since int64  epoch-ms lower bound (default 0 = all time)
//   - until int64  epoch-ms upper bound (default 0 = "now")
//   - top   int    # of top sessions to attach per project (default 3,
//                  max 10). Zero disables the drill-down.
func (h *ProjectInsightsHandler) Get(w http.ResponseWriter, r *http.Request) {
	since, err := queryInt64(r, "since", 0)
	if err != nil || since < 0 {
		http.Error(w, "invalid since: must be a non-negative epoch-ms integer", http.StatusBadRequest)
		return
	}
	until, err := queryInt64(r, "until", 0)
	if err != nil || until < 0 {
		http.Error(w, "invalid until: must be a non-negative epoch-ms integer", http.StatusBadRequest)
		return
	}

	// Validate top before applying its default so an empty value
	// (which is what the standard "no param" case looks like) still
	// passes through to the default rather than failing validation.
	rawTop := r.URL.Query().Get("top")
	top := defaultTopSessions
	if rawTop != "" {
		v, err := queryInt(r, "top", defaultTopSessions)
		if err != nil || v < 0 || v > maxTopSessions {
			http.Error(w, fmt.Sprintf("invalid top: must be 0-%d", maxTopSessions), http.StatusBadRequest)
			return
		}
		top = v
	}

	// Resolve the upper bound: 0 means "open-ended / now". We use a
	// far-future sentinel so the SQL BETWEEN clauses don't need
	// per-query branching.
	effUntil := until
	if effUntil == 0 {
		// Using a fixed sentinel keeps the SQL deterministic for the
		// row scanners. Year 9999 is comfortably past any plausible
		// session timestamp.
		effUntil = 253_402_300_799_000
	}

	// Compute the prior window of equal duration. When since == 0 the
	// window is unbounded on the left, so there is no comparable
	// "prior" — record (0, 0) and skip the prior aggregation entirely.
	priorSince, priorUntil := int64(0), int64(0)
	if since > 0 {
		duration := effUntil - since
		priorSince = since - duration
		if priorSince < 0 {
			priorSince = 0
		}
		priorUntil = since
	}

	ctx := r.Context()

	rows, err := h.aggregateProjects(ctx, since, effUntil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if priorSince != priorUntil {
		priorTotals, err := h.aggregatePriorTokens(ctx, priorSince, priorUntil)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		for path, prior := range priorTotals {
			if row, ok := rows[path]; ok {
				row.PriorTokens = prior
				row.TrendPct = trendPct(row.Tokens, prior)
				rows[path] = row
			}
		}
	}

	if err := h.attachCompactCounts(ctx, since, effUntil, rows); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := h.attachDaily(ctx, since, effUntil, rows); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if top > 0 {
		if err := h.attachTopSessions(ctx, since, effUntil, top, rows); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	projects := make([]api.ProjectInsight, 0, len(rows))
	for _, p := range rows {
		// Derived display fields are computed last so they reflect
		// the final aggregated tokens after all attach* steps.
		p.CacheHitPct = cacheHitPct(p.CachedReadTokens, p.TokensIn)
		p.TokensPerMessage = tokensPerMessage(p.Tokens, p.Messages)
		projects = append(projects, p)
	}
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].Tokens != projects[j].Tokens {
			return projects[i].Tokens > projects[j].Tokens
		}
		// Stable tiebreak on path so the same data always returns the
		// same order — helps the cockpit chart legend stay consistent
		// between polls.
		return projects[i].ProjectPath < projects[j].ProjectPath
	})

	totals := api.ProjectInsight{Name: "total"}
	for _, p := range projects {
		totals.Tokens += p.Tokens
		totals.TokensIn += p.TokensIn
		totals.TokensOut += p.TokensOut
		totals.Messages += p.Messages
		totals.Sessions += p.Sessions
		totals.CachedReadTokens += p.CachedReadTokens
		totals.CompactCount += p.CompactCount
		totals.PriorTokens += p.PriorTokens
		totals.Claude.Tokens += p.Claude.Tokens
		totals.Claude.TokensIn += p.Claude.TokensIn
		totals.Claude.TokensOut += p.Claude.TokensOut
		totals.Claude.Messages += p.Claude.Messages
		totals.Claude.Sessions += p.Claude.Sessions
		totals.Codex.Tokens += p.Codex.Tokens
		totals.Codex.TokensIn += p.Codex.TokensIn
		totals.Codex.TokensOut += p.Codex.TokensOut
		totals.Codex.Messages += p.Codex.Messages
		totals.Codex.Sessions += p.Codex.Sessions
		if p.LastMsgAt > totals.LastMsgAt {
			totals.LastMsgAt = p.LastMsgAt
		}
	}
	totals.CacheHitPct = cacheHitPct(totals.CachedReadTokens, totals.TokensIn)
	totals.TokensPerMessage = tokensPerMessage(totals.Tokens, totals.Messages)
	totals.TrendPct = trendPct(totals.Tokens, totals.PriorTokens)

	writeJSON(w, http.StatusOK, api.ProjectInsightsResponse{
		Since:      since,
		Until:      until,
		PriorSince: priorSince,
		PriorUntil: priorUntil,
		Projects:   projects,
		Totals:     totals,
	})
}

// aggregateProjects builds the (project_path, cli) cross-tab from the
// sessions table for the current window. Filters on last_msg_at so a
// session counts toward whichever window contains its most recent
// activity — same semantics as the existing rollupByProjects query in
// cost.go.
func (h *ProjectInsightsHandler) aggregateProjects(
	ctx context.Context, since, until int64,
) (map[string]api.ProjectInsight, error) {
	const q = `
SELECT
    project_path,
    COALESCE(cli, '') AS cli,
    SUM(COALESCE(tokens_in,           0)) AS tokens_in,
    SUM(COALESCE(tokens_out,          0)) AS tokens_out,
    SUM(COALESCE(cached_read_tokens,  0)) AS cached_read,
    SUM(COALESCE(msg_count,           0)) AS messages,
    COUNT(*)                              AS sessions,
    MAX(last_msg_at)                      AS last_msg_at
FROM sessions
WHERE last_msg_at >= ? AND last_msg_at <= ?
GROUP BY project_path, cli`

	rows, err := h.db.Read().QueryContext(ctx, q, since, until)
	if err != nil {
		return nil, fmt.Errorf("insights: aggregate query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := map[string]api.ProjectInsight{}
	for rows.Next() {
		var (
			path                                                          string
			cli                                                           string
			tokensIn, tokensOut, cachedRead, messages, sessions, lastMsg  int64
		)
		if err := rows.Scan(&path, &cli, &tokensIn, &tokensOut, &cachedRead, &messages, &sessions, &lastMsg); err != nil {
			return nil, fmt.Errorf("insights: aggregate scan: %w", err)
		}
		row, ok := out[path]
		if !ok {
			row = api.ProjectInsight{
				ProjectPath: path,
				Name:        projectDisplayName(path),
			}
		}
		row.TokensIn += tokensIn
		row.TokensOut += tokensOut
		row.Tokens += tokensIn + tokensOut
		row.CachedReadTokens += cachedRead
		row.Messages += messages
		row.Sessions += sessions
		if lastMsg > row.LastMsgAt {
			row.LastMsgAt = lastMsg
		}
		slice := api.AgentSlice{
			Tokens:    tokensIn + tokensOut,
			TokensIn:  tokensIn,
			TokensOut: tokensOut,
			Messages:  messages,
			Sessions:  sessions,
		}
		switch cli {
		case "claude":
			row.Claude = slice
		case "codex":
			row.Codex = slice
		}
		out[path] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("insights: aggregate rows: %w", err)
	}
	return out, nil
}

// aggregatePriorTokens returns just the tokens_in + tokens_out total
// per project for the prior window. We don't need the full breakdown
// here — only the headline number to compute TrendPct.
func (h *ProjectInsightsHandler) aggregatePriorTokens(
	ctx context.Context, since, until int64,
) (map[string]int64, error) {
	const q = `
SELECT project_path,
       SUM(COALESCE(tokens_in,  0)) + SUM(COALESCE(tokens_out, 0))
FROM sessions
WHERE last_msg_at >= ? AND last_msg_at <= ?
GROUP BY project_path`

	rows, err := h.db.Read().QueryContext(ctx, q, since, until)
	if err != nil {
		return nil, fmt.Errorf("insights: prior query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := map[string]int64{}
	for rows.Next() {
		var path string
		var total int64
		if err := rows.Scan(&path, &total); err != nil {
			return nil, fmt.Errorf("insights: prior scan: %w", err)
		}
		out[path] = total
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("insights: prior rows: %w", err)
	}
	return out, nil
}

// attachCompactCounts joins compact_events on sessions.id to count the
// /compact events that fired per project inside the window. We filter
// on the compact event's own ts (not the session's last_msg_at) so the
// count correctly reflects "compactions that happened in this window".
func (h *ProjectInsightsHandler) attachCompactCounts(
	ctx context.Context, since, until int64, rows map[string]api.ProjectInsight,
) error {
	const q = `
SELECT s.project_path, COUNT(*)
FROM compact_events ce
JOIN sessions s ON s.id = ce.session_id
WHERE ce.ts >= ? AND ce.ts <= ?
GROUP BY s.project_path`

	queryRows, err := h.db.Read().QueryContext(ctx, q, since, until)
	if err != nil {
		return fmt.Errorf("insights: compact query: %w", err)
	}
	defer queryRows.Close() //nolint:errcheck

	for queryRows.Next() {
		var path string
		var count int64
		if err := queryRows.Scan(&path, &count); err != nil {
			return fmt.Errorf("insights: compact scan: %w", err)
		}
		if row, ok := rows[path]; ok {
			row.CompactCount = count
			rows[path] = row
		}
	}
	return queryRows.Err()
}

// attachDaily fills in the per-project daily sparkline. We aggregate
// from the messages table (not sessions) so the time-bucketed shape is
// honest — a session with activity on three different days gets three
// daily points, not a single lump at last_msg_at.
func (h *ProjectInsightsHandler) attachDaily(
	ctx context.Context, since, until int64, rows map[string]api.ProjectInsight,
) error {
	const q = `
SELECT s.project_path,
       date(m.ts / 1000, 'unixepoch') AS day,
       SUM(COALESCE(m.tokens_in,  0)) + SUM(COALESCE(m.tokens_out, 0)) AS tokens
FROM messages m
JOIN sessions s ON s.id = m.session_id
WHERE m.ts >= ? AND m.ts <= ?
GROUP BY s.project_path, day
ORDER BY s.project_path, day ASC`

	queryRows, err := h.db.Read().QueryContext(ctx, q, since, until)
	if err != nil {
		return fmt.Errorf("insights: daily query: %w", err)
	}
	defer queryRows.Close() //nolint:errcheck

	for queryRows.Next() {
		var path, day string
		var tokens int64
		if err := queryRows.Scan(&path, &day, &tokens); err != nil {
			return fmt.Errorf("insights: daily scan: %w", err)
		}
		if row, ok := rows[path]; ok {
			row.Daily = append(row.Daily, api.DailyPoint{Day: day, Tokens: tokens})
			rows[path] = row
		}
	}
	return queryRows.Err()
}

// attachTopSessions pulls the heaviest sessions per project within the
// window. SQLite lacks a window function for top-N-per-group on every
// supported version, so we fetch ordered (project_path, tokens DESC)
// and slice the first `limit` per project in Go. This is cheap in
// practice because the rowset is already bounded by the window.
func (h *ProjectInsightsHandler) attachTopSessions(
	ctx context.Context, since, until int64, limit int, rows map[string]api.ProjectInsight,
) error {
	const q = `
SELECT id, project_path, COALESCE(cli, ''), COALESCE(model, ''),
       COALESCE(tokens_in, 0) + COALESCE(tokens_out, 0) AS tokens,
       COALESCE(msg_count, 0), last_msg_at
FROM sessions
WHERE last_msg_at >= ? AND last_msg_at <= ?
ORDER BY project_path, tokens DESC, id ASC`

	queryRows, err := h.db.Read().QueryContext(ctx, q, since, until)
	if err != nil {
		return fmt.Errorf("insights: top query: %w", err)
	}
	defer queryRows.Close() //nolint:errcheck

	counts := map[string]int{}
	for queryRows.Next() {
		var s api.TopSession
		var path string
		if err := queryRows.Scan(&s.SessionID, &path, &s.CLI, &s.Model, &s.Tokens, &s.Messages, &s.LastMsgAt); err != nil {
			return fmt.Errorf("insights: top scan: %w", err)
		}
		if counts[path] >= limit {
			continue
		}
		if row, ok := rows[path]; ok {
			row.TopSessions = append(row.TopSessions, s)
			rows[path] = row
			counts[path]++
		}
	}
	return queryRows.Err()
}

// projectDisplayName extracts the human label from an absolute path.
// Empty paths become "(unknown)" so a row never renders blank.
func projectDisplayName(path string) string {
	if path == "" {
		return "(unknown)"
	}
	name := filepath.Base(path)
	if name == "." || name == "/" || name == "" {
		return path
	}
	return name
}

// cacheHitPct = cached_read / tokens_in * 100. Returns 0 when tokens_in
// is zero (no cache hit is possible without input tokens).
func cacheHitPct(cachedRead, tokensIn int64) float64 {
	if tokensIn <= 0 {
		return 0
	}
	return float64(cachedRead) / float64(tokensIn) * 100
}

// tokensPerMessage = total tokens / message count. Returns 0 when no
// messages — keeps the UI from rendering NaN or +Inf.
func tokensPerMessage(tokens, messages int64) float64 {
	if messages <= 0 {
		return 0
	}
	return float64(tokens) / float64(messages)
}

// trendPct = (current - prior) / prior * 100. Returns 0 when prior is
// zero (no meaningful comparison possible). The UI renders large
// values directly; we intentionally don't clamp.
func trendPct(current, prior int64) float64 {
	if prior <= 0 {
		return 0
	}
	return float64(current-prior) / float64(prior) * 100
}

