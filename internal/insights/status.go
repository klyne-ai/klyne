package insights

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// Status snapshot
// ===============
// Aggregator that synthesises klyne's installed-state into a portable
// Markdown report. Unlike generate_handoff (per-task) or bootstrap
// (Day-1 brief), the status snapshot is per-installation: "what has
// klyne actually observed in the last N days?". Useful for weekly
// review, sharing with a teammate, or proving to yourself that the
// daemon is actually doing its job.
//
// All data comes from the local SQLite store + the existing JSONL
// readers. Zero AI calls, zero network, deterministic given the same
// store state and window.

// StatusWindow tunes which data the snapshot considers. Defaults
// match the README marketing: 7-day rolling window.
type StatusWindow struct {
	// SinceMs is the epoch-ms cutoff; sessions/events older than
	// this are excluded. Zero disables the cutoff.
	SinceMs int64
	// ProjectPath, when non-empty, restricts every metric to one
	// project. Empty means "all projects on this machine".
	ProjectPath string
	// MaxSessions caps how many sessions the walker considers.
	// Zero defaults to 500.
	MaxSessions int
	// TopProjects caps the project leaderboard length.
	TopProjects int
	// TopSessions caps the recent-sessions list length.
	TopSessions int
}

// DefaultStatusWindow returns the v1 tuned defaults: last 7 days,
// all projects, top-5 leaderboards.
func DefaultStatusWindow() StatusWindow {
	return StatusWindow{
		SinceMs:     time.Now().Add(-7 * 24 * time.Hour).UnixMilli(),
		MaxSessions: 500,
		TopProjects: 5,
		TopSessions: 5,
	}
}

// ProjectRollup is one project's contribution to the snapshot.
type ProjectRollup struct {
	ProjectPath string `json:"project_path"`
	Sessions    int    `json:"sessions"`
	Messages    int64  `json:"messages"`
	TokensIn    int64  `json:"tokens_in"`
	TokensOut   int64  `json:"tokens_out"`
	CompactRuns int    `json:"compact_runs"`
}

// SessionRollup is one session's contribution to the snapshot.
type SessionRollup struct {
	ID          string  `json:"id"`
	CLI         string  `json:"cli"`
	ProjectPath string  `json:"project_path"`
	LastMsgAt   int64   `json:"last_msg_at"`
	Messages    int64   `json:"messages"`
	TokensIn    int64   `json:"tokens_in"`
	TokensOut   int64   `json:"tokens_out"`
	CostUSD     float64 `json:"cost_usd"`
}

// CompactRollup is one /compact event observed inside the window.
type CompactRollup struct {
	SessionID string `json:"session_id"`
	Ts        int64  `json:"ts"`
	Before    int64  `json:"before_token_count"`
	After     int64  `json:"after_token_count"`
}

// StatusSnapshot is the structured aggregate produced by Snapshot.
type StatusSnapshot struct {
	GeneratedAtMs    int64           `json:"generated_at_ms"`
	WindowSinceMs    int64           `json:"window_since_ms"`
	ProjectPath      string          `json:"project_path,omitempty"`
	TotalSessions    int             `json:"total_sessions"`
	TotalMessages    int64           `json:"total_messages"`
	TotalTokensIn    int64           `json:"total_tokens_in"`
	TotalTokensOut   int64           `json:"total_tokens_out"`
	TotalCachedRead  int64           `json:"total_cached_read"`
	TotalCostUSD     float64         `json:"total_cost_usd"`
	TopProjects      []ProjectRollup `json:"top_projects"`
	RecentSessions   []SessionRollup `json:"recent_sessions"`
	CompactEvents    []CompactRollup `json:"compact_events"`
	CompactCount     int             `json:"compact_count"`
	MemoriesProject  int             `json:"memories_project"`
	MemoriesGlobal   int             `json:"memories_global"`
	StopSummaryCount int             `json:"stop_summary_count"`
}

