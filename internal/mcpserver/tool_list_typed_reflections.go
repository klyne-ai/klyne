package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/store"
)

// ListTypedReflectionsInput names the (project_path, day) tuple whose
// typed worklog_reflections rows should be returned. Day is the local
// YYYY-MM-DD the reflection COVERS (the `day` column from migration
// 022), not the row's write-ts day — catch-up reflections written
// today for a past day must surface when the second LLM pass requests
// that past day's slice.
type ListTypedReflectionsInput struct {
	ProjectPath string `json:"project_path" jsonschema:"absolute project path"`
	Day         string `json:"day" jsonschema:"local YYYY-MM-DD the typed reflections cover"`
}

// TypedReflectionRow is one worklog_reflections row whose body_json
// payload has been parsed. The second LLM pass groups these by service
// and synthesizes one What-was-done card per service.
//
// BodyJSON is the parsed payload — the LLM never has to deal with the
// raw JSON string. ReflectionID + TS (epoch-ms) provide enough
// provenance for the LLM to cite the source row if it wants to. Day
// echoes the input so a caller iterating over multiple days can tell
// which row belongs to which day without re-keying.
type TypedReflectionRow struct {
	ReflectionID string           `json:"reflection_id"`
	TS           int64            `json:"ts"`
	Day          string           `json:"day"`
	ProjectPath  string           `json:"project_path"`
	BodyJSON     store.WWDPayload `json:"body_json"`
}

// ListTypedReflectionsOutput carries the rows the LLM will consume.
// Rows is always populated (possibly empty) so the slash command can
// check len(rows) == 0 for the C1 no-op.
type ListTypedReflectionsOutput struct {
	Rows []TypedReflectionRow `json:"rows"`
}

// handleListTypedReflections is the pure-Go inner handler — separated
// from the MCP shell so tests can exercise it without the surrounding
// transport plumbing.
func handleListTypedReflections(ctx context.Context, db *store.DB, in ListTypedReflectionsInput) (*ListTypedReflectionsOutput, error) {
	projectPath := strings.TrimSpace(in.ProjectPath)
	if projectPath == "" {
		return nil, fmt.Errorf("list_typed_reflections: project_path required")
	}
	dayStr := strings.TrimSpace(in.Day)
	if dayStr == "" {
		return nil, fmt.Errorf("list_typed_reflections: day required")
	}
	if _, err := time.ParseInLocation("2006-01-02", dayStr, time.Local); err != nil {
		return nil, fmt.Errorf("list_typed_reflections: bad day %q (want YYYY-MM-DD): %w", dayStr, err)
	}

	rows, err := store.ListReflectionsForProjectDay(ctx, db, projectPath, dayStr)
	if err != nil {
		return nil, fmt.Errorf("list_typed_reflections: list reflections: %w", err)
	}

	out := &ListTypedReflectionsOutput{Rows: []TypedReflectionRow{}}
	for _, r := range rows {
		if strings.TrimSpace(r.BodyJSON) == "" {
			// Legacy prose-only row — no typed contribution. Skip.
			continue
		}
		payload, err := store.ParseBodyJSON(r)
		if err != nil {
			// Defensive: a malformed payload was already rejected at the
			// MCP tool boundary; skip rather than fail the whole batch.
			continue
		}
		out.Rows = append(out.Rows, TypedReflectionRow{
			ReflectionID: r.ID,
			TS:           r.TS,
			Day:          r.Day,
			ProjectPath:  r.ProjectPath,
			BodyJSON:     payload,
		})
	}
	return out, nil
}

// HandleListTypedReflections is the MCP entry-point for the
// list_typed_reflections tool. Opens the daemon DB, dispatches to the
// inner handler, and renders a one-line text summary so the AI host
// can confirm the row count without re-reading the structured output.
func HandleListTypedReflections(ctx context.Context, _ *mcp.CallToolRequest, in ListTypedReflectionsInput) (*mcp.CallToolResult, ListTypedReflectionsOutput, error) {
	db, err := store.Open(ctx, config.DBPath())
	if err != nil {
		return nil, ListTypedReflectionsOutput{}, fmt.Errorf("list_typed_reflections: open db: %w", err)
	}
	defer db.Close() //nolint:errcheck
	out, err := handleListTypedReflections(ctx, db, in)
	if err != nil {
		return nil, ListTypedReflectionsOutput{}, err
	}
	summary := fmt.Sprintf("list_typed_reflections: %d typed reflection rows for %s on %s",
		len(out.Rows), in.ProjectPath, in.Day)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: summary}}}, *out, nil
}
