package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/worklog"
)

// RecordReflectionInput is the MCP-facing input schema. Insights MUST
// each carry at least one evidence id (session_id from the proposer
// output); the underlying worklog helper rejects empty-evidence
// insights up-front.
//
// Day is the YYYY-MM-DD (UTC) calendar day the reflection covers.
// Strongly recommended — omitting it files the reflection under
// today's UTC date, which is wrong for multi-day catch-up (the
// reason Day exists). Empty is allowed for back-compat with the
// legacy single-day call shape.
type RecordReflectionInput struct {
	ProjectPath string            `json:"project_path" jsonschema:"absolute project path"`
	Day         string            `json:"day,omitempty" jsonschema:"calendar day this reflection covers, YYYY-MM-DD (UTC); strongly recommended (omitting it files under today UTC which is wrong for multi-day catch-up)"`
	Insights    []worklog.Insight `json:"insights" jsonschema:"synthesized insights — each must cite at least one entry session_id"`
}

// RecordReflectionOutput surfaces the persisted id and a citation count
// so the AI host can confirm the round-trip back to the user.
type RecordReflectionOutput struct {
	ReflectionID  string `json:"reflection_id"`
	EvidenceCount int    `json:"evidence_count"`
}

func handleRecordReflection(ctx context.Context, db *store.DB, in RecordReflectionInput) (*RecordReflectionOutput, error) {
	var day time.Time
	if in.Day != "" {
		parsed, err := time.ParseInLocation("2006-01-02", in.Day, time.UTC)
		if err != nil {
			return nil, fmt.Errorf("record_reflection: bad day %q (want YYYY-MM-DD): %w", in.Day, err)
		}
		day = parsed
	}
	refl, err := worklog.RecordReflection(ctx, db, in.ProjectPath, day, in.Insights)
	if err != nil {
		return nil, err
	}
	return &RecordReflectionOutput{ReflectionID: refl.ID, EvidenceCount: len(refl.EvidenceEntryIDs)}, nil
}

// HandleRecordReflection is the MCP entry-point. The citation invariant
// is enforced inside worklog.RecordReflection; this thin shell just
// adapts errors to the tool-log surface.
func HandleRecordReflection(ctx context.Context, _ *mcp.CallToolRequest, in RecordReflectionInput) (*mcp.CallToolResult, RecordReflectionOutput, error) {
	db, err := store.Open(ctx, config.DBPath())
	if err != nil {
		return nil, RecordReflectionOutput{}, fmt.Errorf("record_reflection: open db: %w", err)
	}
	defer db.Close()
	out, err := handleRecordReflection(ctx, db, in)
	if err != nil {
		return nil, RecordReflectionOutput{}, err
	}
	summary := fmt.Sprintf("record_reflection: stored %s with %d evidence entries", out.ReflectionID, out.EvidenceCount)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: summary}}}, *out, nil
}