// Snapshot computes a StatusSnapshot for win. Walks sessions,
// compact events, decisions/memories, and stop-hook summaries.
// Errors surface I/O failures only — empty data sets render as
// "_(none)_" sections, not as errors.
func Snapshot(ctx context.Context, db *store.DB, win StatusWindow) (StatusSnapshot, error) {
	if win.MaxSessions <= 0 {
		win.MaxSessions = 500
	}
	if win.TopProjects <= 0 {
		win.TopProjects = 5
	}
	if win.TopSessions <= 0 {
		win.TopSessions = 5
	}

	out := StatusSnapshot{
		GeneratedAtMs: time.Now().UnixMilli(),
		WindowSinceMs: win.SinceMs,
		ProjectPath:   win.ProjectPath,
	}

	sessions, err := store.ListSessions(ctx, db, store.SessionFilter{
		ProjectPath: win.ProjectPath,
		Limit:       win.MaxSessions,
	})
	if err != nil {
		return out, fmt.Errorf("list sessions: %w", err)
	}

	// Filter sessions by window and accumulate.
	projectRollups := map[string]*ProjectRollup{}
	for _, s := range sessions {
		if win.SinceMs > 0 && s.LastMsgAt < win.SinceMs {
			continue
		}
		out.TotalSessions++
		out.TotalMessages += s.MsgCount
		out.TotalTokensIn += s.TokensIn
		out.TotalTokensOut += s.TokensOut
		out.TotalCachedRead += s.CachedReadTokens
		out.TotalCostUSD += s.CostUSD

		pr := projectRollups[s.ProjectPath]
		if pr == nil {
			pr = &ProjectRollup{ProjectPath: s.ProjectPath}
			projectRollups[s.ProjectPath] = pr
		}
		pr.Sessions++
		pr.Messages += s.MsgCount
		pr.TokensIn += s.TokensIn
		pr.TokensOut += s.TokensOut
	}

	// Top projects by tokens-in.
	for _, pr := range projectRollups {
		out.TopProjects = append(out.TopProjects, *pr)
	}
	sort.Slice(out.TopProjects, func(i, j int) bool {
		if out.TopProjects[i].TokensIn != out.TopProjects[j].TokensIn {
			return out.TopProjects[i].TokensIn > out.TopProjects[j].TokensIn
		}
		return out.TopProjects[i].ProjectPath < out.TopProjects[j].ProjectPath
	})
	if len(out.TopProjects) > win.TopProjects {
		out.TopProjects = out.TopProjects[:win.TopProjects]
	}

	// Recent sessions (newest first, top N).
	for _, s := range sessions {
		if win.SinceMs > 0 && s.LastMsgAt < win.SinceMs {
			continue
		}
		out.RecentSessions = append(out.RecentSessions, SessionRollup{
			ID:          s.ID,
			CLI:         string(s.CLI),
			ProjectPath: s.ProjectPath,
			LastMsgAt:   s.LastMsgAt,
			Messages:    s.MsgCount,
			TokensIn:    s.TokensIn,
			TokensOut:   s.TokensOut,
			CostUSD:     s.CostUSD,
		})
	}
	sort.Slice(out.RecentSessions, func(i, j int) bool {
		return out.RecentSessions[i].LastMsgAt > out.RecentSessions[j].LastMsgAt
	})
	if len(out.RecentSessions) > win.TopSessions {
		out.RecentSessions = out.RecentSessions[:win.TopSessions]
	}

	// Compact events inside the window. For each session in scope,
	// query its events; cheap because the window already prunes
	// the candidate session list.
	for _, s := range sessions {
		if win.SinceMs > 0 && s.LastMsgAt < win.SinceMs {
			continue
		}
		evs, err := listCompactEventsForSession(ctx, db, s.ID, win.SinceMs)
		if err != nil {
			return out, fmt.Errorf("list compact events: %w", err)
		}
		for _, e := range evs {
			out.CompactEvents = append(out.CompactEvents, CompactRollup{
				SessionID: e.SessionID,
				Ts:        e.Ts,
				Before:    e.Before,
				After:     e.After,
			})
		}
	}
	out.CompactCount = len(out.CompactEvents)
	sort.Slice(out.CompactEvents, func(i, j int) bool {
		return out.CompactEvents[i].Ts > out.CompactEvents[j].Ts
	})
	if len(out.CompactEvents) > 10 {
		out.CompactEvents = out.CompactEvents[:10]
	}

	// Memory + stop-summary counts.
	if projCount, globCount, err := countMemoriesForProject(ctx, db, win.ProjectPath); err == nil {
		out.MemoriesProject = projCount
		out.MemoriesGlobal = globCount
	}
	if win.ProjectPath != "" {
		rows, err := store.ListStopSummariesForProject(ctx, db, win.ProjectPath, 100)
		if err == nil {
			cnt := 0
			for _, r := range rows {
				if win.SinceMs > 0 && r.Ts < win.SinceMs {
					continue
				}
				cnt++
			}
			out.StopSummaryCount = cnt
		}
	}

	return out, nil
}

