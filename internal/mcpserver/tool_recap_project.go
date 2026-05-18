package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/store"
)

// RecapProjectArgs is the pure input shape used by handleRecapProject.
// The MCP-facing input (RecapProjectInput) mirrors these fields but
// carries json/jsonschema tags so the host can introspect the surface.
// Tests drive the pure form to skip MCP wrapping.
type RecapProjectArgs struct {
	ProjectPath string
	SinceDays   int
	Topic       string // optional substring filter on recap_topic
}

// RecapProjectInput is the MCP-facing input. Fields mirror
// RecapProjectArgs; the json/jsonschema tags expose the schema to the
// host so prompts and slash menus can render parameter docs.
type RecapProjectInput struct {
	ProjectPath string `json:"project_path" jsonschema:"absolute project path to scope recap entries"`
	SinceDays   int    `json:"since_days,omitempty" jsonschema:"lookback window in days (default 7)"`
	Topic       string `json:"topic,omitempty" jsonschema:"optional substring filter on recap_topic"`
}

// RecapEntry is one visible worklog entry as surfaced by the recap
// tools (recap_project today; user_recap and the bootstrap injection
// will reuse this shape in T10/T11). ProjectPath is carried even for
// the per-project call so the cross-project variant can reuse the type
// without a parallel struct.
type RecapEntry struct {
	SessionID        string    `json:"session_id"`
	CLI              string    `json:"cli"`
	RecapTopic       string    `json:"recap_topic,omitempty"`
	AIDraftedSummary string    `json:"ai_drafted_summary,omitempty"`
	TS               time.Time `json:"ts"`
	Importance       int       `json:"importance"`
	ProjectPath      string    `json:"project_path,omitempty"`
}

// ReflectionSummary is a recap-tool-facing projection of a stored
// reflection. EvidenceCount surfaces the citation invariant (T15) so an
// AI consumer can see at a glance how grounded a synthesis is.
type ReflectionSummary struct {
	ID            string    `json:"id"`
	ProjectPath   string    `json:"project_path,omitempty"`
	Title         string    `json:"title"`
	BodyMD        string    `json:"body_md"`
	Tier          int       `json:"tier"`
	TS            time.Time `json:"ts"`
	EvidenceCount int       `json:"evidence_count"`
}

// RecapProjectOutput is the pure result of a per-project recap query.
// Entries are ordered newest-first and capped server-side to keep the
// MCP payload bounded. Reflections (T16) ride alongside so the
// synthesis tier is visible in the same call.
type RecapProjectOutput struct {
	Entries     []RecapEntry        `json:"entries"`
	Reflections []ReflectionSummary `json:"reflections,omitempty"`
}

// handleRecapProject is the pure business-logic form — tests drive
// this directly to skip MCP wrapping. Keep the surface narrow: open
// no resources, mutate no globals.
func handleRecapProject(ctx context.Context, db *store.DB, args RecapProjectArgs) (*RecapProjectOutput, error) {
	if args.SinceDays <= 0 {
		args.SinceDays = 7
	}
	cutoff := time.Now().Add(-time.Duration(args.SinceDays) * 24 * time.Hour).UnixMilli()
	q := `SELECT session_id, cli, COALESCE(recap_topic,''), COALESCE(ai_drafted_summary,''), ts, importance, project_path
          FROM stop_summaries
          WHERE project_path = ? AND recap_visible = 1 AND ts >= ?`
	sqlArgs := []any{args.ProjectPath, cutoff}
	if args.Topic != "" {
		q += " AND recap_topic LIKE ?"
		sqlArgs = append(sqlArgs, "%"+args.Topic+"%")
	}
	q += " ORDER BY ts DESC LIMIT 50"
	rows, err := db.Read().QueryContext(ctx, q, sqlArgs...)
	if err != nil {
		return nil, fmt.Errorf("recap_project: query: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	out := &RecapProjectOutput{Entries: []RecapEntry{}}
	for rows.Next() {
		var e RecapEntry
		var tsMs int64
		if err := rows.Scan(&e.SessionID, &e.CLI, &e.RecapTopic, &e.AIDraftedSummary, &tsMs, &e.Importance, &e.ProjectPath); err != nil {
			return nil, fmt.Errorf("recap_project: scan: %w", err)
		}
		e.TS = time.UnixMilli(tsMs)
		out.Entries = append(out.Entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reflections (T15/T16): surface up to 5 most recent for the project.
	refls, refErr := store.ListReflectionsForProject(ctx, db, args.ProjectPath, 5)
	if refErr == nil {
		for _, r := range refls {
			if time.UnixMilli(r.TS).Before(time.UnixMilli(cutoff)) {
				continue
			}
			out.Reflections = append(out.Reflections, ReflectionSummary{
				ID: r.ID, ProjectPath: r.ProjectPath, Title: r.Title, BodyMD: r.BodyMD,
				Tier: r.Tier, TS: time.UnixMilli(r.TS),
				EvidenceCount: len(r.EvidenceEntryIDs) + len(r.EvidenceReflectionIDs),
			})
		}
	}
	// Silent on refErr: older databases lacking migration 016 must not break
	// recap_project. The error is non-fatal by design.
	return out, nil
}

// HandleRecapProject is the MCP-facing handler. It opens the DB, runs
// the pure handler, and adapts the result to the MCP CallToolResult
// shape. The summary TextContent is what shows in the host's tool log;
// the structured output rides alongside for the agent to read.
func HandleRecapProject(ctx context.Context, _ *mcp.CallToolRequest, in RecapProjectInput) (*mcp.CallToolResult, RecapProjectOutput, error) {
	db, err := store.Open(ctx, config.DBPath())
	if err != nil {
		return nil, RecapProjectOutput{}, fmt.Errorf("recap_project: open db: %w", err)
	}
	defer db.Close()
	out, err := handleRecapProject(ctx, db, RecapProjectArgs{
		ProjectPath: in.ProjectPath,
		SinceDays:   in.SinceDays,
		Topic:       in.Topic,
	})
	if err != nil {
		return nil, RecapProjectOutput{}, err
	}
	days := in.SinceDays
	if days <= 0 {
		days = 7
	}
	summary := fmt.Sprintf("recap_project: %d entries in %s (last %d days)", len(out.Entries), in.ProjectPath, days)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: summary}},
	}, *out, nil
}
