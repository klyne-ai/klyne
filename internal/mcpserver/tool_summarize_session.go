package mcpserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/store"
)

// summarize_session tool
// ======================
// Deterministic per-session timeline. Stitches together every data
// source klyne already keeps about one session — stop_summaries (the
// recurring "what just happened" snapshots written by the Stop hook),
// session_summaries (the rolling AI-generated summary), decisions
// linked to the session, and the de-duplicated union of files touched
// across all stop_summaries — into one Markdown block the agent can
// echo verbatim.
//
// No AI call. No JSONL read. Pure synthesis from klyne.db.

// summarizeStopSummaryLimit caps how many stop-hook rows the timeline
// includes. Fifty covers ~a week of typical use (most projects fire
// the Stop hook 3–10 times per day) without blowing the agent's
// reading budget. Not user-configurable — if a longer history is
// needed, the caller can drop to get_session or read stop_summaries
// directly via klyne's CLI.
const summarizeStopSummaryLimit = 50

// SummarizeSessionInput is the JSON-Schema input for the tool.
type SummarizeSessionInput struct {
	SessionID string `json:"session_id" jsonschema:"the session id to summarize; required"`
}

// SummarizeSessionOutput is the structured payload returned to the
// agent plus a verbatim-renderable markdown body.
type SummarizeSessionOutput struct {
	SessionID     string              `json:"session_id"`
	Found         bool                `json:"found"`
	Reason        string              `json:"reason,omitempty"`
	Session       *SessionMetaRow     `json:"session,omitempty"`
	StopSummaries []store.StopSummary `json:"stop_summaries,omitempty"`
	LatestSummary *store.Summary      `json:"latest_summary,omitempty"`
	Decisions     []store.Decision    `json:"decisions,omitempty"`
	FilesTouched  []string            `json:"files_touched,omitempty"`
	Markdown      string              `json:"markdown"`
}

// HandleSummarizeSession assembles the per-session timeline.
func HandleSummarizeSession(ctx context.Context, _ *mcp.CallToolRequest, in SummarizeSessionInput) (*mcp.CallToolResult, SummarizeSessionOutput, error) {
	if strings.TrimSpace(in.SessionID) == "" {
		const reason = "session_id is required"
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: reason}},
		}, SummarizeSessionOutput{Reason: reason, Markdown: reason}, nil
	}

	db, err := store.Open(ctx, config.DBPath())
	if err != nil {
		return nil, SummarizeSessionOutput{}, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	sess, err := store.GetSession(ctx, db, in.SessionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, SummarizeSessionOutput{}, fmt.Errorf("get session: %w", err)
	}
	// Even when the session row is absent the timeline can still be
	// useful — stop_summaries do NOT FK against sessions, so the agent
	// may have a hook-emitted summary for a session the ingestor never
	// saw. Continue gathering the rest.

	stops, err := store.ListStopSummariesForSession(ctx, db, in.SessionID, summarizeStopSummaryLimit)
	if err != nil {
		return nil, SummarizeSessionOutput{}, fmt.Errorf("stop summaries: %w", err)
	}

	latest, err := store.LatestSummary(ctx, db, in.SessionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, SummarizeSessionOutput{}, fmt.Errorf("latest summary: %w", err)
	}

	decisions, err := store.ListDecisions(ctx, db, store.DecisionFilter{
		SessionID: in.SessionID,
		Limit:     100,
	})
	if err != nil {
		return nil, SummarizeSessionOutput{}, fmt.Errorf("decisions: %w", err)
	}

	files := dedupFilesFromStops(stops)

	// Found is true when we have ANY useful data. A session-row-only
	// response (no stops, no rolling summary, no decisions) is still
	// considered "found" — the metadata block alone is useful when
	// the user asks about a session that started but hasn't yet been
	// stop-hooked or summarised. The agent will see a header-only
	// Markdown body in that case; that's intentional.
	out := SummarizeSessionOutput{
		SessionID:     in.SessionID,
		Found:         sess != nil || len(stops) > 0 || latest != nil || len(decisions) > 0,
		StopSummaries: stops,
		LatestSummary: latest,
		Decisions:     decisions,
		FilesTouched:  files,
	}
	if sess != nil {
		out.Session = &SessionMetaRow{
			ID:          sess.ID,
			CLI:         string(sess.CLI),
			ProjectPath: sess.ProjectPath,
			StartedAt:   sess.StartedAt,
			LastMsgAt:   sess.LastMsgAt,
			MsgCount:    int(sess.MsgCount),
			TokensIn:    sess.TokensIn,
			TokensOut:   sess.TokensOut,
			CostUSD:     sess.CostUSD,
			Model:       sess.Model,
			Status:      string(sess.Status),
		}
	}
	if !out.Found {
		out.Reason = fmt.Sprintf("session %q not found in klyne store and no derived data exists for it", in.SessionID)
	}
	out.Markdown = formatSessionSummaryAsMarkdown(out)

	summary := fmt.Sprintf("summarize_session %s: %d stop-summaries, %d decisions, %d files",
		in.SessionID, len(stops), len(decisions), len(files))
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, out, nil
}