// compactEventRow is a minimal projection of compact_events for the
// snapshot. Co-located in this file so we don't add a new store
// helper just for status; the SQL is straightforward.
type compactEventRow struct {
	SessionID string
	Ts        int64
	Before    int64
	After     int64
}

func listCompactEventsForSession(ctx context.Context, db *store.DB, sessionID string, sinceMs int64) ([]compactEventRow, error) {
	q := `SELECT session_id, ts, before_token_count, after_token_count
	        FROM compact_events
	       WHERE session_id = ?`
	args := []any{sessionID}
	if sinceMs > 0 {
		q += " AND ts >= ?"
		args = append(args, sinceMs)
	}
	q += " ORDER BY ts DESC"
	rows, err := db.Read().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []compactEventRow
	for rows.Next() {
		var r compactEventRow
		if err := rows.Scan(&r.SessionID, &r.Ts, &r.Before, &r.After); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// countMemoriesForProject returns (projectCount, globalCount) for
// the decisions store. When projectPath is empty, projectCount is
// zero by design — there is no specific project to count.
func countMemoriesForProject(ctx context.Context, db *store.DB, projectPath string) (int, int, error) {
	all, err := store.ListDecisions(ctx, db, store.DecisionFilter{Limit: 5000})
	if err != nil {
		return 0, 0, err
	}
	proj := 0
	glob := 0
	for _, d := range all {
		switch {
		case d.ProjectPath == "":
			glob++
		case projectPath != "" && d.ProjectPath == projectPath:
			proj++
		}
	}
	return proj, glob, nil
}

// RenderStatusAsMarkdown renders a StatusSnapshot to the canonical
// Markdown shape. Used by both the MCP tool and the CLI.
func RenderStatusAsMarkdown(s StatusSnapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# klyne status\n\n")
	gen := time.UnixMilli(s.GeneratedAtMs).UTC()
	fmt.Fprintf(&b, "Generated: %s\n", gen.Format(time.RFC3339))
	if s.WindowSinceMs > 0 {
		from := time.UnixMilli(s.WindowSinceMs).UTC()
		fmt.Fprintf(&b, "Window: since %s (%s)\n", from.Format(time.RFC3339),
			humanWindow(s.WindowSinceMs, s.GeneratedAtMs))
	} else {
		b.WriteString("Window: full history\n")
	}
	if s.ProjectPath != "" {
		fmt.Fprintf(&b, "Project: `%s`\n", s.ProjectPath)
	} else {
		b.WriteString("Project: _all projects_\n")
	}
	b.WriteString("\n")

	// Totals block ---------------------------------------------------
	b.WriteString("## Window totals\n\n")
	fmt.Fprintf(&b, "- Sessions: **%d**\n", s.TotalSessions)
	fmt.Fprintf(&b, "- Messages: **%s**\n", humanInt64(s.TotalMessages))
	fmt.Fprintf(&b, "- Input tokens: **%s** (cached read %s)\n",
		humanInt64(s.TotalTokensIn), humanInt64(s.TotalCachedRead))
	fmt.Fprintf(&b, "- Output tokens: **%s**\n", humanInt64(s.TotalTokensOut))
	fmt.Fprintf(&b, "- Priced compute: **$%.2f**\n", s.TotalCostUSD)
	fmt.Fprintf(&b, "- /compact events: **%d**\n", s.CompactCount)
	b.WriteString("\n")

	// Projects -------------------------------------------------------
	b.WriteString("## Top projects (by input tokens)\n\n")
	if len(s.TopProjects) == 0 {
		b.WriteString("_(none in window)_\n\n")
	} else {
		b.WriteString("| Project | Sessions | Messages | Input tokens |\n")
		b.WriteString("|---|---:|---:|---:|\n")
		for _, p := range s.TopProjects {
			fmt.Fprintf(&b, "| `%s` | %d | %s | %s |\n",
				compactProjectPath(p.ProjectPath),
				p.Sessions, humanInt64(p.Messages), humanInt64(p.TokensIn))
		}
		b.WriteString("\n")
	}

	// Recent sessions ------------------------------------------------
	b.WriteString("## Recent sessions\n\n")
	if len(s.RecentSessions) == 0 {
		b.WriteString("_(none in window)_\n\n")
	} else {
		b.WriteString("| Session | CLI | Last activity | Messages | $ |\n")
		b.WriteString("|---|---|---|---:|---:|\n")
		for _, r := range s.RecentSessions {
			when := time.UnixMilli(r.LastMsgAt).UTC().Format("2006-01-02 15:04")
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | $%.2f |\n",
				shortID(r.ID), r.CLI, when, humanInt64(r.Messages), r.CostUSD)
		}
		b.WriteString("\n")
	}

	// Compact events -------------------------------------------------
	b.WriteString("## /compact events in window\n\n")
	if len(s.CompactEvents) == 0 {
		b.WriteString("_(none)_\n\n")
	} else {
		b.WriteString("| Session | When | Before | After | Compression |\n")
		b.WriteString("|---|---|---:|---:|---:|\n")
		for _, e := range s.CompactEvents {
			when := time.UnixMilli(e.Ts).UTC().Format("2006-01-02 15:04")
			ratio := "—"
			if e.After > 0 {
				ratio = fmt.Sprintf("%.1fx", float64(e.Before)/float64(e.After))
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n",
				shortID(e.SessionID), when,
				humanInt64(e.Before), humanInt64(e.After), ratio)
		}
		b.WriteString("\n")
	}

	// Memory + stop-hook footer -------------------------------------
	b.WriteString("## Memory & summaries\n\n")
	fmt.Fprintf(&b, "- Project memories (`%s`): **%d**\n",
		compactProjectPath(s.ProjectPath), s.MemoriesProject)
	fmt.Fprintf(&b, "- Global memories: **%d**\n", s.MemoriesGlobal)
	if s.ProjectPath != "" {
		fmt.Fprintf(&b, "- Stop-hook summaries in window: **%d**\n", s.StopSummaryCount)
	}
	return b.String()
}

// humanInt64 renders a token count compactly (1.2K, 3.4M, 5.6B).
func humanInt64(n int64) string {
	if n <= 0 {
		return "0"
	}
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// humanWindow describes a duration since the cutoff (e.g., "7d").
func humanWindow(sinceMs, nowMs int64) string {
	if nowMs <= sinceMs {
		return "0d"
	}
	d := time.Duration(nowMs-sinceMs) * time.Millisecond
	if d >= 24*time.Hour {
		return fmt.Sprintf("%.0fd", d.Hours()/24)
	}
	if d >= time.Hour {
		return fmt.Sprintf("%.0fh", d.Hours())
	}
	return fmt.Sprintf("%.0fm", d.Minutes())
}

// compactProjectPath collapses a long absolute project path to its
// final two segments so a wide table stays readable. Named to avoid
// colliding with roast.go's single-segment shortProject.
func compactProjectPath(path string) string {
	if path == "" {
		return "_global_"
	}
	parts := strings.Split(strings.TrimRight(path, "/"), "/")
	if len(parts) <= 2 {
		return path
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

// shortID returns the leading 8 hex chars of a UUID/string.
func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// _ keeps imports tight when the connectors package is brought in
// transitively by store.ListSessions's return type — the linter
// would otherwise flag the explicit import as unused.
var _ = connectors.RoleAssistant
