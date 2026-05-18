package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/store"
)

// UserRecapArgs is the pure input shape used by handleUserRecap. Tests
// drive this form directly to skip MCP wrapping. The "user" in the name
// reflects scope: aggregate across every project on the machine, not
// just the cwd's project (that's recap_project's job).
type UserRecapArgs struct {
	SinceDays int
	GroupBy   string // "cli" | "project" | "" (both)
}

// UserRecapInput is the MCP-facing input. Mirrors UserRecapArgs with
// json/jsonschema tags so the host can render parameter docs.
type UserRecapInput struct {
	SinceDays int    `json:"since_days,omitempty" jsonschema:"lookback window in days (default 7)"`
	GroupBy   string `json:"group_by,omitempty" jsonschema:"\"cli\" | \"project\" | \"\" (default: both groupings returned)"`
}

// UserRecapOutput is the pure result of a cross-project aggregation.
// ByCLI and ByProject are always populated (the GroupBy hint is a UI
// preference, not a filter — the agent can ignore whichever map it
// doesn't need). TopEntries is capped server-side to bound payload.
type UserRecapOutput struct {
	TotalEntries int                 `json:"total_entries"`
	ByCLI        map[string]int      `json:"by_cli"`
	ByProject    map[string]int      `json:"by_project"`
	TopEntries   []RecapEntry        `json:"top_entries"`
	Reflections  []ReflectionSummary `json:"reflections,omitempty"`
}

// Server-side caps. TopEntries bounds what we return to the agent; the
// sweep limit bounds how many rows we scan from SQLite per call. The
// importance-DESC ordering means the sweep already prefers the highest
// signal entries when the user has more than 200 visible recaps in the
// window.
const userRecapTopEntries = 10
const userRecapSweepLimit = 200

// handleUserRecap is the pure business-logic form. Aggregates visible
// stop_summaries rows in the window across ALL projects and CLIs.
func handleUserRecap(ctx context.Context, db *store.DB, args UserRecapArgs) (*UserRecapOutput, error) {
	if args.SinceDays <= 0 {
		args.SinceDays = 7
	}
	cutoff := time.Now().Add(-time.Duration(args.SinceDays) * 24 * time.Hour).UnixMilli()
	out := &UserRecapOutput{
		ByCLI:      map[string]int{},
		ByProject:  map[string]int{},
		TopEntries: []RecapEntry{},
	}
	rows, err := db.Read().QueryContext(ctx,
		`SELECT session_id, cli, project_path, COALESCE(recap_topic,''), COALESCE(ai_drafted_summary,''), ts, importance
         FROM stop_summaries
         WHERE recap_visible = 1 AND ts >= ?
         ORDER BY importance DESC, ts DESC
         LIMIT ?`,
		cutoff, userRecapSweepLimit)
	if err != nil {
		return nil, fmt.Errorf("user_recap: query: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var e RecapEntry
		var tsMs int64
		if err := rows.Scan(&e.SessionID, &e.CLI, &e.ProjectPath, &e.RecapTopic, &e.AIDraftedSummary, &tsMs, &e.Importance); err != nil {
			return nil, fmt.Errorf("user_recap: scan: %w", err)
		}
		e.TS = time.UnixMilli(tsMs)
		out.TotalEntries++
		out.ByCLI[e.CLI]++
		out.ByProject[e.ProjectPath]++
		if len(out.TopEntries) < userRecapTopEntries {
			out.TopEntries = append(out.TopEntries, e)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Cross-project reflections (T16) — surface up to 10 most recent in the window.
	refRows, refErr := db.Read().QueryContext(ctx,
		`SELECT id, ts, project_path, tier, title, body_md, evidence_entry_ids_json, evidence_reflection_ids_json
         FROM worklog_reflections
         WHERE ts >= ?
         ORDER BY ts DESC LIMIT 10`,
		cutoff)
	if refErr == nil {
		defer refRows.Close() //nolint:errcheck
		for refRows.Next() {
			var r ReflectionSummary
			var tsMs int64
			var entryJSON, refJSON string
			if err := refRows.Scan(&r.ID, &tsMs, &r.ProjectPath, &r.Tier, &r.Title, &r.BodyMD, &entryJSON, &refJSON); err != nil {
				continue
			}
			r.TS = time.UnixMilli(tsMs)
			var ev, rev []string
			_ = json.Unmarshal([]byte(entryJSON), &ev)
			_ = json.Unmarshal([]byte(refJSON), &rev)
			r.EvidenceCount = len(ev) + len(rev)
			out.Reflections = append(out.Reflections, r)
		}
	}
	// Silent on refErr — same rationale as recap_project.
	return out, nil
}

// HandleUserRecap is the MCP-facing handler. Opens the DB, runs the
// pure handler, formats a one-line summary for the host tool log, and
// returns the structured output alongside.
func HandleUserRecap(ctx context.Context, _ *mcp.CallToolRequest, in UserRecapInput) (*mcp.CallToolResult, UserRecapOutput, error) {
	db, err := store.Open(ctx, config.DBPath())
	if err != nil {
		return nil, UserRecapOutput{}, fmt.Errorf("user_recap: open db: %w", err)
	}
	defer db.Close()
	out, err := handleUserRecap(ctx, db, UserRecapArgs{
		SinceDays: in.SinceDays,
		GroupBy:   in.GroupBy,
	})
	if err != nil {
		return nil, UserRecapOutput{}, err
	}
	days := in.SinceDays
	if days <= 0 {
		days = 7
	}
	summary := fmt.Sprintf(
		"user_recap: %d entries across %d project(s) in the last %d days (claude=%d, codex=%d)",
		out.TotalEntries, len(out.ByProject), days, out.ByCLI["claude"], out.ByCLI["codex"],
	)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, *out, nil
}