// dedupFilesFromStops takes the union of files mentioned across every
// stop_summary's Files slice, sorted for stable output.
func dedupFilesFromStops(stops []store.StopSummary) []string {
	seen := make(map[string]bool, 16)
	for _, s := range stops {
		for _, f := range s.Files {
			seen[f] = true
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// formatSessionSummaryAsMarkdown renders the structured timeline as a
// stable Markdown block. Empty sections are omitted so the rendering
// does not include `_(none)_` clutter — the agent can echo it directly
// without filtering.
func formatSessionSummaryAsMarkdown(out SummarizeSessionOutput) string {
	if !out.Found {
		return out.Reason
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Session summary: `%s`\n\n", short(out.SessionID))

	if out.Session != nil {
		fmt.Fprintf(&b, "- Project: `%s`\n", out.Session.ProjectPath)
		fmt.Fprintf(&b, "- CLI: `%s`  ·  Model: `%s`  ·  Status: `%s`\n",
			out.Session.CLI, out.Session.Model, out.Session.Status)
		fmt.Fprintf(&b, "- Started: %s  ·  Last msg: %s\n",
			time.UnixMilli(out.Session.StartedAt).UTC().Format(time.RFC3339),
			time.UnixMilli(out.Session.LastMsgAt).UTC().Format(time.RFC3339))
		fmt.Fprintf(&b, "- Messages: %d  ·  Tokens in/out: %d / %d  ·  Cost: $%.2f\n\n",
			out.Session.MsgCount, out.Session.TokensIn, out.Session.TokensOut, out.Session.CostUSD)
	}

	if len(out.StopSummaries) > 0 {
		b.WriteString("## Stop-summary timeline\n\n")
		for _, s := range out.StopSummaries {
			when := time.UnixMilli(s.Ts).UTC().Format(time.RFC3339)
			fmt.Fprintf(&b, "- **%s** — %s\n", when, oneLine(s.Summary))
			if s.LastUser != "" {
				fmt.Fprintf(&b, "  - last user: %s\n", oneLine(truncatePreview(s.LastUser)))
			}
			if s.LastBash != "" {
				fmt.Fprintf(&b, "  - last bash: `%s`\n", oneLine(s.LastBash))
			}
		}
		b.WriteString("\n")
	}

	if len(out.FilesTouched) > 0 {
		b.WriteString("## Files touched\n\n")
		for _, f := range out.FilesTouched {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
		b.WriteString("\n")
	}

	if len(out.Decisions) > 0 {
		b.WriteString("## Decisions\n\n")
		for _, d := range out.Decisions {
			fmt.Fprintf(&b, "- `%s` — %s\n", d.ID, oneLine(d.Text))
		}
		b.WriteString("\n")
	}

	if out.LatestSummary != nil {
		fmt.Fprintf(&b, "## Latest rolling summary (v%d, model=%s)\n\n", out.LatestSummary.Version, out.LatestSummary.Model)
		b.WriteString(out.LatestSummary.Text)
		b.WriteString("\n")
	}

	return b.String()
}
