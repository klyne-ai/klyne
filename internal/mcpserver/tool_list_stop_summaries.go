package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/projectpath"
	"github.com/klyne-ai/klyne/internal/store"
)

// jsonUnmarshal is a tiny rename to avoid shadowing the encoding/json
// import inside parseFilesJSON.
var jsonUnmarshal = json.Unmarshal

// ListStopSummariesForDayInput is the MCP tool input — narrow on
// purpose. The second-pass productivity-sync slash command reads raw L1
// stop_summaries directly (not pre-digested reflections) so the LLM can
// synthesize the day's narrative from ground truth.
type ListStopSummariesForDayInput struct {
	ProjectPath string `json:"project_path" jsonschema:"absolute project path"`
	Day         string `json:"day"          jsonschema:"local YYYY-MM-DD to list"`
	// IncludeSuppressed defaults to true. The L2 suppression rules drop
	// many real work turns (investigations, push-only turns) — the v2
	// productivity-sync needs to see them. Set false to honor the
	// recap_visible filter as the dashboard's "pending entries" tracker does.
	IncludeSuppressed *bool `json:"include_suppressed,omitempty" jsonschema:"default true: return rows with recap_visible=0 too (their AI summary often carries real work the upstream filter dropped)"`
}

// StopSummaryRow is the per-row shape returned to the LLM. Narrow
// projection of stop_summaries — only the fields the synthesis prompt
// actually consumes. Files is split out of files_json so the LLM sees a
// real array, not raw JSON text.
type StopSummaryRow struct {
	SessionID        string   `json:"session_id"`
	Ts               int64    `json:"ts"`
	TsLocal          string   `json:"ts_local"`
	CLI              string   `json:"cli"`
	RecapVisible     int      `json:"recap_visible"`
	Importance       int      `json:"importance"`
	RecapTopic       string   `json:"recap_topic,omitempty"`
	LastUser         string   `json:"last_user,omitempty"`
	LastBash         string   `json:"last_bash,omitempty"`
	Files            []string `json:"files,omitempty"`
	AIDraftedSummary string   `json:"ai_drafted_summary,omitempty"`
}

// ListStopSummariesForDayOutput is the response — a flat array plus
// counts so the LLM (and any debugging caller) can see at a glance how
// much data is in play.
type ListStopSummariesForDayOutput struct {
	ProjectPath string           `json:"project_path"`
	Day         string           `json:"day"`
	Count       int              `json:"count"`
	Rows        []StopSummaryRow `json:"rows"`
}

func handleListStopSummariesForDay(ctx context.Context, db *store.DB, in ListStopSummariesForDayInput) (*ListStopSummariesForDayOutput, error) {
	in.ProjectPath = projectpath.Canonical(strings.TrimSpace(in.ProjectPath))
	if in.ProjectPath == "" {
		return nil, fmt.Errorf("list_stop_summaries_for_day: project_path required")
	}
	dayStr := strings.TrimSpace(in.Day)
	if dayStr == "" {
		return nil, fmt.Errorf("list_stop_summaries_for_day: day required")
	}
	if _, err := time.ParseInLocation("2006-01-02", dayStr, time.Local); err != nil {
		return nil, fmt.Errorf("list_stop_summaries_for_day: bad day %q (want YYYY-MM-DD): %w", dayStr, err)
	}
	includeSuppressed := true
	if in.IncludeSuppressed != nil {
		includeSuppressed = *in.IncludeSuppressed
	}

	q := `
SELECT session_id, ts, cli, recap_visible, importance,
       COALESCE(recap_topic, ''), COALESCE(last_user, ''),
       COALESCE(last_bash, ''), COALESCE(files_json, '[]'),
       COALESCE(ai_drafted_summary, '')
  FROM stop_summaries
 WHERE project_path = ?
   AND date(ts / 1000, 'unixepoch', 'localtime') = ?`
	if !includeSuppressed {
		q += "\n   AND recap_visible = 1"
	}
	q += "\n ORDER BY ts ASC"

	rows, err := db.Read().QueryContext(ctx, q, in.ProjectPath, dayStr)
	if err != nil {
		return nil, fmt.Errorf("list_stop_summaries_for_day: query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := ListStopSummariesForDayOutput{
		ProjectPath: in.ProjectPath,
		Day:         dayStr,
		Rows:        []StopSummaryRow{},
	}
	for rows.Next() {
		var r StopSummaryRow
		var filesJSON string
		if err := rows.Scan(&r.SessionID, &r.Ts, &r.CLI, &r.RecapVisible, &r.Importance,
			&r.RecapTopic, &r.LastUser, &r.LastBash, &filesJSON, &r.AIDraftedSummary); err != nil {
			return nil, fmt.Errorf("list_stop_summaries_for_day: scan: %w", err)
		}
		r.TsLocal = time.UnixMilli(r.Ts).Local().Format("2006-01-02 15:04:05")
		if filesJSON != "" && filesJSON != "[]" {
			r.Files = parseFilesJSON(filesJSON)
		}
		out.Rows = append(out.Rows, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list_stop_summaries_for_day: iterate: %w", err)
	}
	out.Count = len(out.Rows)
	return &out, nil
}

// HandleListStopSummariesForDay is the MCP transport adaptor.
func HandleListStopSummariesForDay(ctx context.Context, _ *mcp.CallToolRequest, in ListStopSummariesForDayInput) (*mcp.CallToolResult, ListStopSummariesForDayOutput, error) {
	db, err := store.Open(ctx, config.DBPath())
	if err != nil {
		return nil, ListStopSummariesForDayOutput{}, fmt.Errorf("list_stop_summaries_for_day: open db: %w", err)
	}
	defer db.Close() //nolint:errcheck
	out, err := handleListStopSummariesForDay(ctx, db, in)
	if err != nil {
		return nil, ListStopSummariesForDayOutput{}, err
	}
	summary := fmt.Sprintf("list_stop_summaries_for_day: %d rows for %s/%s", out.Count, in.ProjectPath, in.Day)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: summary}}}, *out, nil
}

// parseFilesJSON is a tiny defensive decoder. files_json on disk is a
// JSON-encoded []string; malformed rows degrade to an empty slice
// rather than failing the tool call.
func parseFilesJSON(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" || s == "null" {
		return nil
	}
	// Minimal one-pass parse — files_json is always a flat array of
	// strings written by us, so json.Unmarshal would work but we avoid
	// the dep here to keep the tool file lean.
	if !strings.HasPrefix(s, "[") {
		return nil
	}
	// Defer to encoding/json for correctness.
	var out []string
	_ = jsonUnmarshal([]byte(s), &out)
	return out
}
