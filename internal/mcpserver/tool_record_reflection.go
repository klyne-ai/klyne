package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/store"
	"github.com/klyne-ai/klyne/internal/worklog"
)

// RecordReflectionInput is the MCP-facing input schema. Insights MUST
// each carry at least one evidence id (session_id from the proposer
// output); the underlying worklog helper rejects empty-evidence
// insights up-front.
type RecordReflectionInput struct {
	ProjectPath string            `json:"project_path" jsonschema:"absolute project path"`
	Insights    []worklog.Insight `json:"insights" jsonschema:"synthesized insights — each must cite at least one entry session_id"`
}

// RecordReflectionOutput surfaces the persisted id and a citation count
// so the AI host can confirm the round-trip back to the user.
type RecordReflectionOutput struct {
	ReflectionID  string `json:"reflection_id"`
	EvidenceCount int    `json:"evidence_count"`
}

func handleRecordReflection(ctx context.Context, db *store.DB, in RecordReflectionInput) (*RecordReflectionOutput, error) {
	refl, err := worklog.RecordReflection(ctx, db, in.ProjectPath, in.Insights)
	if err != nil {
		return nil, err
	}
	return &RecordReflectionOutput{ReflectionID: refl.ID, EvidenceCount: len(refl.EvidenceEntryIDs)}, nil
}

// HandleRecordReflection is the MCP entry-point. The citation invariant
// is enforced inside worklog.RecordReflection; this thin shell just
// adapts errors to the tool-log surface.
func HandleRecordReflection(ctx context.Context, _ *mcp.CallToolRequest, in RecordReflectionInput) (*mcp.CallToolResult, RecordReflectionOutput, error) {
	db, err := store.Open(config.DBPath())
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
